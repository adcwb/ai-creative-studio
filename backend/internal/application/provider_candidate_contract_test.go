package application

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Synthetic transport contracts are not live access or quality evidence.
func TestGemini38CandidateThinkingContract(t *testing.T) {
	for _, finish := range []string{"STOP", "MAX_TOKENS", "CONTINUATION", "SAFETY"} {
		t.Run(finish, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != "POST" || r.URL.Path != "/v1beta/models/gemini-3.8-flash:generateContent" || r.Header.Get("x-goog-api-key") != "synthetic" {
					t.Error("wrong candidate endpoint/auth")
				}
				var body map[string]json.RawMessage
				if json.NewDecoder(r.Body).Decode(&body) != nil {
					t.Fatal("invalid request")
				}
				var cfg struct {
					Max      int    `json:"maxOutputTokens"`
					Count    int    `json:"candidateCount"`
					MIME     string `json:"responseMimeType"`
					Thinking struct {
						Level   string `json:"thinkingLevel"`
						Include bool   `json:"includeThoughts"`
					} `json:"thinkingConfig"`
				}
				if json.Unmarshal(body["generationConfig"], &cfg) != nil || cfg.Max != 6000 || cfg.Count != 1 || cfg.MIME != "application/json" || cfg.Thinking.Level != "low" || cfg.Thinking.Include {
					t.Error("unbounded or incompatible candidate config")
				}
				if body["tools"] != nil || body["continuationToken"] != nil {
					t.Error("unexpected billable tool/continuation")
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"candidates": []any{map[string]any{"finishReason": finish, "content": map[string]any{"parts": []any{map[string]any{"thought": true, "text": "not output JSON"}, map[string]any{"text": `{"summary":"合成需求分析","questions":[],"suggestions":[]}`}}}}}, "continuationToken": "must-not-replay"})
			}))
			defer server.Close()
			p := newNativeTextProvider("gemini-text", "synthetic", "gemini-3.8-flash", server.URL, server.Client())
			out, err := p.Analyze(context.Background(), ProjectInput{Title: "测试", Brief: "合成简报"})
			if (finish == "STOP") != (err == nil) || (err == nil && out.Summary != "合成需求分析") || calls != 1 {
				t.Fatalf("out=%#v err=%v calls=%d", out, err, calls)
			}
		})
	}
}

func TestOpenAISunburstCandidateImageContracts(t *testing.T) {
	for _, ratio := range []string{"1:1", "16:9", "4:5"} {
		for _, reference := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/reference=%v", ratio, reference), func(t *testing.T) {
				size, width, height := imageSize(ratio)
				if width%16 != 0 || height%16 != 0 || width > 3840 || height > 3840 || width > 3*height || height > 3*width || width*height < 655360 || width*height > 8294400 {
					t.Fatal("invalid official dimensions")
				}
				var imageData bytes.Buffer
				if err := png.Encode(&imageData, image.NewRGBA(image.Rect(0, 0, width, height))); err != nil {
					t.Fatal(err)
				}
				calls := 0
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls++
					fields := map[string]string{}
					if reference {
						if r.URL.Path != "/images/edits" {
							t.Error("wrong edit endpoint")
						}
						if err := r.ParseMultipartForm(18 << 20); err != nil {
							t.Fatal(err)
						}
						defer r.MultipartForm.RemoveAll()
						for _, key := range []string{"model", "size", "quality", "output_format", "background", "n"} {
							fields[key] = r.FormValue(key)
						}
						f, _, err := r.FormFile("image[]")
						if err != nil {
							t.Fatal(err)
						}
						f.Close()
						if r.FormValue("input_fidelity") != "" {
							t.Error("unsupported fidelity override")
						}
					} else {
						if r.URL.Path != "/images/generations" {
							t.Error("wrong generation endpoint")
						}
						var raw map[string]any
						if json.NewDecoder(r.Body).Decode(&raw) != nil {
							t.Fatal("invalid request")
						}
						for key, val := range raw {
							fields[key] = fmt.Sprint(val)
						}
					}
					for key, want := range map[string]string{"model": "gpt-image-2.5-sunburst", "size": size, "quality": "low", "output_format": "png", "background": "opaque", "n": "1"} {
						if fields[key] != want {
							t.Errorf("%s=%q want %q", key, fields[key], want)
						}
					}
					if r.Method != "POST" || r.Header.Get("Authorization") != "Bearer synthetic" {
						t.Error("invalid request method/auth")
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"b64_json": base64.StdEncoding.EncodeToString(imageData.Bytes())}}})
				}))
				defer server.Close()
				p := newOpenAIProvider("synthetic", server.URL+"/images/generations", server.Client())
				p.model = "gpt-image-2.5-sunburst"
				in := Input{Kind: "general", Prompt: "合成候选协议测试", Ratio: ratio, Style: "natural"}
				var out GenerationResult
				var err error
				if reference {
					out, err = p.GenerateWithReference(context.Background(), in, syntheticCreativePNG(), "image/png")
				} else {
					out, err = p.Generate(context.Background(), in)
				}
				if err != nil || out.Width != width || out.Height != height || calls != 1 {
					t.Fatalf("result=%dx%d error=%v calls=%d", out.Width, out.Height, err, calls)
				}
			})
		}
	}
}
