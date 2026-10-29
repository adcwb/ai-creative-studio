package application

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"net/http"
	"strconv"
	"time"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/library"
	"github.com/adcwb/ai-creative-studio/backend/internal/persistence/libraryrepo"
)

type libraryReferenceInput struct {
	ProjectID          string `json:"project_id,omitempty"`
	AssetID            string `json:"asset_id,omitempty"`
	LibraryVersionID   string `json:"library_version_id,omitempty"`
	LibraryReferenceID string `json:"library_reference_id,omitempty"`
	Role               string `json:"role"`
	ViewAngle          string `json:"view_angle"`
}
type libraryWrite struct {
	Kind            string                  `json:"kind"`
	ExpectedVersion string                  `json:"expected_asset_version_id,omitempty"`
	Card            library.Card            `json:"card"`
	References      []libraryReferenceInput `json:"references"`
}
type libraryCursor struct {
	Space  string `json:"space"`
	Filter string `json:"filter"`
	After  string `json:"after"`
}

func libraryError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, library.ErrInput):
		problem(w, 400, "invalid_library_input", "资产卡类型、参考图或字段无效")
	case errors.Is(err, library.ErrConflict):
		problem(w, 409, "asset_version_conflict", "资产版本已更新，请保留草稿并重新读取")
	case errors.Is(err, library.ErrArchived):
		problem(w, 409, "asset_archived", "资产已归档，不能新增版本或绑定")
	case errors.Is(err, libraryrepo.ErrKey):
		problem(w, 409, "idempotency_conflict", "同一请求键不能用于不同内容")
	case errors.Is(err, libraryrepo.ErrNotFound), errors.Is(err, ErrNotFound):
		problem(w, 404, "not_found", "资产或参考素材不存在")
	case errors.Is(err, libraryrepo.ErrLimit), errors.Is(err, errMediaLimit):
		problem(w, 429, "library_limit", "资产库容量已达上限")
	default:
		problem(w, 503, "library_storage_error", "资产保存结果未确认，请使用原请求键核对")
	}
}
func libraryPage(r *http.Request, space, filter string) (string, int, error) {
	limit := 20
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 100 {
			return "", 0, library.ErrInput
		}
		limit = n
	}
	after := ""
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		if len(raw) > 1024 {
			return "", 0, library.ErrInput
		}
		b, e := base64.RawURLEncoding.DecodeString(raw)
		var c libraryCursor
		if e != nil || json.Unmarshal(b, &c) != nil || c.Space != space || c.Filter != filter || !library.IDPattern.MatchString(c.After) {
			return "", 0, library.ErrInput
		}
		after = c.After
	}
	return after, limit, nil
}
func libraryNext(space, filter, after string) string {
	b, _ := json.Marshal(libraryCursor{space, filter, after})
	return base64.RawURLEncoding.EncodeToString(b)
}
func libraryStableID(space, key, role string) string {
	h := sha256.Sum256([]byte(space + "\x00" + key + "\x00" + role))
	return hex.EncodeToString(h[:16])
}

