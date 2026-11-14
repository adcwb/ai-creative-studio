package application

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAvatarPauseCandidatesAndManualCuts(t *testing.T) {
	const total = 23 * 48000
	raw := make([]byte, total*2)
	for i := 0; i < total; i++ {
		binary.LittleEndian.PutUint16(raw[i*2:], uint16(2000))
	}
	path := filepath.Join(t.TempDir(), "speech.s16")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	ends, reasons, err := avatarCutBounds(f, total, avatarSplitSpec{Mode: "pause"})
	if err != nil || ends[0] != 18*48000 || reasons[0] != "window" {
		t.Fatal("continuous speech fallback", ends, reasons, err)
	}
	for i := 17 * 48000; i < 18*48000; i++ {
		raw[i*2] = 0
		raw[i*2+1] = 0
	}
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	ends, reasons, err = avatarCutBounds(f, total, avatarSplitSpec{Mode: "pause"})
	if err != nil || ends[0] != int64(17.5*48000) || reasons[0] != "pause" {
		t.Fatal("pause candidate", ends, reasons, err)
	}
	ends, reasons, err = avatarCutBounds(f, total, avatarSplitSpec{Mode: "manual", Cuts: []float64{12.125}})
	if err != nil || len(ends) != 2 || ends[0] != 582000 || ends[1] != total || reasons[0] != "manual" {
		t.Fatal("manual cuts", ends, reasons, err)
	}
	for _, cuts := range [][]float64{{23}, {20}, {.001}, {10, 9}, {math.NaN()}, {math.Inf(1)}} {
		if _, _, err := avatarCutBounds(f, total, avatarSplitSpec{Mode: "manual", Cuts: cuts}); err == nil {
			t.Fatal("unsafe/unrenderable cut accepted", cuts)
		}
	}
	if _, _, err := avatarCutBounds(f, total, avatarSplitSpec{Mode: "pause", Cuts: []float64{12}}); err == nil {
		t.Fatal("ignored cut list")
	}
}

func TestAvatarApprovedPreviewPinsDecodedPlanBeforeDispatch(t *testing.T) {
	s := testStore(t)
	p, in, _ := cloudAvatarFixture(t, s, "preview-pin")
	s.creative.avatarPhotoCaps = Capabilities{Provider: wanAvatarProvider, Model: "wan2.2-s2v", Mode: "real", Ready: true}
	s.creative.avatarPhoto = &avatarCloud{inputs: &S3ReferenceBridge{}, tenants: map[string]bool{p.SpaceID: true}, fingerprint: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}
	path, _, err := s.media.file(p.ID, p.SpaceID, in.AudioAssetID, false, false)
	if err != nil {
		t.Fatal(err)
	}
	work := &avatarWork{audioPath: path, videoPath: filepath.Join(t.TempDir(), "out.mp4")}
	spec := avatarSplitSpec{Mode: "pause"}
	plan, _, total, err := prepareAvatarSegments(context.Background(), work, spec)
	if err != nil {
		t.Fatal(err)
	}
	hash := avatarPlanDigest(in.AudioAssetID, spec, total, plan)
	spec.Approved = hash
	if avatarPlanDigest(in.AudioAssetID, spec, total, plan) != hash {
		t.Fatal("approval changed digest")
	}
	in.AvatarSplitMode = "pause"
	in.AvatarPlanSHA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	job, _, err := s.createCreativeJob(p.SpaceID, p.ID, "changed-preview", in)
	if err != nil {
		t.Fatal(err)
	}
	manual := job
	manual.AvatarSplitMode = "manual"
	manual.AvatarCutPoints = []float64{.25, .5, .75}
	if s.creativeJobTimeout(manual) != 40*time.Minute {
		t.Fatal("manual windows inherited a single-request deadline")
	}
	setAvatarTestRunning(t, s, &job)
	// No transport is configured: a mismatch must fail before upload/submission.
	err = s.executeCreativeJob(context.Background(), &job)
	if err == nil || func() bool { code, _ := creativeFailure(err); return code != "avatar_plan_changed" }() || job.AvatarCheckpoint != nil {
		t.Fatal("changed plan reached dispatch", err, job.AvatarCheckpoint)
	}
}

func TestAvatarCutAndReplacementHTTPRequireWriterScopeAndCSRF(t *testing.T) {
	for _, mode := range []string{"file", "postgres"} {
		t.Run(mode, func(t *testing.T) {
			s := testStore(t)
			if mode == "postgres" {
				s = postgresTestStore(t, postgresTestDB(t))
			}
			_, _, auth, actor := workflowFixture(t, s)
			_, token, session, err := auth.login("workflow-editor@example.test", "synthetic-password", false)
			if err != nil {
				t.Fatal(err)
			}
			space := ""
			auth.mu.Lock()
			for id, o := range auth.data.Organizations {
				if o.Members[actor] == "editor" {
					space = id
				}
			}
			auth.mu.Unlock()
			p, _, in, _ := avatarFixture(t, s, space)
			handler := SecureHandler(s, "http://localhost:5173", auth, AuthOptions{})
			viewer, vt, vs, err := auth.login("cut-viewer@example.test", "synthetic-viewer-password", true)
			if err != nil {
				t.Fatal(err)
			}
			auth.mu.Lock()
			snapshot := auth.clone()
			org := snapshot.Organizations[space]
			org.Members[viewer.ID] = "viewer"
			org.Version++
			snapshot.Organizations[space] = org
			err = auth.commit(snapshot)
			auth.mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			_, ft, fs, err := auth.login("cut-foreign@example.test", "synthetic-foreign-password", true)
			if err != nil {
				t.Fatal(err)
			}
			previewPath := "/api/v1/projects/" + p.ID + "/avatar-cut-preview?space_id=" + space
			replacePath := "/api/v1/projects/" + p.ID + "/creative-jobs/nonexistent/segment-replacements?space_id=" + space
			for _, path := range []string{previewPath, replacePath} {
				authRequest(t, handler, "POST", path, `{}`, token, "", 403)
				authRequest(t, handler, "POST", path, `{}`, vt, vs.CSRF, 403)
				authRequest(t, handler, "POST", path, `{}`, ft, fs.CSRF, 403)
			}
			body := projectJSON(t, map[string]any{"audio_asset_id": in.AudioAssetID, "avatar_split_mode": "pause"})
			response := authRequest(t, handler, "POST", previewPath, body, token, session.CSRF, 200)
			var preview avatarCutPreview
			if err = json.Unmarshal(response.Body.Bytes(), &preview); err != nil || len(preview.ID) != 64 || len(preview.Segments) != 1 {
				t.Fatal("invalid preview", response.Body.String(), err)
			}
			authRequest(t, handler, "POST", previewPath, `{"audio_asset_id":"foreign-audio","avatar_split_mode":"pause"}`, token, session.CSRF, 400)
			body = projectJSON(t, avatarReplacementInput{SourceAsset: "unknown", ExpectedVersion: p.Version, Segments: []int{1}, Confirmed: true})
			authRequest(t, handler, "POST", replacePath, body, token, session.CSRF, 404)
		})
	}
}
