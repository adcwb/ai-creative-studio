package application

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io"
)

// A bounded contact sheet allows any configured vision adapter to compare
// appearance in two project-owned images. This is creative feedback, not identity recognition.
func consistencyContactSheet(left, right []byte) ([]byte, error) {
	return consistencyContactSheetReaders([]io.ReadSeeker{bytes.NewReader(left), bytes.NewReader(right)})
}
func consistencyContactSheetFiles(left, right string) ([]byte, error) {
	var readers []io.ReadSeeker
	for _, path := range []string{left, right} {
		f, e := openLocalBlob(path)
		if e != nil {
			return nil, e
		}
		defer f.Close()
		readers = append(readers, f)
	}
	return consistencyContactSheetReaders(readers)
}
func consistencyContactSheetReaders(readers []io.ReadSeeker) ([]byte, error) {

	out := image.NewRGBA(image.Rect(0, 0, 1024, 512))
	draw.Draw(out, out.Bounds(), &image.Uniform{C: color.RGBA{240, 240, 240, 255}}, image.Point{}, draw.Src)
	for i, reader := range readers {
		cfg, format, err := image.DecodeConfig(reader)
		if err != nil || cfg.Width < 1 || cfg.Height < 1 || cfg.Width > 20_000_000/cfg.Height || (format != "png" && format != "jpeg") {
			return nil, providerFailure("provider_rejected")
		}
		if _, err = reader.Seek(0, 0); err != nil {
			return nil, err
		}
		img, _, err := image.Decode(reader)
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
