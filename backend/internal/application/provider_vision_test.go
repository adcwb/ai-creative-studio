package application

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func visionTestProvider(kind, url string, client *http.Client) VisionProvider {
	switch kind {
	case "openai-text":
		p := newCreativeProvider("synthetic-key", url, client)
		p.SetModels("vision-test", "", "")
		return p
	case "ark-text":
		p := newCreativeProvider("synthetic-key", url, client)
		p.SetModels("vision-test", "", "")
		return &arkTextProvider{OpenAICreativeProvider: p}
	default:
		return newNativeTextProvider(kind, "synthetic-key", "vision-test", url, client)
	}
}
func visionTestResponse(kind, content string) any {
	switch kind {
	case "anthropic-text":
		return map[string]any{"stop_reason": "end_turn", "content": []map[string]string{{"type": "text", "text": content}}}
	case "gemini-text":
		return map[string]any{"candidates": []map[string]any{{"finishReason": "STOP", "content": map[string]any{"parts": []map[string]any{{"thought": true, "text": "not user-facing reasoning"}, {"text": content}}}}}}
	default:
		return map[string]any{"choices": []map[string]any{{"finish_reason": "stop", "message": map[string]string{"content": content}}}}
	}
}

const visionValidJSON = `{"summary":"Synthetic image analysis","questions":["What tone is intended?"],"suggestions":["Use a wide establishing shot."]}`

