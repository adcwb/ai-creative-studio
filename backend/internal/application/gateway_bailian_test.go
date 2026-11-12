package application

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
)

func TestGatewayVendorPersistenceAndCompatibility(t *testing.T) {
	runtime, g, m := gatewayFixture(t, "https://synthetic.example/api/v1")
	s := runtime.auth.models
	q, _, err := runtime.Resolve(context.Background(), "tenant-a", "actor-a", m.ID, "text")
	if err != nil {
		t.Fatal(err)
	}
	if g.Vendor != gatewayOpenAI {
		t.Fatal("old clients must default to OpenAI", g)
	}
	g.Vendor = "unknown"
	if _, err := s.saveGateway("tenant-a", g.Version, g, ModelCredentialInput{Action: "keep"}); !errors.Is(err, errModelInput) {
		t.Fatal("unknown vendor accepted", err)
	}
	g.Vendor = gatewayBailian
	report, err := s.saveGateway("tenant-a", g.Version, g, ModelCredentialInput{Action: "keep"})
	if err != nil {
		t.Fatal(err)
	}
	if gatewaySelectionCurrent(s.data, q) {
		t.Fatal("vendor change retained stale selection")
	}
	g = report.Gateways[0]
	g.Vendor = "" // Older clients must not reset an existing native gateway.
	if _, err := s.saveGateway("tenant-a", g.Version, g, ModelCredentialInput{Action: "keep"}); err != nil {
		t.Fatal(err)
	}
	restored, err := OpenModelConfigStore(filepath.Dir(s.path), bytes.Repeat([]byte{11}, 32))
	if err != nil {
		t.Fatal(err)
	}
	report, err = restored.managementReport("tenant-a")
	if err != nil || report.Gateways[0].Vendor != gatewayBailian {
		t.Fatal("lost vendor on restart", err)
	}
	secret, err := restored.gatewaySecretLocked("tenant-a", restored.data.Tenants["tenant-a"].Gateways[g.ID])
	if err != nil || string(secret) != "synthetic-gateway-key" {
		t.Fatal("credential migration broke", err)
	}
	clear(secret)
	for _, raw := range []string{"https://synthetic.example", "https://synthetic.example/compatible-mode/v1/", "https://synthetic.example/api/v1"} {
		if got, err := normalizeVendorGatewayURL(gatewayBailian, raw); err != nil || got != "https://synthetic.example/api/v1" {
			t.Fatal(raw, got, err)
		}
	}
	for _, raw := range []string{"https://synthetic.example/api/v1?key=x", "https://user:secret@synthetic.example", "https://synthetic.example/arbitrary"} {
		if _, err := normalizeVendorGatewayURL(gatewayBailian, raw); err == nil {
			t.Fatal("unsafe/ambiguous prefix accepted", raw)
		}
	}
}

func TestBailianNativeCatalogPagination(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/api/v1/models" || r.Header.Get("Authorization") != "Bearer synthetic" || r.URL.Query().Get("page_size") != "100" {
			t.Error("wrong native catalog request", r.URL)
		}
		if r.URL.Query().Get("page_no") == "1" {
			fmt.Fprint(w, `{"output":{"total":2,"models":[{"model":"wan2.7-image"}]}}`)
		} else {
			fmt.Fprint(w, `{"output":{"total":2,"models":[{"model":"qwen3.8-max"}]}}`)
		}
	}))
	defer srv.Close()
	result := checkBailianCatalog(context.Background(), srv.Client(), srv.URL+"/api/v1", "synthetic")
	if result["reachable"] != true || len(result["models"].([]string)) != 2 || calls != 2 || result["generation_verified"] != false {
		t.Fatal(result, calls)
	}
}

