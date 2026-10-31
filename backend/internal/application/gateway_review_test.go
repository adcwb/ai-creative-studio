package application

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestGatewayVisionHonorsConfiguredOutputBound(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			t.Error("invalid payload")
		}
		if body["max_completion_tokens"] != float64(128) {
			t.Errorf("configured output bound ignored: %v", body["max_completion_tokens"])
		}
		_, _ = w.Write([]byte(`{"choices":[{"finish_reason":"stop","message":{"content":"{\"summary\":\"Synthetic image description\",\"questions\":[],\"suggestions\":[]}"}}]}`))
	}))
	defer upstream.Close()
	p := &gatewayCreativeProvider{OpenAICreativeProvider: newCreativeProvider("synthetic", upstream.URL, upstream.Client()), model: GatewayModel{Model: "vendor/vision", MaxOutputTokens: 128}}
	if _, err := describeGatewayVision(context.Background(), p, "Describe", nativeTestImage(t, "png"), "image/png"); err != nil {
		t.Fatal(err)
	}
}

func TestGatewayCancelledDispatchedRequestRemainsUnknown(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(entered); <-release }))
	defer upstream.Close()
	defer close(release)
	u, _ := url.Parse(upstream.URL)
	t.Setenv("MODEL_GATEWAY_ALLOWED_HOSTS", u.Host)
	runtime, _, model := gatewayFixture(t, upstream.URL+"/v1")
	selection, _, err := runtime.Resolve(context.Background(), "tenant-a", "actor-a", model.ID, "text")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan error, 1)
	go func() {
		finished <- runtime.Execute(ctx, selection, func(ctx context.Context, p RuntimeProviders) error {
			_, e := p.Creative.Analyze(ctx, ProjectInput{Title: "Synthetic", Brief: "Synthetic creative request"})
			return e
		})
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("request not dispatched")
	}
	report, err := runtime.auth.models.usage.report("tenant-a", httptest.NewRequest("GET", "/model-usage", nil))
	if err != nil || len(report.Items) != 1 || report.Items[0].HTTPCalls != 1 || report.Items[0].State != "unknown" {
		t.Errorf("in-flight attempt missing: %+v %v", report, err)
	}
	cancel()
	select {
	case err = <-finished:
		if err == nil {
			t.Error("cancelled request succeeded")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancellation not propagated")
	}
	report, err = runtime.auth.models.usage.report("tenant-a", httptest.NewRequest("GET", "/model-usage", nil))
	if err != nil || len(report.Items) != 1 || report.Items[0].State != "unknown" {
		t.Fatalf("ambiguous dispatched request classified as definitive failure: %+v %v", report, err)
	}
}

func TestGatewayPrivateTargetsAndRedirectsFailClosed(t *testing.T) {
	var hits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1); w.WriteHeader(200) }))
	defer target.Close()
	t.Setenv("MODEL_GATEWAY_ALLOWED_HOSTS", "")
	for _, endpoint := range []string{target.URL, strings.Replace(target.URL, "http:", "https:", 1)} {
		client := newGatewayClient(AIGateway{BaseURL: endpoint + "/v1", TimeoutSeconds: 5})
		if res, err := client.Get(endpoint + "/v1/models"); err == nil {
			res.Body.Close()
			t.Error("private target accepted without deployment grant")
		}
	}
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/v1/models", http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	u, _ := url.Parse(redirect.URL)
	t.Setenv("MODEL_GATEWAY_ALLOWED_HOSTS", u.Host)
	client := newGatewayClient(AIGateway{BaseURL: redirect.URL + "/v1", TimeoutSeconds: 5})
	req, _ := http.NewRequest("GET", redirect.URL+"/v1/models", nil)
	req.Header.Set("Authorization", "Bearer synthetic-secret")
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 307 || hits.Load() != 0 {
		t.Fatal("redirect followed or private endpoint contacted")
	}
}

