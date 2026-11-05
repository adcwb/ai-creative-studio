package application

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Encode frame numbers as nine high-contrast bars. Decoding those bars compares
// source-frame selection across a segment seam without mistaking H.264's lossy
// quantization/GOP differences for a timing error.
func renderFrameNumbers(t *testing.T, path string) []int {
	t.Helper()
	b, e := exec.Command("ffmpeg", "-v", "error", "-i", path, "-vf", "crop=iw:2:0:ih/2,scale=9:1:flags=neighbor", "-an", "-f", "rawvideo", "-pix_fmt", "rgb24", "-").Output()
	if e != nil || len(b)%27 != 0 {
		t.Fatal("frame barcode decode", len(b), e)
	}
	out := []int{}
	for pos := 0; pos < len(b); pos += 27 {
		n := 0
		for bit := 0; bit < 9; bit++ {
			if b[pos+bit*3] > 127 {
				n |= 1 << bit
			}
		}
		out = append(out, n)
	}
	return out
}
func TestRenderCheckpointVideoFrameSelectionMatchesContinuousRender(t *testing.T) {
	m, e := openMediaStore(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer m.close()
	source := t.TempDir()
	for n := range 400 {
		pic := image.NewRGBA(image.Rect(0, 0, 18, 2))
		for bit := range 9 {
			v := uint8(0)
			if n&(1<<bit) != 0 {
				v = 255
			}
			for y := range 2 {
				for x := bit * 2; x < bit*2+2; x++ {
					pic.SetRGBA(x, y, color.RGBA{v, v, v, 255})
				}
			}
		}
		f, e := os.Create(filepath.Join(source, fmt.Sprintf("%04d.png", n)))
		if e != nil {
			t.Fatal(e)
		}
		e = png.Encode(f, pic)
		e2 := f.Close()
		if e != nil || e2 != nil {
			t.Fatal(e, e2)
		}
	}
	id := mediaID()
	path := filepath.Join(m.dir, id)
	if e = ffmpeg(context.Background(), "-framerate", "30", "-i", filepath.Join(source, "%04d.png"), "-c:v", "libx264", "-crf", "0", "-pix_fmt", "yuv420p", "-f", "mp4", path); e != nil {
		t.Fatal(e)
	}
	p := MediaState{Assets: []MediaAsset{{ID: id, Kind: "video", Duration: 400.0 / 30}}}
	clip := MediaClip{ID: "video", AssetID: id, Duration: 12.625, TrimStart: .07}
	timeline := MediaTimeline{Clips: []MediaClip{clip}, SubtitleMode: "none"}
	reference := filepath.Join(t.TempDir(), "reference.mp4")
	if e = m.renderClipVideo(context.Background(), p, clip, 0, 303, reference, 640, 360, "4M"); e != nil {
		t.Fatal(e)
	}
	export := MediaExport{ID: mediaID(), Timeline: timeline}
	if e = m.render(context.Background(), p, export); e != nil {
		t.Fatal(e)
	}
	want, got := renderFrameNumbers(t, reference), renderFrameNumbers(t, filepath.Join(m.dir, export.ID+".mp4"))
	if len(want) != 303 || len(got) != len(want) {
		t.Fatal("frame count", len(want), len(got))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("source frame at output %d differs after segmentation: got %d expected %d", i, got[i], want[i])
		}
	}
}
