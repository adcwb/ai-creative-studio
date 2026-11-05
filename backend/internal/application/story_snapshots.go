package application

import (
	"context"
	"time"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/story"
	"github.com/adcwb/ai-creative-studio/backend/internal/persistence/storyrepo"
)

func (s *FileStore) storySnapshotScope(ctx context.Context, space, episode string) (storyrepo.Scope, error) {
	scope, e := s.series.Context(ctx, space, episode)
	if e != nil {
		return storyrepo.Scope{}, e
	}
	return storyrepo.Scope{Space: space, Series: scope.Series.ID}, nil
}
func (s *FileStore) CreateStoryContext(ctx context.Context, a story.Actor, episode, key string, selection story.Selection) (story.ContextSnapshot, error) {
	var out story.ContextSnapshot
	if !a.Writable {
		return out, story.ErrDenied
	}
	q, e := s.storySnapshotScope(ctx, a.Space, episode)
	if e != nil {
		return out, e
	}
	if replay, found, e := s.story.ReplayContext(ctx, a, q.Series, episode, key, selection); e != nil || found {
		return replay, e
	}
	selection, e = story.NormalizeSelection(selection)
	if e != nil {
		return out, e
	}
	dir, e := s.series.Directory(ctx, a.Space, q.Series)
	if e != nil {
		return out, e
	}
	out = story.ContextSnapshot{Format: 1, ID: storyrepo.ContextID(a, q.Series, episode, key), Space: a.Space, SeriesID: q.Series, EpisodeID: episode, DirectorySHA256: story.Digest(dir), Actor: a.User, CreatedAt: time.Now().UTC()}
	seasons := map[string]int64{}
	for _, n := range dir.Seasons {
		seasons[n.ID] = n.Ordinal
	}
	order := []story.Episode{}
	for _, n := range dir.Episodes {
		order = append(order, story.Episode{ID: n.ID, SeasonID: n.Parent, SeasonOrdinal: seasons[n.Parent], Ordinal: n.Ordinal, Version: n.Version})
	}
	out.Context, e = story.Retrieve(q.Series, episode, order, selection, func(fn func(story.Version) error) error { return s.story.Visit(ctx, q, fn) })
	if e != nil {
		return out, e
	}
	s.mu.Lock()
	if s.pg != nil && s.pg.err != nil {
		e = s.pg.err
	} else {
		var p Project
		p, e = s.projectLocked(a.Space, episode)
		if e == nil {
			if p.SeriesEpisodeID != episode {
				e = story.ErrInput
			} else {
				out.ProjectVersion = p.Version
				out.ScriptVersion = p.ScriptVersion
				out.Script, e = story.Clone(p.Script)
				out.ScriptSHA256 = story.Digest(p.Script)
			}
		}
	}
	s.mu.Unlock()
	if e != nil {
		return out, e
	}
	latest, e := s.series.Directory(ctx, a.Space, q.Series)
	if e != nil {
		return out, e
	}
	if story.Digest(latest) != out.DirectorySHA256 {
		return out, story.ErrConflict
	}
	if out.Script == nil {
		out.Context.Problems = append(out.Context.Problems, story.Problem{Code: "script_required", Message: "本集尚未保存剧本；可保留资料快照，开始AI检查前请先完成剧本。"})
		out.Context.Preview.Digest = ""
		out.Context.Preview.Digest = story.Digest(out.Context.Preview)
	}
	checkCastScript(&out.Context.Preview, out.Script, out.ScriptVersion)
	out.SHA256 = story.SnapshotDigest(out)
	return s.story.SaveContext(ctx, a, key, out)
}

// StoryContextFreshness is a read-only comparison, never authorization to send
// a provider request. A future executor must recheck its own current pins.
type StoryContextFreshness struct {
	SnapshotID       string `json:"snapshot_id"`
	Current          bool   `json:"current"`
	DirectoryChanged bool   `json:"directory_changed"`
	ProjectChanged   bool   `json:"project_changed"`
	ScriptChanged    bool   `json:"script_changed"`
	MaterialChanged  bool   `json:"material_changed"`
}

func (s *FileStore) CheckStoryContext(ctx context.Context, space, episode, id string) (StoryContextFreshness, error) {
	out := StoryContextFreshness{SnapshotID: id}
	q, e := s.storySnapshotScope(ctx, space, episode)
	if e != nil {
		return out, e
	}
	snapshot, e := s.story.GetContext(ctx, q, episode, id)
	if e != nil {
		return out, e
	}
	dir, e := s.series.Directory(ctx, space, q.Series)
	if e != nil {
		return out, e
	}
	out.DirectoryChanged = story.Digest(dir) != snapshot.DirectorySHA256
	heads, e := s.story.HeadDigest(ctx, q)
	if e != nil {
		return out, e
	}
	out.MaterialChanged = heads != snapshot.Context.SourcesSHA256
	s.mu.Lock()
	if s.pg != nil && s.pg.err != nil {
		e = s.pg.err
	} else {
		var p Project
		p, e = s.projectLocked(space, episode)
		if e == nil {
			out.ProjectChanged = p.Version != snapshot.ProjectVersion
			out.ScriptChanged = p.ScriptVersion != snapshot.ScriptVersion || story.Digest(p.Script) != snapshot.ScriptSHA256
		}
	}
	s.mu.Unlock()
	if e != nil {
		return out, e
	}
	out.Current = !out.DirectoryChanged && !out.ProjectChanged && !out.ScriptChanged && !out.MaterialChanged
	return out, nil
}
