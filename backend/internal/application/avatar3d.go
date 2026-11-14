package application

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/adcwb/ai-creative-studio/backend/internal/platform/nativefs"
)

const avatar3DBuiltin = "mpfb-cc0-v1"
const avatar3DBuiltinSHA = "63c645a2a863b9972e9a9c2ed576a1de4c390b8475508e1473e69c87a3ee299c"

type Avatar3D struct {
	ID        string          `json:"id"`
	SpaceID   string          `json:"space_id"`
	ProjectID string          `json:"project_id"`
	Name      string          `json:"name"`
	Builtin   string          `json:"builtin,omitempty"`
	Body      string          `json:"body"`
	License   string          `json:"license_reference"`
	Consent   string          `json:"consent_reference"`
	CreatedBy string          `json:"created_by"`
	CreatedAt time.Time       `json:"created_at"`
	RevokedAt *time.Time      `json:"revoked_at,omitempty"`
	Model     *BlobInfo       `json:"model,omitempty"`
	Profile   avatar3DProfile `json:"profile"`
	SHA       string          `json:"model_sha256"`
}
type avatar3DInput struct {
	Name      string `json:"name"`
	Builtin   string `json:"builtin,omitempty"`
	Body      string `json:"body"`
	License   string `json:"license_reference"`
	Consent   string `json:"consent_reference"`
	Confirmed bool   `json:"confirmed"`
}

