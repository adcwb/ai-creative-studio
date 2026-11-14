package application

import (
	"net/http"
)

func registerCreativeRoutes(mux *http.ServeMux, s *FileStore) {
	registerDigitalHumanRoutes(mux, s)
	registerCreativeControls(mux, s)
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
		for i := range jobs {
			jobs[i] = publicCreativeJob(jobs[i])
		}
		respond(w, 200, jobs)
	})
	mux.HandleFunc("GET /api/v1/projects/{id}/creative-jobs/{jobId}", func(w http.ResponseWriter, r *http.Request) {
		space, ok := spaceID(w, r)
		if !ok {
			return
		}
		if _, e := s.GetProject(space, r.PathValue("id")); e != nil {
			storeError(w, e)
			return
		}
		s.creative.mu.Lock()
		job, ok := s.creative.data.Jobs[r.PathValue("jobId")]
		job = cloneCreativeJob(job)
		s.creative.mu.Unlock()
		if !ok || job.SpaceID != space || job.ProjectID != r.PathValue("id") {
			storeError(w, ErrNotFound)
			return
		}
		job.RuntimeSelection = nil
		respond(w, 200, publicCreativeJob(job))
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
			if !s.resolveCreativeLibraryVoice(w, r, space, r.PathValue("id"), &in) {
				return
			}
			if !s.resolveCreativePrompt(w, r, space, r.PathValue("id"), key, &in) {
				return
			}
			if !resolveModelRequest(w, r, s, &in.ModelRequest, creativeRuntimeCapability(in.Kind), operationFeature("creative", &in)) {
				return
			}
			scope, _ := r.Context().Value(authScopeKey{}).(authScope)
			in.consentActor = scope.UserID
			job, created, err := s.createCreativeJob(space, r.PathValue("id"), key, in)
			if err != nil {
				storeError(w, err)
				return
			}
			status := 200
			if created {
				status = 202
			}
			respond(w, status, publicCreativeJob(job))
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
		respond(w, 200, publicCreativeJob(job))
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
		respond(w, 200, publicCreativeJob(job))
	})
}
