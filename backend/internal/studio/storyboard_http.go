package studio

import "net/http"

func registerStoryboardRoutes(mux *http.ServeMux, repo StoryboardRepository) {
	mux.HandleFunc("GET /api/v1/projects/{id}/storyboard-generations", func(w http.ResponseWriter, r *http.Request) {
		space, ok := spaceID(w, r)
		if !ok {
			return
		}
		list, err := repo.ListStoryboardGenerations(space, r.PathValue("id"))
		if err != nil {
			storeError(w, err)
			return
		}
		respond(w, 200, list)
	})
	mux.HandleFunc("POST /api/v1/projects/{id}/storyboard-generations", func(w http.ResponseWriter, r *http.Request) {
		space, key, ok := projectWriteScope(w, r)
		if !ok {
			return
		}
		var in ScriptGenerationInput
		if !decodeProject(w, r, &in) {
			return
		}
		if !resolveModelRequest(w, r, repo, &in.ModelRequest, "text") {
			return
		}
		if !validateModelPreview(w, r, repo, in.ModelRequest, "storyboard", r.PathValue("id"), in) {
			return
		}
		g, created, err := repo.CreateStoryboardGeneration(space, r.PathValue("id"), key, in)
		if err != nil {
			storeError(w, err)
			return
		}
		status := 200
		if created {
			status = 202
		}
		respond(w, status, g)
	})
	mux.HandleFunc("PUT /api/v1/projects/{id}/storyboard", func(w http.ResponseWriter, r *http.Request) {
		space, key, ok := projectWriteScope(w, r)
		if !ok {
			return
		}
		var in StoryboardSaveInput
		if !decodeProject(w, r, &in) {
			return
		}
		p, err := repo.SaveProjectStoryboard(space, r.PathValue("id"), key, in)
		if err != nil {
			storeError(w, err)
			return
		}
		respond(w, 200, p)
	})
}
