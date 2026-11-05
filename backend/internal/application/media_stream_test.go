package application

import (
	"context"
	"encoding/json"
	"math"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMediaStreamPreservesEarlierUploadDigestsAndPermissionRevocation(t *testing.T) {
	m, err := openMediaStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer m.close()
	data := syntheticPNG(0)
	path := filepath.Join(m.dir, "fixture")
	if err = os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	digest, err := mediaFileDigest(context.Background(), path)
	if err != nil || digest != mediaDigest(data) {
		t.Fatal("upgrade changed upload idempotency", digest, err)
	}
	if _, err = m.addAssetFile(context.Background(), "p", "space", "revoked-upload", "file", "reference", "", "", "uploaded", path, "image/png", func() bool { return false }); err != errRuntimeDenied {
		t.Fatal("revoked permission allowed publication", err)
	}
	if len(m.Get("p", "space").Assets) != 0 {
		t.Fatal("denied upload created an asset")
	}
	if _, err = m.AddAsset("p", "space", "old-upload", "file", "reference", "", "", "uploaded", data, "image/png"); err != nil {
		t.Fatal(err)
	}
	// A client retry crosses the streamed path with the key accepted by the old
	// byte-based API. It must return the same asset, never a conflict or duplicate.
	p, err := m.addAssetFile(context.Background(), "p", "space", "old-upload", "file", "reference", "", "", "uploaded", path, "image/png", nil)
	if err != nil || len(p.Assets) != 1 {
		t.Fatal("streamed replay", len(p.Assets), err)
	}
}

func TestOutputProfilesHaveNoDurationOrCollectionQuota(t *testing.T) {
	p := MediaState{Assets: []MediaAsset{{ID: "image", Kind: "image"}, {ID: "voice", Kind: "audio", Duration: 200}}}
	for _, resolution := range []string{"1080p", "1440p", "1800p", "2160p"} {
		for _, ratio := range []string{"16:9", "9:16", "1:1"} {
			timeline := MediaTimeline{OutputResolution: resolution, AspectRatio: ratio, Clips: []MediaClip{{ID: "shot", AssetID: "image", Duration: 120}}}
			if err := validateTimeline(p, timeline); err != nil {
				t.Fatal(resolution, ratio, err)
			}
			w, h, err := outputSpec(timeline)
			if err != nil || w*h > 3840*2160 {
				t.Fatal("output dimensions", w, h, err)
			}
		}
	}
	timeline := MediaTimeline{Clips: []MediaClip{}}
	for i := range 40 {
		timeline.Clips = append(timeline.Clips, MediaClip{ID: strings.Repeat("a", i+1), AssetID: "image", Duration: 2})
	}
	for i := range 9 {
		timeline.AudioTracks = append(timeline.AudioTracks, MediaAudioTrack{ID: strings.Repeat("a", i+1), AssetID: "voice", Duration: 2, Volume: .01})
	}
	if err := validateTimeline(p, timeline); err != nil {
		t.Fatal("old clip or track count quota", err)
	}
	timeline.Clips[0].Duration = math.NaN()
	if validateTimeline(p, timeline) == nil {
		t.Fatal("nonfinite timeline accepted")
	}
}

func TestRenderBeyondFormerAudioAndOverlayCounts(t *testing.T) {
	m, err := openMediaStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer m.close()
	p, err := m.Demo("project", "space", "many-layers", 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	timeline := p.Timeline
	for i := range timeline.Clips {
		timeline.Clips[i].Duration = 1
	}
	for i := range 9 {
		timeline.AudioTracks = append(timeline.AudioTracks, MediaAudioTrack{ID: strings.Repeat("a", i+1), AssetID: timeline.MusicAssetID, Duration: 2, Volume: .02})
	}
	for i := range 5 {
		timeline.VideoOverlays = append(timeline.VideoOverlays, MediaVideoOverlay{ID: strings.Repeat("v", i+1), AssetID: timeline.Clips[0].AssetID, Duration: 2, Width: .2, Height: .2, Opacity: .05})
	}
	p, err = m.SaveTimeline("project", "space", "many-timeline", p.Version, timeline)
	if err != nil {
		t.Fatal(err)
	}
	e, err := m.StartExport("project", "space", "many-export", p.Version)
	if err != nil {
		t.Fatal(err)
	}
	m.jobs.wait()
	p = m.Get("project", "space")
	if p.Exports[0].Status != "succeeded" {
		t.Fatal(p.Exports[0].Error)
	}
	if err = ffmpeg(context.Background(), "-i", filepath.Join(m.dir, e.ID+".mp4"), "-f", "null", "-"); err != nil {
		t.Fatal("multi-track output does not decode", err)
	}
}

// Opt in to complete-duration CPU encoding; short schema/fixture tests cannot
// establish that a two-minute 4K movie actually finishes and decodes.
func TestFullDurationHDOutputs(t *testing.T) {
	if os.Getenv("STUDIO_MEDIA_LONG_TEST") != "1" {
		t.Skip("explicit full-duration media test required")
	}
	for _, tc := range []struct {
		resolution, ratio string
		duration          float64
	}{{"1080p", "9:16", 60}, {"1440p", "16:9", 90}, {"1800p", "16:9", 90}, {"2160p", "16:9", 120}, {"2160p", "9:16", 120}} {
		t.Run(tc.resolution+"/"+tc.ratio, func(t *testing.T) {
			m, err := openMediaStore(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer m.close()
			p, err := m.Demo("project", "space", "full-duration", 1, nil)
			if err != nil {
				t.Fatal(err)
			}
			timeline := p.Timeline
			timeline.OutputResolution = tc.resolution
			timeline.AspectRatio = tc.ratio
			for i := range timeline.Clips {
				timeline.Clips[i].Duration = tc.duration / float64(len(timeline.Clips))
			}
			p, err = m.SaveTimeline("project", "space", "full-timeline", p.Version, timeline)
			if err != nil {
				t.Fatal(err)
			}
			e, err := m.StartExport("project", "space", "full-export", p.Version)
			if err != nil {
				t.Fatal(err)
			}
			m.jobs.wait()
			p = m.Get("project", "space")
			if p.Exports[0].Status != "succeeded" {
				t.Fatal("complete render failed", p.Exports[0].Error)
			}
			path := filepath.Join(m.dir, e.ID+".mp4")
			probe, err := probeMedia(path)
			if err != nil {
				t.Fatal(err)
			}
			w, h, err := outputSpec(timeline)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, stream := range probe.Streams {
				if stream.CodecType == "video" && stream.Width == w && stream.Height == h {
					found = true
				}
			}
			if !found {
				t.Fatal("wrong output pixels", probe)
			}
			if err = ffmpeg(context.Background(), "-i", path, "-f", "null", "-"); err != nil {
				t.Fatal("complete decode failed", err)
			}
			t.Logf("complete encoded/decoded %dx%d, target %.0fs, probed %s seconds", w, h, tc.duration, probe.Format.Duration)
		})
	}
}

func TestLargeVideoHTTPUploadUsesStreamingAndStableReceipt(t *testing.T) {
	if os.Getenv("STUDIO_MEDIA_LONG_TEST") != "1" {
		t.Skip("explicit large media encoding test required")
	}
	path := filepath.Join(t.TempDir(), "large.mp4")
	if err := ffmpeg(context.Background(), "-f", "lavfi", "-i", "testsrc2=size=1920x1080:rate=24", "-vf", "noise=alls=100:allf=t+u:all_seed=17", "-t", "5", "-c:v", "libx264", "-preset", "ultrafast", "-crf", "0", "-pix_fmt", "yuv420p", path); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(path)
	if err != nil || st.Size() <= MaxBlobBytes {
		t.Fatal("fixture did not cross the former object limit", st, err)
	}
	s := testStore(t)
	project, _, err := s.CreateProject("personal-demo", "large-project", ProjectInput{Title: "大文件验收", Brief: "仅本地合成素材，不请求模型"})
	if err != nil {
		t.Fatal(err)
	}
	h := Handler(s, "http://localhost:5173")
	first := ""
	for range 2 {
		f, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest("POST", "/api/v1/projects/"+project.ID+"/media/uploads?space_id=personal-demo&filename=large.mp4", f)
		r.Header.Set("Content-Type", "video/mp4")
		r.Header.Set("Idempotency-Key", "large-upload-key")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		f.Close()
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		var result struct {
			UploadedAssetID string `json:"uploaded_asset_id"`
		}
		if err = json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if first == "" {
			first = result.UploadedAssetID
		} else if first != result.UploadedAssetID {
			t.Fatal("stream retry duplicated asset")
		}
	}
	if len(s.media.Get(project.ID, "personal-demo").Assets) != 1 {
		t.Fatal("large upload replay duplicated media")
	}
	t.Logf("uploaded and replayed valid H.264 MP4: %d bytes", st.Size())
}
