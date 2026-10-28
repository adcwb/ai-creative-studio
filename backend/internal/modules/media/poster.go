package media

import (
	"math"
	"regexp"
	"strings"
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

func ValidatePoster(p MediaState, v PosterDocument) error {
	if v.Width < 256 || v.Height < 256 || v.Width > 2048 || v.Height > 2048 || len(v.Layers) > 12 || !posterColor.MatchString(v.Background) {
		return Invalid("海报尺寸、背景或图层数量无效")
	}
	ids := map[string]bool{}
	for _, l := range v.Layers {
		if !shotIDPattern.MatchString(l.ID) || ids[l.ID] || l.X < 0 || l.Y < 0 || l.Width < 1 || l.Height < 1 || l.Width > v.Width || l.Height > v.Height || l.X > v.Width || l.Y > v.Height || l.X+l.Width > v.Width || l.Y+l.Height > v.Height || math.IsNaN(l.Opacity) || math.IsInf(l.Opacity, 0) || l.Opacity < 0 || l.Opacity > 1 {
			return Invalid("海报图层位置或透明度无效")
		}
		ids[l.ID] = true
		switch l.Kind {
		case "text":
			if !utf8.ValidString(l.Text) || utf8.RuneCountInString(l.Text) < 1 || utf8.RuneCountInString(l.Text) > 300 || strings.Count(l.Text, "\n") > 8 || l.FontSize < 12 || l.FontSize > 200 || !posterColor.MatchString(l.Color) || l.AssetID != "" {
				return Invalid("海报文字设置无效")
			}
			for _, r := range l.Text {
				if unicode.IsControl(r) && r != '\n' {
					return Invalid("海报文字包含无效控制字符")
				}
			}
		case "logo":
			a, ok := Asset(p, l.AssetID)
			if !ok || a.Kind != "image" || l.Text != "" {
				return Invalid("海报 Logo 必须使用本项目图片")
			}
		default:
			return Invalid("海报图层类型无效")
		}
	}
	return nil
}
