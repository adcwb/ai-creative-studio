package studio

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func editMaskFixture(w, h int) []byte {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w/2; x++ {
			img.Set(x, y, color.NRGBA{A: 255})
		}
	}
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	return b.Bytes()
}
func TestMaskEditMultipartAndValidation(t *testing.T) {
	reference := syntheticCreativePNG()
	mask := editMaskFixture(640, 360)
	if err := validateEditMask(reference, "image/png", mask); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range [][]byte{editMaskFixture(12, 12), reference, []byte("not png"), make([]byte, 4<<20+1)} {
		if validateEditMask(reference, "image/png", invalid) == nil {
			t.Fatal("invalid mask accepted")
		}
	}
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/images/edits" {
			t.Error(r.URL.Path)
		}
		if err := r.ParseMultipartForm(20 << 20); err != nil {
			t.Error(err)
			return
		}
		defer r.MultipartForm.RemoveAll()
		f, h, e := r.FormFile("mask")
		if e != nil {
			t.Error(e)
			return
		}
		defer f.Close()
		b, _ := io.ReadAll(f)
		if !bytes.Equal(b, mask) || h.Header.Get("Content-Type") != "image/png" {
			t.Error("mask mismatch")
		}
		w.WriteHeader(429)
	}))
	defer server.Close()
	p := newOpenAIProvider("synthetic", server.URL+"/images/generations", server.Client())
	_, err := p.GenerateWithMask(context.Background(), Input{Kind: "general", Prompt: "replace selected region", Ratio: "16:9", Style: "natural"}, reference, "image/png", mask)
	if err == nil || calls != 1 {
		t.Fatal("upstream error retried or accepted", calls, err)
	}
}
func TestGoogleFirstFrameAndVideoEditNativeContract(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var body struct {
			Input []struct {
				Type    string              `json:"type"`
				Content []map[string]string `json:"content"`
			} `json:"input"`
			Response map[string]string `json:"response_format"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			t.Error("json")
		}
		if len(body.Input) != 1 || body.Input[0].Type != "user_input" || len(body.Input[0].Content) != 2 {
			t.Error("native input")
			return
		}
		block := body.Input[0].Content[0]
		if block["uri"] != "" || block["data"] == "" {
			t.Error("input must be inline")
		}
		if calls == 1 && block["type"] != "image" || calls == 2 && block["type"] != "video" {
			t.Error(block)
		}
		if body.Response["delivery"] != "uri" {
			t.Error("not recoverable")
		}
		_, _ = io.WriteString(w, `{"status":"completed","steps":[{"type":"model_output","content":[{"type":"video","mime_type":"video/mp4","uri":"files/synthetic-file"}]}]}`)
	}))
	defer server.Close()
	p := newGoogleMediaProvider("gemini-video", "synthetic", "synthetic-model", "", server.URL, server.Client(), nil)
	j, e := p.SubmitVideoWithImage(context.Background(), "animate the scene", syntheticCreativePNG(), "image/png")
	if e != nil || j.ID != "synthetic-file" {
		t.Fatal(j, e)
	}
	j, e = p.SubmitVideoWithVideo(context.Background(), "change the light", []byte("0000ftypisom"), "video/mp4")
	if e != nil || j.ID != "synthetic-file" {
		t.Fatal(j, e)
	}
	if _, e = p.SubmitVideoWithVideo(context.Background(), "invalid video", []byte("not video"), "video/mp4"); e == nil || calls != 2 {
		t.Fatal("invalid video sent")
	}
}

type fixtureBridge struct{ calls int }

func (b *fixtureBridge) ReferenceURL(context.Context, []byte, string) (string, error) {
	b.calls++
	return "https://objects.example.test/bucket/random-object?X-Amz-Expires=300", nil
}
func TestJimengReferenceBridgeNativeContract(t *testing.T) {
	bridge := &fixtureBridge{}
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		urls, ok := body["image_urls"].([]any)
		if !ok || len(urls) != 1 || body["force_single"] != true {
			t.Error(body)
		}
		_, _ = io.WriteString(w, `{"code":10000,"data":{"task_id":"synthetic-task"}}`)
	}))
	defer server.Close()
	p := newJimengImageProvider("synthetic-ak", "synthetic-sk", "synthetic-model", server.URL, server.Client())
	in := Input{Kind: "general", Prompt: "synthetic scene", Ratio: "16:9", Style: "natural"}
	if _, e := p.SubmitImageWithReference(context.Background(), in, syntheticCreativePNG(), "image/png"); e == nil || calls != 0 {
		t.Fatal("bridge default not closed")
	}
	p.SetReferenceBridge(bridge)
	j, e := p.SubmitImageWithReference(context.Background(), in, syntheticCreativePNG(), "image/png")
	if e != nil || j.ID != "synthetic-task" || calls != 1 || bridge.calls != 1 {
		t.Fatal(j, e, calls)
	}
}
func TestRetiredOpenAIVideoNeverCallsNetwork(t *testing.T) {
	t.Setenv("VIDEO_PROVIDER", "openai-video")
	t.Setenv("VIDEO_MODEL", "sora-2")
	t.Setenv("OPENAI_API_KEY", "synthetic")
	p, c := ConfigureVideoFromEnv()
	if p != nil || c.Ready || c.ReasonCode != "provider_retired" {
		t.Fatal(c)
	}
	direct := NewOpenAICreativeProvider("synthetic")
	direct.SetModels("", "", "sora-2")
	if _, e := direct.SubmitVideo(context.Background(), "synthetic video"); e == nil {
		t.Fatal("retired submit")
	}
	if _, e := direct.PollVideo(context.Background(), "video_test"); e == nil {
		t.Fatal("retired poll")
	}
	if _, e := direct.DownloadVideo(context.Background(), "video_test"); e == nil {
		t.Fatal("retired download")
	}
}
func TestMusicNativeInstrumentalNoRetry(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		if r.URL.Path != "/music_generation" || b["is_instrumental"] != true || b["output_format"] != "hex" || b["stream"] != false {
			t.Error(b)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"audio": hex.EncodeToString([]byte("ID3synthetic")), "status": 2}, "base_resp": map[string]int{"status_code": 0}})
	}))
	defer server.Close()
	p := &MiniMaxMusicProvider{transport: newCreativeProvider("synthetic", server.URL, server.Client()), model: "music-3.0"}
	b, e := p.Music(context.Background(), "instrumental scene music")
	if e != nil || string(b) != "ID3synthetic" || calls != 1 {
		t.Fatal(e, calls)
	}
	t.Setenv("MUSIC_PROVIDER", "minimax-music")
	t.Setenv("MUSIC_MODEL", "music-3.0")
	t.Setenv("MUSIC_EXISTING_ACCOUNT_ACK", "")
	if _, c := ConfigureMusicFromEnv(); c.Ready || c.ReasonCode != "requires_existing_account_entitlement" {
		t.Fatal(c)
	}
}
func localTransformFixture(t *testing.T, out string) *LocalTransform {
	t.Helper()
	file := filepath.Join(t.TempDir(), "test command")
	// Test-only executable: consumes JSON but never performs model inference.
	script := "#!/bin/sh\ncat >/dev/null\nif [ -n \"${STUDIO_TEST_SECRET+x}\" ]; then exit 9; fi\nprintf '%s' '" + out + "'\n"
	if err := os.WriteFile(file, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	return &LocalTransform{command: file, model: "synthetic-fixture"}
}
func TestLocalModelContractIsolationAndValidation(t *testing.T) {
	t.Setenv("STUDIO_TEST_SECRET", "synthetic-not-a-credential")
	p := localTransformFixture(t, `{"cues":[{"start":0,"end":1,"text":"你好"}]}`)
	r, e := p.Align(context.Background(), []byte("synthetic-audio"), "audio/wav", 2, "你好")
	if e != nil || r.TimingBasis != "forced-alignment" {
		t.Fatal(r, e)
	}
	if _, e = p.Align(context.Background(), []byte("synthetic-audio"), "audio/wav", 2, "different"); e == nil {
		t.Fatal("changed transcript accepted")
	}
	if _, e = p.Align(context.Background(), []byte("synthetic-audio"), "audio/wav", 0.5, "你好"); e == nil {
		t.Fatal("invalid timestamps accepted")
	}
	video := []byte("0000ftypisom")
	p = localTransformFixture(t, `{"video":"`+base64.StdEncoding.EncodeToString(video)+`"}`)
	got, e := p.Sync(context.Background(), video, []byte("synthetic-audio"), "audio/wav")
	if e != nil || !bytes.Equal(got, video) {
		t.Fatal(e)
	}
	p = localTransformFixture(t, strings.Repeat("x", 1000))
	if _, e = p.run(context.Background(), map[string]string{"test": "only"}, 20); e == nil {
		t.Fatal("output limit bypass")
	}
}
func TestConsistencyContactSheetBounded(t *testing.T) {
	b, e := consistencyContactSheet(syntheticCreativePNG(), syntheticCreativePNG())
	if e != nil {
		t.Fatal(e)
	}
	cfg, e := png.DecodeConfig(bytes.NewReader(b))
	if e != nil || cfg.Width != 1024 || cfg.Height != 512 {
		t.Fatal(cfg, e)
	}
	if _, e = consistencyContactSheet([]byte("invalid"), syntheticCreativePNG()); e == nil {
		t.Fatal("bad image accepted")
	}
}

func TestForcedAlignmentWorkflowOwnershipReplayAndSource(t *testing.T) {
	m, state := alignmentFixture(t)
	defer m.alignment.close()
	p := localTransformFixture(t, `{"cues":[{"start":0,"end":1,"text":"你好"}]}`)
	m.alignment.forced = p
	m.alignment.forcedCaps = Capabilities{Provider: "local-forced-alignment", Model: "synthetic-fixture", Mode: "real", Ready: true}
	clip := state.Timeline.Clips[0]
	input := AlignmentInput{ClipID: clip.ID, AudioAssetID: clip.AudioAssetID, Mode: "forced", Text: "你好", ExpectedVersion: state.Version, ExpectedProvider: "local-forced-alignment", ExpectedModel: "synthetic-fixture"}
	job, created, e := m.CreateAlignment("p", "s", "forced-test-001", input)
	if e != nil || !created {
		t.Fatal(e)
	}
	result := awaitAlignment(t, m.alignment, job.ID)
	if result.Status != "succeeded" || result.TimingBasis != "forced-alignment" {
		t.Fatal(result)
	}
	replay, created, e := m.CreateAlignment("p", "s", "forced-test-001", input)
	if e != nil || created || replay.ID != job.ID {
		t.Fatal(e)
	}
	if _, _, e = m.CreateAlignment("p", "other-space", "forced-cross-001", input); e == nil {
		t.Fatal("cross-space audio accepted")
	}
	state.Timeline.Clips[0].SubtitleCues = result.Cues
	state.Timeline.Clips[0].SubtitleSource = "forced"
	state.Timeline.Clips[0].SubtitleAlignmentID = result.ID
	saved, e := m.SaveTimeline("p", "s", "forced-apply-001", state.Version, state.Timeline)
	if e != nil {
		t.Fatal(e)
	}
	saved.Timeline.Clips[0].SubtitleCues[0].Text = "修改"
	if _, e = m.SaveTimeline("p", "s", "forced-tamper-001", saved.Version, saved.Timeline); e == nil {
		t.Fatal("modified text retained forced provenance")
	}
	reopened, e := openAlignmentStore(filepath.Dir(m.alignment.path))
	if e != nil {
		t.Fatal(e)
	}
	defer reopened.close()
	if len(reopened.list("p", "s")) != 1 {
		t.Fatal("forced task not recoverable")
	}
}
func TestCreativeMaskAndComparisonRejectCrossProjectAssets(t *testing.T) {
	s, e := OpenStore(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	project := createProject(t, s)
	assets, e := s.media.AddAsset(project.ID, project.SpaceID, "mask-ref-001", "ref.png", "reference", "", "", "uploaded", syntheticCreativePNG(), "image/png")
	if e != nil {
		t.Fatal(e)
	}
	ref := assets.Assets[0].ID
	assets, e = s.media.AddAsset(project.ID, project.SpaceID, "mask-image-001", "mask.png", "reference", "", "", "uploaded", editMaskFixture(640, 360), "image/png")
	if e != nil {
		t.Fatal(e)
	}
	mask := assets.Assets[len(assets.Assets)-1].ID
	SetCreativeImage(s, NewOpenAIProvider("synthetic"), Capabilities{Provider: "openai-images", Model: "synthetic", Mode: "real", Ready: true})
	in := CreativeJobInput{Kind: "image", Prompt: "change chosen region", ExpectedVersion: project.Version, ExpectedProvider: "openai-images", ExpectedModel: "synthetic", ReferenceAssetID: ref, MaskAssetID: mask}
	j, created, e := s.createCreativeJob(project.SpaceID, project.ID, "mask-create-001", in)
	if e != nil || !created || j.MaskAssetID != mask {
		t.Fatal(j, e)
	}
	other, _, e := s.CreateProject(project.SpaceID, "mask-other-001", projectInput())
	if e != nil {
		t.Fatal(e)
	}
	in.ExpectedVersion = other.Version
	if _, _, e = s.createCreativeJob(other.SpaceID, other.ID, "mask-cross-001", in); e == nil {
		t.Fatal("cross-project reference accepted")
	}
	in.Kind = "consistency"
	in.MaskAssetID = ""
	in.ComparisonAssetID = mask
	in.ExpectedProvider = "openai-text"
	if _, _, e = s.createCreativeJob(other.SpaceID, other.ID, "compare-cross-001", in); e == nil {
		t.Fatal("cross-project comparison accepted")
	}
}

type consistencyFixtureProvider struct {
	unsupportedCreativeProvider
	calls int
}

func (p *consistencyFixtureProvider) DescribeImage(_ context.Context, prompt string, data []byte, mime string) (BriefAnalysis, error) {
	p.calls++
	cfg, e := png.DecodeConfig(bytes.NewReader(data))
	if e != nil || cfg.Width != 1024 || cfg.Height != 512 || mime != "image/png" || !strings.Contains(prompt, "LEFT is the reference") {
		return BriefAnalysis{}, providerFailure("invalid_provider_response")
	}
	return BriefAnalysis{Summary: "Synthetic comparison fixture", Questions: []string{}, Suggestions: []string{"Review costume color manually."}, Provider: "synthetic-vision"}, nil
}
func TestConsistencyWorkflowActualComparisonAndOwnership(t *testing.T) {
	s, e := OpenStore(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	p := createProject(t, s)
	media, e := s.media.AddAsset(p.ID, p.SpaceID, "consistency-ref", "ref.png", "reference", "", "", "uploaded", syntheticCreativePNG(), "image/png")
	if e != nil {
		t.Fatal(e)
	}
	ref := media.Assets[0].ID
	media, e = s.media.AddAsset(p.ID, p.SpaceID, "consistency-shot", "shot.png", "shot", "", "", "uploaded", syntheticCreativePNG(), "image/png")
	if e != nil {
		t.Fatal(e)
	}
	shot := media.Assets[len(media.Assets)-1].ID
	provider := &consistencyFixtureProvider{}
	if e = ConfigureCreative(s, provider, Capabilities{Provider: "synthetic-vision", Model: "fixture", Mode: "real", Ready: true}); e != nil {
		t.Fatal(e)
	}
	in := CreativeJobInput{Kind: "consistency", ExpectedVersion: p.Version, ExpectedProvider: "synthetic-vision", ExpectedModel: "fixture", Prompt: "compare costume continuity", ReferenceAssetID: ref, ComparisonAssetID: shot}
	other, _, e := s.CreateProject(p.SpaceID, "consistency-other", projectInput())
	if e != nil {
		t.Fatal(e)
	}
	in.ExpectedVersion = other.Version
	if _, _, e = s.createCreativeJob(other.SpaceID, other.ID, "consistency-cross", in); e == nil {
		t.Fatal("cross-project images accepted")
	}
	in.ExpectedVersion = p.Version
	job, _, e := s.createCreativeJob(p.SpaceID, p.ID, "consistency-create", in)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.executeCreativeJob(context.Background(), &job); e != nil || provider.calls != 1 || job.Analysis == nil || job.ComparisonAssetID != shot {
		t.Fatal(job, e, provider.calls)
	}
}
func TestLocalSoundEffectContract(t *testing.T) {
	wave := syntheticWAV(1)
	p := localTransformFixture(t, `{"audio":"`+base64.StdEncoding.EncodeToString(wave)+`"}`)
	got, e := p.Music(context.Background(), "test synthetic fixture")
	if e != nil || !bytes.Equal(got, wave) {
		t.Fatal(e)
	}
	p = localTransformFixture(t, `{"audio":"aW52YWxpZA=="}`)
	if _, e = p.Music(context.Background(), "invalid fixture"); e == nil {
		t.Fatal("invalid sound accepted")
	}
}
