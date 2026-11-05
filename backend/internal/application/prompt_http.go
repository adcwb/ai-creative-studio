package application

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/adcwb/ai-creative-studio/backend/internal/modules/prompts"
	"github.com/adcwb/ai-creative-studio/backend/internal/persistence/promptrepo"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

var promptReadActionPattern = regexp.MustCompile(`^/api/v1/prompt-templates/[a-f0-9]{32}/(preview|favorite)$`)

func promptReadAction(r *http.Request) bool {
	return promptReadActionPattern.MatchString(r.URL.Path) && ((r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/preview")) || (r.Method == "PUT" && strings.HasSuffix(r.URL.Path, "/favorite")))
}
func promptActor(r *http.Request, space string) prompts.Actor {
	if scope, ok := r.Context().Value(authScopeKey{}).(authScope); ok {
		return prompts.Actor{Space: space, User: scope.UserID, Writable: scope.Role != "" && scope.Role != "viewer", Manage: scope.Role == "owner" || scope.Role == "admin"}
	}
	return prompts.Actor{Space: space, User: "demo-user", Writable: true}
}
func promptError(w http.ResponseWriter, e error) {
	var field *prompts.FieldError
	if errors.As(e, &field) {
		respond(w, 400, map[string]any{"error": map[string]string{"code": field.Code, "message": "提示词变量或展开文本无效，请检查对应字段", "field": field.Field}})
		return
	}
	switch {
	case errors.Is(e, prompts.ErrInput):
		problem(w, 400, "invalid_prompt_parameter", "提示词名称、正文或参数无效，请检查配方字段")
	case errors.Is(e, prompts.ErrNotFound):
		problem(w, 404, "not_found", "提示词不存在或当前不可见")
	case errors.Is(e, prompts.ErrDenied):
		problem(w, 403, "role_denied", "当前权限不能修改此提示词")
	case errors.Is(e, prompts.ErrConflict):
		problem(w, 409, "prompt_version_conflict", "提示词已更新，请保留草稿并重新读取")
	case errors.Is(e, prompts.ErrArchived):
		problem(w, 409, "prompt_archived", "提示词已归档，请先恢复或选择其他提示词")
	case errors.Is(e, promptrepo.ErrKey):
		problem(w, 409, "idempotency_conflict", "同一请求键不能用于不同内容")
	default:
		problem(w, 503, "prompt_storage_error", "保存结果未确认，请保留草稿并使用原请求键核对")
	}
}

type promptCursor struct{ Space, Actor, Filter, After string }

func promptPage(r *http.Request, a prompts.Actor, filter string) (string, int, error) {
	limit := 20
	var e error
	if raw := r.URL.Query().Get("limit"); raw != "" {
		limit, e = strconv.Atoi(raw)
		if e != nil || limit < 1 || limit > 100 {
			return "", 0, prompts.ErrInput
		}
	}
	after := ""
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		if len(raw) > 1024 {
			return "", 0, prompts.ErrInput
		}
		b, e := base64.RawURLEncoding.DecodeString(raw)
		var c promptCursor
		if e != nil || json.Unmarshal(b, &c) != nil || c.Space != a.Space || c.Actor != a.User || c.Filter != filter {
			return "", 0, prompts.ErrInput
		}
		after = c.After
	}
	return after, limit, nil
}
func promptNext(a prompts.Actor, filter, after string) string {
	if after == "" {
		return ""
	}
	b, _ := json.Marshal(promptCursor{a.Space, a.User, filter, after})
	return base64.RawURLEncoding.EncodeToString(b)
}
func registerPromptRoutes(mux *http.ServeMux, s *FileStore) {
	if s.prompts == nil {
		return
	}
	base := "/api/v1/prompt-templates"
	scope := func(w http.ResponseWriter, r *http.Request) (prompts.Actor, bool) {
		space, ok := spaceID(w, r)
		return promptActor(r, space), ok
	}
	writeScope := func(w http.ResponseWriter, r *http.Request) (prompts.Actor, string, bool) {
		space, key, ok := projectWriteScope(w, r)
		return promptActor(r, space), key, ok
	}
	mux.HandleFunc("GET "+base, func(w http.ResponseWriter, r *http.Request) {
		a, ok := scope(w, r)
		if !ok {
			return
		}
		v := r.URL.Query()
		q := promptrepo.Query{Text: strings.TrimSpace(v.Get("q")), Category: v.Get("category"), Tag: v.Get("tag"), Source: v.Get("source"), Status: v.Get("status"), Favorite: v.Get("favorite") == "true"}
		if q.Status == "" {
			q.Status = "active"
		}
		if utf8.RuneCountInString(q.Text) > 100 || utf8.RuneCountInString(q.Category) > 80 || utf8.RuneCountInString(q.Tag) > 40 || (v.Get("favorite") != "" && v.Get("favorite") != "true" && v.Get("favorite") != "false") {
			promptError(w, prompts.ErrInput)
			return
		}
		filter := prompts.Digest(q)
		after, limit, e := promptPage(r, a, filter)
		if e != nil {
			promptError(w, e)
			return
		}
		q.After, q.Limit = after, limit
		items, e := s.prompts.List(r.Context(), a, q)
		if e != nil {
			promptError(w, e)
			return
		}
		next := ""
		if len(items) == limit {
			next = promptNext(a, filter, items[len(items)-1].Template.ID)
		}
		respond(w, 200, map[string]any{"items": items, "next_cursor": next})
	})
	mux.HandleFunc("GET "+base+"/{templateId}", func(w http.ResponseWriter, r *http.Request) {
		a, ok := scope(w, r)
		if !ok {
			return
		}
		out, e := s.prompts.Get(r.Context(), a, r.PathValue("templateId"))
		if e != nil {
			promptError(w, e)
			return
		}
		respond(w, 200, out)
	})
	mux.HandleFunc("GET "+base+"/{templateId}/versions", func(w http.ResponseWriter, r *http.Request) {
		a, ok := scope(w, r)
		if !ok {
			return
		}
		filter := "versions:" + r.PathValue("templateId")
		after, limit, e := promptPage(r, a, filter)
		if e != nil {
			promptError(w, e)
			return
		}
		items, next, e := s.prompts.Versions(r.Context(), a, r.PathValue("templateId"), after, limit)
		if e != nil {
			promptError(w, e)
			return
		}
		respond(w, 200, map[string]any{"items": items, "next_cursor": promptNext(a, filter, next)})
	})
	mux.HandleFunc("GET "+base+"/{templateId}/versions/{versionId}", func(w http.ResponseWriter, r *http.Request) {
		a, ok := scope(w, r)
		if !ok {
			return
		}
		out, e := s.prompts.Version(r.Context(), a, r.PathValue("templateId"), r.PathValue("versionId"))
		if e != nil {
			promptError(w, e)
			return
		}
		respond(w, 200, out)
	})
	mux.HandleFunc("POST "+base, func(w http.ResponseWriter, r *http.Request) {
		a, key, ok := writeScope(w, r)
		if !ok {
			return
		}
		var in struct {
			prompts.Metadata
			Version prompts.Recipe `json:"version"`
		}
		if !decodeProject(w, r, &in) {
			return
		}
		if _, e := s.promptReferences(r.Context(), a.Space, in.Version); e != nil {
			promptError(w, e)
			return
		}
		id := libraryStableID(a.Space, a.User+":"+key, "prompt")
		out, e := s.prompts.Mutate(r.Context(), a, id, key, promptrepo.Command{Operation: "create", Metadata: in.Metadata, Recipe: in.Version})
		if e != nil {
			promptError(w, e)
			return
		}
		respond(w, 201, out)
	})
	mux.HandleFunc("PATCH "+base+"/{templateId}", func(w http.ResponseWriter, r *http.Request) {
		a, key, ok := writeScope(w, r)
		if !ok {
			return
		}
		var in struct {
			prompts.Metadata
			Expected int64  `json:"expected_row_version"`
			Status   string `json:"status"`
		}
		if !decodeProject(w, r, &in) {
			return
		}
		out, e := s.prompts.Mutate(r.Context(), a, r.PathValue("templateId"), key, promptrepo.Command{Operation: "metadata", Metadata: in.Metadata, ExpectedRow: in.Expected, Status: in.Status})
		if e != nil {
			promptError(w, e)
			return
		}
		respond(w, 200, out)
	})
	mux.HandleFunc("POST "+base+"/{templateId}/versions", func(w http.ResponseWriter, r *http.Request) {
		a, key, ok := writeScope(w, r)
		if !ok {
			return
		}
		var in struct {
			Metadata    *prompts.Metadata `json:"metadata,omitempty"`
			ExpectedRow int64             `json:"expected_row_version,omitempty"`
			Expected    string            `json:"expected_head_version_id"`
			Version     prompts.Recipe    `json:"version"`
		}
		if !decodeProject(w, r, &in) {
			return
		}
		if _, e := s.promptReferences(r.Context(), a.Space, in.Version); e != nil {
			promptError(w, e)
			return
		}
		cmd := promptrepo.Command{Operation: "version", Recipe: in.Version, ExpectedHead: in.Expected, ExpectedRow: in.ExpectedRow}
		if in.Metadata != nil {
			cmd.Metadata = *in.Metadata
			cmd.ChangeMetadata = true
		}
		out, e := s.prompts.Mutate(r.Context(), a, r.PathValue("templateId"), key, cmd)
		if e != nil {
			promptError(w, e)
			return
		}
		respond(w, 201, out)
	})
	mux.HandleFunc("POST "+base+"/{templateId}/copies", func(w http.ResponseWriter, r *http.Request) {
		a, key, ok := writeScope(w, r)
		if !ok {
			return
		}
		var in struct {
			VersionID  string `json:"version_id"`
			Name       string `json:"name"`
			Visibility string `json:"visibility"`
		}
		if !decodeProject(w, r, &in) {
			return
		}
		source, e := s.prompts.Get(r.Context(), a, r.PathValue("templateId"))
		if e != nil {
			promptError(w, e)
			return
		}
		v, e := s.prompts.Version(r.Context(), a, source.Template.ID, in.VersionID)
		if e != nil {
			promptError(w, e)
			return
		}
		m := source.Template.Metadata
		m.Name, m.Visibility = in.Name, in.Visibility
		id := libraryStableID(a.Space, a.User+":"+key, "copy-prompt:"+source.Template.ID)
		out, e := s.prompts.Mutate(r.Context(), a, id, key, promptrepo.Command{Operation: "create", RequestIdentity: prompts.Digest(in), Metadata: m, Recipe: v.Recipe})
		if e != nil {
			promptError(w, e)
			return
		}
		respond(w, 201, out)
	})
	mux.HandleFunc("POST "+base+"/{templateId}/admin-archive", func(w http.ResponseWriter, r *http.Request) {
		a, key, ok := writeScope(w, r)
		if !ok {
			return
		}
		if !a.Manage {
			promptError(w, prompts.ErrDenied)
			return
		}
		var in struct {
			Expected int64 `json:"expected_row_version"`
		}
		if !decodeProject(w, r, &in) {
			return
		}
		out, e := s.prompts.Mutate(r.Context(), a, r.PathValue("templateId"), key, promptrepo.Command{Operation: "admin_archive", ExpectedRow: in.Expected})
		if e != nil {
			promptError(w, e)
			return
		}
		respond(w, 200, map[string]any{"id": out.Template.ID, "status": out.Template.Status, "row_version": out.Template.RowVersion})
	})
	mux.HandleFunc("PUT "+base+"/{templateId}/favorite", func(w http.ResponseWriter, r *http.Request) {
		a, _, ok := writeScope(w, r)
		if !ok {
			return
		}
		var in struct {
			Enabled bool `json:"enabled"`
		}
		if !decodeProject(w, r, &in) {
			return
		}
		if e := s.prompts.Favorite(r.Context(), a, r.PathValue("templateId"), in.Enabled); e != nil {
			promptError(w, e)
			return
		}
		respond(w, 200, map[string]bool{"enabled": in.Enabled})
	})
	mux.HandleFunc("POST "+base+"/{templateId}/preview", func(w http.ResponseWriter, r *http.Request) {
		a, ok := scope(w, r)
		if !ok {
			return
		}
		var in struct {
			VersionID string            `json:"version_id"`
			Variables map[string]string `json:"variables,omitempty"`
		}
		if !decodeProject(w, r, &in) {
			return
		}
		out, e := s.prompts.Get(r.Context(), a, r.PathValue("templateId"))
		if e != nil {
			promptError(w, e)
			return
		}
		if out.Template.Status != "active" {
			promptError(w, prompts.ErrArchived)
			return
		}
		v, e := s.prompts.Version(r.Context(), a, out.Template.ID, in.VersionID)
		if e != nil {
			promptError(w, e)
			return
		}
		compiled, e := prompts.Compile(v.Recipe, in.Variables)
		if e != nil {
			promptError(w, e)
			return
		}
		respond(w, 200, compiled)
	})
}
