package application

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func (m *MediaStore) SavePoster(project, space, key string, version int, v PosterDocument) (MediaState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d := m.clone()
	k := mediaKey(space, project)
	p := mediaState(d, k)
	digest := mediaDigest([]any{"poster", version, v})
	if replay, err := m.check(d, k, key, digest); err != nil || replay {
		return p, err
	}
	if p.Version != version {
		return p, errMediaVersion
	}
	if err := validatePoster(p, v); err != nil {
		return p, err
	}
	p.Poster = &v
	p.Version++
	d.Projects[k] = p
	d.Receipts[k+":"+key] = mediaReceipt{Digest: digest}
	return p, m.commit(d)
}
func posterRGBA(s string) color.RGBA {
	var r, g, b uint8
	fmt.Sscanf(s, "#%02x%02x%02x", &r, &g, &b)
	return color.RGBA{r, g, b, 255}
}
func (m *MediaStore) renderPoster(ctx context.Context, space, project, dir string, v PosterDocument) ([]byte, error) {
	base := image.NewRGBA(image.Rect(0, 0, v.Width, v.Height))
	draw.Draw(base, base.Bounds(), &image.Uniform{C: posterRGBA(v.Background)}, image.Point{}, draw.Src)
	current := filepath.Join(dir, "base.png")
	f, err := os.Create(current)
	if err != nil {
		return nil, err
	}
	err = png.Encode(f, base)
	closeErr := f.Close()
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		return nil, closeErr
	}
	for i, l := range v.Layers {
		out := filepath.Join(dir, fmt.Sprintf("layer-%d.png", i))
		args := []string{"-protocol_whitelist", "file,pipe", "-i", current}
		if l.Kind == "logo" {
			source, err := m.localBlob(space, project, l.AssetID)
			if err != nil {
				return nil, err
			}
			args = append(args, "-protocol_whitelist", "file,pipe", "-i", source, "-filter_complex", fmt.Sprintf("[1:v]scale=%d:%d:force_original_aspect_ratio=decrease,format=rgba,colorchannelmixer=aa=%s[logo];[0:v][logo]overlay=%d:%d:format=auto", l.Width, l.Height, seconds(l.Opacity), l.X, l.Y))
		} else {
			// Never interpolate user text in a filter expression. Disable expansion so
			// %{...}, colons, quotes, paths and newlines remain literal text.
			textPath := filepath.Join(dir, fmt.Sprintf("text-%d.txt", i))
			if strings.ContainsAny(textPath, "\\':;,[]\n\r") {
				return nil, mediaInvalid("poster data directory contains unsupported filter-path characters")
			}
			if err := os.WriteFile(textPath, []byte(l.Text), 0600); err != nil {
				return nil, err
			}
			args = append(args, "-filter_complex", fmt.Sprintf("color=c=black@0:s=%dx%d,format=rgba,drawtext=font='sans':textfile='%s':expansion=none:fontsize=%d:fontcolor=%s@%s:x=0:y=0[text];[0:v][text]overlay=%d:%d:format=auto", l.Width, l.Height, textPath, l.FontSize, l.Color, seconds(l.Opacity), l.X, l.Y))
		}
		args = append(args, "-frames:v", "1", "-threads", "1", "-update", "1", out)
		if err := ffmpeg(ctx, args...); err != nil {
			return nil, err
		}
		current = out
	}
	return os.ReadFile(current)
}

// Rendering is bounded to 12 layers/4 MP/30 seconds. The media transaction stays
// locked so an export always corresponds to exactly the accepted saved version.
func (m *MediaStore) ExportPoster(project, space, key string, version int) (MediaState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d := m.clone()
	k := mediaKey(space, project)
	p := mediaState(d, k)
	digest := mediaDigest([]any{"poster-export", version})
	if replay, err := m.check(d, k, key, digest); err != nil || replay {
		return p, err
	}
	if p.Version != version {
		return p, errMediaVersion
	}
	if p.Poster == nil {
		return p, mediaInvalid("请先保存海报")
	}
	if err := validatePoster(p, *p.Poster); err != nil {
		return p, err
	}
	if len(p.Assets) >= 120 || m.overQuota(32<<20) {
		return p, errMediaLimit
	}
	dir, err := os.MkdirTemp(m.dir, "poster-")
	if err != nil {
		return p, err
	}
	defer os.RemoveAll(dir)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	data, err := m.renderPoster(ctx, space, project, dir, *p.Poster)
	if err != nil {
		return p, err
	}
	if len(data) > 32<<20 {
		return p, errMediaLimit
	}
	id := mediaID()
	path := filepath.Join(m.dir, id)
	if err = os.WriteFile(path, data, 0600); err != nil {
		return p, err
	}
	if _, _, err = validateMedia(path, "image/png"); err != nil {
		os.Remove(path)
		return p, err
	}
	if err = m.storeBlob(space, project, id); err != nil {
		os.Remove(path)
		return p, err
	}
	p.Assets = append(p.Assets, MediaAsset{ID: id, Kind: "image", MIMEType: "image/png", Name: "poster.png", Role: "poster", Source: "poster-composite", FileURL: mediaURL(space, project, "assets/"+id+"/file")})
	p.Version++
	d.Projects[k] = p
	d.Receipts[k+":"+key] = mediaReceipt{Digest: digest, AssetID: id}
	return p, m.commit(d)
}
