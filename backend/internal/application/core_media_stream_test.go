package application

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNativeGatewayImageResponsesHaveNoLegacyByteQuota(t *testing.T) {
	path := nativeReferenceFile(t, 4096, 3072, false)
	_, size := nativeFileHash(t, path)
	if size <= 32<<20 {
		t.Fatal("small image fixture")
	}
	calls, downloads := 0, 0
	urlMode := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		io.Copy(io.Discard, r.Body)
		if urlMode {
			fmt.Fprint(w, `{"data":[{"url":"https://cdn.synthetic.invalid/result.png"}]}`)
			return
		}
		fmt.Fprint(w, `{"data":[{"b64_json":"`)
		nativeWriteBase64(t, w, path)
		fmt.Fprint(w, `"}]}`)
	}))
	defer srv.Close()
	p := gatewayImageProvider{api: newCreativeProvider("synthetic", srv.URL, srv.Client()), model: GatewayModel{Model: "synthetic", Capabilities: []string{"image", "image_edit"}, MaxReferenceImages: 2}}
	p.downloadTo = func(ctx context.Context, url string, out io.Writer) error {
		downloads++
		if url != "https://cdn.synthetic.invalid/result.png" {
			t.Error("changed URL")
		}
		f, e := os.Open(path)
		if e != nil {
			return e
		}
		defer f.Close()
		n, e := io.CopyBuffer(out, contextBlobReader{ctx, f}, make([]byte, 64<<10))
		if n != size {
			t.Error("short download")
		}
		return e
	}
	in := Input{Kind: "general", Prompt: "synthetic large gateway response"}
	for _, run := range []func() (GenerationResult, error){func() (GenerationResult, error) { return p.Generate(context.Background(), in) }, func() (GenerationResult, error) {
		return p.GenerateWithReference(context.Background(), in, syntheticPNG(0), "image/png")
	}, func() (GenerationResult, error) {
		return p.GenerateWithReferenceFiles(context.Background(), in, path, "image/png", "")
	}, func() (GenerationResult, error) { urlMode = true; return p.Generate(context.Background(), in) }} {
		out, e := run()
		if e != nil || out.Width != 4096 || out.Height != 3072 {
			t.Fatal("gateway output", e, out.Width, out.Height)
		}
	}
	if calls != 4 || downloads != 1 {
		t.Fatal("retry or lost URL result", calls, downloads)
	}
}
func TestNativeGatewayImageFailureDoesNotFetchFallbackOrLeaveFiles(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	calls := 0
	p := gatewayImageProvider{downloadTo: func(context.Context, string, io.Writer) error { calls++; return nil }}
	for _, body := range []string{`{"data":[{"b64_json":"bad!","url":"https://public.invalid/x"}]}`, `{"data":[{"b64_json":"YWJj","b64_json":"ZA=="}]}`, `{"error":{"message":"upstream failure"},"data":[{"url":"https://public.invalid/x"}]}`, `{"data":[{"b64_json":"YWJj"}],"tail":`} {
		if _, e := p.decodeImageReader(context.Background(), strings.NewReader(body)); e == nil {
			t.Fatal("invalid upstream accepted")
		}
	}
	if calls != 0 {
		t.Fatal("invalid JSON used URL fallback")
	}
	files, _ := filepath.Glob(filepath.Join(os.TempDir(), "studio-*-*"))
	if len(files) != 0 {
		t.Fatal("response files leaked", files)
	}
	for _, url := range []string{"http://127.0.0.1/a", "https://127.0.0.1/a", "https://[::1]/a", "https://user:password@public.invalid/a"} {
		if e := safeImageDownloadTo(context.Background(), url, io.Discard); e == nil {
			t.Fatal("unsafe URL accepted", url)
		}
	}
	lifetime, cancel := context.WithCancel(context.Background())
	cancel()
	guarded := guardRuntimeDownloadTo(lifetime, func(context.Context, string, io.Writer) error { calls++; return nil }, func(context.Context) error { return nil }, nil)
	if e := guarded(context.Background(), "https://public.invalid/a", io.Discard); e == nil || calls != 0 {
		t.Fatal("expired download lifetime dispatched", e, calls)
	}
}
func longCoreWave(t *testing.T) string {
	t.Helper()
	path := largeWaveFile(t)
	f, e := os.OpenFile(path, os.O_RDWR, 0)
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	var rate [8]byte
	binary.LittleEndian.PutUint32(rate[:4], 48000)
	binary.LittleEndian.PutUint32(rate[4:], 288000)
	if _, e = f.WriteAt(rate[:], 24); e != nil {
		t.Fatal(e)
	}
	return path
}
func TestNativeLongMediaAndGatewayASRUseOwnedFilesWithoutLocalQuota(t *testing.T) {
	path := longCoreWave(t)
	want, size := nativeFileHash(t, path)
	kind, duration, e := validateMedia(path, "audio/wav")
	if e != nil || kind != "audio" || duration <= 180 {
		t.Fatal("long valid media", e, duration)
	}
	if e = validateProviderMediaFile(context.Background(), path, "audio/wav"); e != nil {
		t.Fatal("long decode", e)
	}
	s := testStore(t)
	project := createProject(t, s)
	state, e := s.media.addAssetFile(context.Background(), project.ID, project.SpaceID, "long-voice", "synthetic long voice", "voice", "", "", "uploaded", path, "audio/wav", nil)
	if e != nil || state.Assets[0].Duration <= 180 {
		t.Fatal("long ingest", e)
	}
	asset := state.Assets[0]
	path, _, e = s.media.file(project.ID, project.SpaceID, asset.ID, false, false)
	if e != nil {
		t.Fatal(e)
	}
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		reader, e := r.MultipartReader()
		if e != nil {
			t.Error(e)
			return
		}
		format := ""
		seen := false
		for {
			part, e := reader.NextPart()
			if e == io.EOF {
				break
			}
			if e != nil {
				t.Error(e)
				return
			}
			if part.FormName() == "file" {
				seen = true
				h := sha256.New()
				n, e := io.Copy(h, part)
				if e != nil || n != size || hex.EncodeToString(h.Sum(nil)) != want {
					t.Error("ASR source changed")
				}
			} else {
				data, _ := io.ReadAll(part)
				if part.FormName() == "response_format" {
					format = string(data)
				}
			}
		}
		if !seen {
			t.Error("no audio")
		}
		if format == "json" {
			fmt.Fprint(w, `{"text":"合成上游转写测试"}`)
		} else {
			fmt.Fprint(w, `{"segments":[{"start":0,"end":1,"text":"合成上游时间戳"}]}`)
		}
	}))
	defer srv.Close()
	p := newOpenAIAlignmentProvider("synthetic", "synthetic-gateway-asr", srv.URL, srv.Client())
	p.gateway = true
	for _, plain := range []bool{false, true} {
		p.plainTranscript = plain
		if _, e = p.TranscribeFile(context.Background(), path, "audio/wav", duration); e != nil {
			t.Fatal("long gateway ASR", e)
		}
	}
	if calls != 2 {
		t.Fatal("ASR retry", calls)
	}
	p.gateway = false
	p.model = "whisper-1"
	if _, e = p.TranscribeFile(context.Background(), path, "audio/wav", duration); e == nil || calls != 2 {
		t.Fatal("direct OpenAI protocol bound lost", e)
	}
	// Timeline identity checks remain independent of media duration and byte size.
	if _, _, e = s.media.file(project.ID, "foreign", asset.ID, false, false); !errors.Is(e, ErrNotFound) {
		t.Fatal("foreign audio", e)
	}
}
func TestNativeManagedVisionUsesLargeFileAndKeepsOriginal(t *testing.T) {
	path := nativeReferenceFile(t, 4096, 3072, false)
	want, _ := nativeFileHash(t, path)
	s := testStore(t)
	project := createProject(t, s)
	state, e := s.media.addAssetFile(context.Background(), project.ID, project.SpaceID, "large-vision", "large vision reference", "reference", "", "", "uploaded", path, "image/png", nil)
	if e != nil {
		t.Fatal(e)
	}
	id := state.Assets[0].ID
	path, _, e = s.media.file(project.ID, project.SpaceID, id, false, false)
	if e != nil {
		t.Fatal(e)
	}
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var body struct {
			Messages []struct {
				Content []struct {
					ImageURL struct {
						URL string `json:"url"`
					} `json:"image_url"`
				}
			}
		}
		if e := json.NewDecoder(r.Body).Decode(&body); e != nil {
			t.Error(e)
			return
		}
		raw, e := base64.StdEncoding.DecodeString(strings.TrimPrefix(body.Messages[0].Content[1].ImageURL.URL, "data:image/png;base64,"))
		sum := sha256.Sum256(raw)
		if e != nil || hex.EncodeToString(sum[:]) != want {
			t.Error("vision file changed")
		}
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]string{"content": `{"summary":"合成上游画面分析","questions":[],"suggestions":[]}`}}}})
	}))
	defer srv.Close()
	p := gatewayCreativeProvider{OpenAICreativeProvider: newCreativeProvider("synthetic", srv.URL, srv.Client()), model: GatewayModel{Model: "synthetic-vision", Capabilities: []string{"text", "vision"}, MaxOutputTokens: 1000}}
	s.execution = &ExecutionGovernor{Moderator: streamTestModerator(func(context.Context, ExecutionRequest) (ModerationDecision, error) {
		return ModerationDecision{Provider: "fixture", Model: "synthetic", Status: "allow", CheckedAt: time.Now()}, nil
	})}
	job := CreativeJob{ID: "large-vision", ProjectID: project.ID, SpaceID: project.SpaceID, Kind: "vision", Provider: "gateway-vision", Model: "synthetic-vision", Prompt: "分析本项目合成素材", ReferenceAssetID: id, RuntimeSelection: &RuntimeSelection{TenantID: project.SpaceID, Capability: "vision"}}
	if e = s.executeCreativeJobWith(context.Background(), &job, &RuntimeProviders{Vision: &p}); e != nil || job.Analysis == nil || calls != 1 {
		t.Fatal("managed vision did not use Vision provider", e, calls)
	}
	sheet, e := consistencyContactSheetFiles(path, path)
	if e != nil {
		t.Fatal(e)
	}
	cfg, e := png.DecodeConfig(bytes.NewReader(sheet))
	if e != nil || cfg.Width != 1024 || cfg.Height != 512 {
		t.Fatal("comparison view", e)
	}
	after, _ := nativeFileHash(t, path)
	if after != want {
		t.Fatal("analysis rewrote source")
	}
}
