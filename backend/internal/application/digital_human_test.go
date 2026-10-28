package application

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Synthetic model frames exercise orchestration, never model quality.
type avatarFixtureProvider struct {
	video  []byte
	calls  int
	before func()
}

func (p *avatarFixtureProvider) Animate(_ context.Context, _ []byte, _ string, _ []byte, _ string) ([]byte, error) {
	p.calls++
	if p.before != nil {
		p.before()
	}
	return p.video, nil
}
func (p *avatarFixtureProvider) Sync(ctx context.Context, video, audio []byte, mime string) ([]byte, error) {
	return p.Animate(ctx, video, "video/mp4", audio, mime)
}

func avatarFixture(t *testing.T, s *FileStore, space string) (Project, DigitalHuman, CreativeJobInput, []byte) {
	t.Helper()
	p, _, err := s.CreateProject(space, "avatar-project", ProjectInput{Title: "Synthetic avatar", Brief: "Synthetic authorized talking video"})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	videoPath, audioPath := filepath.Join(dir, "video.mp4"), filepath.Join(dir, "audio.wav")
	if err = ffmpeg(context.Background(), "-f", "lavfi", "-i", "color=c=blue:s=320x320:r=24:d=1", "-an", "-c:v", "libx264", "-pix_fmt", "yuv420p", videoPath); err != nil {
		t.Fatal(err)
	}
	if err = ffmpeg(context.Background(), "-f", "lavfi", "-i", "sine=frequency=440:duration=1", "-c:a", "pcm_s16le", audioPath); err != nil {
		t.Fatal(err)
	}
	video, err := os.ReadFile(videoPath)
	if err != nil {
		t.Fatal(err)
	}
	audio, err := os.ReadFile(audioPath)
	if err != nil {
		t.Fatal(err)
	}
	media, err := s.media.AddAsset(p.ID, space, "avatar-photo", "Synthetic photo", "reference", "", "", "synthetic", pngFixture(t, 320, 320), "image/png")
	if err != nil {
		t.Fatal(err)
	}
	avatar, _, err := s.createDigitalHuman(space, p.ID, "avatar-profile", "test-actor", DigitalHumanInput{Name: "Synthetic person", ReferenceAssetID: media.Assets[0].ID, ConsentReference: "synthetic-consent-001", LikenessConfirmed: true, SinglePersonConfirmed: true})
	if err != nil {
		t.Fatal(err)
	}
	media, err = s.media.AddAsset(p.ID, space, "avatar-audio", "Synthetic voice", "voice", "", "", "synthetic", audio, "audio/wav")
	if err != nil {
		t.Fatal(err)
	}
	caps := Capabilities{Provider: "test-avatar", Model: "synthetic-v1", Mode: "real", Ready: true}
	s.creative.avatarPhotoCaps = caps
	s.creative.avatarLicenseConfirmed = true
	in := CreativeJobInput{Kind: "digital_human", ExpectedVersion: p.Version, ExpectedProvider: caps.Provider, ExpectedModel: caps.Model, Prompt: "Synthetic speech", AvatarID: avatar.ID, AudioAssetID: media.Assets[1].ID, VoiceConfirmed: true, VoiceConsentReference: "synthetic-voice-001"}
	return p, avatar, in, video
}

