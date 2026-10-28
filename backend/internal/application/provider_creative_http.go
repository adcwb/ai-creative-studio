package application

import (
	"net/http"
)

func registerCreativeRoutes(mux *http.ServeMux, s *FileStore) {
	mux.HandleFunc("GET /api/v1/creative-registry", func(w http.ResponseWriter, r *http.Request) { respond(w, 200, CreativeRegistry()) })
	mux.HandleFunc("GET /api/v1/creative-capabilities", func(w http.ResponseWriter, r *http.Request) { respond(w, 200, s.creative.caps) })
	mux.HandleFunc("GET /api/v1/creative-media-capabilities", func(w http.ResponseWriter, r *http.Request) { respond(w, 200, s.creative.mediaCaps()) })
	mux.HandleFunc("GET /api/v1/projects/{id}/creative-jobs", func(w http.ResponseWriter, r *http.Request) {
		space, ok := spaceID(w, r)
		if !ok {
			return
		}
		jobs, err := s.listCreativeJobs(space, r.PathValue("id"))
		if err != nil {
			storeError(w, err)
			return
		}
		respond(w, 200, jobs)
	})
	create := func(analysis bool) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			space, key, ok := projectWriteScope(w, r)
			if !ok {
				return
			}
			var in CreativeJobInput
			if analysis {
				var a ScriptGenerationInput
				if !decodeProject(w, r, &a) {
					return
				}
				in = CreativeJobInput{ModelRequest: a.ModelRequest, Kind: "analysis", ExpectedVersion: a.ExpectedVersion, ExpectedProvider: a.ExpectedProvider, ExpectedModel: a.ExpectedModel}
			} else {
				if !decodeProject(w, r, &in) {
					return
				}
			}
			if !resolveModelRequest(w, r, s, &in.ModelRequest, creativeRuntimeCapability(in.Kind)) {
				return
			}
			if !validateModelPreview(w, r, s, in.ModelRequest, "creative", r.PathValue("id"), in) {
				return
			}
			job, created, err := s.createCreativeJob(space, r.PathValue("id"), key, in)
			if err != nil {
				storeError(w, err)
				return
			}
			status := 200
			if created {
				status = 202
			}
			respond(w, status, job)
		}
	}
	mux.HandleFunc("POST /api/v1/projects/{id}/analysis", create(true))
	mux.HandleFunc("POST /api/v1/projects/{id}/creative-jobs", create(false))
	mux.HandleFunc("POST /api/v1/projects/{id}/creative-jobs/{jobId}/resume", func(w http.ResponseWriter, r *http.Request) {
		space, key, ok := projectWriteScope(w, r)
		if !ok {
			return
		}
		var in struct{}
		if !decodeProject(w, r, &in) {
			return
		}
		job, err := s.resumeCreativeJob(space, r.PathValue("id"), r.PathValue("jobId"), key)
		if err != nil {
			storeError(w, err)
			return
		}
		respond(w, 200, job)
	})
	mux.HandleFunc("POST /api/v1/projects/{id}/creative-jobs/{jobId}/cancel", func(w http.ResponseWriter, r *http.Request) {
		space, key, ok := projectWriteScope(w, r)
		if !ok {
			return
		}
		var in struct{}
		if !decodeProject(w, r, &in) {
			return
		}
		job, err := s.cancelCreativeJob(space, r.PathValue("id"), r.PathValue("jobId"), key)
		if err != nil {
			storeError(w, err)
			return
		}
		respond(w, 200, job)
	})
}
