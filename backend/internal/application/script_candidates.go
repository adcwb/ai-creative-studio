package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/projects"
	"github.com/adcwb/ai-creative-studio/backend/internal/modules/story"
	"github.com/adcwb/ai-creative-studio/backend/internal/persistence/seriesrepo"
	"github.com/adcwb/ai-creative-studio/backend/internal/persistence/storyrepo"
)

type ScriptAdoptionInput struct {
	ReviewContextID string `json:"review_context_snapshot_id,omitempty"`
	actor           string
	ExpectedVersion int     `json:"expected_version"`
	ReviewedChanges bool    `json:"reviewed_changes,omitempty"`
	Script          *Script `json:"script,omitempty"`
	Reason          string  `json:"reason,omitempty"`
}

// Replays are receipts, not permission for a new execution. Current membership
// is checked by HTTP middleware; a later disabled model must not hide acceptance.
func (s *FileStore) replayScriptGeneration(space, project, key string, in ScriptGenerationInput) (ScriptGeneration, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// For a new direct request, preserve model validation before project lookup.
	// Only an existing receipt can bypass resolving a model for a fresh call.
	if !in.CandidateOnly {
		if _, found := s.data.ProjectReceipts[projectReceiptKey(space, key)]; !found {
			return ScriptGeneration{}, false, nil
		}
	}
	if _, e := s.projectLocked(space, project); e != nil {
		return ScriptGeneration{}, false, e
	}
	receipt, found, e := s.projectReceiptLocked(space, key, "generate", project, in)
	if e != nil || !found {
		return ScriptGeneration{}, false, e
	}
	g, ok := s.data.ScriptGenerations[receipt.GenerationID]
	if !ok || g.SpaceID != space || g.ProjectID != project {
		return ScriptGeneration{}, false, ErrNotFound
	}
	return scriptGenerationSummary(g), true, nil
}

// Candidate bodies are read on demand, never in project polling or list rows.
func (s *FileStore) GetScriptGeneration(space, project, id string) (ScriptGeneration, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, e := s.projectLocked(space, project); e != nil {
		return ScriptGeneration{}, e
	}
	g, ok := s.data.ScriptGenerations[id]
	if !ok || g.SpaceID != space || g.ProjectID != project {
		return ScriptGeneration{}, ErrNotFound
	}
	return cloneScriptGeneration(g), nil
}

