package application

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	_ "image/png"
	"math"
	"net/http"
	"os"
	"path/filepath"
)

type photoNormalize struct {
	ExpectedVersion int    `json:"expected_version"`
	AssetID         string `json:"asset_id"`
	Confirmed       bool   `json:"confirmed"`
}

// Bilinear resampling of premultiplied channels followed by a white matte.
// Originals remain immutable; normalized pixels become a separate owned asset.
func normalizedPhoto(src image.Image) ([]byte, error) {
	bounds := src.Bounds()
	sw, sh := bounds.Dx(), bounds.Dy()
	if sw < 1 || sh < 1 || int64(sw)*int64(sh) > 20000000 {
		return nil, errMediaLimit
	}
	scale := math.Min(1, math.Min(1080/float64(sw), 1080/float64(sh)))
	w, h := max(1, int(float64(sw)*scale)), max(1, int(float64(sh)*scale))
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			sx := math.Max(0, (float64(x)+.5)*float64(sw)/float64(w)-.5)
			sy := math.Max(0, (float64(y)+.5)*float64(sh)/float64(h)-.5)
			x0, y0 := int(sx), int(sy)
			fx, fy := sx-float64(x0), sy-float64(y0)
			var out [4]float64
			for j := 0; j < 2; j++ {
				for i := 0; i < 2; i++ {
					r, g, b, a := src.At(bounds.Min.X+min(sw-1, x0+i), bounds.Min.Y+min(sh-1, y0+j)).RGBA()
					weight := []float64{1 - fx, fx}[i] * []float64{1 - fy, fy}[j]
					for c, v := range []uint32{r, g, b, a} {
						out[c] += float64(v) * weight
					}
				}
			}
			dst.SetRGBA(x, y, color.RGBA{uint8(math.Round((out[0] + 65535 - out[3]) / 257)), uint8(math.Round((out[1] + 65535 - out[3]) / 257)), uint8(math.Round((out[2] + 65535 - out[3]) / 257)), 255})
		}
	}
	var buf bytes.Buffer
	e := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: 90})
	return buf.Bytes(), e
}
func (m *MediaStore) normalizeSocialPhoto(project, space, key string, in photoNormalize) (MediaState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d := m.clone()
	k := mediaKey(space, project)
	p := mediaState(d, k)
	digest := mediaDigest([]any{"photo-normalize", in})
	if replay, e := m.check(d, k, key, digest); e != nil || replay {
		return p, e
	}
	if p.Version != in.ExpectedVersion {
		return p, errMediaVersion
	}
	if !in.Confirmed {
		return p, mediaInvalid("explicit JPEG conversion consent required")
	}
	a, ok := mediaAsset(p, in.AssetID)
	if !ok || a.Kind != "image" || (a.MIMEType != "image/png" && a.MIMEType != "image/jpeg") {
		return p, ErrNotFound
	}
	path, e := m.localBlob(space, project, a.ID)
	if e != nil {
		return p, e
	}
	f, e := os.Open(path)
	if e != nil {
		return p, e
	}
	defer f.Close()
	st, e := f.Stat()
	if e != nil || !st.Mode().IsRegular() || st.Size() <= 0 || st.Size() > 20000000 {
		return p, errMediaLimit
	}
	cfg, format, e := image.DecodeConfig(f)
	if e != nil || (format != "png" && format != "jpeg") || cfg.Width < 1 || cfg.Height < 1 || int64(cfg.Width)*int64(cfg.Height) > 20000000 {
		return p, errMediaLimit
	}
	if _, e = f.Seek(0, 0); e != nil {
		return p, e
	}
	src, _, e := image.Decode(f)
	if e != nil {
		return p, mediaInvalid("image decode failed")
	}
	data, e := normalizedPhoto(src)
	if e != nil {
		return p, e
	}
	if len(data) > 20000000 || len(p.Assets) >= 120 || m.overQuota(int64(len(data))) {
		return p, errMediaLimit
	}
	id := mediaID()
	out := filepath.Join(m.dir, id)
	if e = os.WriteFile(out, data, 0600); e != nil {
		return p, e
	}
	if e = m.storeBlob(space, project, id); e != nil {
		os.Remove(out)
		return p, e
	}
	p.Assets = append(p.Assets, MediaAsset{ID: id, Kind: "image", MIMEType: "image/jpeg", Name: "social-photo.jpg", Role: "photo", Source: "social-photo-normalized", FileURL: mediaURL(space, project, "assets/"+id+"/file")})
	p.Version++
	d.Projects[k] = p
	d.Receipts[k+":"+key] = mediaReceipt{Digest: digest, AssetID: id}
	return p, m.commit(d)
}
func registerPhotoNormalize(mux *http.ServeMux, s *FileStore, scope func(http.ResponseWriter, *http.Request, bool) (string, string, bool)) {
	mux.HandleFunc("POST /api/v1/projects/{id}/social/photos/normalize", func(w http.ResponseWriter, r *http.Request) {
		space, key, ok := scope(w, r, true)
		if !ok {
			return
		}
		var in photoNormalize
		if !decodeProject(w, r, &in) {
			return
		}
		out, e := s.media.normalizeSocialPhoto(r.PathValue("id"), space, key, in)
		if e != nil {
			socialError(w, e)
			return
		}
		asset, e := s.media.AssetForReceipt(r.PathValue("id"), space, key)
		if e != nil {
			socialError(w, e)
			return
		}
		respond(w, 200, struct {
			MediaState
			NormalizedAssetID string `json:"normalized_asset_id"`
		}{out, asset.ID})
	})
}
