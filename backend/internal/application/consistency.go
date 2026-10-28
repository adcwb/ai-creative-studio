package application

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/png"
)

// A bounded contact sheet allows any configured vision adapter to compare
// appearance in two project-owned images. This is creative feedback, not identity recognition.
func consistencyContactSheet(left, right []byte) ([]byte, error) {
	out := image.NewRGBA(image.Rect(0, 0, 1024, 512))
	draw.Draw(out, out.Bounds(), &image.Uniform{C: color.RGBA{240, 240, 240, 255}}, image.Point{}, draw.Src)
	for i, data := range [][]byte{left, right} {
		if len(data) == 0 || len(data) > 16<<20 {
			return nil, providerFailure("provider_rejected")
		}
		cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
		if err != nil || cfg.Width < 1 || cfg.Height < 1 || cfg.Width > 20_000_000/cfg.Height {
			return nil, providerFailure("provider_rejected")
		}
		img, _, err := image.Decode(bytes.NewReader(data))
		if err != nil {
			return nil, providerFailure("provider_rejected")
		}
		w, h := 504, 504
		if cfg.Width > cfg.Height {
			h = max(1, 504*cfg.Height/cfg.Width)
		} else {
			w = max(1, 504*cfg.Width/cfg.Height)
		}
		ox, oy := i*512+(512-w)/2, (512-h)/2
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				out.Set(ox+x, oy+y, img.At(img.Bounds().Min.X+x*cfg.Width/w, img.Bounds().Min.Y+y*cfg.Height/h))
			}
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, out); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}
