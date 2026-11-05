package application

import (
	"net/http"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/story"
)

func registerStorySnapshotRoutes(mux *http.ServeMux, s *FileStore) {
	base := "/api/v1/episodes/{id}/context-snapshots"
	mux.HandleFunc("POST "+base, func(w http.ResponseWriter, r *http.Request) {
		space, key, ok := projectWriteScope(w, r)
		if !ok {
			return
		}
		var in story.Selection
		if !decodeProject(w, r, &in) {
			return
		}
		actor := seriesActorFor(r, space)
		out, e := s.CreateStoryContext(r.Context(), story.Actor{Space: space, User: actor.User, Writable: actor.Writable}, r.PathValue("id"), key, in)
		if e != nil {
			storyError(w, e)
			return
		}
		respond(w, 201, out)
	})
	mux.HandleFunc("GET "+base, func(w http.ResponseWriter, r *http.Request) {
		space, ok := spaceID(w, r)
		if !ok {
			return
		}
		q, e := s.storySnapshotScope(r.Context(), space, r.PathValue("id"))
		if e != nil {
			storyError(w, e)
			return
		}
		limit, e := storyLimit(r)
		if e != nil {
			storyError(w, e)
			return
		}
		out, e := s.story.ListContexts(r.Context(), q, r.PathValue("id"), r.URL.Query().Get("cursor"), limit)
		if e != nil {
			storyError(w, e)
			return
		}
		respond(w, 200, out)
	})
	mux.HandleFunc("GET "+base+"/{snapshot_id}/freshness", func(w http.ResponseWriter, r *http.Request) {
		space, ok := spaceID(w, r)
		if !ok {
			return
		}
		out, e := s.CheckStoryContext(r.Context(), space, r.PathValue("id"), r.PathValue("snapshot_id"))
		if e != nil {
			storyError(w, e)
			return
		}
		respond(w, 200, out)
	})
	mux.HandleFunc("GET "+base+"/{snapshot_id}", func(w http.ResponseWriter, r *http.Request) {
		space, ok := spaceID(w, r)
		if !ok {
			return
		}
		q, e := s.storySnapshotScope(r.Context(), space, r.PathValue("id"))
		if e != nil {
			storyError(w, e)
			return
		}
		out, e := s.story.GetContext(r.Context(), q, r.PathValue("id"), r.PathValue("snapshot_id"))
		if e != nil {
			storyError(w, e)
			return
		}
		respond(w, 200, out)
	})
}
