package application

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/revisions"
	"github.com/adcwb/ai-creative-studio/backend/internal/persistence/revisionrepo"
)

func revisionError(w http.ResponseWriter, e error) {
	switch {
	case errors.Is(e, revisions.ErrInput):
		problem(w, 400, "invalid_revision", "请选择有效的制作分支、修订名称和来源版本")
	case errors.Is(e, revisionrepo.ErrNotFound):
		problem(w, 404, "not_found", "制作分支或修订不存在")
	case errors.Is(e, revisions.ErrConflict):
		problem(w, 409, "revision_version_conflict", "分支已有新修订，请保留说明并重新核对")
	case errors.Is(e, revisionrepo.ErrKey):
		problem(w, 409, "idempotency_conflict", "原请求键不能改用于另一份修订")
	case errors.Is(e, ErrVersionConflict), errors.Is(e, ErrNotFound):
		storeError(w, e)
	case errors.Is(e, errMediaVersion):
		mediaError(w, e)
	case errors.Is(e, errRuntimeDenied):
		problem(w, 403, "role_denied", "当前权限不能保存制作修订")
	default:
		problem(w, 503, "revision_storage_error", "修订结果未确认，请保留选择并核对原请求")
	}
}

type revisionCursor struct{ Space, Project, Kind, Branch, After string }

func publicRevisionMutation(v revisionrepo.Record) any {
	id := v.Branch.ID
	if v.Revision != nil {
		id = v.Revision.ID
	}
	return struct {
		ID       string              `json:"id"`
		Branch   revisions.Branch    `json:"branch"`
		Revision *revisions.Revision `json:"revision,omitempty"`
	}{id, v.Branch, v.Revision}
}

