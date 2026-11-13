package application

import (
	"context"
	"io"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/publishing"
	"github.com/adcwb/ai-creative-studio/backend/internal/platform/nativefs"
)

func deliveryMetadata(state MediaState, export string, version int) (*publishing.DeliveryMetadata, error) {
	var chosen *publishing.DeliveryMetadata
	for _, v := range state.DeliveryMetadata {
		if v.ExportID == export && (version == 0 || v.Version == version) {
			v := v
			chosen = &v
		}
	}
	if version > 0 && chosen == nil {
		return nil, ErrNotFound
	}
	return chosen, nil
}
func openDeliveryCover(m *MediaStore, ctx context.Context, space, project string, v publishing.DeliveryMetadata) (deliveryInput, error) {
	path, e := m.localBlob(space, project, v.CoverAssetID)
	if e != nil {
		return deliveryInput{}, e
	}
	f, e := nativefs.OpenFile(path, os.O_RDONLY|nativefs.NoFollow|nativefs.NonBlock, 0)
	if e != nil {
		return deliveryInput{}, e
	}
	_, sha, e := copyDeliveryFile(ctx, io.Discard, f)
	if e == nil && sha != v.CoverSHA256 {
		e = ErrBlobIntegrity
	}
	if e == nil {
		_, e = f.Seek(0, io.SeekStart)
	}
	if e != nil {
		f.Close()
		return deliveryInput{}, e
	}
	ext := ""
	switch v.CoverMIME {
	case "image/png":
		ext = "png"
	case "image/jpeg":
		ext = "jpg"
	case "image/webp":
		ext = "webp"
	default:
		f.Close()
		return deliveryInput{}, ErrBlobIntegrity
	}
	return deliveryInput{"cover." + ext, f}, nil
}
func (m *MediaStore) saveDeliveryMetadata(ctx context.Context, space, project, export, key, actor string, in publishing.DeliveryInput) (publishing.DeliveryMetadata, error) {
	in, e := publishing.NormalizeDelivery(in)
	if e != nil {
		return publishing.DeliveryMetadata{}, mediaInvalid("请核对发布标题、标签及素材权利确认")
	}
	if in.CoverAssetID != "" && !mediaFileID(in.CoverAssetID) {
		return publishing.DeliveryMetadata{}, mediaInvalid("请选择本项目的封面图片")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	d := m.clone()
	k := mediaKey(space, project)
	p := mediaState(d, k)
	digest := mediaDigest([]any{"delivery-metadata", export, actor, in})
	replay, e := m.check(d, k, key, digest)
	if e != nil {
		return publishing.DeliveryMetadata{}, e
	}
	if replay {
		for _, v := range p.DeliveryMetadata {
			if v.ID == d.Receipts[k+":"+key].AssetID {
				return v, nil
			}
		}
		return publishing.DeliveryMetadata{}, ErrBlobIntegrity
	}
	valid := false
	for _, v := range p.Exports {
		if v.ID == export && v.Status == "succeeded" && v.Purpose == "" {
			valid = true
		}
	}
	if !valid {
		return publishing.DeliveryMetadata{}, mediaInvalid("只有已完成的正式成片可以准备发布资料")
	}
	current, _ := deliveryMetadata(p, export, 0)
	number := 0
	if current != nil {
		number = current.Version
	}
	if number != in.ExpectedMetadataVersion {
		return publishing.DeliveryMetadata{}, errMediaVersion
	}
	v := publishing.DeliveryMetadata{ID: mediaID(), ExportID: export, Version: number + 1, Title: in.Title, Description: in.Description, Tags: in.Tags, CoverAssetID: in.CoverAssetID, RightsConfirmed: true, CreatedAt: time.Now().UTC(), CreatedBy: actor}
	if in.CoverAssetID != "" {
		found := false
		for _, a := range p.Assets {
			if a.ID == in.CoverAssetID && a.Kind == "image" && (a.MIMEType == "image/png" || a.MIMEType == "image/jpeg" || a.MIMEType == "image/webp") {
				v.CoverMIME = a.MIMEType
				found = true
				break
			}
		}
		if !found {
			return v, mediaInvalid("请选择本项目的封面图片")
		}
		path, e := m.localBlob(space, project, in.CoverAssetID)
		if e != nil {
			return v, e
		}
		f, e := nativefs.OpenFile(path, os.O_RDONLY|nativefs.NoFollow|nativefs.NonBlock, 0)
		if e != nil {
			return v, e
		}
		_, v.CoverSHA256, e = copyDeliveryFile(ctx, io.Discard, f)
		e2 := f.Close()
		if e != nil {
			return v, e
		}
		if e2 != nil {
			return v, e2
		}
	}
	p.DeliveryMetadata = append(p.DeliveryMetadata, v)
	p.Version++
	d.Projects[k] = p
	d.Receipts[k+":"+key] = mediaReceipt{Digest: digest, AssetID: v.ID}
	if e = m.commit(d); e != nil {
		return v, e
	}
	return v, nil
}
func registerDeliveryMetadata(mux *http.ServeMux, s *FileStore, scope func(http.ResponseWriter, *http.Request, bool) (string, string, bool)) {
	base := "/api/v1/projects/{id}/media/exports/{export_id}/delivery-metadata"
	mux.HandleFunc("GET "+base, func(w http.ResponseWriter, r *http.Request) {
		space, _, ok := scope(w, r, false)
		if !ok {
			return
		}
		state, e := s.media.GetChecked(r.PathValue("id"), space)
		if e != nil {
			mediaError(w, e)
			return
		}
		found := false
		for _, v := range state.Exports {
			if v.ID == r.PathValue("export_id") {
				found = true
				break
			}
		}
		if !found {
			mediaError(w, ErrNotFound)
			return
		}
		items := []publishing.DeliveryMetadata{}
		for _, v := range state.DeliveryMetadata {
			if v.ExportID == r.PathValue("export_id") {
				items = append(items, v)
			}
		}
		w.Header().Set("Cache-Control", "private, no-store")
		respond(w, 200, map[string]any{"version": state.Version, "items": items})
	})
	mux.HandleFunc("POST "+base, func(w http.ResponseWriter, r *http.Request) {
		space, key, ok := scope(w, r, true)
		if !ok {
			return
		}
		var in publishing.DeliveryInput
		if !decode(w, r, &in) {
			return
		}
		actor := "demo-user"
		if a, ok := r.Context().Value(authScopeKey{}).(authScope); ok {
			actor = a.UserID
		}
		v, e := s.media.saveDeliveryMetadata(r.Context(), space, r.PathValue("id"), r.PathValue("export_id"), key, actor, in)
		if e != nil {
			mediaError(w, e)
			return
		}
		respond(w, 200, v)
	})
}
func deliveryMetadataQuery(r *http.Request) (int, error) {
	raw := r.URL.Query().Get("metadata_version")
	if raw == "" {
		return 0, nil
	}
	n, e := strconv.Atoi(raw)
	if e != nil || n < 1 {
		return 0, mediaInvalid("发布资料版本无效")
	}
	return n, nil
}
