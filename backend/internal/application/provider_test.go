package application

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const syntheticAPIKey = "synthetic-test-key-not-a-real-credential"

func pngFixture(t *testing.T, width, height int) []byte {
	t.Helper()
	canvas := image.NewRGBA(image.Rect(0, 0, width, height))
	draw.Draw(canvas, canvas.Bounds(), &image.Uniform{C: color.RGBA{R: 120, G: 162, B: 143, A: 255}}, image.Point{}, draw.Src)
	draw.Draw(canvas, image.Rect(width/4, height/4, width*3/4, height*3/4), &image.Uniform{C: color.RGBA{R: 234, G: 213, B: 171, A: 255}}, image.Point{}, draw.Src)
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, canvas); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func imageResponse(data []byte) []byte {
	encoded, _ := json.Marshal(map[string]any{"data": []map[string]string{{"b64_json": base64.StdEncoding.EncodeToString(data)}}})
	return encoded
}

func realCapabilities() Capabilities {
	return Capabilities{Provider: "openai-images", Mode: "real", Ready: true, Model: OpenAIModel, Message: "synthetic local test configuration"}
}

func TestProviderConfigurationIsSafeAndDoesNotProbeUpstream(t *testing.T) {
	for _, tc := range []struct {
		name, key, provider, reason string
		ready                       bool
	}{
		{"", "", "mock-svg", "", true},
		{"mock-svg", syntheticAPIKey, "mock-svg", "", true},
		{"openai-images", syntheticAPIKey, "openai-images", "", true},
		{"openai-images", "", "openai-images", "missing_api_key", false},
		{"openai-images", "  ", "openai-images", "missing_api_key", false},
		{"openai-images", "synthetic\nkey", "openai-images", "invalid_configuration", false},
		{"openai-images", "synthetic key", "openai-images", "invalid_configuration", false},
		{"unrecognized-" + syntheticAPIKey, syntheticAPIKey, "unavailable", "invalid_provider", false},
	} {
		provider, caps := ConfigureProvider(tc.name, tc.key)
		if provider == nil || caps.Ready != tc.ready || caps.Provider != tc.provider || caps.ReasonCode != tc.reason {
			t.Fatalf("configuration %q: %#v", tc.name, caps)
		}
		encoded, _ := json.Marshal(caps)
		if bytes.Contains(encoded, []byte(syntheticAPIKey)) || strings.Contains(caps.Message, "synthetic") {
			t.Fatal("configuration response exposed a supplied setting or credential")
		}
	}
	production := NewOpenAIProvider(syntheticAPIKey)
	if production.endpoint != "https://api.openai.com/v1/images/generations" || production.client.Timeout != 120*time.Second {
		t.Fatal("production endpoint/timeout differ from the frozen configuration")
	}
	transport, ok := production.client.Transport.(*http.Transport)
	if !ok || transport.Proxy != nil {
		t.Fatal("production provider must not inherit environment proxy settings")
	}
}

func TestOpenAIRequestContractThroughEino(t *testing.T) {
	fixtures := map[string][]byte{
		"1024x1024": pngFixture(t, 1024, 1024),
		"1024x1280": pngFixture(t, 1024, 1280),
		"1536x864":  pngFixture(t, 1536, 864),
	}
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != "POST" || r.URL.Path != "/v1/images/generations" || r.Header.Get("Authorization") != "Bearer "+syntheticAPIKey || r.Header.Get("Content-Type") != "application/json" {
			t.Error("upstream request does not match the official endpoint/auth contract")
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if len(body) != 7 || body["model"] != OpenAIModel || body["n"] != float64(1) || body["quality"] != "low" || body["output_format"] != "png" || body["background"] != "opaque" || body["response_format"] != nil || body["style"] != nil {
			t.Errorf("unexpected official request fields: %#v", body)
		}
		prompt, _ := body["prompt"].(string)
		if !strings.Contains(prompt, "synthetic normalized brief") || !strings.Contains(prompt, "Visual style:") || !strings.Contains(prompt, "Template:") || strings.Contains(prompt, "  synthetic") {
			t.Errorf("Eino-normalized input/style/kind were not sent: %q", prompt)
		}
		data, ok := fixtures[body["size"].(string)]
		if !ok {
			t.Error("unexpected requested pixel size")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(imageResponse(data))
	}))
	defer server.Close()
	provider := newOpenAIProvider(syntheticAPIKey, server.URL+"/v1/images/generations", server.Client())
	pipeline, err := NewPipeline(context.Background(), provider, realCapabilities())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		ratio, kind, style, size string
		width, height            int
	}{
		{"1:1", "general", "natural", "1024x1024", 1024, 1024},
		{"4:5", "portrait", "studio", "1024x1280", 1024, 1280},
		{"16:9", "poster", "illustration", "1536x864", 1536, 864},
	} {
		result, err := pipeline.Invoke(context.Background(), Input{Prompt: "  synthetic normalized brief  ", Kind: tc.kind, Ratio: tc.ratio, Style: tc.style})
		if err != nil || result.MIMEType != "image/png" || result.Width != tc.width || result.Height != tc.height || !bytes.Equal(result.Data, fixtures[tc.size]) {
			t.Fatalf("real adapter result for %s: MIME=%s %dx%d error=%v", tc.ratio, result.MIMEType, result.Width, result.Height, err)
		}
	}
	if _, err := pipeline.Invoke(context.Background(), Input{Prompt: "invalid ratio", Kind: "general", Ratio: "3:4"}); err == nil {
		t.Fatal("invalid input reached the real provider pipeline")
	}
	if calls.Load() != 3 {
		t.Fatalf("unexpected provider calls or automatic retries: %d", calls.Load())
	}
}

