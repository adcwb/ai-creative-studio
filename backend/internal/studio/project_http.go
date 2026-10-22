package studio

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
)

// Project requests require every field and forbid null, including nested
// fields. Check tokens before decoding into Go values: a repeated string
// field followed by null would otherwise leave its previous value unchanged.
func decodeProject(w http.ResponseWriter, r *http.Request, value any) bool {
	var raw json.RawMessage
	if !decodeLimited(w, r, &raw, 64<<10) {
		return false
	}
	tokens := json.NewDecoder(bytes.NewReader(raw))
	for {
		token, err := tokens.Token()
		if err == io.EOF {
			break
		}
		if err != nil || token == nil {
			problem(w, 400, "invalid_json", "项目 JSON 字段不能为 null")
			return false
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		problem(w, 400, "invalid_json", "项目请求 JSON 字段或类型无效")
		return false
	}
	return true
}

func projectWriteScope(w http.ResponseWriter, r *http.Request) (string, string, bool) {
	space, ok := spaceID(w, r)
	if !ok {
		return "", "", false
	}
	key := r.Header.Get("Idempotency-Key")
	if !keyPattern.MatchString(key) {
		problem(w, 400, "invalid_key", "Idempotency-Key 需为 8–128 位字母、数字、下划线或连字符")
		return "", "", false
	}
	return space, key, true
}

func registerProjectRoutes(mux *http.ServeMux, repo ProjectRepository) {
	mux.HandleFunc("GET /api/v1/projects", func(w http.ResponseWriter, r *http.Request) {
		space, ok := spaceID(w, r)
		if !ok {
			return
		}
		respond(w, 200, repo.ListProjects(space))
	})
	mux.HandleFunc("POST /api/v1/projects", func(w http.ResponseWriter, r *http.Request) {
		space, key, ok := projectWriteScope(w, r)
		if !ok {
			return
		}
		var in ProjectInput
		if !decodeProject(w, r, &in) {
			return
		}
		project, created, err := repo.CreateProject(space, key, in)
		if err != nil {
			storeError(w, err)
			return
		}
		status := 200
		if created {
			status = 201
		}
		respond(w, status, project)
	})
	mux.HandleFunc("GET /api/v1/projects/{id}", func(w http.ResponseWriter, r *http.Request) {
		space, ok := spaceID(w, r)
		if !ok {
			return
		}
		project, err := repo.GetProject(space, r.PathValue("id"))
		if err != nil {
			storeError(w, err)
			return
		}
		respond(w, 200, project)
	})
	mux.HandleFunc("PUT /api/v1/projects/{id}", func(w http.ResponseWriter, r *http.Request) {
		space, key, ok := projectWriteScope(w, r)
		if !ok {
			return
		}
		var in ProjectUpdate
		if !decodeProject(w, r, &in) {
			return
		}
		project, err := repo.UpdateProject(space, r.PathValue("id"), key, in)
		if err != nil {
			storeError(w, err)
			return
		}
		respond(w, 200, project)
	})
	mux.HandleFunc("GET /api/v1/projects/{id}/generations", func(w http.ResponseWriter, r *http.Request) {
		space, ok := spaceID(w, r)
		if !ok {
			return
		}
		generations, err := repo.ListScriptGenerations(space, r.PathValue("id"))
		if err != nil {
			storeError(w, err)
			return
		}
		respond(w, 200, generations)
	})
	mux.HandleFunc("POST /api/v1/projects/{id}/generations", func(w http.ResponseWriter, r *http.Request) {
		space, key, ok := projectWriteScope(w, r)
		if !ok {
			return
		}
		var in ScriptGenerationInput
		if !decodeProject(w, r, &in) {
			return
		}
		generation, created, err := repo.CreateScriptGeneration(space, r.PathValue("id"), key, in)
		if err != nil {
			storeError(w, err)
			return
		}
		status := 200
		if created {
			status = 202
		}
		respond(w, status, generation)
	})
	mux.HandleFunc("PUT /api/v1/projects/{id}/script", func(w http.ResponseWriter, r *http.Request) {
		space, key, ok := projectWriteScope(w, r)
		if !ok {
			return
		}
		var in ScriptSaveInput
		if !decodeProject(w, r, &in) {
			return
		}
		project, err := repo.SaveProjectScript(space, r.PathValue("id"), key, in)
		if err != nil {
			storeError(w, err)
			return
		}
		respond(w, 200, project)
	})
}
