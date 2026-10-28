package studio

import (
	"context"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestKeyframeAnd4KBounds(t *testing.T) {
	layer := MediaVideoOverlay{Width: .25, Height: .25, Duration: 2, Keyframes: []MediaKeyframe{{Time: 0, X: 0, Y: 0, Opacity: 0}, {Time: 2, X: .75, Y: .75, Opacity: 1}}}
	if err := validateKeyframes(layer); err != nil {
		t.Fatal(err)
	}
	cases := []MediaKeyframe{{Time: 0, X: 0, Y: 0, Opacity: 1}, {Time: 3, X: 0, Y: 0, Opacity: 1}, {Time: 1, X: .8, Y: 0, Opacity: 1}, {Time: 1, X: 0, Y: math.NaN(), Opacity: 1}, {Time: 1, X: 0, Y: 0, Opacity: 1.1}}
	for _, k := range cases {
		bad := layer
		bad.Keyframes = []MediaKeyframe{layer.Keyframes[0], k}
		if validateKeyframes(bad) == nil {
			t.Fatal("invalid keyframe accepted", k)
		}
	}
	bad := layer
	bad.Keyframes = []MediaKeyframe{{Time: .1, Opacity: 1}}
	if validateKeyframes(bad) == nil {
		t.Fatal("nonzero first time accepted")
	}
	bad.Keyframes = make([]MediaKeyframe, 17)
	if validateKeyframes(bad) == nil {
		t.Fatal("excess keys accepted")
	}
	p := MediaState{Assets: []MediaAsset{{ID: "a", Kind: "image"}}}
	timeline := MediaTimeline{OutputResolution: "2160p", Clips: []MediaClip{{ID: "a", AssetID: "a", Duration: 15}}}
	if err := validateTimeline(p, timeline); err != nil {
		t.Fatal(err)
	}
	timeline.Clips[0].Duration = 15.01
	if validateTimeline(p, timeline) == nil {
		t.Fatal("4K workload limit bypassed")
	}
	for _, ratio := range []string{"16:9", "9:16", "1:1"} {
		timeline.AspectRatio = ratio
		w, h, max, e := outputSpec(timeline)
		if e != nil || max != 15 || w*h > 3840*2160 {
			t.Fatal(w, h, max, e)
		}
	}
	for _, name := range []string{"wipeleft", "wiperight", "slideleft", "slideright", "circleopen"} {
		timeline = MediaTimeline{Clips: []MediaClip{{ID: "a", AssetID: "a", Duration: 1, Transition: name, TransitionDuration: .5}, {ID: "b", AssetID: "a", Duration: 1}}}
		if err := validateTimeline(p, timeline); err != nil {
			t.Fatal(name, err)
		}
		if timelineDuration(timeline) != 1.5 {
			t.Fatal("wrong overlap clock")
		}
		timeline.Clips = timeline.Clips[:1]
		if validateTimeline(p, timeline) == nil {
			t.Fatal("final transition accepted")
		}
	}
}
func TestKeyframeActualMotionOpacityAnd4K(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	m, err := openMediaStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer m.close()
	red, blue, translucent := mediaID(), mediaID(), mediaID()
	for id, c := range map[string]color.Color{red: color.RGBA{255, 0, 0, 255}, blue: color.RGBA{0, 0, 255, 255}, translucent: color.NRGBA{0, 0, 255, 128}} {
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
	p := MediaState{Assets: []MediaAsset{{ID: red, Kind: "image"}, {ID: blue, Kind: "image"}, {ID: translucent, Kind: "image"}}}
	timeline := MediaTimeline{AspectRatio: "1:1", Clips: []MediaClip{{ID: "one", AssetID: red, Duration: 2}}, VideoOverlays: []MediaVideoOverlay{{ID: "moving", AssetID: blue, Start: .25, Duration: 1.5, Width: .25, Height: .25, Opacity: 1, Keyframes: []MediaKeyframe{{Time: 0, X: 0, Y: 0, Opacity: 0}, {Time: 1, X: .5, Y: .5, Opacity: 1}}}}}
	id := mediaID()
	if err = m.render(ctx, p, MediaExport{ID: id, Timeline: timeline}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(m.dir, id+".mp4")
	middle := compositePixel(t, path, "0.75", 125, 125)
	if middle[0] < 70 || middle[2] < 70 || middle[1] > 40 {
		t.Fatal("animated alpha midpoint", middle)
	}
	old := compositePixel(t, path, "1.4", 30, 30)
	end := compositePixel(t, path, "1.4", 220, 220)
	if old[0] < 200 || old[2] > 40 || end[2] < 200 || end[0] > 40 {
		t.Fatal("motion/endpoint hold", old, end)
	}
	after := compositePixel(t, path, "1.9", 220, 220)
	if after[0] < 200 || after[2] > 40 {
		t.Fatal("layer persists after interval", after)
	}

	timeline.VideoOverlays = []MediaVideoOverlay{{ID: "transparent", AssetID: translucent, Duration: 1, Width: 1, Height: 1, Opacity: 1, Keyframes: []MediaKeyframe{{Time: 0, Opacity: .5}, {Time: 1, Opacity: .5}}}}
	id = mediaID()
	if err = m.render(ctx, p, MediaExport{ID: id, Timeline: timeline}); err != nil {
		t.Fatal(err)
	}
	alphaPixel := compositePixel(t, filepath.Join(m.dir, id+".mp4"), "0.5", 180, 180)
	if alphaPixel[0] < 165 || alphaPixel[0] > 210 || alphaPixel[2] < 40 || alphaPixel[2] > 90 {
		t.Fatal("source alpha must multiply keyframe opacity", alphaPixel)
	}
	// Actual 3840x2160 H.264 decode, not only a preset/schema assertion.
	timeline = MediaTimeline{OutputResolution: "2160p", Clips: []MediaClip{{ID: "fourk", AssetID: red, Duration: .5}}}
	id = mediaID()
	if err = m.render(ctx, p, MediaExport{ID: id, Timeline: timeline}); err != nil {
		t.Fatal(err)
	}
	path = filepath.Join(m.dir, id+".mp4")
	probe, e := probeMedia(path)
	if e != nil {
		t.Fatal(e)
	}
	found := false
	for _, stream := range probe.Streams {
		if stream.CodecType == "video" {
			found = stream.Width == 3840 && stream.Height == 2160
		}
	}
	if !found {
		t.Fatal("actual 4K dimensions missing", probe)
	}
	pixel := compositePixel(t, path, "0.25", 1920, 1080)
	if pixel[0] < 200 || pixel[2] > 40 {
		t.Fatal("4K frame decode", pixel)
	}

	for _, spec := range []struct {
		ratio string
		w, h  int
	}{{"9:16", 2160, 3840}, {"1:1", 2160, 2160}} {
		timeline.AspectRatio = spec.ratio
		id = mediaID()
		if err = m.render(ctx, p, MediaExport{ID: id, Timeline: timeline}); err != nil {
			t.Fatal(spec.ratio, err)
		}
		path = filepath.Join(m.dir, id+".mp4")
		probe, e = probeMedia(path)
		if e != nil {
			t.Fatal(e)
		}
		found = false
		for _, stream := range probe.Streams {
			if stream.CodecType == "video" {
				found = stream.Width == spec.w && stream.Height == spec.h
			}
		}
		if !found {
			t.Fatal("actual UHD shape missing", spec.ratio, probe)
		}
		pixel = compositePixel(t, path, "0.25", spec.w/2, spec.h/2)
		if pixel[0] < 200 || pixel[2] > 40 {
			t.Fatal("UHD shape decode", spec.ratio, pixel)
		}
	}
	// Different transitions compose in one timeline and retain shortened subtitle clock.
	timeline = MediaTimeline{AspectRatio: "1:1", Clips: []MediaClip{{ID: "a", AssetID: red, Duration: 1, Transition: "wipeleft", TransitionDuration: .5}, {ID: "b", AssetID: blue, Duration: 1, Transition: "circleopen", TransitionDuration: .5}, {ID: "c", AssetID: red, Duration: 1}}}
	id = mediaID()
	if err = m.render(ctx, p, MediaExport{ID: id, Timeline: timeline}); err != nil {
		t.Fatal(err)
	}
	path = filepath.Join(m.dir, id+".mp4")
	probe, e = probeMedia(path)
	duration, _ := strconv.ParseFloat(probe.Format.Duration, 64)
	if e != nil || math.Abs(duration-2) > .1 {
		t.Fatal("combined transitions", duration, e)
	}
	left, right := compositePixel(t, path, "0.75", 20, 180), compositePixel(t, path, "0.75", 340, 180)
	if !((left[0] > 180 && right[2] > 180) || (left[2] > 180 && right[0] > 180)) {
		t.Fatal("wipe is not spatial", left, right)
	}

	center, corner := compositePixel(t, path, "1.25", 180, 180), compositePixel(t, path, "1.25", 10, 10)
	if !((center[0] > 180 && corner[2] > 180) || (center[2] > 180 && corner[0] > 180)) {
		t.Fatal("circle transition is not radial", center, corner)
	}
	for _, name := range []string{"wiperight", "slideleft", "slideright"} {
		timeline.Clips = timeline.Clips[:2]
		timeline.Clips[0].Transition = name
		timeline.Clips[1].Transition = ""
		timeline.Clips[1].TransitionDuration = 0
		id = mediaID()
		if err = m.render(ctx, p, MediaExport{ID: id, Timeline: timeline}); err != nil {
			t.Fatal(name, err)
		}
		path = filepath.Join(m.dir, id+".mp4")
		left, right = compositePixel(t, path, "0.75", 20, 180), compositePixel(t, path, "0.75", 340, 180)
		if !((left[0] > 180 && right[2] > 180) || (left[2] > 180 && right[0] > 180)) {
			t.Fatal("spatial transition", name, left, right)
		}
	}
}

func TestKeyframesPersistReplayIsolation(t *testing.T) {
	dir := t.TempDir()
	m, err := openMediaStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	assetID := mediaID()
	p := MediaState{Version: 1, Assets: []MediaAsset{{ID: assetID, Kind: "image"}}, Exports: []MediaExport{}}
	m.data.Projects[mediaKey("tenant-a", "project")] = p
	timeline := MediaTimeline{Clips: []MediaClip{{ID: "one", AssetID: assetID, Duration: 1}}, VideoOverlays: []MediaVideoOverlay{{ID: "layer", AssetID: assetID, Duration: 1, Width: .25, Height: .25, Opacity: 1, Keyframes: []MediaKeyframe{{Time: 0, Opacity: 0}, {Time: 1, X: .5, Y: .5, Opacity: 1}}}}}
	saved, err := m.SaveTimeline("project", "tenant-a", "save-keyframes", 1, timeline)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := m.SaveTimeline("project", "tenant-a", "save-keyframes", 1, timeline)
	if err != nil || replay.Version != saved.Version {
		t.Fatal("replay", err)
	}
	if _, err = m.SaveTimeline("project", "tenant-b", "save-keyframes", 1, timeline); err == nil {
		t.Fatal("foreign assets accepted")
	}
	changed := timeline
	changed.VideoOverlays = append([]MediaVideoOverlay(nil), timeline.VideoOverlays...)
	changed.VideoOverlays[0].Opacity = .4
	if _, err = m.SaveTimeline("project", "tenant-a", "save-keyframes", 1, changed); err == nil {
		t.Fatal("idempotency mismatch accepted")
	}
	if _, err = m.SaveTimeline("project", "tenant-a", "stale", 1, timeline); err == nil {
		t.Fatal("stale version accepted")
	}
	m.close()
	m, err = openMediaStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer m.close()
	state := m.Get("project", "tenant-a")
	if len(state.Timeline.VideoOverlays) != 1 || len(state.Timeline.VideoOverlays[0].Keyframes) != 2 || state.Timeline.VideoOverlays[0].Keyframes[1].X != .5 {
		t.Fatal("keyframes lost after restart", state)
	}
	state.Timeline.VideoOverlays[0].Keyframes[1].X = .2
	if m.Get("project", "tenant-a").Timeline.VideoOverlays[0].Keyframes[1].X != .5 {
		t.Fatal("caller mutated persisted keys")
	}
}
