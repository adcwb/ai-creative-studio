package application

import (
	"context"
	"errors"
	"strings"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/story"
	"github.com/adcwb/ai-creative-studio/backend/internal/persistence/storyrepo"
)

func (s *FileStore) validateStory(ctx context.Context, space, root string, in story.Input) error {
	if x := in.Body.Outline; x != nil {
		n, e := s.series.Get(ctx, space, "season", x.SeasonID)
		if e != nil {
			return e
		}
		if n.SeriesID != root {
			return story.ErrInput
		}
		if x.EpisodeID != "" {
			ep, e := s.series.Get(ctx, space, "episode", x.EpisodeID)
			if e != nil {
				return e
			}
			if ep.Parent != x.SeasonID || ep.SeriesID != root {
				return story.ErrInput
			}
		}
	}
	if in.Body.Cast != nil {
		if e := s.validateStoryCast(ctx, space, root, *in.Body.Cast); e != nil {
			return e
		}
	}
	if _, e := s.series.Get(ctx, space, "series", root); e != nil {
		return e
	}
	for _, id := range story.EpisodeRefs(in.Body) {
		n, e := s.series.Get(ctx, space, "episode", id)
		if e != nil {
			return e
		}
		if n.SeriesID != root {
			return story.ErrInput
		}
	}
	if in.Body.FromEpisodeID != "" && in.Body.ToEpisodeID != "" || in.Body.Foreshadow != nil && in.Body.Foreshadow.PayoffEpisodeID != "" {
		dir, e := s.series.Directory(ctx, space, root)
		if e != nil {
			return e
		}
		ranks := map[string][2]int64{}
		seasons := map[string]int64{}
		for _, n := range dir.Seasons {
			seasons[n.ID] = n.Ordinal
		}
		for _, n := range dir.Episodes {
			ranks[n.ID] = [2]int64{seasons[n.Parent], n.Ordinal}
		}
		greater := func(a, b string) bool { x, y := ranks[a], ranks[b]; return x[0] > y[0] || x[0] == y[0] && x[1] > y[1] }
		if in.Body.FromEpisodeID != "" && in.Body.ToEpisodeID != "" && greater(in.Body.FromEpisodeID, in.Body.ToEpisodeID) {
			return story.ErrInput
		}
		if x := in.Body.Foreshadow; x != nil && x.PayoffEpisodeID != "" && greater(x.SetupEpisodeID, x.PayoffEpisodeID) {
			return story.ErrInput
		}
	}
	// These are canonical series characters, not a guess based on a repeated
	// display name in an independent episode's cast.
	if f := in.Body.Fact; f != nil && (f.BelieverID != "" || len(f.KnownBy) > 0) {
		bible, e := s.story.Get(ctx, storyrepo.Scope{Space: space, Series: root}, storyrepo.BibleID(root), "")
		if e != nil {
			return story.ErrInput
		}
		if bible.Status != "active" {
			return story.ErrInput
		}
		ids := map[string]bool{}
		for _, c := range bible.Body.Bible.Characters {
			ids[c.ID] = true
		}
		if f.BelieverID != "" && !ids[f.BelieverID] {
			return story.ErrInput
		}
		for _, k := range f.KnownBy {
			if !ids[k.CharacterID] {
				return story.ErrInput
			}
		}
	}
	if len(in.Body.Evidence) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pg != nil && s.pg.err != nil {
		return s.pg.err
	}
	for _, evidence := range in.Body.Evidence {
		p, e := s.projectLocked(space, evidence.EpisodeID)
		if e != nil {
			return e
		}
		if p.SeriesEpisodeID != evidence.EpisodeID {
			return story.ErrInput
		}
		script, e := s.structureSourceLocked(p, evidence.ScriptVersion)
		if e != nil {
			return e
		}
		if script.StructureVersion != 2 || story.Digest(script) != evidence.ScriptSHA256 {
			return story.ErrConflict
		}
		found := false
		for _, scene := range script.Scenes {
			if scene.ID != evidence.SceneID {
				continue
			}
			texts := []string{scene.Heading, scene.Action}
			for _, d := range scene.Dialogue {
				texts = append(texts, d.Text)
			}
			for _, text := range texts {
				if strings.Contains(text, evidence.Quote) {
					found = true
					break
				}
			}
		}
		if !found {
			return story.ErrInput
		}
	}
	return nil
}
func (s *FileStore) SaveStory(ctx context.Context, a story.Actor, root, id, key string, in story.Input) (story.Version, error) {
	if !a.Writable {
		return story.Version{}, story.ErrDenied
	}
	if _, e := s.series.Get(ctx, a.Space, "series", root); e != nil {
		return story.Version{}, e
	}
	// Accepted receipts survive later edits to their evidence. Only the original
	// normalized request is allowed to replay; no new formal version is written.
	normalized, e := story.Normalize(in)
	if e != nil {
		return story.Version{}, e
	}
	if replay, found, e := s.story.Replay(ctx, a, root, id, key, normalized); e != nil || found {
		return replay, e
	}
	if e = s.validateStory(ctx, a.Space, root, normalized); e != nil {
		return story.Version{}, e
	}
	return s.story.Save(ctx, a, root, id, key, normalized)
}

