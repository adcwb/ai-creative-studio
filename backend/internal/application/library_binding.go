package application

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"path/filepath"
	"time"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/library"
	"github.com/adcwb/ai-creative-studio/backend/internal/modules/media"
)

type libraryBindingInput struct {
	ExpectedVersion int    `json:"expected_media_version"`
	AssetID         string `json:"library_asset_id"`
	VersionID       string `json:"asset_version_id"`
	Usage           string `json:"usage"`
	ShotID          string `json:"shot_id"`
}

func registerLibraryBindingRoutes(mux *http.ServeMux, s *FileStore) {
	base := "/api/v1/projects/{id}/library-bindings"
	mux.HandleFunc("GET "+base, func(w http.ResponseWriter, r *http.Request) {
		space, ok := spaceID(w, r)
		if !ok {
			return
		}
		if _, err := s.GetProject(space, r.PathValue("id")); err != nil {
			storeError(w, err)
			return
		}
		if err := s.media.Available(); err != nil {
			mediaError(w, err)
			return
		}
		p := s.media.Get(r.PathValue("id"), space)
		items := p.LibraryBindings
		if items == nil {
			items = []media.LibraryBinding{}
		}
		respond(w, 200, map[string]any{"items": items, "media_version": p.Version})
	})
	mux.HandleFunc("POST "+base, func(w http.ResponseWriter, r *http.Request) {
		space, key, ok := projectWriteScope(w, r)
		if !ok {
			return
		}
		project := r.PathValue("id")
		p, err := s.GetProject(space, project)
		if err != nil {
			storeError(w, err)
			return
		}
		var in libraryBindingInput
		if !decodeProject(w, r, &in) {
			return
		}
		if in.ExpectedVersion < 1 || !library.IDPattern.MatchString(in.AssetID) || !library.IDPattern.MatchString(in.VersionID) || (in.Usage != "character" && in.Usage != "product" && in.Usage != "scene") {
			libraryError(w, library.ErrInput)
			return
		}
		if in.ShotID != "" {
			found := false
			if p.Storyboard != nil {
				for _, shot := range p.Storyboard.Shots {
					found = found || shot.ID == in.ShotID
				}
			}
			if !found {
				libraryError(w, library.ErrInput)
				return
			}
		}
		// Replay is handled before archive state so a successful binding can be
		// reconciled after the library is archived. The project remains authorized.
		m := s.media
		m.mu.Lock()
		d := m.clone()
		k := mediaKey(space, project)
		digest := mediaDigest(struct {
			Op    string
			Input libraryBindingInput
		}{"library-bind", in})
		replay, checkErr := m.check(d, k, key, digest)
		existing := mediaState(d, k)
		m.mu.Unlock()
		if checkErr != nil {
			mediaError(w, checkErr)
			return
		}
		if replay {
			respond(w, 200, existing)
			return
		}
		identity, err := s.library.Get(r.Context(), space, in.AssetID)
		if err != nil {
			libraryError(w, err)
			return
		}
		v, err := s.library.Version(r.Context(), space, in.AssetID, in.VersionID)
		if err != nil {
			libraryError(w, err)
			return
		}
		if err = library.CanBind(identity.Asset, v, space); err != nil {
			libraryError(w, err)
			return
		}
		if identity.Asset.Kind != in.Usage {
			libraryError(w, library.ErrInput)
			return
		}
		data := make([][]byte, len(v.References))
		for i, ref := range v.References {
			path, e := m.localBlob(space, "library", ref.ID)
			if e != nil {
				libraryError(w, e)
				return
			}
			data[i], e = safeReadLocal(path, 4<<20)
			h := sha256.Sum256(data[i])
			if e != nil || hex.EncodeToString(h[:]) != ref.SHA256 {
				libraryError(w, ErrBlobIntegrity)
				return
			}
		}
		result, err := m.bindLibrary(project, space, key, in, digest, v, data)
		if err != nil {
			mediaError(w, err)
			return
		}
		respond(w, 201, result)
	})
}

func (m *MediaStore) bindLibrary(project, space, key string, in libraryBindingInput, digest string, v library.Version, files [][]byte) (MediaState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d := m.clone()
	k := mediaKey(space, project)
	p := mediaState(d, k)
	if replay, err := m.check(d, k, key, digest); err != nil || replay {
		return p, err
	}
	if p.Version != in.ExpectedVersion {
		return p, errMediaVersion
	}
	if len(p.LibraryBindings) >= 60 || len(p.Assets)+len(files) > 120 {
		return p, errMediaLimit
	}
	var size int64
	for _, b := range files {
		size += int64(len(b))
	}
	if m.overQuota(size) {
		return p, errMediaLimit
	}
	binding := media.LibraryBinding{ID: mediaID(), AssetID: in.AssetID, VersionID: in.VersionID, Name: v.Card.Name, Kind: in.Usage, Usage: in.Usage, ShotID: in.ShotID, MediaAssetIDs: []string{}, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	for i, b := range files {
		id := mediaID()
		path := filepath.Join(m.dir, id)
		if err := writePreparedLibraryCopy(path, b); err != nil {
			return p, err
		}
		if err := m.storeBlob(space, project, id); err != nil {
			return p, err
		}
		ref := v.References[i]
		character := ""
		if in.Usage == "character" {
			character = v.Card.Name
		}
		p.Assets = append(p.Assets, MediaAsset{ID: id, Kind: "image", MIMEType: ref.MIMEType, Name: v.Card.Name + " · " + ref.ViewAngle, Role: "reference", Character: character, ShotID: in.ShotID, Source: "library:" + v.AssetID + ":" + v.ID, FileURL: mediaURL(space, project, "assets/"+id+"/file")})
		binding.MediaAssetIDs = append(binding.MediaAssetIDs, id)
	}
	p.LibraryBindings = append(p.LibraryBindings, binding)
	p.Version++
	d.Projects[k] = p
	d.Receipts[k+":"+key] = mediaReceipt{Digest: digest}
	return p, m.commit(d)
}