func TestPostgresGatewayUsageAndRevocationAcrossInstances(t *testing.T) {
	db := postgresTestDB(t)
	key := bytes.Repeat([]byte{43}, 32)
	a, err := OpenPostgresModelConfigStore(t.TempDir(), db, key)
	if err != nil {
		t.Fatal(err)
	}
	b, err := OpenPostgresModelConfigStore(t.TempDir(), db, key)
	if err != nil {
		t.Fatal(err)
	}
	report, err := a.saveGateway("tenant-a", 0, AIGateway{Name: "Synthetic", BaseURL: "https://gateway.example.com/v1", Enabled: true, TimeoutSeconds: 5}, ModelCredentialInput{Action: "set", Secret: "synthetic-key"})
	if err != nil {
		t.Fatal(err)
	}
	g := report.Gateways[0]
	report, err = b.managementReport("tenant-a")
	if err != nil || len(report.Gateways) != 1 {
		t.Fatal("gateway not visible across instances", err)
	}
	if report, err = b.managementReport("tenant-b"); err != nil || len(report.Gateways) != 0 {
		t.Fatal("gateway tenant leak", err)
	}
	if err = a.usage.save(ModelCall{ID: "synthetic-call", Tenant: "tenant-a", GatewayID: g.ID, State: "unknown", HTTPCalls: 1, StartedAt: time.Now().UTC(), Currency: "USD", MaximumMinor: 10}); err != nil {
		t.Fatal(err)
	}
	usage, err := b.usage.report("tenant-a", httptest.NewRequest("GET", "/model-usage", nil))
	if err != nil || len(usage.Items) != 1 || usage.Items[0].HTTPCalls != 1 {
		t.Fatal("usage not persisted across instances", err)
	}
	if _, err = b.saveGateway("tenant-a", g.Version, g, ModelCredentialInput{Action: "revoke"}); err != nil {
		t.Fatal(err)
	}
	report, err = a.managementReport("tenant-a")
	if err != nil || report.Gateways[0].SecretStatus != "revoked" || report.Gateways[0].Enabled {
		t.Fatal("revocation not refreshed", err)
	}
	restored, err := OpenPostgresModelConfigStore(t.TempDir(), db, key)
	if err != nil {
		t.Fatal(err)
	}
	usage, err = restored.usage.report("tenant-a", httptest.NewRequest("GET", "/model-usage", nil))
	if err != nil || len(usage.Items) != 1 {
		t.Fatal("usage missing after reopen", err)
	}
}

