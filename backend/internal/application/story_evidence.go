package application

import (
	"context"
	"net/http"
	"sort"
	"strconv"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/story"
)

// Only metadata is returned by the directory. Reading a particular version is
// explicit and never restores, upgrades or generates a script.
type StoryEvidenceScriptSummary struct {
	Version int    `json:"script_version"`
	Title   string `json:"title"`
	Stable  bool   `json:"stable_scene_ids"`
	Current bool   `json:"is_current"`
}
type StoryEvidenceScripts struct {
	Items []StoryEvidenceScriptSummary `json:"items"`
	Next  string                       `json:"next_cursor"`
}
type StoryEvidenceScript struct {
	Episode string  `json:"episode_id"`
	Version int     `json:"script_version"`
	SHA256  string  `json:"script_sha256"`
	Script  *Script `json:"script"`
}

func (s *FileStore) storyEvidenceEpisode(ctx context.Context, space, root, episode string) error {
	if _, e := s.series.Get(ctx, space, "series", root); e != nil {
		return e
	}
	n, e := s.series.Get(ctx, space, "episode", episode)
	if e != nil {
		return e
	}
	if n.SeriesID != root {
		return story.ErrNotFound
	}
	return nil
}
func (s *FileStore) StoryEvidenceScripts(ctx context.Context, space, root, episode string, before, limit int) (StoryEvidenceScripts, error) {
	out := StoryEvidenceScripts{Items: []StoryEvidenceScriptSummary{}}
	if limit < 1 || limit > 100 || before < 0 || int64(before) > story.MaxVersion {
		return out, story.ErrInput
	}
	if e := s.storyEvidenceEpisode(ctx, space, root, episode); e != nil {
		return out, e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pg != nil && s.pg.err != nil {
		return out, s.pg.err
	}
	p, e := s.projectLocked(space, episode)
	if e != nil {
		return out, e
	}
	if p.SeriesEpisodeID != episode {
		return out, story.ErrNotFound
	}
	versions := map[int]StoryEvidenceScriptSummary{}
	add := func(v int, script *Script) {
		if script != nil && v > 0 && (before == 0 || v < before) {
			versions[v] = StoryEvidenceScriptSummary{v, script.Title, script.StructureVersion == 2, v == p.ScriptVersion}
		}
	}
	for _, r := range s.data.History[episode] {
		if r.Kind == "script" {
			add(r.ScriptVersion, r.Script)
		}
	}
	add(p.ScriptVersion, p.Script)
	numbers := make([]int, 0, len(versions))
	for v := range versions {
		numbers = append(numbers, v)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(numbers)))
	for i, v := range numbers {
		if i == limit {
			out.Next = strconv.Itoa(numbers[i-1])
			break
		}
		out.Items = append(out.Items, versions[v])
	}
	return out, nil
}
func (s *FileStore) StoryEvidenceScript(ctx context.Context, space, root, episode string, version int) (StoryEvidenceScript, error) {
	out := StoryEvidenceScript{Episode: episode, Version: version}
	if version < 1 || int64(version) > story.MaxVersion {
		return out, story.ErrInput
	}
	if e := s.storyEvidenceEpisode(ctx, space, root, episode); e != nil {
		return out, e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pg != nil && s.pg.err != nil {
		return out, s.pg.err
	}
	p, e := s.projectLocked(space, episode)
	if e != nil {
		return out, e
	}
	if p.SeriesEpisodeID != episode {
		return out, story.ErrNotFound
	}
	found := p.Script != nil && p.ScriptVersion == version
	for _, r := range s.data.History[episode] {
		found = found || r.Kind == "script" && r.Script != nil && r.ScriptVersion == version
	}
	if !found {
		return out, story.ErrNotFound
	}
	script, e := s.structureSourceLocked(p, version)
	if e != nil {
		return out, e
	}
	if script.StructureVersion != 2 {
		return out, story.ErrInput
	}
	out.Script, out.SHA256 = script, story.Digest(script)
	return out, nil
}

func registerStoryEvidenceRoutes(mux *http.ServeMux, s *FileStore) {
	base := "/api/v1/series/{id}/story-evidence/episodes/{episode}/scripts"
	mux.HandleFunc("GET "+base, func(w http.ResponseWriter, r *http.Request) {
		space, ok := spaceID(w, r)
		if !ok {
			return
		}
		limit, e := storyLimit(r)
		before := 0
		if raw := r.URL.Query().Get("cursor"); raw != "" {
			var parseErr error
			before, parseErr = strconv.Atoi(raw)
			if parseErr != nil || before < 1 {
				e = story.ErrInput
			}
		}
		if e != nil {
			storyError(w, e)
			return
		}
		out, e := s.StoryEvidenceScripts(r.Context(), space, r.PathValue("id"), r.PathValue("episode"), before, limit)
		if e != nil {
			storyError(w, e)
			return
		}
		respond(w, 200, out)
	})
	mux.HandleFunc("GET "+base+"/{version}", func(w http.ResponseWriter, r *http.Request) {
		space, ok := spaceID(w, r)
		if !ok {
			return
		}
		version, e := strconv.Atoi(r.PathValue("version"))
		if e != nil || version < 1 {
			storyError(w, story.ErrInput)
			return
		}
		out, e := s.StoryEvidenceScript(r.Context(), space, r.PathValue("id"), r.PathValue("episode"), version)
		if e != nil {
			storyError(w, e)
			return
		}
		respond(w, 200, out)
	})
}
