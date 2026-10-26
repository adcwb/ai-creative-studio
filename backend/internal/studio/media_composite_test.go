package studio

import (
	"context"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestCompositeTimelineBounds(t *testing.T) {
	p := MediaState{Assets: []MediaAsset{{ID: "image", Kind: "image"}, {ID: "video", Kind: "video", Duration: 5}}}
	base := MediaTimeline{Clips: []MediaClip{{ID: "one", AssetID: "image", Duration: 1, Transition: "crossfade", TransitionDuration: .5}, {ID: "two", AssetID: "image", Duration: 1}}, VideoOverlays: []MediaVideoOverlay{{ID: "layer", AssetID: "video", Start: .2, Duration: 1, Width: .5, Height: .5, Opacity: .5}}}
	if err := validateTimeline(p, base); err != nil {
		t.Fatal(err)
	}
	if timelineDuration(base) != 1.5 {
		t.Fatal("overlap was not subtracted")
	}
	cases := []struct {
		name   string
		change func(*MediaTimeline)
	}{
		{"last-crossfade", func(v *MediaTimeline) { v.Clips[1].Transition = "crossfade"; v.Clips[1].TransitionDuration = .1 }},
		{"adjacent-half", func(v *MediaTimeline) { v.Clips[0].TransitionDuration = .6 }},
		{"foreign-overlay", func(v *MediaTimeline) { v.VideoOverlays[0].AssetID = "other-project" }},
		{"overlay-shortened-end", func(v *MediaTimeline) { v.VideoOverlays[0].Start = .6 }},
		{"overlay-source-end", func(v *MediaTimeline) { v.VideoOverlays[0].TrimStart = 4.5 }},
		{"overlay-rectangle", func(v *MediaTimeline) { v.VideoOverlays[0].X = .6 }},
		{"overlay-nan", func(v *MediaTimeline) { v.VideoOverlays[0].Opacity = math.NaN() }},
		{"overlay-muted-volume", func(v *MediaTimeline) { v.VideoOverlays[0].SourceAudioVolume = 1 }},
		{"overlay-image-audio", func(v *MediaTimeline) {
			v.VideoOverlays[0].AssetID = "image"
			v.VideoOverlays[0].PreserveSourceAudio = true
		}},
		{"overlay-image-trim", func(v *MediaTimeline) { v.VideoOverlays[0].AssetID = "image"; v.VideoOverlays[0].TrimStart = 1 }},
		{"overlay-duplicate", func(v *MediaTimeline) { v.VideoOverlays = append(v.VideoOverlays, v.VideoOverlays[0]) }},
		{"overlay-cap", func(v *MediaTimeline) { v.VideoOverlays = make([]MediaVideoOverlay, 5) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := base
			v.Clips = append([]MediaClip(nil), base.Clips...)
			v.VideoOverlays = append([]MediaVideoOverlay(nil), base.VideoOverlays...)
			tc.change(&v)
			if validateTimeline(p, v) == nil {
				t.Fatal("invalid composition accepted")
			}
		})
	}
}
func compositePixel(t *testing.T, path, at string, x, y int) []byte {
	t.Helper()
	out, err := exec.Command("ffmpeg", "-v", "error", "-ss", at, "-i", path, "-vf", "crop=2:2:"+strconv.Itoa(x)+":"+strconv.Itoa(y)+",scale=1:1", "-frames:v", "1", "-f", "rawvideo", "-pix_fmt", "rgb24", "-").Output()
	if err != nil || len(out) != 3 {
		t.Fatalf("pixel decode: %v, %d", err, len(out))
	}
	return out
}
func compositeRMS(t *testing.T, path, at string) float64 {
	t.Helper()
	pcm, err := exec.Command("ffmpeg", "-v", "error", "-ss", at, "-i", path, "-map", "0:a:0", "-t", "0.1", "-f", "s16le", "-ac", "1", "-").Output()
	if err != nil || len(pcm) == 0 {
		t.Fatal("PCM decode", err)
	}
	energy := 0.0
	for i := 0; i+1 < len(pcm); i += 2 {
		v := float64(int16(binary.LittleEndian.Uint16(pcm[i : i+2])))
		energy += v * v
	}
	return math.Sqrt(energy / float64(len(pcm)/2))
}
func TestCompositeActualPixelsAudioAndSubtitles(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	m, err := openMediaStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer m.close()
	red, blue, green, tone := mediaID(), mediaID(), mediaID(), mediaID()
	if err = os.WriteFile(filepath.Join(m.dir, tone), syntheticWAV(1), 0600); err != nil {
		t.Fatal(err)
	}
	for id, c := range map[string]color.Color{red: color.RGBA{255, 0, 0, 255}, blue: color.RGBA{0, 0, 255, 255}} {
		f, e := os.Create(filepath.Join(m.dir, id))
		if e != nil {
			t.Fatal(e)
		}
		pic := image.NewRGBA(image.Rect(0, 0, 32, 32))
		for y := 0; y < 32; y++ {
			for x := 0; x < 32; x++ {
				pic.Set(x, y, c)
			}
		}
		e = png.Encode(f, pic)
		f.Close()
		if e != nil {
			t.Fatal(e)
		}
	}
	if err = ffmpeg(ctx, "-f", "lavfi", "-i", "color=c=lime:s=64x64:r=24:d=1", "-f", "lavfi", "-i", "sine=frequency=880:duration=1", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", "-t", "1", "-f", "mp4", filepath.Join(m.dir, green)); err != nil {
		t.Fatal(err)
	}
	p := MediaState{Assets: []MediaAsset{{ID: red, Kind: "image"}, {ID: blue, Kind: "image"}, {ID: green, Kind: "video", Duration: 1}, {ID: tone, Kind: "audio", Duration: 1}}}
	timeline := MediaTimeline{AspectRatio: "1:1", SubtitleMode: "none", Clips: []MediaClip{{ID: "one", AssetID: red, Duration: 1, Transition: "crossfade", TransitionDuration: .5, Subtitle: "FIRST", AudioAssetID: tone, AudioVolume: 1}, {ID: "two", AssetID: blue, Duration: 1, Subtitle: "SECOND", AudioAssetID: tone, AudioVolume: 1}}}
	id := mediaID()
	if err = m.render(ctx, p, MediaExport{ID: id, Timeline: timeline}); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(m.dir, id+".mp4")
	probe, err := probeMedia(output)
	if err != nil {
		t.Fatal(err)
	}
	duration, _ := strconv.ParseFloat(probe.Format.Duration, 64)
	if math.Abs(duration-1.5) > .1 {
		t.Fatal("wrong crossfade duration", duration)
	}
	if compositeRMS(t, output, "0.75") < 100 {
		t.Fatal("crossfade dropped clip audio")
	}
	middle := compositePixel(t, output, "0.75", 180, 180)
	if middle[0] < 60 || middle[2] < 60 || middle[1] > 40 {
		t.Fatal("crossfade midpoint not blended red/blue", middle)
	}
	srt, err := os.ReadFile(filepath.Join(m.dir, id+".srt"))
	if err != nil || !strings.Contains(string(srt), "00:00:00,500 --> 00:00:01,500\nSECOND") {
		t.Fatal("second subtitle not shifted into overlap", string(srt), err)
	}
	for i := range timeline.Clips {
		timeline.Clips[i].AudioAssetID = ""
		timeline.Clips[i].AudioVolume = 0
	}
	timeline.VideoOverlays = []MediaVideoOverlay{{ID: "green", AssetID: green, Start: .2, Duration: .4, X: .25, Y: .25, Width: .5, Height: .5, Opacity: 1, PreserveSourceAudio: true, SourceAudioVolume: 1}, {ID: "blue", AssetID: blue, Start: .2, Duration: .4, X: .25, Y: .25, Width: .5, Height: .5, Opacity: .5}}
	id = mediaID()
	if err = m.render(ctx, p, MediaExport{ID: id, Timeline: timeline}); err != nil {
		t.Fatal(err)
	}
	output = filepath.Join(m.dir, id+".mp4")
	inside := compositePixel(t, output, "0.35", 180, 180)
	outside := compositePixel(t, output, "0.35", 20, 20)
	if inside[1] < 70 || inside[2] < 70 || inside[0] > 50 {
		t.Fatal("ordered half-opacity overlay not blended green/blue", inside)
	}
	if outside[0] < 200 || outside[1] > 40 || outside[2] > 40 {
		t.Fatal("overlay modified pixels outside rectangle", outside)
	}
	before := compositePixel(t, output, "0.05", 180, 180)
	if before[0] < 200 || before[1] > 40 {
		t.Fatal("overlay visible before start", before)
	}
	if compositeRMS(t, output, "0.3") < 100 || compositeRMS(t, output, "0.05") > 20 || compositeRMS(t, output, "0.9") > 20 {
		t.Fatal("overlay source audio not restricted to explicit interval")
	}
	// A non-overlapping boundary after a crossfade still uses the shortened clock.
	timeline.VideoOverlays = nil
	timeline.Clips = append(timeline.Clips, MediaClip{ID: "three", AssetID: red, Duration: 1})
	id = mediaID()
	if err = m.render(ctx, p, MediaExport{ID: id, Timeline: timeline}); err != nil {
		t.Fatal(err)
	}
	probe, err = probeMedia(filepath.Join(m.dir, id+".mp4"))
	if err != nil {
		t.Fatal(err)
	}
	duration, _ = strconv.ParseFloat(probe.Format.Duration, 64)
	if math.Abs(duration-2.5) > .1 {
		t.Fatal("mixed concat/xfade duration", duration)
	}
}