func TestOpenAIProviderRejectsUnsafeResponsesWithoutURLFetches(t *testing.T) {
	valid := pngFixture(t, 1024, 1024)
	oversizedDimensions := append([]byte(nil), valid...)
	binary.BigEndian.PutUint32(oversizedDimensions[16:20], 8192)
	binary.BigEndian.PutUint32(oversizedDimensions[20:24], 8192)
	binary.BigEndian.PutUint32(oversizedDimensions[29:33], crc32.ChecksumIEEE(oversizedDimensions[12:29]))
	var fetched atomic.Int32
	trap := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { fetched.Add(1); _, _ = w.Write(valid) }))
	defer trap.Close()
	for _, tc := range []struct {
		name string
		body func() []byte
	}{
		{"malformed-json", func() []byte { return []byte(`{"data":`) }},
		{"missing-data", func() []byte { return []byte(`{"data":[]}`) }},
		{"url-only", func() []byte {
			b, _ := json.Marshal(map[string]any{"data": []map[string]string{{"url": trap.URL}}})
			return b
		}},
		{"multiple-images", func() []byte { return []byte(`{"data":[{"b64_json":"AAAA"},{"b64_json":"AAAA"}]}`) }},
		{"invalid-base64", func() []byte { return []byte(`{"data":[{"b64_json":"not-base64!"}]}`) }},
		{"svg-disguised-as-png", func() []byte { return imageResponse([]byte(`<svg><script>synthetic</script></svg>`)) }},
		{"truncated-png", func() []byte { return imageResponse(valid[:len(valid)/2]) }},
		{"wrong-requested-size", func() []byte { return imageResponse(pngFixture(t, 1024, 1280)) }},
		{"pixel-bomb-header", func() []byte { return imageResponse(oversizedDimensions) }},
		{"png-byte-limit", func() []byte { return imageResponse(bytes.Repeat([]byte{'x'}, (16<<20)+1)) }},
		{"json-byte-limit", func() []byte { return append(bytes.Repeat([]byte{' '}, 24<<20), imageResponse(valid)...) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := tc.body()
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write(body)
			}))
			defer server.Close()
			provider := newOpenAIProvider(syntheticAPIKey, server.URL, server.Client())
			_, err := provider.Generate(context.Background(), Input{Prompt: "synthetic", Kind: "general", Ratio: "1:1", Style: "natural"})
			var failure *ProviderError
			if !errors.As(err, &failure) || failure.Code != "invalid_provider_response" {
				t.Fatalf("unsafe response accepted or wrong classification: %v", err)
			}
			if calls.Load() != 1 {
				t.Fatal("unsafe response was automatically retried")
			}
		})
	}
	if fetched.Load() != 0 {
		t.Fatal("provider fetched an upstream-supplied URL")
	}
}

func TestOpenAIFailuresAreSanitizedAndNeverAutomaticallyRetried(t *testing.T) {
	for _, tc := range []struct {
		status int
		code   string
	}{{401, "provider_auth_failed"}, {403, "provider_auth_failed"}, {429, "provider_rate_limited"}, {400, "provider_rejected"}, {500, "provider_error"}, {503, "provider_error"}} {
		var calls atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			calls.Add(1)
			w.WriteHeader(tc.status)
			_, _ = w.Write([]byte(`{"error":{"message":"` + syntheticAPIKey + ` upstream-private-body"}}`))
		}))
		provider := newOpenAIProvider(syntheticAPIKey, server.URL, server.Client())
		_, err := provider.Generate(context.Background(), Input{Prompt: "synthetic", Kind: "general", Ratio: "1:1"})
		server.Close()
		var failure *ProviderError
		if !errors.As(err, &failure) || failure.Code != tc.code || strings.Contains(err.Error(), syntheticAPIKey) || strings.Contains(err.Error(), "upstream-private-body") || calls.Load() != 1 {
			t.Fatalf("status %d leaked, retried, or misclassified: %v calls=%d", tc.status, err, calls.Load())
		}
	}
	var calls, redirected atomic.Int32
	redirectTarget := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { redirected.Add(1); w.WriteHeader(200) }))
	defer redirectTarget.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Redirect(w, r, redirectTarget.URL, http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	provider := newOpenAIProvider(syntheticAPIKey, redirect.URL, redirect.Client())
	if _, err := provider.Generate(context.Background(), Input{Prompt: "synthetic", Kind: "general", Ratio: "1:1"}); err == nil || redirected.Load() != 0 || calls.Load() != 1 {
		t.Fatal("provider followed a redirect or retried it")
	}
	release := make(chan struct{})
	timeout := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer timeout.Close()
	client := timeout.Client()
	client.Timeout = 25 * time.Millisecond
	provider = newOpenAIProvider(syntheticAPIKey, timeout.URL, client)
	_, err := provider.Generate(context.Background(), Input{Prompt: "synthetic", Kind: "general", Ratio: "1:1"})
	close(release)
	var failure *ProviderError
	if !errors.As(err, &failure) || failure.Code != "provider_timeout" {
		t.Fatalf("timeout not classified safely: %v", err)
	}
}