func registerLibraryRoutes(mux *http.ServeMux, s *FileStore) {
	if s.library == nil {
		return
	}
	registerLibraryBindingRoutes(mux, s)
	base := "/api/v1/library/assets"
	mux.HandleFunc("GET "+base, func(w http.ResponseWriter, r *http.Request) {
		space, ok := spaceID(w, r)
		if !ok {
			return
		}
		kind, status := r.URL.Query().Get("kind"), r.URL.Query().Get("status")
		filter := "assets:" + kind + ":" + status
		after, limit, err := libraryPage(r, space, filter)
		if err != nil {
			libraryError(w, err)
			return
		}
		items, err := s.library.List(r.Context(), space, libraryrepo.Query{Kind: kind, Status: status, After: after, Limit: limit})
		if err != nil {
			libraryError(w, err)
			return
		}
		next := ""
		if len(items) == limit {
			next = libraryNext(space, filter, items[len(items)-1].ID)
		}
		respond(w, 200, map[string]any{"items": items, "next_cursor": next})
	})
	mux.HandleFunc("GET "+base+"/{assetId}", func(w http.ResponseWriter, r *http.Request) {
		space, ok := spaceID(w, r)
		if !ok {
			return
		}
		result, err := s.library.Get(r.Context(), space, r.PathValue("assetId"))
		if err != nil {
			libraryError(w, err)
			return
		}
		respond(w, 200, result)
	})
	mux.HandleFunc("GET "+base+"/{assetId}/versions", func(w http.ResponseWriter, r *http.Request) {
		space, ok := spaceID(w, r)
		if !ok {
			return
		}
		filter := "versions:" + r.PathValue("assetId")
		after, limit, err := libraryPage(r, space, filter)
		if err != nil {
			libraryError(w, err)
			return
		}
		items, err := s.library.Versions(r.Context(), space, r.PathValue("assetId"), after, limit)
		if err != nil {
			libraryError(w, err)
			return
		}
		next := ""
		if len(items) == limit {
			next = libraryNext(space, filter, items[len(items)-1].ID)
		}
		respond(w, 200, map[string]any{"items": items, "next_cursor": next})
	})
	mux.HandleFunc("GET "+base+"/{assetId}/versions/{versionId}", func(w http.ResponseWriter, r *http.Request) {
		space, ok := spaceID(w, r)
		if !ok {
			return
		}
		v, err := s.library.Version(r.Context(), space, r.PathValue("assetId"), r.PathValue("versionId"))
		if err != nil {
			libraryError(w, err)
			return
		}
		respond(w, 200, v)
	})
	write := func(w http.ResponseWriter, r *http.Request) {
		space, key, ok := projectWriteScope(w, r)
		if !ok {
			return
		}
		var in libraryWrite
		if !decodeProject(w, r, &in) {
			return
		}
		card, err := library.Normalize(in.Kind, in.Card)
		if err != nil || len(in.References) > 12 {
			libraryError(w, library.ErrInput)
			return
		}
		in.Card = card
		if in.References == nil {
			in.References = []libraryReferenceInput{}
		}
		assetID := r.PathValue("assetId")
		create := assetID == ""
		if create {
			if in.ExpectedVersion != "" {
				libraryError(w, library.ErrInput)
				return
			}
			assetID = libraryStableID(space, key, "identity")
		} else if !library.IDPattern.MatchString(assetID) || in.ExpectedVersion == "" {
			libraryError(w, library.ErrInput)
			return
		}
		digest, _ := library.Digest(struct {
			AssetID string
			Input   libraryWrite
		}{assetID, in})
		receiptKey := "write:" + key
		previous, replay, err := s.library.Replay(r.Context(), space, receiptKey, digest)
		if err != nil {
			libraryError(w, err)
			return
		}
		if replay {
			respond(w, 200, previous)
			return
		}
		now := time.Now().UTC()
		a := library.Asset{ID: assetID, SpaceID: space, Kind: in.Kind, Name: in.Card.Name, Status: "active", CreatedAt: now, UpdatedAt: now}
		number := 1
		if !create {
			old, e := s.library.Get(r.Context(), space, assetID)
			if e != nil {
				libraryError(w, e)
				return
			}
			if old.Asset.HeadVersionID != in.ExpectedVersion || old.Asset.Kind != in.Kind {
				libraryError(w, library.ErrConflict)
				return
			}
			if old.Asset.Status != "active" {
				libraryError(w, library.ErrArchived)
				return
			}
			a = old.Asset
			a.Name = card.Name
			a.UpdatedAt = now
			number = old.Version.Number + 1
		}
		refs := []library.Reference{}
		type prepared struct {
			ref  library.Reference
			data []byte
		}
		files := []prepared{}
		for i, source := range in.References {
			if source.LibraryVersionID != "" || source.LibraryReferenceID != "" {
				if create || source.ProjectID != "" || source.AssetID != "" || !library.IDPattern.MatchString(source.LibraryVersionID) || !library.IDPattern.MatchString(source.LibraryReferenceID) {
					libraryError(w, library.ErrInput)
					return
				}
				old, e := s.library.Version(r.Context(), space, assetID, source.LibraryVersionID)
				if e != nil {
					libraryError(w, e)
					return
				}
				var retained *library.Reference
				for _, ref := range old.References {
					if ref.ID == source.LibraryReferenceID {
						retained = &ref
						break
					}
				}
				if retained == nil {
					libraryError(w, libraryrepo.ErrNotFound)
					return
				}
				path, e := s.media.localBlob(space, "library", retained.ID)
				if e != nil {
					libraryError(w, e)
					return
				}
				data, e := safeReadLocal(path, 4<<20)
				h := sha256.Sum256(data)
				if e != nil || int64(len(data)) != retained.SizeBytes || hex.EncodeToString(h[:]) != retained.SHA256 {
					libraryError(w, libraryrepo.ErrStorage)
					return
				}
				retained.Role, retained.ViewAngle = source.Role, source.ViewAngle
				refs = append(refs, *retained)
				continue
			}
			if !library.IDPattern.MatchString(source.ProjectID) || !library.IDPattern.MatchString(source.AssetID) {
				libraryError(w, library.ErrInput)
				return
			}
			if _, e := s.GetProject(space, source.ProjectID); e != nil {
				libraryError(w, e)
				return
			}
			path, mime, e := s.media.file(source.ProjectID, space, source.AssetID, false, false)
			if e != nil {
				libraryError(w, e)
				return
			}
			if mime != "image/png" && mime != "image/jpeg" {
				libraryError(w, library.ErrInput)
				return
			}
			data, e := safeReadLocal(path, 4<<20)
			if e != nil {
				libraryError(w, library.ErrInput)
				return
			}
			cfg, _, e := image.DecodeConfig(bytes.NewReader(data))
			if e != nil {
				libraryError(w, library.ErrInput)
				return
			}
			h := sha256.Sum256(data)
			ref := library.Reference{ID: libraryStableID(space, key, assetID+":"+digest+":"+strconv.Itoa(i)), Role: source.Role, ViewAngle: source.ViewAngle, MIMEType: mime, Width: cfg.Width, Height: cfg.Height, SizeBytes: int64(len(data)), SHA256: hex.EncodeToString(h[:]), SourceProjectID: source.ProjectID, SourceAssetID: source.AssetID}
			refs = append(refs, ref)
			files = append(files, prepared{ref, data})
		}
		if library.ValidateReferences(refs) != nil {
			libraryError(w, library.ErrInput)
			return
		}
		for _, f := range files {
			if e := s.media.writeLibraryObject(space, f.ref, f.data); e != nil {
				libraryError(w, e)
				return
			}
		}

		actor := "demo-user"
		if scope, ok := r.Context().Value(authScopeKey{}).(authScope); ok {
			actor = scope.UserID
		}
		v := library.Version{ID: libraryStableID(space, key, assetID+":version"), SpaceID: space, AssetID: assetID, Number: number, Card: card, References: refs, CreatedAt: now, CreatedBy: actor}
		v.SHA256, _ = library.Digest(struct {
			Card       library.Card
			References []library.Reference
		}{card, refs})
		a.HeadVersionID = v.ID
		result, err := s.library.Mutate(r.Context(), space, receiptKey, digest, in.ExpectedVersion, libraryrepo.Result{Asset: a, Version: v}, false)
		if err != nil {
			libraryError(w, err)
			return
		}
		respond(w, 201, result)
	}
	mux.HandleFunc("POST "+base, write)
	mux.HandleFunc("POST "+base+"/{assetId}/versions", write)
	mux.HandleFunc("POST "+base+"/{assetId}/archive", func(w http.ResponseWriter, r *http.Request) {
		space, key, ok := projectWriteScope(w, r)
		if !ok {
			return
		}
		var in struct {
			Expected string `json:"expected_asset_version_id"`
		}
		if !decodeProject(w, r, &in) {
			return
		}
		if in.Expected == "" {
			libraryError(w, library.ErrInput)
			return
		}
		id := r.PathValue("assetId")
		digest, _ := library.Digest([]string{id, in.Expected})
		receiptKey := "archive:" + key
		previous, replay, err := s.library.Replay(r.Context(), space, receiptKey, digest)
		if err != nil {
			libraryError(w, err)
			return
		}
		if replay {
			respond(w, 200, previous)
			return
		}
		result, err := s.library.Get(r.Context(), space, id)
		if err != nil {
			libraryError(w, err)
			return
		}
		result.Asset.Status = "archived"
		result.Asset.UpdatedAt = time.Now().UTC()
		result, err = s.library.Mutate(r.Context(), space, receiptKey, digest, in.Expected, result, true)
		if err != nil {
			libraryError(w, err)
			return
		}
		respond(w, 200, result)
	})
	mux.HandleFunc("GET "+base+"/{assetId}/versions/{versionId}/references/{referenceId}/file", func(w http.ResponseWriter, r *http.Request) {
		space, ok := spaceID(w, r)
		if !ok {
			return
		}
		v, err := s.library.Version(r.Context(), space, r.PathValue("assetId"), r.PathValue("versionId"))
		if err != nil {
			libraryError(w, err)
			return
		}
		for _, ref := range v.References {
			if ref.ID != r.PathValue("referenceId") {
				continue
			}
			path, e := s.media.localBlob(space, "library", ref.ID)
			if e != nil {
				libraryError(w, e)
				return
			}
			data, e := safeReadLocal(path, 4<<20)
			h := sha256.Sum256(data)
			if e != nil || hex.EncodeToString(h[:]) != ref.SHA256 {
				libraryError(w, libraryrepo.ErrStorage)
				return
			}
			w.Header().Set("Content-Type", ref.MIMEType)
			w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
			w.WriteHeader(200)
			_, _ = w.Write(data)
			return
		}
		libraryError(w, libraryrepo.ErrNotFound)
	})
}
