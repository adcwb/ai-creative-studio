package studio

import "net/http"

func registerPosterRoutes(mux *http.ServeMux, s *FileStore) {
	scope := func(w http.ResponseWriter, r *http.Request) (string, string, bool) {
		if err := s.media.Available(); err != nil {
			mediaError(w, err)
			return "", "", false
		}
		space, key, ok := projectWriteScope(w, r)
		if !ok {
			return "", "", false
		}
		if _, err := s.GetProject(space, r.PathValue("id")); err != nil {
			storeError(w, err)
			return "", "", false
		}
		return space, key, true
	}
	mux.HandleFunc("PUT /api/v1/projects/{id}/media/poster", func(w http.ResponseWriter, r *http.Request) {
		space, key, ok := scope(w, r)
		if !ok {
			return
		}
		var in struct {
			ExpectedVersion int            `json:"expected_version"`
			Poster          PosterDocument `json:"poster"`
		}
		if !decodeProject(w, r, &in) {
			return
		}
		p, err := s.media.SavePoster(r.PathValue("id"), space, key, in.ExpectedVersion, in.Poster)
		if err != nil {
			mediaError(w, err)
			return
		}
		respond(w, 200, p)
	})
	mux.HandleFunc("POST /api/v1/projects/{id}/media/poster/exports", func(w http.ResponseWriter, r *http.Request) {
		space, key, ok := scope(w, r)
		if !ok {
			return
		}
		var in struct {
			ExpectedVersion int `json:"expected_version"`
		}
		if !decodeProject(w, r, &in) {
			return
		}
		p, err := s.media.ExportPoster(r.PathValue("id"), space, key, in.ExpectedVersion)
		if err != nil {
			mediaError(w, err)
			return
		}
		respond(w, 200, p)
	})
}
