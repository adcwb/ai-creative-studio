package studio

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Coordinates are integer output pixels. Saved order is back-to-front.
type PosterLayer struct {
	ID       string  `json:"id"`
	Kind     string  `json:"kind"`
	Text     string  `json:"text"`
	AssetID  string  `json:"asset_id"`
	X        int     `json:"x"`
	Y        int     `json:"y"`
	Width    int     `json:"width"`
	Height   int     `json:"height"`
	FontSize int     `json:"font_size"`
	Color    string  `json:"color"`
	Opacity  float64 `json:"opacity"`
}
type PosterDocument struct {
	Width      int           `json:"width"`
	Height     int           `json:"height"`
	Background string        `json:"background"`
	Layers     []PosterLayer `json:"layers"`
}

var posterColor = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

func validatePoster(p MediaState, v PosterDocument) error {
	if v.Width < 256 || v.Height < 256 || v.Width > 2048 || v.Height > 2048 || len(v.Layers) > 12 || !posterColor.MatchString(v.Background) {
		return mediaInvalid("海报尺寸、背景或图层数量无效")
	}
	ids := map[string]bool{}
	for _, l := range v.Layers {
		if !shotIDPattern.MatchString(l.ID) || ids[l.ID] || l.X < 0 || l.Y < 0 || l.Width < 1 || l.Height < 1 || l.Width > v.Width || l.Height > v.Height || l.X > v.Width || l.Y > v.Height || l.X+l.Width > v.Width || l.Y+l.Height > v.Height || math.IsNaN(l.Opacity) || math.IsInf(l.Opacity, 0) || l.Opacity < 0 || l.Opacity > 1 {
			return mediaInvalid("海报图层位置或透明度无效")
		}
		ids[l.ID] = true
		switch l.Kind {
		case "text":
			if !utf8.ValidString(l.Text) || utf8.RuneCountInString(l.Text) < 1 || utf8.RuneCountInString(l.Text) > 300 || strings.Count(l.Text, "\n") > 8 || l.FontSize < 12 || l.FontSize > 200 || !posterColor.MatchString(l.Color) || l.AssetID != "" {
				return mediaInvalid("海报文字设置无效")
			}
			for _, r := range l.Text {
				if unicode.IsControl(r) && r != '\n' {
					return mediaInvalid("海报文字包含无效控制字符")
				}
			}
		case "logo":
			a, ok := mediaAsset(p, l.AssetID)
			if !ok || a.Kind != "image" || l.Text != "" {
				return mediaInvalid("海报 Logo 必须使用本项目图片")
			}
		default:
			return mediaInvalid("海报图层类型无效")
		}
	}
	return nil
}
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
