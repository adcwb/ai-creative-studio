package application

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestGoogleInteractionsImageAndReference(t *testing.T) {
	fixture := nativeTestImage(t, "png")
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/v1beta/interactions" || r.Method != "POST" || r.Header.Get("x-goog-api-key") != "synthetic" {
			t.Error("wrong protocol")
		}
		var body map[string]any
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			t.Error("invalid request")
		}
		format := body["response_format"].(map[string]any)
		if format["type"] != "image" || format["image_size"] != "1K" || format["aspect_ratio"] != "16:9" || format["mime_type"] != "image/png" {
			t.Error("wrong format")
		}
		input := body["input"].([]any)
		if len(input) != 2 || input[1].(map[string]any)["data"] != base64.StdEncoding.EncodeToString(fixture) {
			t.Error("missing inline reference")
		}
		_ = json.NewEncoder(w).Encode(googleTestOutput("image", "image/png", base64.StdEncoding.EncodeToString(fixture), ""))
	}))
	defer server.Close()
	p := &GoogleInteractionsImageProvider{api: newGoogleMediaProvider("gemini-interactions-images", "synthetic", "image-model", "", server.URL, server.Client(), nil)}
	r, err := p.GenerateWithReference(context.Background(), Input{Prompt: "synthetic portrait", Kind: "portrait", Ratio: "16:9"}, fixture, "image/png")
	if err != nil || r.Width != 64 || r.Height != 48 || r.MIMEType != "image/png" || calls.Load() != 1 {
		t.Fatal(r, err)
	}
}
func TestGoogleInteractionsImageRejectsURLMultipleAndInvalidReference(t *testing.T) {
	image := nativeTestImage(t, "png")
	valid := googleTestOutput("image", "image/png", base64.StdEncoding.EncodeToString(image), "").(map[string]any)
	multi := googleTestOutput("image", "image/png", base64.StdEncoding.EncodeToString(image), "").(map[string]any)
	steps := multi["steps"].([]map[string]any)
	blocks := steps[1]["content"].([]map[string]string)
	steps[1]["content"] = append(blocks, blocks[0])
	for _, fixture := range []any{googleTestOutput("image", "image/png", "", "https://evil.invalid/image.png"), multi, map[string]any{"status": "completed", "output_image": map[string]string{"data": "not-raw-rest"}}} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _ = json.NewEncoder(w).Encode(fixture) }))
		p := &GoogleInteractionsImageProvider{api: newGoogleMediaProvider("gemini-interactions-images", "synthetic", "image-model", "", server.URL, server.Client(), nil)}
		if _, err := p.Generate(context.Background(), Input{Prompt: "synthetic portrait", Kind: "portrait"}); err == nil {
			t.Fatal("unsafe response accepted")
		}
		server.Close()
	}
	var count atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { count.Add(1); _ = json.NewEncoder(w).Encode(valid) }))
	defer server.Close()
	p := &GoogleInteractionsImageProvider{api: newGoogleMediaProvider("gemini-interactions-images", "synthetic", "image-model", "", server.URL, server.Client(), nil)}
	if _, err := p.GenerateWithReference(context.Background(), Input{Prompt: "synthetic portrait", Kind: "portrait"}, []byte("not image"), "image/png"); err == nil || count.Load() != 0 {
		t.Fatal("invalid reference sent")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.Generate(ctx, Input{Prompt: "synthetic portrait", Kind: "portrait"}); err == nil || count.Load() != 0 {
		t.Fatal("cancelled request sent")
	}
}
