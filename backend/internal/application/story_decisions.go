package application

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/story"
	"github.com/adcwb/ai-creative-studio/backend/internal/persistence/seriesrepo"
	"github.com/adcwb/ai-creative-studio/backend/internal/persistence/storyrepo"
)

type StorySourceChange struct {
	Previous *story.Summary `json:"previous,omitempty"`
	Current  *story.Summary `json:"current,omitempty"`
}
type StoryFindingReview struct {
	CheckID              string                `json:"check_id"`
	Finding              story.Finding         `json:"finding"`
	Snapshot             story.ContextSnapshot `json:"snapshot"`
	CurrentTarget        *story.Version        `json:"current_target,omitempty"`
	CurrentBible         *story.Version        `json:"current_bible,omitempty"`
	ReviewSHA256         string                `json:"review_sha256"`
	SourcesChanged       bool                  `json:"sources_changed"`
	Decision             *story.Decision       `json:"decision,omitempty"`
	CurrentScript        *Script               `json:"current_script"`
	CurrentScriptVersion int                   `json:"current_script_version"`
	ScriptChanged        bool                  `json:"script_changed"`
	ProjectChanged       bool                  `json:"project_changed"`
	DirectoryChanged     bool                  `json:"directory_changed"`
	ChangedSources       []StorySourceChange   `json:"changed_sources"`
	HeadsSHA256          string                `json:"-"`
}