func TestVisionFiveNativeProtocols(t *testing.T) {
	fixture := nativeTestImage(t, "png")
	base64Image := base64.StdEncoding.EncodeToString(fixture)
	for _, kind := range []string{"openai-text", "ark-text", "anthropic-text", "gemini-text", "minimax-text"} {
		t.Run(kind, func(t *testing.T) {
			var count atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				count.Add(1)
				if r.Method != "POST" || r.URL.RawQuery != "" {
					t.Error("incorrect method/auth in URL")
				}
				var body map[string]any
				if json.NewDecoder(r.Body).Decode(&body) != nil {
					t.Error("bad JSON")
				}
				switch kind {
				case "anthropic-text":
					if r.URL.Path != "/v1/messages" || r.Header.Get("x-api-key") != "synthetic-key" || r.Header.Get("anthropic-version") != "2023-06-01" {
						t.Error("wrong Anthropic protocol")
					}
					image := body["messages"].([]any)[0].(map[string]any)["content"].([]any)[0].(map[string]any)
					source := image["source"].(map[string]any)
					if image["type"] != "image" || source["type"] != "base64" || source["data"] != base64Image || source["media_type"] != "image/png" {
						t.Error("wrong native image block")
					}
				case "gemini-text":
					if r.URL.Path != "/v1beta/models/vision-test:generateContent" || r.Header.Get("x-goog-api-key") != "synthetic-key" {
						t.Error("wrong Gemini protocol")
					}
					part := body["contents"].([]any)[0].(map[string]any)["parts"].([]any)[0].(map[string]any)
					inline := part["inlineData"].(map[string]any)
					if inline["mimeType"] != "image/png" || inline["data"] != base64Image {
						t.Error("wrong inlineData")
					}
				default:
					expected := "/chat/completions"
					if kind == "minimax-text" {
						expected = "/v1/chat/completions"
					}
					if r.URL.Path != expected || r.Header.Get("Authorization") != "Bearer synthetic-key" {
						t.Error("wrong compatible endpoint/auth")
					}
					content := body["messages"].([]any)[0].(map[string]any)["content"].([]any)
					image := content[1].(map[string]any)
					if image["type"] != "image_url" || image["image_url"].(map[string]any)["url"] != "data:image/png;base64,"+base64Image {
						t.Error("not inline data URI")
					}
					if kind == "minimax-text" {
						if body["reasoning_split"] != true {
							t.Error("reasoning not separated")
						}
						if _, ok := body["response_format"]; ok {
							t.Error("undocumented MiniMax response format")
						}
					}
				}
				_ = json.NewEncoder(w).Encode(visionTestResponse(kind, visionValidJSON))
			}))
			defer server.Close()
			p := visionTestProvider(kind, server.URL, server.Client())
			a, err := p.DescribeImage(context.Background(), "Describe this fictional setting", fixture, "image/png")
			if err != nil || a.Provider != kind || a.SourceVersion != 0 || a.Summary != "Synthetic image analysis" || count.Load() != 1 {
				t.Fatal(a, err)
			}
		})
	}
}
func TestVisionRejectsInputBeforeNetwork(t *testing.T) {
	var count atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { count.Add(1) }))
	defer server.Close()
	p := visionTestProvider("openai-text", server.URL, server.Client())
	png := nativeTestImage(t, "png")
	cases := []struct {
		prompt string
		data   []byte
		mime   string
	}{{"ab", png, "image/png"}, {strings.Repeat("长", 2001), png, "image/png"}, {"describe image", png, "image/jpeg"}, {"describe image", png[:len(png)/2], "image/png"}, {"describe image", []byte("https://external.invalid/image.png"), "image/png"}, {"describe image", make([]byte, (4<<20)+1), "image/png"}, {"describe image", png, "image/svg+xml"}}
	for i, v := range cases {
		if _, err := p.DescribeImage(context.Background(), v.prompt, v.data, v.mime); err == nil {
			t.Fatal("invalid accepted", i)
		}
	}
	bad := newNativeTextProvider("gemini-text", "synthetic", "../../model", server.URL, server.Client())
	if _, err := bad.DescribeImage(context.Background(), "describe image", png, "image/png"); err == nil {
		t.Fatal("model injection accepted")
	}
	if count.Load() != 0 {
		t.Fatal("invalid input reached network")
	}
}
func TestVisionOutputMetadataAndMalformedResults(t *testing.T) {
	png := nativeTestImage(t, "png")
	cases := []string{`{"summary":"valid summary","questions":[],"suggestions":[],"source_version":999}`, `{"summary":"valid summary","questions":[],"suggestions":[],"provider":"forged"}`, `{"summary":"valid summary","questions":null,"suggestions":[]}`, `{"summary":"valid summary","questions":[null],"suggestions":[]}`, `{"summary":"valid summary","questions":[],"suggestions":[],"unexpected":"value"}`, "```json\n" + visionValidJSON + "\n```", visionValidJSON + visionValidJSON, `{"summary":"x","questions":[],"suggestions":[]}`}
	for _, content := range cases {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(visionTestResponse("openai-text", content))
		}))
		p := visionTestProvider("openai-text", server.URL, server.Client())
		_, err := p.DescribeImage(context.Background(), "describe image", png, "image/png")
		server.Close()
		if err == nil {
			t.Fatal("invalid/forged result accepted", content)
		}
	}
}
func TestVisionNormalizedPromptAndTimeout(t *testing.T) {
	png := nativeTestImage(t, "png")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if len(b) > 8192 {
			t.Error("whitespace prompt bypassed request normalization")
		}
		_ = json.NewEncoder(w).Encode(visionTestResponse("openai-text", visionValidJSON))
	}))
	defer server.Close()
	p := visionTestProvider("openai-text", server.URL, server.Client())
	if _, err := p.DescribeImage(context.Background(), strings.Repeat(" ", 1<<20)+"describe image ", png, "image/png"); err != nil {
		t.Fatal(err)
	}
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { time.Sleep(40 * time.Millisecond) }))
	defer slow.Close()
	p = visionTestProvider("openai-text", slow.URL, &http.Client{Timeout: 5 * time.Millisecond})
	_, err := p.DescribeImage(context.Background(), "describe image", png, "image/png")
	var safe *ProviderError
	if !errors.As(err, &safe) || safe.Code != "provider_timeout" {
		t.Fatal("timeout not classified", err)
	}
}
func TestVisionRedirectCancelErrorAndBoundedOutput(t *testing.T) {
	png := nativeTestImage(t, "png")
	var reached atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached.Add(1) }))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 307) }))
	defer server.Close()
	p := visionTestProvider("openai-text", server.URL, server.Client())
	if _, err := p.DescribeImage(context.Background(), "describe image", png, "image/png"); err == nil || reached.Load() != 0 {
		t.Fatal("redirect followed")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.DescribeImage(ctx, "describe image", png, "image/png"); err == nil {
		t.Fatal("cancel accepted")
	}
	for _, code := range []int{401, 403, 429, 500, 200} {
		var calls atomic.Int32
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			if code == 200 {
				_, _ = w.Write([]byte(strings.Repeat("x", (256<<10)+1)))
				return
			}
			w.WriteHeader(code)
			_, _ = w.Write([]byte("secret private upstream detail"))
		}))
		p = visionTestProvider("openai-text", s.URL, s.Client())
		_, err := p.DescribeImage(context.Background(), "describe image", png, "image/png")
		s.Close()
		var safe *ProviderError
		if !errors.As(err, &safe) || strings.Contains(err.Error(), "secret") || calls.Load() != 1 {
			t.Fatal("unsafe error/retry", err)
		}
		if (code == 401 || code == 403) && safe.Code != "provider_auth_failed" {
			t.Fatal("auth not classified", err)
		}
	}
}

func TestVisionRejectsIncompleteAndRefusalAcrossProviders(t *testing.T) {
	image := nativeTestImage(t, "jpeg")
	for _, kind := range []string{"openai-text", "ark-text", "anthropic-text", "gemini-text", "minimax-text"} {
		t.Run(kind, func(t *testing.T) {
			fixture := visionTestResponse(kind, visionValidJSON).(map[string]any)
			switch kind {
			case "anthropic-text":
				fixture["stop_reason"] = "max_tokens"
			case "gemini-text":
				fixture["candidates"].([]map[string]any)[0]["finishReason"] = "SAFETY"
			default:
				choice := fixture["choices"].([]map[string]any)[0]
				choice["message"].(map[string]string)["refusal"] = "refused"
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _ = json.NewEncoder(w).Encode(fixture) }))
			defer server.Close()
			p := visionTestProvider(kind, server.URL, server.Client())
			if _, err := p.DescribeImage(context.Background(), "describe synthetic image", image, "image/jpeg"); err == nil {
				t.Fatal("incomplete/refusal accepted")
			}
		})
	}
}
