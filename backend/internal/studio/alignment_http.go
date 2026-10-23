package studio

import (
	"errors"
	"net/http"
)

func alignmentError(w http.ResponseWriter, err error) {
	if errors.Is(err, ErrProviderNotReady) || errors.Is(err, ErrProviderChanged) || errors.Is(err, ErrGenerationActive) || errors.Is(err, ErrConflict) {
		storeError(w, err)
		return
	}
	mediaError(w, err)
}
func registerAlignmentRoutes(mux *http.ServeMux, s *FileStore) {
	mux.HandleFunc("GET /api/v1/alignment-capabilities", func(w http.ResponseWriter, r *http.Request) { respond(w, 200, s.media.alignment.capabilities()) })
	scope := func(w http.ResponseWriter, r *http.Request, write bool) (string, string, bool) {
		if err := s.media.Available(); err != nil {
			alignmentError(w, err)
			return "", "", false
		}
		if err := s.media.alignment.available(); err != nil {
			alignmentError(w, err)
			return "", "", false
		}
		var space, key string
		var ok bool
		if write {
			space, key, ok = projectWriteScope(w, r)
		} else {
			space, ok = spaceID(w, r)
		}
		if !ok {
			return "", "", false
		}
		if _, err := s.GetProject(space, r.PathValue("id")); err != nil {
			storeError(w, err)
			return "", "", false
		}
		return space, key, true
	}
	base := "/api/v1/projects/{id}/media/alignments"
	mux.HandleFunc("GET "+base, func(w http.ResponseWriter, r *http.Request) {
		space, _, ok := scope(w, r, false)
		if !ok {
			return
		}
		respond(w, 200, s.media.alignment.list(r.PathValue("id"), space))
	})
	mux.HandleFunc("POST "+base, func(w http.ResponseWriter, r *http.Request) {
		space, key, ok := scope(w, r, true)
		if !ok {
			return
		}
		var in AlignmentInput
		if !decodeProject(w, r, &in) {
			return
		}
		if !resolveModelRequest(w, r, s, &in.ModelRequest, in.Mode) {
			return
		}
		if !validateModelPreview(w, r, s, in.ModelRequest, "alignment", r.PathValue("id"), in) {
			return
		}
		task, created, err := s.media.CreateAlignment(r.PathValue("id"), space, key, in)
		if err != nil {
			alignmentError(w, err)
			return
		}
		status := 200
		if created {
			status = 202
		}
		respond(w, status, task)
	})
	mux.HandleFunc("POST "+base+"/{taskId}/cancel", func(w http.ResponseWriter, r *http.Request) {
		space, key, ok := scope(w, r, true)
		if !ok {
			return
		}
		var in struct{}
		if !decodeProject(w, r, &in) {
			return
		}
		task, err := s.media.alignment.cancel(r.PathValue("id"), space, key, r.PathValue("taskId"))
		if err != nil {
			alignmentError(w, err)
			return
		}
		respond(w, 200, task)
	})
}