func TestBailianImageWireAndDownload(t *testing.T) {
	calls, downloads := 0, 0
	wantRef := false
	response := `{"output":{"choices":[{"message":{"content":[{"text":"done"},{"image":"https://cdn.synthetic.invalid/image.png"}]}}]}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/api/v1"+bailianMultimodal || r.Header.Get("Authorization") != "Bearer synthetic" {
			t.Error("wrong native image request")
		}
		var body struct {
			Model  string `json:"model"`
			Prompt string `json:"prompt"`
			Input  struct {
				Messages []struct {
					Content []map[string]string `json:"content"`
				} `json:"messages"`
			} `json:"input"`
			Parameters struct {
				Size string `json:"size"`
				N    int    `json:"n"`
			} `json:"parameters"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		if body.Model != "wan2.7-image" || body.Prompt != "" || body.Parameters.Size != "1536*864" || body.Parameters.N != 1 || len(body.Input.Messages) != 1 {
			t.Error("incorrect image payload", body)
		}
		if wantRef && !strings.HasPrefix(body.Input.Messages[0].Content[0]["image"], "data:image/png;base64,") {
			t.Error("missing native reference")
		}
		fmt.Fprint(w, response)
	}))
	defer srv.Close()
	model := GatewayModel{Model: "wan2.7-image", Capabilities: []string{"image", "image_edit"}, MaxReferenceImages: 2}
	p := bailianImageProvider{wire: &bailianAPI{api: newCreativeProvider("synthetic", srv.URL+"/api/v1", srv.Client()), model: model}, images: gatewayImageProvider{downloadTo: func(_ context.Context, url string, out io.Writer) error {
		downloads++
		if url != "https://cdn.synthetic.invalid/image.png" {
			t.Error("incorrect result URL")
		}
		_, err := out.Write(syntheticPNG(0))
		return err
	}}}
	in := Input{Kind: "general", Prompt: "synthetic landscape", Ratio: "16:9"}
	result, err := p.Generate(context.Background(), in)
	if err != nil || result.Width < 1 || len(result.Data) == 0 {
		t.Fatal(result, err)
	}
	wantRef = true
	if _, err := p.GenerateWithReference(context.Background(), in, syntheticPNG(0), "image/png"); err != nil {
		t.Fatal(err)
	}
	if _, err := p.GenerateWithMask(context.Background(), in, nil, "image/png", nil); err == nil {
		t.Fatal("unsupported mask accepted")
	}
	if calls != 2 || downloads != 2 {
		t.Fatal("retry or unguarded mask call", calls, downloads)
	}
	wantRef = false
	for _, invalid := range []string{`{"output":{"choices":[]}}`, `{"code":"InvalidParameter","message":"private upstream text"}`, `{"data":[{"url":"https://cdn.synthetic.invalid/image.png"}]}`, `{"output":{"choices":[{"message":{"content":[{"image":"a"},{"image":"b"}]}}]}}`} {
		response = invalid
		if _, err := p.Generate(context.Background(), in); err == nil {
			t.Fatal("invalid native image response accepted")
		}
	}
	if downloads != 2 {
		t.Fatal("download on malformed response")
	}
}

func TestBailianRuntimeTextAndUsageUseVendorCompatibleProtocol(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/compatible-mode/v1/chat/completions" {
			t.Error("incorrect Bailian text endpoint", r.URL.Path)
		}
		var body map[string]json.RawMessage
		if json.NewDecoder(r.Body).Decode(&body) != nil || body["messages"] == nil || body["max_tokens"] == nil || body["max_completion_tokens"] != nil || body["input"] != nil || string(body["enable_thinking"]) != "false" {
			t.Error("wrong text wire payload")
		}
		fmt.Fprint(w, `{"request_id":"bailian-synthetic","choices":[{"finish_reason":"stop","message":{"content":"{\"summary\":\"Synthetic native answer\",\"questions\":[],\"suggestions\":[]}"}}],"usage":{"input_tokens":123,"output_tokens":45}}`)
	}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	t.Setenv("MODEL_GATEWAY_ALLOWED_HOSTS", u.Host)
	runtime, g, m := gatewayFixture(t, srv.URL+"/api/v1")
	g.Vendor = gatewayBailian
	if _, err := runtime.auth.models.saveGateway("tenant-a", g.Version, g, ModelCredentialInput{Action: "keep"}); err != nil {
		t.Fatal(err)
	}
	q, entry, err := runtime.Resolve(context.Background(), "tenant-a", "actor-a", m.ID, "text")
	if err != nil || entry.Protocol != gatewayBailian {
		t.Fatal(entry, err)
	}
	err = runtime.Execute(context.Background(), q, func(ctx context.Context, p RuntimeProviders) error {
		a, e := p.Creative.Analyze(ctx, ProjectInput{Title: "Synthetic", Brief: "Synthetic native test description"})
		if e == nil && a.Summary != "Synthetic native answer" {
			t.Error(a)
		}
		return e
	})
	if err != nil || calls != 1 {
		t.Fatal(err, calls)
	}
	report, err := runtime.auth.models.usage.report("tenant-a", httptest.NewRequest("GET", "/model-usage", nil))
	if err != nil || report.Total != 1 || report.Items[0].InputTokens == nil || *report.Items[0].InputTokens != 123 {
		t.Fatal(report, err)
	}
}