func (s *FileStore) verifiedStoryCheck(ctx context.Context, q storyrepo.Scope, ep, id string) (story.Check, story.ContextSnapshot, error) {
	r, e := s.story.GetCheck(ctx, q, ep, id)
	if e != nil {
		return story.Check{}, story.ContextSnapshot{}, e
	}
	snapshot, e := s.story.GetContext(ctx, q, ep, r.Check.Input.ContextID)
	if e != nil {
		return r.Check, snapshot, e
	}
	if r.Check.State == "succeeded" {
		candidate, e := story.Clone(*r.Check.Result)
		if e != nil {
			return r.Check, snapshot, e
		}
		for i := range candidate.Findings {
			candidate.Findings[i].ID = ""
		}
		verified, e := story.ValidateResult(r.Check.ID, snapshot, candidate)
		if e != nil || story.Digest(verified) != story.Digest(r.Check.Result) {
			return r.Check, snapshot, storyrepo.ErrStorage
		}
	}
	return r.Check, snapshot, nil
}
func (s *FileStore) reviewStoryFinding(ctx context.Context, space, ep, check, id string) (StoryFindingReview, error) {
	var out StoryFindingReview
	q, e := s.storySnapshotScope(ctx, space, ep)
	if e != nil {
		return out, e
	}
	c, snapshot, e := s.verifiedStoryCheck(ctx, q, ep, check)
	if e != nil {
		return out, e
	}
	if c.State != "succeeded" || c.Result == nil {
		return out, story.ErrInput
	}
	out.CheckID = check
	out.Snapshot = snapshot
	found := false
	for _, f := range c.Result.Findings {
		if f.ID == id {
			out.Finding = f
			found = true
			break
		}
	}
	if !found {
		return out, story.ErrNotFound
	}
	target, e := s.story.Get(ctx, q, story.AdoptionTarget(check, out.Finding), "")
	if e == nil {
		out.CurrentTarget = &target
	} else if !errors.Is(e, story.ErrNotFound) {
		return out, e
	}
	bible, e := s.story.Get(ctx, q, storyrepo.BibleID(q.Series), "")
	if e == nil {
		out.CurrentBible = &bible
	} else if !errors.Is(e, story.ErrNotFound) {
		return out, e
	}
	dir, e := s.series.Directory(ctx, space, q.Series)
	if e != nil {
		return out, e
	}
	currentHeads, e := s.story.HeadSummaries(ctx, q)
	if e != nil {
		return out, e
	}
	heads := story.Digest(currentHeads)
	out.ChangedSources = []StorySourceChange{}
	old := map[string]story.Summary{}
	for _, h := range snapshot.Context.Sources {
		old[h.ID] = h
	}
	for _, h := range currentHeads {
		before, ok := old[h.ID]
		delete(old, h.ID)
		if !ok {
			copy := h
			out.ChangedSources = append(out.ChangedSources, StorySourceChange{Current: &copy})
		} else if story.Digest(h) != story.Digest(before) {
			copy := h
			out.ChangedSources = append(out.ChangedSources, StorySourceChange{Previous: &before, Current: &copy})
		}
	}
	for _, h := range snapshot.Context.Sources {
		if _, ok := old[h.ID]; ok {
			copy := h
			out.ChangedSources = append(out.ChangedSources, StorySourceChange{Previous: &copy})
		}
	}
	out.HeadsSHA256 = heads
	s.mu.Lock()
	var p Project
	if s.pg != nil && s.pg.err != nil {
		e = s.pg.err
	} else {
		p, e = s.projectLocked(space, ep)
	}
	if e == nil {
		directory := story.Digest(dir)
		script := story.Digest(p.Script)
		out.CurrentScript, e = story.Clone(p.Script)
		out.CurrentScriptVersion = p.ScriptVersion
		out.ProjectChanged = p.Version != snapshot.ProjectVersion
		out.ScriptChanged = p.ScriptVersion != snapshot.ScriptVersion || script != snapshot.ScriptSHA256
		out.DirectoryChanged = directory != snapshot.DirectorySHA256
		out.SourcesChanged = directory != snapshot.DirectorySHA256 || heads != snapshot.Context.SourcesSHA256 || p.Version != snapshot.ProjectVersion || p.ScriptVersion != snapshot.ScriptVersion || script != snapshot.ScriptSHA256
		out.ReviewSHA256 = reviewDigest(check, id, directory, heads, p)
	}
	s.mu.Unlock()
	if e != nil {
		return out, e
	}
	decision, e := s.story.GetDecision(ctx, q, ep, check, id)
	if e == nil {
		out.Decision = &decision
	} else if !errors.Is(e, story.ErrNotFound) {
		return out, e
	}
	return out, nil
}
func (s *FileStore) decideStoryFinding(ctx context.Context, a story.Actor, ep, check, id, key string, in story.DecisionInput) (story.Decision, error) {
	if !a.Writable {
		return story.Decision{}, story.ErrDenied
	}
	q, e := s.storySnapshotScope(ctx, a.Space, ep)
	if e != nil {
		return story.Decision{}, e
	}
	in, e = story.NormalizeDecision(in)
	if e != nil {
		return story.Decision{}, e
	}
	if replay, found, e := s.story.ReplayDecision(ctx, a, q, ep, check, id, key, in); e != nil || found {
		return replay, e
	}
	replayOrError := func(cause error) (story.Decision, error) {
		d, found, e := s.story.ReplayDecision(ctx, a, q, ep, check, id, key, in)
		if e != nil || found {
			return d, e
		}
		return story.Decision{}, cause
	}
	view, e := s.reviewStoryFinding(ctx, a.Space, ep, check, id)
	if e != nil {
		return replayOrError(e)
	}
	if in.Action == "adopt" {
		if in.ReviewSHA256 != view.ReviewSHA256 || view.SourcesChanged && !in.AcknowledgeChanged {
			return replayOrError(story.ErrConflict)
		}
		if in.Proposal == nil {
			return story.Decision{}, story.ErrInput
		}
		// Evidence is authored or selected explicitly by the creator. Every script
		// quote is checked against the true, immutable historical source again.
		if e = s.validateStory(ctx, a.Space, q.Series, *in.Proposal); e != nil {
			return replayOrError(e)
		}
	}
	// No provider request or automatic correction occurs in this local action.
	// The repository rechecks the complete head digest and target CAS inside its
	// short series transaction, then atomically appends the decision and version.
	if in.Action == "adopt" && s.pg == nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		p, e := s.projectLocked(a.Space, ep)
		if e != nil {
			return story.Decision{}, e
		}
		var out story.Decision
		e = s.series.WithDirectory(ctx, a.Space, q.Series, func(dir seriesrepo.DirectorySnapshot) error {
			if reviewDigest(check, id, story.Digest(dir), view.HeadsSHA256, p) != in.ReviewSHA256 {
				return story.ErrConflict
			}
			var e error
			out, e = s.story.Decide(ctx, a, q, ep, check, id, key, in, view.HeadsSHA256, nil)
			return e
		})
		if e != nil {
			return replayOrError(e)
		}
		return out, nil
	}
	guard := func(ctx context.Context, tx *sql.Tx) (func(), error) {
		if tx == nil {
			return nil, story.ErrInput
		}
		dir, e := s.series.DirectoryTx(ctx, tx, a.Space, q.Series)
		if e != nil {
			return nil, e
		}
		var raw []byte
		if e = tx.QueryRowContext(ctx, `SELECT payload FROM studio_projects WHERE tenant_id=$1 AND entity_id=$2 FOR UPDATE`, a.Space, ep).Scan(&raw); e != nil {
			return nil, e
		}
		var p Project
		if json.Unmarshal(raw, &p) != nil || p.ID != ep || p.SpaceID != a.Space || p.SeriesEpisodeID != ep {
			return nil, story.ErrInput
		}
		if reviewDigest(check, id, story.Digest(dir), view.HeadsSHA256, p) != in.ReviewSHA256 {
			return nil, story.ErrConflict
		}
		return nil, nil
	}
	out, e := s.story.Decide(ctx, a, q, ep, check, id, key, in, view.HeadsSHA256, guard)
	if e != nil {
		return replayOrError(e)
	}
	return out, nil
}
func reviewDigest(check, id, directory, heads string, p Project) string {
	return story.Digest(struct {
		Check, Finding, Directory, Heads, Script string
		Project, ScriptVersion                   int
	}{check, id, directory, heads, story.Digest(p.Script), p.Version, p.ScriptVersion})
}