func revisionPageInput(w http.ResponseWriter, r *http.Request, space, kind, branch string) (string, int, bool) {
	limit := 20
	var e error
	if v := r.URL.Query().Get("limit"); v != "" {
		limit, e = strconv.Atoi(v)
		if e != nil || limit < 1 || limit > 100 {
			revisionError(w, revisions.ErrInput)
			return "", 0, false
		}
	}
	after := ""
	if v := r.URL.Query().Get("cursor"); v != "" {
		var c revisionCursor
		raw, e := base64.RawURLEncoding.DecodeString(v)
		if len(v) > 1024 || e != nil || json.Unmarshal(raw, &c) != nil || c.Space != space || c.Project != r.PathValue("id") || c.Kind != kind || c.Branch != branch {
			revisionError(w, revisions.ErrInput)
			return "", 0, false
		}
		after = c.After
	}
	return after, limit, true
}
func revisionNext(space, project, kind, branch, after string) string {
	if after == "" {
		return ""
	}
	raw, _ := json.Marshal(revisionCursor{space, project, kind, branch, after})
	return base64.RawURLEncoding.EncodeToString(raw)
}
func registerRevisionRoutes(mux *http.ServeMux, s *FileStore) {
	registerRevisionRestoreRoutes(mux, s)
	base := "/api/v1/projects/{id}"
	scope := func(w http.ResponseWriter, r *http.Request, write bool) (string, string, bool) {
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
		if _, e := s.GetProject(space, r.PathValue("id")); e != nil {
			storeError(w, e)
			return "", "", false
		}
		return space, key, true
	}
	mux.HandleFunc("GET "+base+"/revision-options", func(w http.ResponseWriter, r *http.Request) {
		space, _, ok := scope(w, r, false)
		if !ok {
			return
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		p, e := s.projectLocked(space, r.PathValue("id"))
		if e != nil {
			revisionError(w, e)
			return
		}
		m := s.media
		m.mu.Lock()
		defer m.mu.Unlock()
		if m.failure != nil {
			revisionError(w, m.failure)
			return
		}
		if m.pg != nil && m.pg.err != nil {
			revisionError(w, m.pg.err)
			return
		}
		state := mediaState(m.data, mediaKey(space, p.ID))
		branch := r.URL.Query().Get("branch_id")
		if branch == "" {
			branch = "main"
		}
		b, e := s.revisions.Branch(r.Context(), revisionrepo.Scope{Space: space, Project: p.ID}, branch)
		var head *revisions.Branch
		if e == nil {
			head = &b
		} else if !errors.Is(e, revisionrepo.ErrNotFound) || branch != "main" {
			revisionError(w, e)
			return
		}
		respond(w, 200, map[string]any{"project_version": p.Version, "media_version": state.Version, "branch": head})
	})
	mux.HandleFunc("GET "+base+"/revision-branches", func(w http.ResponseWriter, r *http.Request) {
		space, _, ok := scope(w, r, false)
		if !ok {
			return
		}
		after, limit, ok := revisionPageInput(w, r, space, "branches", "")
		if !ok {
			return
		}
		page, e := s.revisions.Branches(r.Context(), revisionrepo.Scope{Space: space, Project: r.PathValue("id")}, after, limit)
		if e != nil {
			revisionError(w, e)
			return
		}
		page.Next = revisionNext(space, r.PathValue("id"), "branches", "", page.Next)
		respond(w, 200, page)
	})
	mux.HandleFunc("POST "+base+"/revision-branches", func(w http.ResponseWriter, r *http.Request) {
		space, key, ok := scope(w, r, true)
		if !ok {
			return
		}
		var in revisionForkInput
		if !decodeProject(w, r, &in) {
			return
		}
		var authorize func() bool
		if a, ok := r.Context().Value(authScopeKey{}).(authScope); ok {
			authorize = a.recheckWrite
		}
		v, e := s.ForkProductionRevision(r.Context(), space, r.PathValue("id"), promptActor(r, space).User, key, in, authorize)
		if e != nil {
			revisionError(w, e)
			return
		}
		respond(w, 201, publicRevisionMutation(v))
	})
	mux.HandleFunc("GET "+base+"/revision-branches/{branch_id}/revisions", func(w http.ResponseWriter, r *http.Request) {
		space, _, ok := scope(w, r, false)
		if !ok {
			return
		}
		branch := r.PathValue("branch_id")
		after, limit, ok := revisionPageInput(w, r, space, "revisions", branch)
		if !ok {
			return
		}
		page, e := s.revisions.History(r.Context(), revisionrepo.Scope{Space: space, Project: r.PathValue("id")}, branch, after, limit)
		if e != nil {
			revisionError(w, e)
			return
		}
		page.Next = revisionNext(space, r.PathValue("id"), "revisions", branch, page.Next)
		respond(w, 200, page)
	})
	mux.HandleFunc("POST "+base+"/revision-branches/{branch_id}/revisions", func(w http.ResponseWriter, r *http.Request) {
		space, key, ok := scope(w, r, true)
		if !ok {
			return
		}
		var in revisionCaptureInput
		if !decodeProject(w, r, &in) {
			return
		}
		var authorize func() bool
		if a, ok := r.Context().Value(authScopeKey{}).(authScope); ok {
			authorize = a.recheckWrite
		}
		v, e := s.CaptureProductionRevision(r.Context(), space, r.PathValue("id"), r.PathValue("branch_id"), promptActor(r, space).User, key, in, authorize)
		if e != nil {
			revisionError(w, e)
			return
		}
		respond(w, 201, publicRevisionMutation(v))
	})
	mux.HandleFunc("GET "+base+"/revisions/{revision_id}", func(w http.ResponseWriter, r *http.Request) {
		space, _, ok := scope(w, r, false)
		if !ok {
			return
		}
		v, e := s.revisions.Get(r.Context(), revisionrepo.Scope{Space: space, Project: r.PathValue("id")}, r.PathValue("revision_id"))
		if e != nil {
			revisionError(w, e)
			return
		}
		respond(w, 200, v)
	})
}
