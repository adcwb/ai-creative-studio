package application

import (
	"context"
	"errors"
	"net/http"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/library"
	"github.com/adcwb/ai-creative-studio/backend/internal/modules/story"
	"github.com/adcwb/ai-creative-studio/backend/internal/persistence/libraryrepo"
	"github.com/adcwb/ai-creative-studio/backend/internal/persistence/storyrepo"
)

type StoryCastOptions struct {
	EpisodeID     string          `json:"episode_id"`
	SeriesID      string          `json:"series_id"`
	Script        *Script         `json:"script"`
	ScriptVersion int             `json:"script_version"`
	ScriptSHA256  string          `json:"script_sha256"`
	Bible         *story.Version  `json:"bible"`
	Current       *story.Version  `json:"current"`
	Problems      []story.Problem `json:"problems"`
}

func (s *FileStore) storyCastOptions(ctx context.Context, space, episode string) (StoryCastOptions, error) {
	out := StoryCastOptions{EpisodeID: episode, Problems: []story.Problem{}}
	q, e := s.storySnapshotScope(ctx, space, episode)
	if e != nil {
		return out, e
	}
	out.SeriesID = q.Series
	p, e := s.GetProject(space, episode)
	if e != nil {
		return out, e
	}
	if p.SeriesEpisodeID != episode {
		return out, story.ErrInput
	}
	out.Script = p.Script
	out.ScriptVersion = p.ScriptVersion
	out.ScriptSHA256 = story.Digest(p.Script)
	b, e := s.story.Get(ctx, q, storyrepo.BibleID(q.Series), "")
	if e == nil {
		out.Bible = &b
	} else if !errors.Is(e, story.ErrNotFound) {
		return out, e
	}
	current, e := s.story.Get(ctx, q, story.CastID(q.Series, episode), "")
	if e == nil {
		out.Current = &current
	} else if !errors.Is(e, story.ErrNotFound) {
		return out, e
	}
	if out.Script == nil || out.Script.StructureVersion != 2 {
		out.Problems = append(out.Problems, story.Problem{Code: "stable_script_required", Message: "请先保存本集的稳定关联剧本，再选择对应人物。"})
	}
	if out.Bible == nil || out.Bible.Status != "active" {
		out.Problems = append(out.Problems, story.Problem{Code: "bible_required", Message: "请先保存正式故事圣经与系列人物。"})
	}
	return out, nil
}
func (s *FileStore) validateStoryCast(ctx context.Context, space, root string, c story.Cast) error {
	options, e := s.storyCastOptions(ctx, space, c.EpisodeID)
	if e != nil {
		return e
	}
	if options.SeriesID != root || len(options.Problems) > 0 {
		return story.ErrInput
	}
	if options.ScriptVersion != c.ScriptVersion || options.ScriptSHA256 != c.ScriptSHA256 || options.Bible.VersionID != c.BibleVersionID {
		return story.ErrConflict
	}
	scripts := map[string]string{}
	for _, x := range options.Script.Characters {
		scripts[x.ID] = x.Name
	}
	canonical := map[string]string{}
	for _, x := range options.Bible.Body.Bible.Characters {
		canonical[x.ID] = x.Name
	}
	for _, x := range c.Characters {
		if scripts[x.ScriptID] == "" || scripts[x.ScriptID] != x.ScriptName || canonical[x.SeriesID] == "" || canonical[x.SeriesID] != x.SeriesName {
			return story.ErrInput
		}
		if x.AssetID == "" {
			continue
		}
		a, e := s.library.Get(ctx, space, x.AssetID)
		if errors.Is(e, libraryrepo.ErrNotFound) {
			return story.ErrInput
		}
		if e != nil {
			return e
		}
		v, e := s.library.Version(ctx, space, x.AssetID, x.AssetVersionID)
		if errors.Is(e, libraryrepo.ErrNotFound) {
			return story.ErrInput
		}
		if e != nil {
			return e
		}
		if a.Asset.Kind != "character" || library.CanBind(a.Asset, v, space) != nil || v.Card.Name != x.AssetName {
			return story.ErrInput
		}
	}
	return nil
}

// A cast pins script identity and names. Later edits remain readable, but a new
// generation must explicitly reconcile this input instead of silently rebinding.
func checkCastScript(p *story.Preview, script *Script, version int) {
	for _, v := range p.Included {
		if c := v.Body.Cast; c != nil && (script == nil || c.ScriptVersion != version || c.ScriptSHA256 != story.Digest(script)) {
			p.Problems = append(p.Problems, story.Problem{Code: "cast_script_changed", RecordID: v.ID, Message: "本集剧本已更新，请核对选角并保存新版本；已有对应关系不会按名字自动替换。"})
		}
	}
	p.Digest = ""
	p.Digest = story.Digest(*p)
}
func registerStoryCastRoutes(mux *http.ServeMux, s *FileStore) {
	mux.HandleFunc("PUT /api/v1/episodes/{id}/cast", func(w http.ResponseWriter, r *http.Request) {
		space, key, ok := projectWriteScope(w, r)
		if !ok {
			return
		}
		episode := r.PathValue("id")
		q, e := s.storySnapshotScope(r.Context(), space, episode)
		if e != nil {
			storyError(w, e)
			return
		}
		var in story.Input
		if !decodeProject(w, r, &in) {
			return
		}
		if in.Kind != "cast" || in.Body.Cast == nil || in.Body.Cast.EpisodeID != episode {
			storyError(w, story.ErrInput)
			return
		}
		actor := seriesActorFor(r, space)
		out, e := s.SaveStory(r.Context(), story.Actor{Space: space, User: actor.User, Writable: actor.Writable}, q.Series, story.CastID(q.Series, episode), key, in)
		if e != nil {
			storyError(w, e)
			return
		}
		respond(w, 200, out)
	})
	mux.HandleFunc("GET /api/v1/episodes/{id}/cast-options", func(w http.ResponseWriter, r *http.Request) {
		space, ok := spaceID(w, r)
		if !ok {
			return
		}
		out, e := s.storyCastOptions(r.Context(), space, r.PathValue("id"))
		if e != nil {
			storyError(w, e)
			return
		}
		respond(w, 200, out)
	})
}
