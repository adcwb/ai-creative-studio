package application

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// These are actual local HTTP contracts, not supplier access or quality claims.
func TestVerticalImageAdaptersKeepRequestedAspectAndActualPixels(t *testing.T) {
	var output bytes.Buffer
	if err := png.Encode(&output, image.NewRGBA(image.Rect(0, 0, 72, 128))); err != nil {
		t.Fatal(err)
	}
	encoded := base64.StdEncoding.EncodeToString(output.Bytes())
	reference := syntheticCreativePNG()
	for _, kind := range []string{"gateway", "gateway-edit", "gemini", "minimax"} {
		t.Run(kind, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != http.MethodPost {
					t.Error("unexpected method")
				}
				if kind == "gateway-edit" {
					if err := r.ParseMultipartForm(1 << 20); err != nil {
						t.Error(err)
						http.Error(w, "invalid request", 400)
						return
					}
					defer r.MultipartForm.RemoveAll()
					if r.FormValue("size") != "864x1536" || r.URL.Path != "/images/edits" {
						t.Error("vertical edit dimensions lost")
					}
					f, _, err := r.FormFile("image[]")
					if err != nil {
						t.Error(err)
						http.Error(w, "missing reference", 400)
						return
					}
					got, err := io.ReadAll(f)
					f.Close()
					if err != nil || sha256.Sum256(got) != sha256.Sum256(reference) {
						t.Error("reference changed", err)
					}
				} else {
					var body map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
						http.Error(w, "invalid request", 400)
						return
					}
					switch kind {
					case "gateway":
						if body["size"] != "864x1536" || r.URL.Path != "/images/generations" {
							t.Error("vertical generation dimensions lost")
						}
					case "gemini":
						cfg, _ := body["generationConfig"].(map[string]any)
						imageCfg, _ := cfg["imageConfig"].(map[string]any)
						if imageCfg["aspectRatio"] != "9:16" || r.Header.Get("x-goog-api-key") != "synthetic" {
							t.Error("native Gemini aspect/auth lost")
						}
					case "minimax":
						if body["aspect_ratio"] != "9:16" || body["width"] != nil || body["height"] != nil {
							t.Error("native MiniMax aspect silently changed")
						}
					}
				}
				if kind != "gemini" && r.Header.Get("Authorization") != "Bearer synthetic" {
					t.Error("authentication lost")
				}
				var response any = map[string]any{"data": []any{map[string]string{"b64_json": encoded}}}
				if kind == "gemini" {
					response = map[string]any{"candidates": []any{map[string]any{"finishReason": "STOP", "content": map[string]any{"parts": []any{map[string]any{"inlineData": map[string]string{"mimeType": "image/png", "data": encoded}}}}}}}
				}
				if kind == "minimax" {
					response = map[string]any{"base_resp": map[string]int{"status_code": 0}, "data": map[string]any{"image_base64": []string{encoded}}}
				}
				_ = json.NewEncoder(w).Encode(response)
			}))
			defer server.Close()
			in := Input{Kind: "general", Prompt: "Synthetic vertical dialogue shot", Ratio: "9:16", Style: "natural"}
			var result GenerationResult
			var err error
			switch kind {
			case "gateway", "gateway-edit":
				p := &gatewayImageProvider{api: newCreativeProvider("synthetic", server.URL, server.Client()), model: GatewayModel{Model: "synthetic-vertical", Capabilities: []string{"image", "image_edit"}}}
				if kind == "gateway-edit" {
					result, err = p.GenerateWithReference(context.Background(), in, reference, "image/png")
				} else {
					result, err = p.Generate(context.Background(), in)
				}
			case "gemini":
				result, err = newNativeImageProvider("gemini-images", "synthetic", "synthetic-image", server.URL, server.Client()).Generate(context.Background(), in)
			case "minimax":
				result, err = newMiniMaxImageProvider("synthetic", "image-01", server.URL, server.Client()).Generate(context.Background(), in)
			}
			if err != nil || result.Width != 72 || result.Height != 128 || result.MIMEType != "image/png" || calls != 1 {
				t.Fatalf("actual output was replaced or aspect lost: %dx%d %s calls=%d err=%v", result.Width, result.Height, result.MIMEType, calls, err)
			}
		})
	}
}
