package application

import (
	"net/http"
	"time"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/library"
)

type LibraryAuthorizationView struct {
	AssetID       string                    `json:"asset_id"`
	HeadVersionID string                    `json:"head_version_id"`
	Authorization *library.UseAuthorization `json:"authorization"`
}

func libraryAuthorizationView(a library.Asset) LibraryAuthorizationView {
	return LibraryAuthorizationView{a.ID, a.HeadVersionID, a.UseAuthorization}
}
func registerLibraryAuthorizationRoutes(mux *http.ServeMux, s *FileStore) {
	base := "/api/v1/library/assets/{assetId}/use-authorization"
	mux.HandleFunc("GET "+base, func(w http.ResponseWriter, r *http.Request) {
		space, ok := spaceID(w, r)
		if !ok {
			return
		}
		current, e := s.library.Get(r.Context(), space, r.PathValue("assetId"))
		if e != nil {
			libraryError(w, e)
			return
		}
		respond(w, 200, libraryAuthorizationView(current.Asset))
	})
	mux.HandleFunc("POST "+base, func(w http.ResponseWriter, r *http.Request) {
		space, key, ok := projectWriteScope(w, r)
		if !ok {
			return
		}
		actor := seriesActorFor(r, space).User
		var envelope struct {
			library.AuthorizationInput
			Expected *int64 `json:"expected_revision"`
		}
		if !decodeProject(w, r, &envelope) {
			return
		}
		if envelope.Expected == nil {
			libraryError(w, library.ErrInput)
			return
		}
		in := envelope.AuthorizationInput
		in.Expected = *envelope.Expected
		normalized, e := library.NormalizeAuthorization(in)
		if e != nil {
			libraryError(w, e)
			return
		}
		id := r.PathValue("assetId")
		digest, e := library.Digest(struct {
			Asset, Actor string
			Input        library.AuthorizationInput
		}{id, actor, normalized})
		if e != nil {
			libraryError(w, library.ErrInput)
			return
		}
		if scope, ok := r.Context().Value(authScopeKey{}).(authScope); ok && scope.recheckWrite != nil && !scope.recheckWrite() {
			problem(w, 403, "forbidden", "当前空间写入权限已失效")
			return
		}
		out, e := s.library.SetAuthorization(r.Context(), space, id, "authorization:"+key, digest, actor, normalized, time.Now().UTC())
		if e != nil {
			libraryError(w, e)
			return
		}
		// A replay is the original receipt, not a statement of current permission.
		// Callers read GET again before presenting current use status.
		respond(w, 200, libraryAuthorizationView(out.Asset))
	})
}
