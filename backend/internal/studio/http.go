package studio

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"unicode/utf8"
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
	mux.HandleFunc("GET /api/v1/templates", func(w http.ResponseWriter, r *http.Request) {
		respond(w, 200, Catalog())
	})
	mux.HandleFunc("GET /api/v1/capabilities", func(w http.ResponseWriter, r *http.Request) {
		respond(w, 200, repo.Capabilities())
	})
	registerProjectRoutes(mux, repo)
	registerStoryboardRoutes(mux, repo)
	if store, ok := repo.(*FileStore); ok {
		registerMediaRoutes(mux, store)
		registerCreativeRoutes(mux, store)
		registerImageResumeRoute(mux, store)
	}
	mux.HandleFunc("GET /api/v1/tasks", func(w http.ResponseWriter, r *http.Request) {
		space, ok := spaceID(w, r)
		if !ok {
			return
		}
		respond(w, 200, repo.List(space))
	})
	mux.HandleFunc("POST /api/v1/tasks", func(w http.ResponseWriter, r *http.Request) {
		createTask(w, r, repo, "")
	})
	mux.HandleFunc("POST /api/v1/tasks/{id}/retry", func(w http.ResponseWriter, r *http.Request) {
		createTask(w, r, repo, r.PathValue("id"))
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
		result, err := repo.ReadResult(space, r.PathValue("id"))
		if err != nil {
			storeError(w, err)
			return
		}
		mime := result.MIMEType
		if mime == "image/svg+xml" {
			mime += "; charset=utf-8"
		}
		w.Header().Set("Content-Type", mime)
		w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
		w.WriteHeader(200)
		_, _ = w.Write(result.Data)
	})
	mux.HandleFunc("GET /api/v1/assets", func(w http.ResponseWriter, r *http.Request) {
		space, ok := spaceID(w, r)
		if !ok {
			return
		}
		query := strings.TrimSpace(r.URL.Query().Get("q"))
		kind := r.URL.Query().Get("kind")
		if utf8.RuneCountInString(query) > 100 {
			problem(w, 400, "invalid_input", "素材搜索词不能超过 100 个字符")
			return
		}
		if kind != "" && kind != "poster" && kind != "portrait" && kind != "general" {
			problem(w, 400, "invalid_input", "素材类型必须为 poster、portrait 或 general")
			return
		}
		query = strings.ToLower(query)
		assets := []Asset{}
		for _, asset := range repo.Assets(space) {
			if (kind == "" || asset.Kind == kind) && strings.Contains(strings.ToLower(asset.Prompt), query) {
				assets = append(assets, asset)
			}
		}
		respond(w, 200, assets)
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
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, OPTIONS")
		}
		if r.Method == "OPTIONS" {
			w.WriteHeader(204)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func createTask(w http.ResponseWriter, r *http.Request, repo Repository, parentID string) {
	space, ok := spaceID(w, r)
	if !ok {
		return
	}
	key := r.Header.Get("Idempotency-Key")
	if !keyPattern.MatchString(key) {
		problem(w, 400, "invalid_key", "Idempotency-Key 需为 8–128 位字母、数字、下划线或连字符")
		return
	}
	var body struct {
		Prompt           string          `json:"prompt"`
		Kind             string          `json:"kind"`
		Ratio            json.RawMessage `json:"ratio"`
		Style            json.RawMessage `json:"style"`
		ExpectedProvider json.RawMessage `json:"expected_provider"`
		ExpectedModel    json.RawMessage `json:"expected_model"`
	}
	if !decode(w, r, &body) {
		return
	}
	in := Input{Prompt: body.Prompt, Kind: body.Kind}
	if !inputOption(w, "ratio", body.Ratio, &in.Ratio) || !inputOption(w, "style", body.Style, &in.Style) {
		return
	}
	var expectation []Expectation
	if len(body.ExpectedProvider) > 0 || len(body.ExpectedModel) > 0 {
		if len(body.ExpectedProvider) == 0 || len(body.ExpectedModel) == 0 {
			problem(w, 400, "invalid_input", "expected_provider 和 expected_model 必须成对提供")
			return
		}
		var expected Expectation
		if !inputOption(w, "expected_provider", body.ExpectedProvider, &expected.Provider) || !inputOption(w, "expected_model", body.ExpectedModel, &expected.Model) {
			return
		}
		if !((expected.Provider == "mock-svg" && expected.Model == "") || (expected.Provider == "openai-images" && expected.Model == OpenAIModel)) {
			problem(w, 400, "invalid_input", "生成模式确认参数无效")
			return
		}
		expectation = []Expectation{expected}
	}
	in, err := Normalize(in)
	if err != nil {
		problem(w, 400, "invalid_input", err.Error())
		return
	}
	var t Task
	var created bool
	if parentID == "" {
		t, created, err = repo.Create(space, key, in, expectation...)
	} else {
		t, created, err = repo.Retry(space, parentID, key, in, expectation...)
	}
	if err != nil {
		storeError(w, err)
		return
	}
	status := 200
	if created {
		status = 202
	}
	respond(w, status, t.Public())
}

// Decode optional parameters only at the request boundary. Input is embedded
// in persisted Task records, so a custom Input unmarshaler could swallow the
// outer task fields when restoring a snapshot.
func inputOption(w http.ResponseWriter, name string, raw json.RawMessage, value *string) bool {
	if len(raw) == 0 {
		return true
	}
	if strings.TrimSpace(string(raw)) == "null" || json.Unmarshal(raw, value) != nil {
		problem(w, 400, "invalid_input", name+" 必须为字符串，不能为 null")
		return false
	}
	return true
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
	return decodeLimited(w, r, v, 16<<10)
}
func decodeLimited(w http.ResponseWriter, r *http.Request, v any, limit int64) bool {
	if r.Header.Get("Content-Type") != "application/json" {
		problem(w, 415, "invalid_content_type", "请使用 application/json")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, limit)
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
	case errors.Is(err, ErrInvalidProjectInput):
		problem(w, 400, "invalid_input", err.Error())
	case errors.Is(err, ErrIdempotencyConflict):
		problem(w, 409, "idempotency_conflict", ErrIdempotencyConflict.Error())
	case errors.Is(err, ErrVersionConflict):
		problem(w, 409, "version_conflict", ErrVersionConflict.Error())
	case errors.Is(err, ErrGenerationActive):
		problem(w, 409, "generation_active", ErrGenerationActive.Error())
	case errors.Is(err, ErrStoryboardMissing):
		problem(w, 409, "storyboard_missing", ErrStoryboardMissing.Error())
	case errors.Is(err, ErrScriptMissing):
		problem(w, 409, "script_missing", ErrScriptMissing.Error())
	case errors.Is(err, ErrNotFound):
		problem(w, 404, "not_found", err.Error())
	case errors.Is(err, ErrConflict):
		problem(w, 409, "conflict", err.Error())
	case errors.Is(err, ErrProviderNotReady):
		problem(w, 503, "provider_not_ready", ErrProviderNotReady.Error())
	case errors.Is(err, ErrProviderChanged):
		problem(w, 409, "provider_changed", ErrProviderChanged.Error())
	default:
		slog.Error("storage operation failed", "error", err)
		problem(w, 500, "storage_error", "存储暂不可用，请稍后重试")
	}
}