type StoryContextPreview struct {
	story.Preview
	DirectoryDigest string `json:"directory_sha256"`
	ProjectVersion  int    `json:"project_version"`
	ScriptVersion   int    `json:"script_version"`
	ScriptDigest    string `json:"script_sha256"`
	SnapshotDigest  string `json:"snapshot_sha256"`
}

func (s *FileStore) PreviewStoryContext(ctx context.Context, space, episode string) (StoryContextPreview, error) {
	var out StoryContextPreview
	scope, e := s.series.Context(ctx, space, episode)
	if e != nil {
		return out, e
	}
	dir, e := s.series.Directory(ctx, space, scope.Series.ID)
	if e != nil {
		return out, e
	}
	out.DirectoryDigest = story.Digest(dir)
	seasons := map[string]int64{}
	for _, n := range dir.Seasons {
		seasons[n.ID] = n.Ordinal
	}
	order := []story.Episode{}
	for _, n := range dir.Episodes {
		order = append(order, story.Episode{ID: n.ID, SeasonID: n.Parent, SeasonOrdinal: seasons[n.Parent], Ordinal: n.Ordinal, Version: n.Version})
	}
	q := storyrepo.Scope{Space: space, Series: scope.Series.ID}
	retrieved, e := story.Retrieve(scope.Series.ID, episode, order, story.Selection{}, func(fn func(story.Version) error) error { return s.story.Visit(ctx, q, fn) })
	out.Preview = retrieved.Preview
	if e != nil {
		return out, e
	}
	s.mu.Lock()
	if s.pg != nil && s.pg.err != nil {
		e = s.pg.err
	} else {
		var p Project
		p, e = s.projectLocked(space, episode)
		if e == nil {
			out.ProjectVersion = p.Version
			out.ScriptVersion = p.ScriptVersion
			out.ScriptDigest = story.Digest(p.Script)
			checkCastScript(&out.Preview, p.Script, p.ScriptVersion)
		}
	}
	s.mu.Unlock()
	if e != nil {
		return out, e
	}
	latest, e := s.series.Directory(ctx, space, scope.Series.ID)
	if e != nil {
		return out, e
	}
	if story.Digest(latest) != out.DirectoryDigest {
		return out, story.ErrConflict
	}
	out.SnapshotDigest = story.Digest(out)
	return out, nil
}
func (s *FileStore) StoryVersion(ctx context.Context, q storyrepo.Scope, id, version string) (story.Version, error) {
	if _, e := s.series.Get(ctx, q.Space, "series", q.Series); e != nil {
		return story.Version{}, e
	}
	v, e := s.story.Get(ctx, q, id, version)
	if e != nil {
		return v, e
	}
	if v.Space != q.Space || v.SeriesID != q.Series {
		return story.Version{}, errors.New("story scope invalid")
	}
	return v, nil
}
