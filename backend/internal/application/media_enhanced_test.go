package application

import (
	"context"
	"encoding/binary"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestEnhancedTimelineValidation(t *testing.T) {
	p := MediaState{Assets: []MediaAsset{{ID: "image", Kind: "image"}, {ID: "audio", Kind: "audio", Duration: 10}}}
	baseline := MediaTimeline{Clips: []MediaClip{{ID: "shot-1", AssetID: "image", Duration: 2, AudioVolume: 1}}}
	cases := []struct {
		name   string
		change func(*MediaTimeline)
	}{
		{"resolution", func(v *MediaTimeline) { v.OutputResolution = "4k" }},
		{"aspect", func(v *MediaTimeline) { v.AspectRatio = "3:2" }},
		{"subtitle", func(v *MediaTimeline) { v.SubtitleMode = "unknown" }},
		{"source-volume", func(v *MediaTimeline) { v.Clips[0].SourceAudioVolume = 2 }},
		{"transition", func(v *MediaTimeline) { v.Clips[0].Transition = "arbitrary-filter" }},
		{"transition-time", func(v *MediaTimeline) { v.Clips[0].Transition = "fade"; v.Clips[0].TransitionDuration = 2 }},
		{"not-finite", func(v *MediaTimeline) { v.Clips[0].Duration = math.NaN() }},
		{"cross-project-track", func(v *MediaTimeline) {
			v.AudioTracks = []MediaAudioTrack{{ID: "track-1", AssetID: "foreign", Duration: 1, Volume: 1}}
		}},
		{"track-end", func(v *MediaTimeline) {
			v.AudioTracks = []MediaAudioTrack{{ID: "track-1", AssetID: "audio", Start: 1.5, Duration: 1, Volume: 1}}
		}},
		{"track-trim", func(v *MediaTimeline) {
			v.AudioTracks = []MediaAudioTrack{{ID: "track-1", AssetID: "audio", TrimStart: 10, Duration: 1, Volume: 1}}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := baseline
			v.Clips = append([]MediaClip(nil), baseline.Clips...)
			tc.change(&v)
			if validateTimeline(p, v) == nil {
				t.Fatal("invalid value accepted")
			}
		})
	}
	baseline.OutputResolution = "1080p"
	baseline.Clips = []MediaClip{{ID: "shot-1", AssetID: "image", Duration: 30}, {ID: "shot-2", AssetID: "image", Duration: 30}, {ID: "shot-3", AssetID: "image", Duration: .5}}
	if validateTimeline(p, baseline) == nil {
		t.Fatal("1080p work limit bypassed")
	}
}

func TestEnhancedMediaActualEncoding(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg unavailable")
	}
	m, err := openMediaStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer m.close()
	videoID, audioID := mediaID(), mediaID()
	ctx := context.Background()
	if err = ffmpeg(ctx, "-f", "lavfi", "-i", "color=c=blue:s=128x72:r=24:d=1", "-f", "lavfi", "-i", "sine=frequency=440:duration=1", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", "-t", "1", "-f", "mp4", filepath.Join(m.dir, videoID)); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(m.dir, audioID), syntheticWAV(1), 0600); err != nil {
		t.Fatal(err)
	}
	p := MediaState{Assets: []MediaAsset{{ID: videoID, Kind: "video", Duration: 1}, {ID: audioID, Kind: "audio", Duration: 1}}}
	timeline := MediaTimeline{Clips: []MediaClip{{ID: "shot-1", AssetID: videoID, Duration: 1, PreserveSourceAudio: true, SourceAudioVolume: 1, Subtitle: "BURNED TEXT", Transition: "fade", TransitionDuration: .1}}, OutputResolution: "720p", AspectRatio: "9:16", SubtitleMode: "burn", AudioTracks: []MediaAudioTrack{{ID: "sound-1", AssetID: audioID, Start: .2, TrimStart: .1, Duration: .5, Volume: .2}}}
	if err = validateTimeline(p, timeline); err != nil {
		t.Fatal(err)
	}
	id := mediaID()
	if err = m.render(ctx, p, MediaExport{ID: id, Timeline: timeline}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(m.dir, id+".mp4")
	probe, err := probeMedia(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(probe.Streams) != 2 || probe.Streams[0].Width != 720 || probe.Streams[0].Height != 1280 {
		t.Fatalf("bad portrait burned output: %+v", probe)
	}
	// Decode actual encoded output: original video audio must remain audible.
	pcm, err := exec.Command("ffmpeg", "-v", "error", "-i", path, "-map", "0:a:0", "-t", "0.15", "-f", "s16le", "-ac", "1", "-").Output()
	if err != nil {
		t.Fatal(err)
	}
	energy := 0.0
	for i := 0; i+1 < len(pcm); i += 2 {
		v := float64(int16(binary.LittleEndian.Uint16(pcm[i : i+2])))
		energy += v * v
	}
	if len(pcm) == 0 || math.Sqrt(energy/float64(len(pcm)/2)) < 100 {
		t.Fatal("source audio missing")
	}
	// Inspect actual decoded video pixels: white subtitle glyphs on blue must be burned in.
	frame, err := exec.Command("ffmpeg", "-v", "error", "-ss", "0.5", "-i", path, "-frames:v", "1", "-f", "rawvideo", "-pix_fmt", "rgb24", "-").Output()
	if err != nil {
		t.Fatal(err)
	}
	white := 0
	for i := 0; i+2 < len(frame); i += 3 {
		if frame[i] > 160 && frame[i+1] > 160 && frame[i+2] > 160 {
			white++
		}
	}
	if white < 20 {
		t.Fatal("no burned subtitle pixels", white)
	}
	first, err := exec.Command("ffmpeg", "-v", "error", "-i", path, "-frames:v", "1", "-f", "rawvideo", "-pix_fmt", "rgb24", "-").Output()
	if err != nil {
		t.Fatal(err)
	}
	sum := 0.0
	for _, v := range first {
		sum += float64(v)
	}
	if len(first) == 0 || sum/float64(len(first)) > 5 {
		t.Fatal("fade did not start at black")
	}
	// All remaining presets really encode at the requested dimensions.
	for _, preset := range []struct {
		resolution, ratio string
		w, h              int
	}{{"1080p", "16:9", 1920, 1080}, {"360p", "1:1", 360, 360}} {
		timeline.OutputResolution, timeline.AspectRatio, timeline.SubtitleMode = preset.resolution, preset.ratio, "soft"
		id = mediaID()
		if err = m.render(ctx, p, MediaExport{ID: id, Timeline: timeline}); err != nil {
			t.Fatal(err)
		}
		probe, err = probeMedia(filepath.Join(m.dir, id+".mp4"))
		if err != nil {
			t.Fatal(err)
		}
		if len(probe.Streams) != 3 || probe.Streams[0].Width != preset.w || probe.Streams[0].Height != preset.h {
			t.Fatalf("wrong output %+v", probe)
		}
	}
}
