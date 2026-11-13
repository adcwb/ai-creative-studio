package application

import (
	"errors"
	"net/http"
	"sort"
)

type personalCopyView struct {
	ID            string `json:"id"`
	Version       int    `json:"version"`
	State         string `json:"state"`
	TargetSpace   string `json:"target_space_id"`
	TargetProject string `json:"target_project_id"`
	Title         string `json:"title"`
	Revoked       bool   `json:"revoked"`
	Copied        int    `json:"copied_files"`
	Verified      int    `json:"verified_files"`
	Total         int    `json:"total_files"`
}

func personalCopyPublic(j personalCopyJob) personalCopyView {
	return personalCopyView{j.ID, j.Version, j.State, j.TargetSpace, j.TargetID, j.Snapshot.Project.Title, j.Revoked, len(j.Copied), len(j.Verified), len(j.Snapshot.Files)}
}
func personalCopyError(w http.ResponseWriter, e error) {
	switch {
	case errors.Is(e, errRuntimeDenied):
		problem(w, 403, "role_denied", "请核对个人作品所有权和目标团队编辑权限")
	case errors.Is(e, ErrNotFound):
		problem(w, 404, "not_found", "作品复制记录不存在")
	case errors.Is(e, ErrVersionConflict), errors.Is(e, ErrIdempotencyConflict):
		problem(w, 409, "copy_changed", "作品、预览或原请求已变化，请核对复制记录")
	case errors.Is(e, ErrGenerationActive):
		storeError(w, e)
	case errors.Is(e, ErrInvalidProjectInput):
		storeError(w, e)
	default:
		problem(w, 503, "copy_unconfirmed", "复制尚未完成，请保留原请求并读取记录后继续；源作品未被删除")
	}
}
func registerPersonalCopyRoutes(mux *http.ServeMux, s *FileStore) {
	base := "/api/v1/projects/{id}/personal-copies"
	slots := make(chan struct{}, 2)
	scope := func(w http.ResponseWriter, r *http.Request) (authScope, string, string, bool) {
		space, key, ok := projectWriteScope(w, r)
		if !ok {
			return authScope{}, "", "", false
		}
		a, found := r.Context().Value(authScopeKey{}).(authScope)
		if !found || !s.personalOwner(a.UserID, space) {
			personalCopyError(w, errRuntimeDenied)
			return a, "", "", false
		}
		if _, e := s.GetProject(space, r.PathValue("id")); e != nil {
			storeError(w, e)
			return a, "", "", false
		}
		return a, space, key, true
	}
	read := func(w http.ResponseWriter, r *http.Request) (authScope, string, bool) {
		space, ok := spaceID(w, r)
		if !ok {
			return authScope{}, "", false
		}
		a, found := r.Context().Value(authScopeKey{}).(authScope)
		if !found || !s.personalOwner(a.UserID, space) {
			personalCopyError(w, errRuntimeDenied)
			return a, "", false
		}
		if _, e := s.GetProject(space, r.PathValue("id")); e != nil {
			storeError(w, e)
			return a, "", false
		}
		return a, space, true
	}
	mux.HandleFunc("POST "+base+"/preview", func(w http.ResponseWriter, r *http.Request) {
		a, space, _, ok := scope(w, r)
		if !ok {
			return
		}
		var in struct {
			Target string `json:"target_space_id"`
		}
		if !decodeProject(w, r, &in) {
			return
		}
		if !s.personalCopyAllowed(a.UserID, space, in.Target) {
			personalCopyError(w, errRuntimeDenied)
			return
		}
		snap, e := s.personalCopySnapshot(r.Context(), space, r.PathValue("id"))
		if e != nil {
			personalCopyError(w, e)
			return
		}
		size := int64(0)
		unknown := 0
		for _, f := range snap.Files {
			size += f.Size
		}
		for _, v := range snap.Media.Assets {
			if v.ProvenanceUnknown {
				unknown++
			}
		}
		respond(w, 200, map[string]any{"target_space_id": in.Target, "title": snap.Project.Title, "preview_sha256": personalCopyDigest(space, in.Target, snap), "files": len(snap.Files), "bytes": size, "assets": len(snap.Media.Assets), "exports": len(snap.Media.Exports), "history": len(snap.History), "revision_records": len(snap.Revisions), "library_sources": len(snap.Sources), "unknown_lineage_assets": unknown})
	})
	mux.HandleFunc("POST "+base, func(w http.ResponseWriter, r *http.Request) {
		a, space, key, ok := scope(w, r)
		if !ok {
			return
		}
		var in struct {
			Target    string `json:"target_space_id"`
			Digest    string `json:"preview_sha256"`
			Confirmed bool   `json:"confirmed"`
		}
		if !decodeProject(w, r, &in) {
			return
		}
		if !in.Confirmed || len(in.Digest) != 64 {
			problem(w, 400, "invalid_input", "请先预览并明确确认复制作品及所用素材到所选团队")
			return
		}
		j, e := s.createPersonalCopy(r.Context(), a.UserID, space, r.PathValue("id"), in.Target, key, in.Digest)
		if e != nil {
			personalCopyError(w, e)
			return
		}
		respond(w, 200, personalCopyPublic(j))
	})
	mux.HandleFunc("GET "+base, func(w http.ResponseWriter, r *http.Request) {
		a, space, ok := read(w, r)
		if !ok {
			return
		}
		s.personalCopies.mu.Lock()
		defer s.personalCopies.mu.Unlock()
		if s.personalCopies.failure != nil || (s.personalCopies.pg != nil && s.personalCopies.pg.err != nil) {
			personalCopyError(w, errors.New("copy storage unavailable"))
			return
		}
		items := []personalCopyView{}
		for _, j := range s.personalCopies.data.Jobs {
			if j.Space == space && j.ProjectID == r.PathValue("id") && j.Actor == a.UserID {
				items = append(items, personalCopyPublic(j))
			}
		}
		sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
		respond(w, 200, map[string]any{"items": items})
	})
	mux.HandleFunc("GET "+base+"/{copy_id}", func(w http.ResponseWriter, r *http.Request) {
		a, space, ok := read(w, r)
		if !ok {
			return
		}
		j, e := s.personalCopies.get(r.PathValue("copy_id"))
		if e != nil {
			personalCopyError(w, e)
			return
		}
		if j.Space != space || j.ProjectID != r.PathValue("id") || j.Actor != a.UserID {
			personalCopyError(w, ErrNotFound)
			return
		}
		respond(w, 200, personalCopyPublic(j))
	})
	for _, action := range []string{"continue", "revoke"} {
		action := action
		mux.HandleFunc("POST "+base+"/{copy_id}/"+action, func(w http.ResponseWriter, r *http.Request) {
			a, space, _, ok := scope(w, r)
			if !ok {
				return
			}
			var in struct {
				Expected  int  `json:"expected_version"`
				Confirmed bool `json:"confirmed,omitempty"`
			}
			if !decodeProject(w, r, &in) {
				return
			}
			if in.Expected < 1 {
				problem(w, 400, "invalid_input", "请先读取当前复制记录")
				return
			}
			id := r.PathValue("copy_id")
			j, e := s.personalCopies.get(id)
			if e != nil {
				personalCopyError(w, e)
				return
			}
			if j.Space != space || j.ProjectID != r.PathValue("id") || j.Actor != a.UserID {
				personalCopyError(w, ErrNotFound)
				return
			}
			if action == "revoke" {
				if !in.Confirmed {
					problem(w, 400, "invalid_input", "请明确确认撤销共享，历史作品保留，新的模型派发将被阻止")
					return
				}
				e = s.personalCopies.change(r.Context(), id, func(d *personalCopyDisk) error {
					v := d.Jobs[id]
					if v.Revoked {
						j = v
						return nil
					}
					if v.Version != in.Expected {
						return ErrVersionConflict
					}
					v.Revoked = true
					v.Version++
					d.Jobs[id] = v
					j = v
					return nil
				})
			} else {
				if j.Version != in.Expected {
					respond(w, 200, personalCopyPublic(j))
					return
				}
				select {
				case slots <- struct{}{}:
					defer func() { <-slots }()
				case <-r.Context().Done():
					return
				}
				j, e = s.continuePersonalCopy(r.Context(), id, in.Expected)
			}
			if e != nil {
				personalCopyError(w, e)
				return
			}
			respond(w, 200, personalCopyPublic(j))
		})
	}
}