func avatar3DKey(v Avatar3D) string {
	id, _ := projectFingerprint("avatar3d-model-bytes", v.ID, []string{v.SpaceID, v.ProjectID, v.SHA})
	return blobKey(v.SpaceID, v.ProjectID, id[:32]+".glb")
}
func publicAvatar3D(v Avatar3D) Avatar3D { v.Model = nil; return v }
func validateAvatar3DModels(d creativeDisk) error {
	for id, v := range d.Avatars3D {
		if id != v.ID || !shotIDPattern.MatchString(id) || v.SpaceID == "" || v.ProjectID == "" || v.CreatedBy == "" || v.CreatedAt.IsZero() || v.Name == "" || v.License == "" || v.Consent == "" || (v.Body != "M" && v.Body != "F") {
			return ErrInvalidProjectInput
		}
		if v.Builtin != "" {
			if v.Builtin != avatar3DBuiltin || v.Model != nil || v.SHA != avatar3DBuiltinSHA {
				return ErrInvalidProjectInput
			}
		} else if v.Model == nil || !validBlobInfo(*v.Model) || v.Model.Key != avatar3DKey(v) || v.SHA != v.Model.SHA256 || v.Profile.Nodes < 1 || v.Profile.Morphs != 3 {
			return ErrBlobIntegrity
		}
	}
	return nil
}
func (s *FileStore) saveAvatar3D(ctx context.Context, space, project, key, actor string, in avatar3DInput, path string) (Avatar3D, error) {
	if _, e := s.GetProject(space, project); e != nil {
		return Avatar3D{}, e
	}
	if actor == "" || !in.Confirmed || (in.Body != "M" && in.Body != "F") {
		return Avatar3D{}, ErrInvalidProjectInput
	}
	var e error
	in.Name, e = normalizeProjectText(in.Name, "角色名称", 1, 40)
	if e != nil {
		return Avatar3D{}, e
	}
	in.License, e = normalizeProjectText(in.License, "模型许可依据", 3, 500)
	if e != nil {
		return Avatar3D{}, e
	}
	in.Consent, e = normalizeProjectText(in.Consent, "形象使用依据", 3, 500)
	if e != nil {
		return Avatar3D{}, e
	}
	hash, _ := projectFingerprint("avatar3d-identity", project, []string{space, key})
	v := Avatar3D{ID: hash[:32], SpaceID: space, ProjectID: project, Name: in.Name, Builtin: in.Builtin, Body: in.Body, License: in.License, Consent: in.Consent, CreatedBy: actor, CreatedAt: time.Now().UTC()}
	if path == "" {
		if in.Builtin != avatar3DBuiltin || in.Body != "F" {
			return v, ErrInvalidProjectInput
		}
		v.SHA = avatar3DBuiltinSHA
		v.License = "CC0 1.0 · MPFB · TalkingHead 1563de184c19b310de4b636664d5835e2c74dbba"
	} else {
		if in.Builtin != "" {
			return v, ErrInvalidProjectInput
		}
		v.Profile, e = validateAvatarGLB(ctx, path)
		if e != nil {
			return v, e
		}
		info, e := localBlobInfo(ctx, avatar3DKey(v), path)
		if e != nil {
			return v, e
		}
		v.SHA = info.SHA256
		info.Key = avatar3DKey(v)
		v.Model = &info
	}
	fingerprint, _ := projectFingerprint("avatar3d-create", project, []any{actor, in, v.SHA})
	c := s.creative
	rk := projectReceiptKey(space, key)
	// Reading a receipt does not hold the creative DB transaction across an
	// upload. Content-derived object keys keep conflicting concurrent bodies
	// separate; the accepted receipt and metadata still commit atomically.
	lookup := func() (Avatar3D, bool, error) {
		if c.pg != nil && c.pg.err != nil {
			return v, false, c.pg.err
		}
		if r, ok := c.data.Receipts[rk]; ok {
			if r.Fingerprint != fingerprint {
				return v, true, ErrIdempotencyConflict
			}
			old, ok := c.data.Avatars3D[r.Avatar3DID]
			if !ok || old.SpaceID != space || old.ProjectID != project {
				return v, true, ErrNotFound
			}
			return old, true, nil
		}
		return v, false, nil
	}
	c.mu.Lock()
	old, exists, err := lookup()
	c.mu.Unlock()
	if err != nil || exists {
		return old, err
	}
	if s.runtime != nil && s.runtime.auth != nil && !s.runtime.auth.can(actor, space, "workspace.write") {
		return v, errRuntimeDenied
	}
	if path != "" {
		if s.blobs != nil {
			e = s.blobs.putFile(ctx, v.Model.Key, path)
		} else if s.pg != nil {
			e = ErrBlobIntegrity
		} else {
			e = publishAvatarCheckpointFile(ctx, path, filepath.Join(s.media.dir, filepath.Base(v.Model.Key)), *v.Model)
		}
		if e != nil {
			return v, e
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	old, exists, err = lookup()
	if err != nil || exists {
		return old, err
	}
	if s.runtime != nil && s.runtime.auth != nil && !s.runtime.auth.can(actor, space, "workspace.write") {
		return v, errRuntimeDenied
	}
	if c.data.Avatars3D == nil {
		c.data.Avatars3D = map[string]Avatar3D{}
	}
	c.data.Avatars3D[v.ID] = v
	c.data.Receipts[rk] = creativeReceipt{Fingerprint: fingerprint, Avatar3DID: v.ID}
	if e = c.saveLocked(); e != nil {
		delete(c.data.Avatars3D, v.ID)
		delete(c.data.Receipts, rk)
		return v, e
	}
	return v, nil
}
func (s *FileStore) avatar3D(space, project, id string, active bool) (Avatar3D, error) {
	if _, e := s.GetProject(space, project); e != nil {
		return Avatar3D{}, e
	}
	c := s.creative
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.pg != nil && c.pg.err != nil {
		return Avatar3D{}, c.pg.err
	}
	v, ok := c.data.Avatars3D[id]
	if !ok || v.SpaceID != space || v.ProjectID != project {
		return v, ErrNotFound
	}
	if active && v.RevokedAt != nil {
		return v, providerFailure("avatar_authorization_revoked")
	}
	return v, nil
}
func (s *FileStore) checkAvatar3DPlayback(ctx context.Context, space, project, id, actor, audio, voice string) error {
	v, e := s.avatar3D(space, project, id, true)
	if e != nil {
		return e
	}
	job := CreativeJob{Kind: "digital_human_authorized", SpaceID: space, ProjectID: project, AudioAssetID: audio, VoiceAuthorizationID: voice, VoiceConsentBy: actor}
	if e = s.checkVoiceAuthorization(ctx, &job); e != nil {
		return e
	}
	if s.runtime != nil && s.runtime.auth != nil && (!s.runtime.auth.can(actor, space, "workspace.write") || !s.runtime.auth.can(v.CreatedBy, space, "workspace.write")) {
		return errRuntimeDenied
	}
	state, e := s.media.GetChecked(project, space)
	if e != nil {
		return e
	}
	a, ok := mediaAsset(state, audio)
	if !ok || a.Kind != "audio" {
		return ErrInvalidProjectInput
	}
	_, e = s.pinCreativeLibraryMedia(ctx, project, space, CreativeJobInput{Kind: "digital_human_authorized", AudioAssetID: audio})
	return e
}
func registerAvatar3DRoutes(mux *http.ServeMux, s *FileStore) {
	base := "/api/v1/projects/{id}/avatar-3d-models"
	mux.HandleFunc("GET "+base, func(w http.ResponseWriter, r *http.Request) {
		space, ok := spaceID(w, r)
		if !ok {
			return
		}
		project := r.PathValue("id")
		if _, e := s.GetProject(space, project); e != nil {
			storeError(w, e)
			return
		}
		c := s.creative
		c.mu.Lock()
		if c.pg != nil && c.pg.err != nil {
			err := c.pg.err
			c.mu.Unlock()
			storeError(w, err)
			return
		}
		out := []Avatar3D{}
		for _, v := range c.data.Avatars3D {
			if v.SpaceID == space && v.ProjectID == project {
				out = append(out, publicAvatar3D(v))
			}
		}
		c.mu.Unlock()
		sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
		respond(w, 200, out)
	})
	mux.HandleFunc("POST "+base, func(w http.ResponseWriter, r *http.Request) {
		space, key, ok := projectWriteScope(w, r)
		if !ok {
			return
		}
		if _, e := s.GetProject(space, r.PathValue("id")); e != nil {
			storeError(w, e)
			return
		}
		var in avatar3DInput
		path := ""
		defer func() {
			if path != "" {
				os.Remove(path)
			}
		}()
		content, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if content == "application/json" {
			if !decodeProject(w, r, &in) {
				return
			}
		} else if content == "multipart/form-data" {
			reader, e := r.MultipartReader()
			if e != nil {
				storeError(w, ErrInvalidProjectInput)
				return
			}
			part, e := reader.NextPart()
			if e != nil || part.FormName() != "metadata" {
				storeError(w, ErrInvalidProjectInput)
				return
			}
			raw, err := io.ReadAll(io.LimitReader(part, 8193))
			if err != nil || len(raw) > 8192 {
				storeError(w, ErrInvalidProjectInput)
				return
			}
			metadataRequest := r.Clone(r.Context())
			metadataRequest.Header = r.Header.Clone()
			metadataRequest.Header.Set("Content-Type", "application/json")
			metadataRequest.Body = io.NopCloser(bytes.NewReader(raw))
			metadataRequest.ContentLength = int64(len(raw))
			if !decodeProject(w, metadataRequest, &in) {
				return
			}
			part.Close()
			part, e = reader.NextPart()
			if e != nil || part.FormName() != "model" {
				storeError(w, ErrInvalidProjectInput)
				return
			}
			privateDir, e := nativefs.MkdirTemp("", "studio-avatar3d-upload-*")
			if e != nil {
				storeError(w, e)
				return
			}
			defer os.RemoveAll(privateDir)
			f, e := os.OpenFile(filepath.Join(privateDir, "model.glb"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			if e != nil {
				storeError(w, e)
				return
			}
			path = f.Name()
			header := make([]byte, 12)
			_, e = io.ReadFull(part, header)
			if e == nil && (string(header[:4]) != "glTF" || binary.LittleEndian.Uint32(header[4:8]) != 2 || binary.LittleEndian.Uint32(header[8:]) < 28) {
				e = ErrInvalidProjectInput
			}
			if e == nil {
				_, e = f.Write(header)
			}
			if e == nil {
				_, e = io.CopyN(f, part, int64(binary.LittleEndian.Uint32(header[8:]))-12)
			}
			if e == nil {
				var extra [1]byte
				var n int
				n, e = part.Read(extra[:])
				if n != 0 || e != io.EOF {
					e = ErrInvalidProjectInput
				} else {
					e = nil
				}
			}
			e = errors.Join(e, f.Close())
			if r.Context().Err() != nil {
				e = r.Context().Err()
			} else if errors.Is(e, io.EOF) || errors.Is(e, io.ErrUnexpectedEOF) {
				e = ErrInvalidProjectInput
			}
			if e != nil {
				storeError(w, e)
				return
			}
			part.Close()
			if _, e = reader.NextPart(); e != io.EOF {
				storeError(w, ErrInvalidProjectInput)
				return
			}
		} else {
			problem(w, 415, "invalid_content_type", "请选择JSON角色配置或包含GLB的multipart上传")
			return
		}
		scope, _ := r.Context().Value(authScopeKey{}).(authScope)
		v, e := s.saveAvatar3D(r.Context(), space, r.PathValue("id"), key, scope.UserID, in, path)
		if e != nil {
			storeError(w, e)
			return
		}
		respond(w, 200, publicAvatar3D(v))
	})
	mux.HandleFunc("GET "+base+"/requests/{key}/result", func(w http.ResponseWriter, r *http.Request) {
		space, ok := spaceID(w, r)
		if !ok {
			return
		}
		project := r.PathValue("id")
		if _, e := s.GetProject(space, project); e != nil {
			storeError(w, e)
			return
		}
		c := s.creative
		c.mu.Lock()
		if c.pg != nil && c.pg.err != nil {
			err := c.pg.err
			c.mu.Unlock()
			storeError(w, err)
			return
		}
		receipt, ok := c.data.Receipts[projectReceiptKey(space, r.PathValue("key"))]
		v, exists := c.data.Avatars3D[receipt.Avatar3DID]
		c.mu.Unlock()
		if !ok || !exists || v.ProjectID != project || v.SpaceID != space {
			storeError(w, ErrNotFound)
			return
		}
		respond(w, 200, publicAvatar3D(v))
	})
	mux.HandleFunc("POST "+base+"/{modelId}/revoke", func(w http.ResponseWriter, r *http.Request) {
		space, _, ok := projectWriteScope(w, r)
		if !ok {
			return
		}
		var in struct {
			Confirmed bool `json:"confirmed"`
		}
		if !decodeProject(w, r, &in) {
			return
		}
		if !in.Confirmed {
			storeError(w, ErrInvalidProjectInput)
			return
		}
		v, e := s.avatar3D(space, r.PathValue("id"), r.PathValue("modelId"), false)
		if e != nil {
			storeError(w, e)
			return
		}
		c := s.creative
		c.mu.Lock()
		old := c.data.Avatars3D[v.ID]
		v = old
		if v.RevokedAt == nil {
			now := time.Now().UTC()
			v.RevokedAt = &now
			c.data.Avatars3D[v.ID] = v
			e = c.saveLocked()
			if e != nil {
				c.data.Avatars3D[v.ID] = old
			}
		}
		c.mu.Unlock()
		if e != nil {
			storeError(w, e)
			return
		}
		respond(w, 200, publicAvatar3D(v))
	})
	mux.HandleFunc("GET "+base+"/{modelId}/file", func(w http.ResponseWriter, r *http.Request) {
		space, ok := spaceID(w, r)
		if !ok {
			return
		}
		v, e := s.avatar3D(space, r.PathValue("id"), r.PathValue("modelId"), true)
		if e != nil {
			storeError(w, e)
			return
		}
		if v.Model == nil {
			storeError(w, ErrNotFound)
			return
		}
		dir, e := nativefs.MkdirTemp("", "studio-avatar3d-read-*")
		if e != nil {
			storeError(w, e)
			return
		}
		defer os.RemoveAll(dir)
		target := filepath.Join(dir, "model.glb")
		if e = s.materializeAvatarMedia(r.Context(), *v.Model, target); e != nil {
			storeError(w, e)
			return
		}
		f, e := os.Open(target)
		if e != nil {
			storeError(w, e)
			return
		}
		defer f.Close()
		w.Header().Set("Content-Type", "model/gltf-binary")
		w.Header().Set("Cache-Control", "private, no-store")
		w.Header().Set("Content-Disposition", `attachment; filename="model.glb"`)
		http.ServeContent(w, r, "model.glb", v.CreatedAt, f)
	})
	mux.HandleFunc("POST "+base+"/{modelId}/playbacks", func(w http.ResponseWriter, r *http.Request) {
		space, _, ok := projectWriteScope(w, r)
		if !ok {
			return
		}
		var in struct {
			Audio string `json:"audio_asset_id"`
			Voice string `json:"voice_authorization_id"`
		}
		if !decodeProject(w, r, &in) {
			return
		}
		scope, _ := r.Context().Value(authScopeKey{}).(authScope)
		if e := s.checkAvatar3DPlayback(r.Context(), space, r.PathValue("id"), r.PathValue("modelId"), scope.UserID, in.Audio, in.Voice); e != nil {
			storeError(w, e)
			return
		}
		respond(w, 200, map[string]string{"id": r.PathValue("modelId"), "lip_sync": "audio_energy", "audio_asset_id": in.Audio})
	})
}