// AdoptScriptCandidate is an artistic decision, not another provider call. The
// candidate and its original source stay immutable, including after later edits.
func (s *FileStore) AdoptScriptCandidate(space, project, id, key string, in ScriptAdoptionInput) (Project, error) {
	// The original receipt is resolved before loading current story material.
	s.mu.Lock()
	p, e := s.projectLocked(space, project)
	if e != nil {
		s.mu.Unlock()
		return Project{}, e
	}
	_, replay, e := s.projectReceiptLocked(space, key, "adopt-script:"+id, project, in)
	if e != nil {
		s.mu.Unlock()
		return Project{}, e
	}
	if replay {
		out := s.projectViewLocked(p)
		s.mu.Unlock()
		return out, nil
	}
	g, ok := s.data.ScriptGenerations[id]
	g = cloneScriptGeneration(g)
	s.mu.Unlock()
	if !ok || g.SpaceID != space || g.ProjectID != project {
		return Project{}, ErrNotFound
	}
	var review *story.ContextSnapshot
	if g.ContextID != "" {
		if !story.ID.MatchString(in.ReviewContextID) {
			return Project{}, projectInputError("请先核对本集正式资料，再明确采纳候选")
		}
		old, e := s.writingSnapshot(context.Background(), space, project, g.ContextID)
		if e != nil {
			return Project{}, e
		}
		v, e := s.writingSnapshot(context.Background(), space, project, in.ReviewContextID)
		if e != nil {
			return Project{}, e
		}
		if old.SHA256 != g.ContextSHA256 || story.Digest(old.Context.Selection) != story.Digest(v.Context.Selection) {
			return Project{}, ErrVersionConflict
		}
		changed := old.DirectorySHA256 != v.DirectorySHA256 || old.Context.SourcesSHA256 != v.Context.SourcesSHA256 || old.ProjectVersion != v.ProjectVersion
		if changed && !in.ReviewedChanges {
			return Project{}, ErrVersionConflict
		}
		review = &v
	} else if in.ReviewContextID != "" {
		return Project{}, projectInputError("这份候选没有正式故事上下文，不接受附加来源")
	}
	return s.adoptScriptCandidate(space, project, id, key, in, review)
}
func (s *FileStore) adoptScriptCandidate(space, project, id, key string, in ScriptAdoptionInput, review *story.ContextSnapshot) (Project, error) {
	if in.ExpectedVersion < 1 || !utf8.ValidString(in.Reason) || utf8.RuneCountInString(in.Reason) > 2000 {
		return Project{}, projectInputError("请核对当前版本与采纳说明")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p, e := s.projectLocked(space, project)
	if e != nil {
		return Project{}, e
	}
	receipt, replay, e := s.projectReceiptLocked(space, key, "adopt-script:"+id, project, in)
	if e != nil {
		return Project{}, e
	}
	if replay {
		return s.projectViewLocked(p), nil
	}
	g, ok := s.data.ScriptGenerations[id]
	if !ok || g.SpaceID != space || g.ProjectID != project {
		return Project{}, ErrNotFound
	}
	if !g.CandidateOnly || g.Status != "succeeded" || g.Candidate == nil || g.Adoption != nil {
		return Project{}, ErrConflict
	}
	if p.Version != in.ExpectedVersion || (p.Version != g.BaseVersion && !in.ReviewedChanges) {
		return Project{}, ErrVersionConflict
	}
	if s.generationActiveLocked(p) {
		return Project{}, ErrGenerationActive
	}
	chosen := cloneScript(*g.Candidate)
	if in.Script != nil {
		chosen = cloneScript(*in.Script)
	}
	chosen, e = NormalizeScript(chosen)
	if e != nil {
		return Project{}, e
	}
	if p.StructureVersion == 2 && chosen.StructureVersion != 2 {
		return Project{}, projectInputError("采纳稿需保留稳定角色与场次标识")
	}
	if p.StructureVersion == 0 && chosen.StructureVersion != 0 {
		return Project{}, ErrConflict
	}
	if p.SeriesEpisodeID != "" {
		if e = singleEpisodeScript(&chosen); e != nil {
			return Project{}, e
		}
	}
	p.Script = &chosen
	p.ScriptProvider = g.Provider
	p.ScriptBrief = g.SourceBrief
	p.ScriptVersion++
	p.Version++
	p.UpdatedAt = time.Now().UTC()
	raw, _ := json.Marshal(chosen)
	digest := sha256.Sum256(raw)
	g = cloneScriptGeneration(g)
	actor := in.actor
	if actor == "" {
		actor = "demo-user"
	}
	g.Adoption = &projects.ScriptAdoption{ReviewContextID: in.ReviewContextID, Actor: actor, GenerationID: id, ProjectVersion: p.Version, ScriptVersion: p.ScriptVersion, SHA256: hex.EncodeToString(digest[:]), Edited: !reflect.DeepEqual(chosen, *g.Candidate), Reason: strings.TrimSpace(in.Reason), CreatedAt: p.UpdatedAt}
	d := s.clone()
	d.Projects[p.ID] = cloneProject(p)
	d.ScriptGenerations[id] = g
	d.ProjectReceipts[projectReceiptKey(space, key)] = receipt
	commit := func() error { return s.commit(d) }
	if review != nil {
		// p has been incremented locally, while the source pins the pre-write view.
		if review.ProjectVersion != in.ExpectedVersion {
			return Project{}, ErrVersionConflict
		}
		q := storyrepo.Scope{Space: space, Series: review.SeriesID}
		if s.pg != nil {
			// Match canonical story writes: story lock, directory lock, core commit.
			e = storyrepo.GuardHeadsTx(s.pg.ctx, s.pg.tx, q, review.Context.SourcesSHA256)
			if e == nil {
				var dir seriesrepo.DirectorySnapshot
				dir, e = s.series.DirectoryTx(s.pg.ctx, s.pg.tx, space, q.Series)
				if e == nil && story.Digest(dir) != review.DirectorySHA256 {
					e = story.ErrConflict
				}
			}
			if e == nil {
				e = commit()
			}
		} else {
			e = s.series.WithDirectory(context.Background(), space, q.Series, func(dir seriesrepo.DirectorySnapshot) error {
				if story.Digest(dir) != review.DirectorySHA256 {
					return story.ErrConflict
				}
				return s.story.WithHeads(context.Background(), q, review.Context.SourcesSHA256, commit)
			})
		}
	} else {
		e = commit()
	}
	if e != nil {
		return Project{}, writingInputError(e)
	}
	return s.projectViewLocked(p), nil
}

func registerScriptCandidateRoutes(mux *http.ServeMux, s *FileStore) {
	base := "/api/v1/projects/{id}/generations/{generation}"
	mux.HandleFunc("GET "+base, func(w http.ResponseWriter, r *http.Request) {
		space, ok := spaceID(w, r)
		if !ok {
			return
		}
		g, e := s.GetScriptGeneration(space, r.PathValue("id"), r.PathValue("generation"))
		if e != nil {
			storeError(w, e)
			return
		}
		respond(w, 200, g)
	})
	mux.HandleFunc("POST "+base+"/adoptions", func(w http.ResponseWriter, r *http.Request) {
		space, key, ok := projectWriteScope(w, r)
		if !ok {
			return
		}
		var in ScriptAdoptionInput
		if !decodeProject(w, r, &in) {
			return
		}
		in.actor = seriesActorFor(r, space).User
		p, e := s.AdoptScriptCandidate(space, r.PathValue("id"), r.PathValue("generation"), key, in)
		if e != nil {
			storeError(w, e)
			return
		}
		respond(w, 200, p)
	})
}
