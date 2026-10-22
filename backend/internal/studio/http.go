package studio

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"regexp"
)

var keyPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{8,128}$`)

func Handler(repo Repository, origin string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		respond(w, 200, map[string]string{"status": "ok", "mode": "demo"})
	})
	mux.HandleFunc("GET /api/v1/spaces", func(w http.ResponseWriter, r *http.Request) {
		respond(w, 200, []map[string]string{{"id": "personal-demo", "name": "个人演示空间", "type": "personal", "owner_id": "demo-user"}, {"id": "team-demo", "name": "团队演示空间", "type": "team", "owner_id": "demo-team"}})
	})
	mux.HandleFunc("GET /api/v1/tasks", func(w http.ResponseWriter, r *http.Request) {
		space, ok := spaceID(w, r)
		if !ok {
			return
		}
		respond(w, 200, repo.List(space))
	})
	mux.HandleFunc("POST /api/v1/tasks", func(w http.ResponseWriter, r *http.Request) {
		space, ok := spaceID(w, r)
		if !ok {
			return
		}
		key := r.Header.Get("Idempotency-Key")
		if !keyPattern.MatchString(key) {
			problem(w, 400, "invalid_key", "Idempotency-Key 需为 8–128 位字母、数字、下划线或连字符")
			return
		}
		var in Input
		if !decode(w, r, &in) {
			return
		}
		in, err := Normalize(in)
		if err != nil {
			problem(w, 400, "invalid_input", err.Error())
			return
		}
		t, created, err := repo.Create(space, key, in)
		if err != nil {
			storeError(w, err)
			return
		}
		status := 200
		if created {
			status = 202
		}
		respond(w, status, t.Public())
	})
	mux.HandleFunc("GET /api/v1/tasks/{id}", func(w http.ResponseWriter, r *http.Request) {
		space, ok := spaceID(w, r)
		if !ok {
			return
		}
		t, err := repo.Get(space, r.PathValue("id"))
		if err != nil {
			storeError(w, err)
			return
		}
		respond(w, 200, t.Public())
	})
	mux.HandleFunc("GET /api/v1/tasks/{id}/result", func(w http.ResponseWriter, r *http.Request) {
		space, ok := spaceID(w, r)
		if !ok {
			return
		}
		t, err := repo.Get(space, r.PathValue("id"))
		if err != nil {
			storeError(w, err)
			return
		}
		if t.Status != "succeeded" {
			storeError(w, ErrConflict)
			return
		}
		w.Header().Set("Content-Type", "image/svg+xml; charset=utf-8")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
		w.WriteHeader(200)
		_, _ = io.WriteString(w, t.SVG)
	})
	mux.HandleFunc("GET /api/v1/assets", func(w http.ResponseWriter, r *http.Request) {
		space, ok := spaceID(w, r)
		if !ok {
			return
		}
		respond(w, 200, repo.Assets(space))
	})
	mux.HandleFunc("POST /api/v1/assets", func(w http.ResponseWriter, r *http.Request) {
		space, ok := spaceID(w, r)
		if !ok {
			return
		}
		var in struct {
			TaskID string `json:"task_id"`
		}
		if !decode(w, r, &in) {
			return
		}
		a, err := repo.SaveAsset(space, in.TaskID)
		if err != nil {
			storeError(w, err)
			return
		}
		respond(w, 200, a)
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
		if o := r.Header.Get("Origin"); o != "" {
			if o != origin {
				problem(w, 403, "origin_denied", "不允许的浏览器来源")
				return
			}
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Idempotency-Key")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		}
		if r.Method == "OPTIONS" {
			w.WriteHeader(204)
			return
		}
		mux.ServeHTTP(w, r)
	})
}
func spaceID(w http.ResponseWriter, r *http.Request) (string, bool) {
	s := r.URL.Query().Get("space_id")
	if s != "personal-demo" && s != "team-demo" {
		problem(w, 400, "invalid_space", "请选择个人或团队演示空间")
		return "", false
	}
	return s, true
}
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if r.Header.Get("Content-Type") != "application/json" {
		problem(w, 415, "invalid_content_type", "请使用 application/json")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		problem(w, 400, "invalid_json", "请求 JSON 无效或过大")
		return false
	}
	if err := d.Decode(new(any)); err != io.EOF {
		problem(w, 400, "invalid_json", "仅允许一个 JSON 对象")
		return false
	}
	return true
}
func respond(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func problem(w http.ResponseWriter, status int, code, message string) {
	respond(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}
func storeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		problem(w, 404, "not_found", err.Error())
	case errors.Is(err, ErrConflict):
		problem(w, 409, "conflict", err.Error())
	default:
		slog.Error("storage operation failed", "error", err)
		problem(w, 500, "storage_error", "存储暂不可用，请稍后重试")
	}
}
