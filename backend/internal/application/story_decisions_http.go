package application

import (
	"net/http"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/story"
)

func registerStoryDecisionRoutes(mux *http.ServeMux, s *FileStore) {
	base := "/api/v1/episodes/{id}/continuity-checks/{check_id}/findings/{finding_id}"
	mux.HandleFunc("GET "+base+"/review", func(w http.ResponseWriter, r *http.Request) {
		space, ok := spaceID(w, r)
		if !ok {
			return
		}
		out, e := s.reviewStoryFinding(r.Context(), space, r.PathValue("id"), r.PathValue("check_id"), r.PathValue("finding_id"))
		if e != nil {
			storyError(w, e)
			return
		}
		respond(w, 200, out)
	})
	mux.HandleFunc("POST "+base+"/decisions", func(w http.ResponseWriter, r *http.Request) {
		space, key, ok := projectWriteScope(w, r)
		if !ok {
			return
		}
		var in story.DecisionInput
		if !decodeProject(w, r, &in) {
			return
		}
		actor := seriesActorFor(r, space)
		out, e := s.decideStoryFinding(r.Context(), story.Actor{Space: space, User: actor.User, Writable: actor.Writable}, r.PathValue("id"), r.PathValue("check_id"), r.PathValue("finding_id"), key, in)
		if e != nil {
			storyError(w, e)
			return
		}
		respond(w, 201, out)
	})
}
