package studio

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"image/jpeg"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func nativeTestImage(t *testing.T, format string) []byte {
	t.Helper()
	im := image.NewRGBA(image.Rect(0, 0, 64, 48))
	var b bytes.Buffer
	var err error
	if format == "jpeg" {
		err = jpeg.Encode(&b, im, nil)
	} else {
		err = png.Encode(&b, im)
	}
	if err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
func nativeTestResponse(kind string, b []byte) any {
	v := base64.StdEncoding.EncodeToString(b)
	if kind == "ark-images" {
		return map[string]any{"data": []map[string]string{{"b64_json": v}}}
	}
	return map[string]any{"candidates": []map[string]any{{"finishReason": "STOP", "content": map[string]any{"parts": []map[string]any{{"text": "synthetic fixture"}, {"inlineData": map[string]string{"mimeType": "image/png", "data": v}}}}}}}
}
func TestNativeImageProtocolsAndReference(t *testing.T) {
	for _, kind := range []string{"gemini-images", "ark-images"} {
		t.Run(kind, func(t *testing.T) {
			fixture := nativeTestImage(t, "png")
			var calls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				var in map[string]any
				if r.Method != "POST" || json.NewDecoder(r.Body).Decode(&in) != nil {
					t.Error("invalid request")
				}
				if kind == "gemini-images" {
					if r.URL.Path != "/v1beta/models/test-model:generateContent" || r.Header.Get("x-goog-api-key") != "synthetic-key" || r.URL.RawQuery != "" {
						t.Error("invalid target/auth")
					}
					parts := in["contents"].([]any)[0].(map[string]any)["parts"].([]any)
					if len(parts) != 2 || parts[1].(map[string]any)["inlineData"].(map[string]any)["data"] != base64.StdEncoding.EncodeToString(fixture) {
						t.Error("reference not inline")
					}
					cfg := in["generationConfig"].(map[string]any)
					if cfg["imageConfig"].(map[string]any)["aspectRatio"] != "16:9" {
						t.Error("ratio missing")
					}
				} else {
					if r.URL.Path != "/api/v3/images/generations" || r.Header.Get("Authorization") != "Bearer synthetic-key" {
						t.Error("invalid Ark target/auth")
					}
					if in["response_format"] != "b64_json" || in["size"] != "2K" || in["image"] != "data:image/png;base64,"+base64.StdEncoding.EncodeToString(fixture) || in["watermark"] != true {
						t.Error("invalid Ark request")
					}
				}
				_ = json.NewEncoder(w).Encode(nativeTestResponse(kind, fixture))
			}))
			defer upstream.Close()
			p := newNativeImageProvider(kind, "synthetic-key", "test-model", upstream.URL, upstream.Client())
			result, err := p.GenerateWithReference(context.Background(), Input{Prompt: "A synthetic scene", Kind: "general", Ratio: "16:9", Style: "natural"}, fixture, "image/png")
			if err != nil || result.MIMEType != "image/png" || result.Width != 64 || result.Height != 48 {
				t.Fatalf("result %+v %v", result, err)
			}
			if calls.Load() != 1 {
				t.Fatal("retried")
			}
		})
	}
}
func TestNativeImageRejectsUnsafeOutputsAndRedirect(t *testing.T) {
	fixtures := []any{map[string]any{"data": []map[string]string{{"url": "http://127.0.0.1/private"}}}, map[string]any{"data": []map[string]string{{"b64_json": "invalid base64!"}}}, map[string]any{"data": []map[string]string{{"b64_json": base64.StdEncoding.EncodeToString([]byte("not image"))}}}}
	for i, out := range fixtures {
		var calls atomic.Int32
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); _ = json.NewEncoder(w).Encode(out) }))
		p := newNativeImageProvider("ark-images", "synthetic", "model", upstream.URL, upstream.Client())
		_, err := p.Generate(context.Background(), Input{Prompt: "synthetic image", Kind: "general"})
		upstream.Close()
		var safe *ProviderError
		if !errors.As(err, &safe) || safe.Code != "invalid_provider_response" || calls.Load() != 1 {
			t.Fatalf("case %d: %v", i, err)
		}
	}
	var downloaded atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { downloaded.Add(1) }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 307) }))
	defer redirect.Close()
	p := newNativeImageProvider("ark-images", "synthetic", "model", redirect.URL, redirect.Client())
	if _, err := p.Generate(context.Background(), Input{Prompt: "synthetic image", Kind: "general"}); err == nil || downloaded.Load() != 0 {
		t.Fatal("redirect followed")
	}
}
func TestNativeImageConfigurationInputAndErrors(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(429)
		_, _ = w.Write([]byte("secret from upstream"))
	}))
	defer upstream.Close()
	in := Input{Prompt: "synthetic image", Kind: "general"}
	p := newNativeImageProvider("gemini-images", "synthetic", "../bad", upstream.URL, upstream.Client())
	if _, err := p.Generate(context.Background(), in); err == nil || calls.Load() != 0 {
		t.Fatal("unsafe model called")
	}
	p.model = "test-model"
	if _, err := p.GenerateWithReference(context.Background(), in, []byte("fake"), "image/png"); err == nil || calls.Load() != 0 {
		t.Fatal("invalid reference sent")
	}
	_, err := p.Generate(context.Background(), in)
	var safe *ProviderError
	if !errors.As(err, &safe) || safe.Code != "provider_rate_limited" || strings.Contains(err.Error(), "secret") || calls.Load() != 1 {
		t.Fatal("unsafe error or retry", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = p.Generate(ctx, in)
	if err == nil || calls.Load() != 1 {
		t.Fatal("cancelled request sent")
	}
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { time.Sleep(60 * time.Millisecond) }))
	defer slow.Close()
	p = newNativeImageProvider("ark-images", "synthetic", "test", slow.URL, &http.Client{Timeout: 10 * time.Millisecond})
	_, err = p.Generate(context.Background(), in)
	if !errors.As(err, &safe) || safe.Code != "provider_timeout" {
		t.Fatal("timeout", err)
	}
}
func TestNativeImageJPEGNormalizationAndMIME(t *testing.T) {
	fixture := nativeTestImage(t, "jpeg")
	r, err := nativeImage(fixture, "image/jpeg")
	if err != nil || r.MIMEType != "image/png" {
		t.Fatal(err)
	}
	if _, err = png.Decode(bytes.NewReader(r.Data)); err != nil {
		t.Fatal(err)
	}
	if _, err = nativeImage(fixture, "image/png"); err == nil {
		t.Fatal("MIME mismatch accepted")
	}
	for _, p := range []*NativeImageProvider{NewGeminiImageProvider("synthetic", "model"), NewArkImageProvider("synthetic", "model")} {
		if p.client.Transport.(*http.Transport).Proxy != nil || !strings.HasPrefix(p.endpoint, "https://") {
			t.Fatal("unsafe transport")
		}
	}
}