func TestDigitalHumanPipelineAndPersistence(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	p, a, in, video := avatarFixture(t, s, "personal-demo")
	provider := &avatarFixtureProvider{video: video}
	s.creative.avatarPhoto = provider
	job, created, err := s.createCreativeJob(p.SpaceID, p.ID, "avatar-generate", in)
	if err != nil || !created {
		t.Fatal(job, err)
	}
	if err = s.executeCreativeJob(context.Background(), &job); err != nil {
		t.Fatal(err)
	}
	asset, err := s.media.AssetForReceipt(p.ID, p.SpaceID, "creative-"+job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if asset.Kind != "video" || !strings.Contains(asset.Name, "AI生成") || asset.Duration < .9 || job.AssetID != asset.ID || !job.AIGenerated || provider.calls != 1 {
		t.Fatal(asset, job, provider.calls)
	}
	path, _, err := s.media.file(p.ID, p.SpaceID, asset.ID, false, false)
	if err != nil {
		t.Fatal(err)
	}
	probe, err := probeMedia(path)
	if err != nil || len(probe.Streams) != 2 {
		t.Fatal("missing authorized soundtrack", probe, err)
	}
	job.Status = "succeeded"
	if err = s.creative.updateJob(job); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	people, err := s.listDigitalHumans(p.SpaceID, p.ID)
	if err != nil || len(people) != 1 || people[0].CreatedBy != "test-actor" {
		t.Fatal(people, err)
	}
	replay, created, err := s.createCreativeJob(p.SpaceID, p.ID, "avatar-generate", in)
	if err != nil || created || replay.AssetID != asset.ID || replay.VoiceConsentReference != in.VoiceConsentReference {
		t.Fatal(replay, err)
	}
	if _, err = s.revokeDigitalHuman(p.SpaceID, p.ID, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.createCreativeJob(p.SpaceID, p.ID, "after-revoke", in); err == nil {
		t.Fatal("revoked likeness accepted")
	}
}

func TestDigitalHumanValidationAndRevocation(t *testing.T) {
	s := testStore(t)
	p, a, in, video := avatarFixture(t, s, "personal-demo")
	for _, change := range []func(*CreativeJobInput){func(i *CreativeJobInput) { i.VoiceConfirmed = false }, func(i *CreativeJobInput) { i.VoiceConsentReference = "" }, func(i *CreativeJobInput) { i.AudioAssetID = "foreign" }, func(i *CreativeJobInput) { i.AvatarID = "foreign" }, func(i *CreativeJobInput) { i.ReferenceAssetID = "foreign" }, func(i *CreativeJobInput) { i.ExpectedModel = "changed" }, func(i *CreativeJobInput) { i.ExpectedVersion++ }} {
		bad := in
		change(&bad)
		if _, _, err := s.createCreativeJob(p.SpaceID, p.ID, "invalid-avatar", bad); err == nil {
			t.Fatal("invalid input accepted", bad)
		}
	}
	s.creative.avatarLicenseConfirmed = false
	if _, _, err := s.createCreativeJob(p.SpaceID, p.ID, "no-license", in); !errors.Is(err, ErrProviderNotReady) {
		t.Fatal(err)
	}
	s.creative.avatarLicenseConfirmed = true
	if _, err := s.listDigitalHumans("team-demo", p.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if _, err := s.revokeDigitalHuman("team-demo", p.ID, a.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	provider := &avatarFixtureProvider{video: video, before: func() {
		if _, err := s.revokeDigitalHuman(p.SpaceID, p.ID, a.ID); err != nil {
			t.Fatal(err)
		}
	}}
	s.creative.avatarPhoto = provider
	job, _, err := s.createCreativeJob(p.SpaceID, p.ID, "revoke-during", in)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.executeCreativeJob(context.Background(), &job); err == nil {
		t.Fatal("published after revocation")
	}
	if job.AssetID != "" || len(s.media.Get(p.ID, p.SpaceID).Assets) != 2 {
		t.Fatal("revoked output ingested")
	}
	if err = s.executeCreativeJob(context.Background(), &job); err == nil || provider.calls != 1 {
		t.Fatal("revoked job called model", err, provider.calls)
	}
}

func TestDigitalHumanHTTPAuthorizationAndIdempotency(t *testing.T) {
	s := testStore(t)
	auth, err := OpenAuthStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	u, token, session, err := auth.login("avatar@example.test", "synthetic-password", true)
	if err != nil {
		t.Fatal(err)
	}
	space := auth.spaces(u.ID)[0]["id"]
	p, a, _, _ := avatarFixture(t, s, space)
	h := SecureHandler(s, "http://localhost:5173", auth, AuthOptions{})
	path := "/api/v1/projects/" + p.ID + "/digital-humans?space_id=" + space
	input := DigitalHumanInput{Name: "HTTP synthetic", ReferenceAssetID: a.ReferenceAssetID, ConsentReference: "http-consent-001", LikenessConfirmed: true, SinglePersonConfirmed: true}
	body, _ := json.Marshal(input)
	authRequest(t, h, "POST", path, string(body), token, "", 403)
	w := authRequest(t, h, "POST", path, string(body), token, session.CSRF, 201)
	var created DigitalHuman
	if err = json.Unmarshal(w.Body.Bytes(), &created); err != nil || created.CreatedBy != u.ID {
		t.Fatal(created, err)
	}
	authRequest(t, h, "POST", path, string(body), token, session.CSRF, 200)
	input.Name = "Changed"
	body, _ = json.Marshal(input)
	authRequest(t, h, "POST", path, string(body), token, session.CSRF, 409)
	authRequest(t, h, "GET", path, "", token, "", 200)
	_, viewerToken, viewerSession, err := auth.login("viewer-avatar@example.test", "synthetic-password", true)
	if err != nil {
		t.Fatal(err)
	}
	authRequest(t, h, "PUT", "/api/v1/organizations/"+space+"/members", `{"email":"viewer-avatar@example.test","role":"viewer"}`, token, session.CSRF, 200)
	authRequest(t, h, "POST", path, string(body), viewerToken, viewerSession.CSRF, 403)
	revoke := "/api/v1/projects/" + p.ID + "/digital-humans/" + created.ID + "/revoke?space_id=" + space
	authRequest(t, h, "POST", revoke, `{}`, viewerToken, viewerSession.CSRF, 403)
	authRequest(t, h, "POST", revoke, `{}`, token, session.CSRF, 200)
	authRequest(t, h, "POST", revoke, `{}`, token, session.CSRF, 200)
	// Wrong project references must not reveal a likeness even within one tenant.
	other, _, err := s.CreateProject(space, "other-project", ProjectInput{Title: "Other", Brief: "Synthetic other project"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.revokeDigitalHuman(space, other.ID, a.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodGet, "/api/v1/projects/"+other.ID+"/digital-humans?space_id="+space, nil)
	r.AddCookie(&http.Cookie{Name: "studio_session", Value: token})
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 || strings.TrimSpace(w.Body.String()) != "[]" {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestDigitalHumanVideoRouteAndInvalidOutput(t *testing.T) {
	s := testStore(t)
	p, _, in, video := avatarFixture(t, s, "personal-demo")
	media, err := s.media.AddAsset(p.ID, p.SpaceID, "reference-video", "Synthetic video", "reference", "", "", "synthetic", video, "video/mp4")
	if err != nil {
		t.Fatal(err)
	}
	a, _, err := s.createDigitalHuman(p.SpaceID, p.ID, "video-avatar", "test-actor", DigitalHumanInput{Name: "Video person", ReferenceAssetID: media.Assets[2].ID, ConsentReference: "video-consent-001", LikenessConfirmed: true, SinglePersonConfirmed: true})
	if err != nil || a.Route != "video" {
		t.Fatal(a, err)
	}
	provider := &avatarFixtureProvider{video: video}
	s.creative.lipSync = provider
	s.creative.lipSyncCaps = s.creative.avatarPhotoCaps
	in.AvatarID = a.ID
	job, _, err := s.createCreativeJob(p.SpaceID, p.ID, "video-generate", in)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.executeCreativeJob(context.Background(), &job); err != nil || provider.calls != 1 {
		t.Fatal(err, provider.calls)
	}
	if _, err = composeAvatarVideo(context.Background(), &avatarWork{video: []byte("invalid model output"), duration: 1}); err == nil {
		t.Fatal("invalid model output accepted")
	}
}

func TestDigitalHumanPhotoRunnerContract(t *testing.T) {
	s := testStore(t)
	_, _, _, video := avatarFixture(t, s, "personal-demo")
	output, err := json.Marshal(map[string][]byte{"video": video})
	if err != nil {
		t.Fatal(err)
	}
	command := filepath.Join(t.TempDir(), "photo-runner")
	script := "#!/bin/sh\nread -r payload\ncase \"$payload\" in *'\"operation\":\"photo_avatar\"'*) ;; *) exit 2;; esac\ncase \"$payload\" in *'\"reference_mime_type\":\"image/png\"'*) ;; *) exit 3;; esac\nprintf '%s' '" + string(output) + "'\n"
	if err = os.WriteFile(command, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DIGITAL_HUMAN_PHOTO_COMMAND", command)
	t.Setenv("DIGITAL_HUMAN_PHOTO_MODEL", "synthetic-photo-v1")
	t.Setenv("DIGITAL_HUMAN_LICENSE_ACK", "false")
	ConfigureDigitalHumanFromEnv(s)
	if s.creative.avatarCaps("photo").Ready {
		t.Fatal("license gate bypassed")
	}
	t.Setenv("DIGITAL_HUMAN_LICENSE_ACK", "true")
	ConfigureDigitalHumanFromEnv(s)
	if !s.creative.avatarCaps("photo").Ready {
		t.Fatal("configured runner unavailable")
	}
	result, err := s.creative.avatarPhoto.Animate(context.Background(), pngFixture(t, 320, 320), "image/png", []byte("synthetic adapter input"), "audio/wav")
	if err != nil || string(result) != string(video) {
		t.Fatal("runner contract mismatch", err)
	}
	if _, err = s.creative.avatarPhoto.Animate(context.Background(), []byte("x"), "text/plain", []byte("x"), "audio/wav"); err == nil {
		t.Fatal("wrong reference MIME accepted")
	}
}
