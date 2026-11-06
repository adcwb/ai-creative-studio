package application

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/projects"
	"github.com/adcwb/ai-creative-studio/backend/internal/modules/series"
	"github.com/adcwb/ai-creative-studio/backend/internal/modules/story"
	"github.com/adcwb/ai-creative-studio/backend/internal/persistence/seriesrepo"
	"github.com/adcwb/ai-creative-studio/backend/internal/persistence/storyrepo"
)

func storyError(w http.ResponseWriter, e error) {
	switch {
	case errors.Is(e, story.ErrInput), errors.Is(e, projects.ErrInvalidProjectInput):
		problem(w, 400, "invalid_story_material", "故事资料、知识边界或来源证据无效，请保留草稿并核对引用")
	case errors.Is(e, story.ErrConflict):
		problem(w, 409, "story_version_conflict", "故事资料或来源版本已变化，请保留草稿并重新核对")
	case errors.Is(e, story.ErrNotFound), errors.Is(e, ErrNotFound):
		problem(w, 404, "not_found", "故事资料或版本不存在")
	case errors.Is(e, story.ErrDenied):
		problem(w, 403, "role_denied", "当前权限不能修改故事资料")
	case errors.Is(e, storyrepo.ErrKey):
		problem(w, 409, "idempotency_conflict", "同一请求键不能用于不同故事资料")
	case errors.Is(e, series.ErrInput), errors.Is(e, series.ErrConflict), errors.Is(e, series.ErrNotFound), errors.Is(e, series.ErrDenied), errors.Is(e, seriesrepo.ErrKey):
		seriesError(w, e)
	default:
		problem(w, 503, "story_storage_error", "故事资料结果未确认，请保留草稿并使用原请求核对")
	}
}
func storyLimit(r *http.Request) (int, error) {
	n := 20
	var e error
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, e = strconv.Atoi(raw)
	}
	if e != nil || n < 1 || n > 100 {
		return 0, story.ErrInput
	}
	return n, nil
}

type storyCursor struct{ Space, Root, Kind, Status, After string }