func TestGatewayImageVideoAndSpeechContracts(t *testing.T) {
	imageData := nativeTestImage(t, "png")
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer synthetic-protocol-key" {
			t.Error("missing auth")
		}
		switch r.URL.Path {
		case "/v1/images/generations":
			var body map[string]any
			if json.NewDecoder(r.Body).Decode(&body) != nil || body["model"] != "vendor/shared" || body["n"] != float64(1) {
				t.Error("image payload mismatch")
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]string{"b64_json": base64.StdEncoding.EncodeToString(imageData)}}})
		case "/v1/images/edits":
			if err := r.ParseMultipartForm(8 << 20); err != nil {
				t.Error(err)
				return
			}
			defer r.MultipartForm.RemoveAll()
			file, header, err := r.FormFile("image[]")
			if err != nil {
				t.Error(err)
				return
			}
			defer file.Close()
			raw, _ := io.ReadAll(file)
			if !bytes.Equal(raw, imageData) || header.Header.Get("Content-Type") != "image/png" || r.FormValue("model") != "vendor/shared" {
				t.Error("edit input mismatch")
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]string{"b64_json": base64.StdEncoding.EncodeToString(imageData)}}})
		case "/v1/videos":
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Error(err)
				return
			}
			defer r.MultipartForm.RemoveAll()
			if r.FormValue("model") != "vendor/shared" || r.FormValue("seconds") != "8" || r.FormValue("size") != "720x1280" {
				t.Error("video options ignored")
			}
			_, _ = w.Write([]byte(`{"id":"video_1","status":"queued"}`))
		case "/v1/videos/video_1":
			if r.Method != "GET" {
				t.Error("poll must be read only")
			}
			_, _ = w.Write([]byte(`{"id":"video_1","status":"completed"}`))
		case "/v1/videos/video_1/content":
			_, _ = w.Write([]byte("0000ftyp0000")) // Header fixture; real decoding is covered by media acceptance.
		case "/v1/audio/speech":
			var body map[string]string
			if json.NewDecoder(r.Body).Decode(&body) != nil || body["voice"] != "synthetic-voice" || body["response_format"] != "wav" || body["model"] != "vendor/shared" {
				t.Error("speech payload mismatch")
			}
			raw := make([]byte, 44)
			copy(raw, "RIFF")
			copy(raw[8:], "WAVE")
			_, _ = w.Write(raw)
		default:
			t.Error("unexpected endpoint", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer upstream.Close()
	api := newCreativeProvider("synthetic-protocol-key", upstream.URL+"/v1", upstream.Client())
	model := GatewayModel{Model: "vendor/shared", Capabilities: []string{"image", "image_edit", "video", "speech"}, VideoSeconds: 8, VideoSize: "720x1280"}
	imageProvider := &gatewayImageProvider{api: api, model: model}
	in := Input{Prompt: "Synthetic creative image", Kind: "poster", Ratio: "1:1", Style: "natural"}
	if _, err := imageProvider.Generate(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	if _, err := imageProvider.GenerateWithReference(context.Background(), in, imageData, "image/png"); err != nil {
		t.Fatal(err)
	}
	creative := &gatewayCreativeProvider{OpenAICreativeProvider: api, model: model, voice: "synthetic-voice"}
	job, err := creative.SubmitVideo(context.Background(), "Synthetic creative video")
	if err != nil || job.ID != "video_1" {
		t.Fatal(err)
	}
	job, err = creative.PollVideo(context.Background(), job.ID)
	if err != nil || job.Status != "completed" {
		t.Fatal(err)
	}
	if _, err = creative.DownloadVideo(context.Background(), job.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = creative.Speech(context.Background(), "Synthetic speech"); err != nil {
		t.Fatal(err)
	}
	if _, err = creative.PollVideo(context.Background(), "../other"); err == nil {
		t.Fatal("path injection accepted")
	}
	if calls.Load() != 6 {
		t.Fatal("unexpected retry or extra dispatch", calls.Load())
	}
}

func TestGatewayCannotReviveRetiredOfficialVideoEndpoint(t *testing.T) {
	runtime, g, m := gatewayFixture(t, "https://api.openai.com/v1")
	m.Capabilities = []string{"video"}
	m.VideoSeconds = 4
	m.VideoSize = "1280x720"
	if _, err := runtime.auth.models.saveGatewayModel("tenant-a", m.Version, m); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runtime.Resolve(context.Background(), "tenant-a", "actor-a", m.ID, "video"); err == nil {
		t.Fatal("retired official endpoint enabled by gateway configuration")
	}
	if gatewayCapabilityRetired(g, "text") || gatewayCapabilityRetired(AIGateway{BaseURL: "https://gateway.example.com/v1"}, "video") {
		t.Fatal("unrelated protocol blocked")
	}
}

func TestGatewayTranscriptionAliasesPreserveTimingContracts(t *testing.T) {
	for _, plain := range []bool{true, false} {
		t.Run(fmt.Sprint(plain), func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/audio/transcriptions" || r.Header.Get("Authorization") != "Bearer synthetic-asr" {
					t.Error("ASR destination/auth mismatch")
				}
				if err := r.ParseMultipartForm(1 << 20); err != nil {
					t.Error(err)
					return
				}
				defer r.MultipartForm.RemoveAll()
				if r.FormValue("model") != "vendor/asr-alias" {
					t.Error("ASR alias not preserved")
				}
				if plain {
					if r.FormValue("response_format") != "json" || len(r.MultipartForm.Value["timestamp_granularities[]"]) != 0 {
						t.Error("transcript requested invented timestamps")
					}
					_, _ = w.Write([]byte(`{"text":"Synthetic transcript"}`))
				} else {
					if r.FormValue("response_format") != "verbose_json" || len(r.MultipartForm.Value["timestamp_granularities[]"]) != 2 {
						t.Error("timestamp contract missing")
					}
					_, _ = w.Write([]byte(`{"segments":[{"start":0,"end":0.5,"text":"Synthetic subtitle"}]}`))
				}
			}))
			defer upstream.Close()
			p := NewOpenAIAlignmentProvider("synthetic-asr", "vendor/asr-alias")
			p.gateway = true
			p.plainTranscript = plain
			p.endpoint = upstream.URL + "/v1/audio/transcriptions"
			p.client = upstream.Client()
			result, err := p.Transcribe(context.Background(), googleTestWAV(), "audio/wav", 1)
			if err != nil {
				t.Fatal(err)
			}
			if plain && (result.Transcript != "Synthetic transcript" || len(result.Cues) != 0 || result.TimingBasis != "transcript-only") {
				t.Fatal("invented transcript cues")
			}
			if !plain && (len(result.Cues) != 1 || result.Cues[0].End != 0.5) {
				t.Fatal("lost supplied timing")
			}
		})
	}
}