func TestBailianMediaProtocols(t *testing.T) {
	paths := []string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		var body map[string]json.RawMessage
		if r.Method == "POST" && json.NewDecoder(r.Body).Decode(&body) != nil {
			t.Error("invalid body")
		}
		switch r.URL.Path {
		case "/api/v1" + bailianVideo:
			if r.Header.Get("X-DashScope-Async") != "enable" || body["input"] == nil || body["parameters"] == nil {
				t.Error("missing async contract")
			}
			fmt.Fprint(w, `{"output":{"task_id":"native-task","task_status":"PENDING"}}`)
		case "/api/v1/tasks/native-task":
			fmt.Fprint(w, `{"output":{"task_id":"native-task","task_status":"SUCCEEDED","video_url":"https://cdn.synthetic.invalid/video.mp4"}}`)
		case "/api/v1/services/audio/tts/SpeechSynthesizer":
			fmt.Fprint(w, `{"output":{"audio":{"url":"https://cdn.synthetic.invalid/audio.wav"}}}`)
		case "/api/v1" + bailianMultimodal:
			if bytes.Contains(body["input"], []byte("input_audio")) {
				fmt.Fprint(w, `{"output":{"text":"native transcript"}}`)
			} else {
				fmt.Fprint(w, `{"output":{"choices":[{"finish_reason":"stop","message":{"content":[{"text":"{\"summary\":\"Synthetic visual answer\",\"questions\":[],\"suggestions\":[]}"}]}}]}}`)
			}
		default:
			t.Error("unexpected vendor route", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	api := newCreativeProvider("synthetic", srv.URL+"/api/v1", srv.Client())
	m := GatewayModel{Model: "synthetic-native", Capabilities: []string{"vision"}, MaxOutputTokens: 1000, VideoSize: "1280x720", VideoSeconds: 4}
	urls := []string{}
	download := func(_ context.Context, url string, w io.Writer) error {
		urls = append(urls, url)
		_, err := w.Write([]byte("synthetic-media"))
		return err
	}
	p := newBailianRuntimeProviders(api, m, RuntimeSelection{Capability: "speech"}, download, download, download).Creative.(runtimeCreative).p.(*bailianCreativeProvider)
	if _, err := p.Speech(context.Background(), "synthetic speech"); err != nil {
		t.Fatal(err)
	}
	job, err := p.SubmitVideo(context.Background(), "synthetic video prompt")
	if err != nil || job.Status != "queued" {
		t.Fatal(job, err)
	}
	if job, err = p.PollVideo(context.Background(), job.ID); err != nil || job.Status != "completed" {
		t.Fatal(job, err)
	}
	if _, err := p.DownloadVideo(context.Background(), job.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := p.DescribeImage(context.Background(), "describe", syntheticPNG(0), "image/png"); err != nil {
		t.Fatal(err)
	}
	asr := bailianAlignmentProvider{wire: p.wire, plain: true}
	if out, err := asr.Transcribe(context.Background(), []byte("synthetic"), "audio/wav", 1); err != nil || out.Transcript != "native transcript" {
		t.Fatal(out, err)
	}
	before := len(paths)
	asr.plain = false
	if _, err := asr.Transcribe(context.Background(), []byte("synthetic"), "audio/wav", 1); err == nil {
		t.Fatal("invented timestamps")
	}
	if _, err := p.SubmitVideoWithImage(context.Background(), "test", nil, "image/png"); err == nil {
		t.Fatal("reference video fallback")
	}
	if len(paths) != before || len(urls) != 2 {
		t.Fatal("unexpected fallback request", paths, urls)
	}
}

func TestBailianErrorsNeverRetryOrFallback(t *testing.T) {
	for _, status := range []int{400, 401, 403, 404, 413, 422, 429, 500} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				io.Copy(io.Discard, r.Body)
				w.WriteHeader(status)
				fmt.Fprint(w, `{"message":"secret upstream details"}`)
			}))
			defer srv.Close()
			p := bailianAPI{api: newCreativeProvider("synthetic", srv.URL, srv.Client())}
			_, err := p.request(context.Background(), http.MethodPost, bailianMultimodal, map[string]string{"model": "test"}, 1024)
			var failure *ProviderError
			if !errors.As(err, &failure) || strings.Contains(err.Error(), "secret") || calls != 1 {
				t.Fatal(err, calls)
			}
		})
	}
}

func TestBailianTextPathGrantDoesNotBroadenOrigin(t *testing.T) {
	origin, _ := url.Parse("https://synthetic.invalid/api/v1")
	hits := 0
	transport := gatewayObservedTransport{origin: origin, extraPaths: map[string]bool{"/compatible-mode/v1/chat/completions": true}, base: runtimeFixtureTransport(func(r *http.Request) (*http.Response, error) {
		hits++
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{}`)), Header: make(http.Header)}, nil
	})}
	for _, target := range []string{"https://synthetic.invalid/compatible-mode/v1/chat/completions", "https://synthetic.invalid/api/v1/tasks/synthetic"} {
		req, _ := http.NewRequest(http.MethodPost, target, nil)
		res, err := transport.RoundTrip(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
	}
	for _, target := range []string{"https://other.invalid/compatible-mode/v1/chat/completions", "http://synthetic.invalid/compatible-mode/v1/chat/completions", "https://synthetic.invalid/compatible-mode/v1/models", "https://synthetic.invalid/compatible-mode/v1/chat/completions/extra", "https://synthetic.invalid/admin"} {
		req, _ := http.NewRequest(http.MethodPost, target, nil)
		if _, err := transport.RoundTrip(req); err == nil {
			t.Fatal("grant escaped exact text path", target)
		}
	}
	if hits != 2 {
		t.Fatal("denied request reached network", hits)
	}
}