func registerStoryRoutes(mux *http.ServeMux, s *FileStore) {
	if s.story == nil {
		return
	}
	registerStoryEpisodeOptions(mux, s)
	registerWritingBeatsRoutes(mux, s)
	registerStorySnapshotRoutes(mux, s)
	registerStoryCheckRoutes(mux, s)
	registerStoryCastRoutes(mux, s)
	registerStoryEvidenceRoutes(mux, s)
	scope := func(w http.ResponseWriter, r *http.Request, write bool) (story.Actor, string, bool) {
		var space, key string
		var ok bool
		if write {
			space, key, ok = projectWriteScope(w, r)
		} else {
			space, ok = spaceID(w, r)
		}
		if !ok {
			return story.Actor{}, "", false
		}
		root := r.PathValue("id")
		if _, e := s.series.Get(r.Context(), space, "series", root); e != nil {
			seriesError(w, e)
			return story.Actor{}, "", false
		}
		a := seriesActorFor(r, space)
		return story.Actor{Space: space, User: a.User, Writable: a.Writable}, key, true
	}
	base := "/api/v1/series/{id}/story-records"
	mux.HandleFunc("GET "+base, func(w http.ResponseWriter, r *http.Request) {
		a, _, ok := scope(w, r, false)
		if !ok {
			return
		}
		limit, e := storyLimit(r)
		if e != nil {
			storyError(w, e)
			return
		}
		q := storyrepo.Query{Kind: r.URL.Query().Get("kind"), Status: r.URL.Query().Get("status"), Limit: limit}
		root := r.PathValue("id")
		if raw := r.URL.Query().Get("cursor"); raw != "" {
			if len(raw) > 1024 {
				storyError(w, story.ErrInput)
				return
			}
			b, e := base64.RawURLEncoding.DecodeString(raw)
			var c storyCursor
			if e != nil || json.Unmarshal(b, &c) != nil || c.Space != a.Space || c.Root != root || c.Kind != q.Kind || c.Status != q.Status {
				storyError(w, story.ErrInput)
				return
			}
			q.After = c.After
		}
		page, e := s.story.List(r.Context(), storyrepo.Scope{Space: a.Space, Series: root}, q)
		if e != nil {
			storyError(w, e)
			return
		}
		if page.Next != "" {
			b, _ := json.Marshal(storyCursor{a.Space, root, q.Kind, q.Status, page.Next})
			page.Next = base64.RawURLEncoding.EncodeToString(b)
		}
		respond(w, 200, page)
	})
	save := func(create bool) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			a, key, ok := scope(w, r, true)
			if !ok {
				return
			}
			var in story.Input
			if !decodeProject(w, r, &in) {
				return
			}
			if create && in.ExpectedVersion != 0 || !create && in.ExpectedVersion < 1 {
				storyError(w, story.ErrInput)
				return
			}
			root, id := r.PathValue("id"), r.PathValue("record_id")
			if create {
				id = story.NewID(a, root, key)
				if in.Kind == "cast" && in.Body.Cast != nil {
					id = story.CastID(root, in.Body.Cast.EpisodeID)
				}
				if in.Kind == "outline" && in.Body.Outline != nil {
					id = story.OutlineID(root, in.Body.Outline.SeasonID, in.Body.Outline.EpisodeID)
				}
				if in.Kind == "bible" {
					id = storyrepo.BibleID(root)
				}
			}
			v, e := s.SaveStory(r.Context(), a, root, id, key, in)
			if e != nil {
				storyError(w, e)
				return
			}
			status := 200
			if create {
				status = 201
			}
			respond(w, status, v)
		}
	}
	mux.HandleFunc("POST "+base, save(true))
	mux.HandleFunc("PUT "+base+"/{record_id}", save(false))
	mux.HandleFunc("GET "+base+"/{record_id}", func(w http.ResponseWriter, r *http.Request) {
		a, _, ok := scope(w, r, false)
		if !ok {
			return
		}
		v, e := s.StoryVersion(r.Context(), storyrepo.Scope{Space: a.Space, Series: r.PathValue("id")}, r.PathValue("record_id"), "")
		if e != nil {
			storyError(w, e)
			return
		}
		respond(w, 200, v)
	})
	mux.HandleFunc("GET "+base+"/{record_id}/versions/{version_id}", func(w http.ResponseWriter, r *http.Request) {
		a, _, ok := scope(w, r, false)
		if !ok {
			return
		}
		v, e := s.StoryVersion(r.Context(), storyrepo.Scope{Space: a.Space, Series: r.PathValue("id")}, r.PathValue("record_id"), r.PathValue("version_id"))
		if e != nil {
			storyError(w, e)
			return
		}
		respond(w, 200, v)
	})
	mux.HandleFunc("GET "+base+"/{record_id}/versions", func(w http.ResponseWriter, r *http.Request) {
		a, _, ok := scope(w, r, false)
		if !ok {
			return
		}
		limit, e := storyLimit(r)
		if e != nil {
			storyError(w, e)
			return
		}
		var before int64
		if raw := r.URL.Query().Get("cursor"); raw != "" {
			before, e = strconv.ParseInt(raw, 10, 64)
			if e != nil || before < 1 || before > story.MaxVersion {
				storyError(w, story.ErrInput)
				return
			}
		}
		page, e := s.story.History(r.Context(), storyrepo.Scope{Space: a.Space, Series: r.PathValue("id")}, r.PathValue("record_id"), before, limit)
		if e != nil {
			storyError(w, e)
			return
		}
		respond(w, 200, page)
	})
	mux.HandleFunc("POST /api/v1/episodes/{id}/context-previews", func(w http.ResponseWriter, r *http.Request) {
		space, _, ok := projectWriteScope(w, r)
		if !ok {
			return
		}
		var in struct{}
		if !decodeProject(w, r, &in) {
			return
		}
		v, e := s.PreviewStoryContext(r.Context(), space, r.PathValue("id"))
		if e != nil {
			storyError(w, e)
			return
		}
		respond(w, 200, v)
	})
}
