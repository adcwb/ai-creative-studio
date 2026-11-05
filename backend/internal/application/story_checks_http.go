package application

import (
	"context"
	"net/http"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/story"
)

func registerStoryCheckRoutes(mux *http.ServeMux, s *FileStore) {
	registerStoryDecisionRoutes(mux, s)
	base := "/api/v1/episodes/{id}/continuity-checks"
	mux.HandleFunc("POST "+base, func(w http.ResponseWriter, r *http.Request) {
		space, key, ok := projectWriteScope(w, r)
		if !ok {
			return
		}
		var in story.CheckInput
		if !decodeProject(w, r, &in) {
			return
		}
		if in.ModelID == "" {
			problem(w, 409, "gateway_model_required", "请配置并选择此功能的网关模型")
			return
		}
		actor := seriesActorFor(r, space)
		a := story.Actor{Space: space, User: actor.User, Writable: actor.Writable}
		q, e := s.storySnapshotScope(r.Context(), space, r.PathValue("id"))
		if e != nil {
			storyError(w, e)
			return
		}
		if replay, found, e := s.story.ReplayCheck(r.Context(), a, q.Series, r.PathValue("id"), key, in); e != nil || found {
			if e != nil {
				storyError(w, e)
			} else {
				respond(w, 202, replay.Check)
			}
			return
		}
		model := ModelRequest{ModelConfigID: in.ModelID, ModelConfigVersion: in.ModelVersion}
		guarded := r.WithContext(context.WithValue(r.Context(), gatewayCreationKey{}, true))
		if !resolveModelRequest(w, guarded, s, &model, "text", "story_continuity") {
			return
		}
		out, e := s.acceptStoryCheck(r.Context(), a, r.PathValue("id"), key, in, model)
		if e != nil {
			storyError(w, e)
			return
		}
		s.wakeStoryChecks()
		respond(w, 202, out)
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
		out, e := s.story.ListChecks(r.Context(), q, r.PathValue("id"), r.URL.Query().Get("cursor"), limit)
		if e != nil {
			storyError(w, e)
			return
		}
		respond(w, 200, out)
	})
	mux.HandleFunc("GET "+base+"/{check_id}", func(w http.ResponseWriter, r *http.Request) {
		space, ok := spaceID(w, r)
		if !ok {
			return
		}
		q, e := s.storySnapshotScope(r.Context(), space, r.PathValue("id"))
		if e != nil {
			storyError(w, e)
			return
		}
		out, _, e := s.verifiedStoryCheck(r.Context(), q, r.PathValue("id"), r.PathValue("check_id"))
		if e != nil {
			storyError(w, e)
			return
		}
		respond(w, 200, out)
	})
}
