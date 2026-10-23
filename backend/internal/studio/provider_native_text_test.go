package studio

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func nativeEnvelope(kind, payload string) string {
	if kind == "minimax-text" {
		b, _ := json.Marshal(map[string]any{"choices": []map[string]any{{"finish_reason": "stop", "message": map[string]string{"content": payload}}}})
		return string(b)
	}
	if kind == "anthropic-text" {
		b, _ := json.Marshal(map[string]any{"stop_reason": "end_turn", "content": []map[string]string{{"type": "text", "text": payload}}})
		return string(b)
	}
	b, _ := json.Marshal(map[string]any{"candidates": []map[string]any{{"finishReason": "STOP", "content": map[string]any{"parts": []map[string]string{{"text": payload}}}}}})
	return string(b)
}
func nativeTestScript() Script {
	return Script{Title: "合成文本", Logline: "两人找到归途", Characters: []ScriptCharacter{{Name: "甲", Description: "外套"}, {Name: "乙", Description: "帽子"}}, Scenes: []ScriptScene{{Heading: "门口", Action: "见面", Dialogue: []ScriptDialogue{{Character: "甲", Text: "你好"}}}, {Heading: "街角", Action: "同行", Dialogue: []ScriptDialogue{{Character: "乙", Text: "走吧"}}}, {Heading: "家中", Action: "到达", Dialogue: []ScriptDialogue{{Character: "甲", Text: "到了"}}}}}
}
func TestNativeTextProtocols(t *testing.T) {
	for _, kind := range []string{"anthropic-text", "gemini-text"} {
		t.Run(kind, func(t *testing.T) {
			payload := `{"summary":"合成需求分析","questions":[],"suggestions":["补充时长"]}`
			count := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				count++
				if r.Method != "POST" || r.URL.RawQuery != "" {
					t.Error("unexpected request")
				}
				var body map[string]any
				if json.NewDecoder(r.Body).Decode(&body) != nil {
					t.Error("invalid body")
				}
				if kind == "anthropic-text" {
					if r.URL.Path != "/v1/messages" || r.Header.Get("x-api-key") != "synthetic-key" || r.Header.Get("anthropic-version") != "2023-06-01" || body["model"] != "test-model" || body["system"] == nil {
						t.Error("invalid Anthropic protocol")
					}
				} else {
					if r.URL.Path != "/v1beta/models/test-model:generateContent" || r.Header.Get("x-goog-api-key") != "synthetic-key" || body["systemInstruction"] == nil || body["generationConfig"] == nil {
						t.Error("invalid Gemini protocol")
					}
				}
				fmt.Fprint(w, nativeEnvelope(kind, payload))
			}))
			defer server.Close()
			p := newNativeTextProvider(kind, "synthetic-key", "test-model", server.URL, server.Client())
			a, err := p.Analyze(context.Background(), ProjectInput{Title: "测试", Brief: "合成简报"})
			if err != nil || a.Provider != kind || a.Summary != "合成需求分析" {
				t.Fatalf("analysis: %#v %v", a, err)
			}
			data, _ := json.Marshal(nativeTestScript())
			payload = string(data)
			s, err := p.Script(context.Background(), ProjectInput{Title: "测试", Brief: "合成简报"})
			if err != nil || len(s.Scenes) != 3 {
				t.Fatalf("script %v", err)
			}
			payload = `{"shots":[{"id":"shot_1","scene_index":1,"description":"合成画面","shot_size":"wide","camera_movement":"static","duration_seconds":3,"dialogue":""}]}`
			shots, err := p.Storyboard(context.Background(), s)
			if err != nil || len(shots) != 1 {
				t.Fatalf("storyboard %v", err)
			}
			if count != 3 {
				t.Fatalf("requests=%d", count)
			}
			before := count
			if _, err = p.Speech(context.Background(), "合成"); err == nil {
				t.Fatal("speech silently supported")
			}
			if _, err = p.SubmitVideo(context.Background(), "合成"); err == nil {
				t.Fatal("video silently supported")
			}
			if count != before {
				t.Fatal("unsupported capability made request")
			}
		})
	}
}
func TestNativeTextRejectsUnsafeOrInvalidOutput(t *testing.T) {
	for _, kind := range []string{"anthropic-text", "gemini-text", "minimax-text"} {
		for _, payload := range []string{`{"summary":"合成需求分析","questions":[],"suggestions":[],"provider":"forged"}`, `{"summary":"合成需求分析","questions":null,"suggestions":[]}`, `{} trailing`, `{"summary":"合成需求分析","questions":[],"suggestions":[]} {}`} {
			t.Run(kind+payload, func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, nativeEnvelope(kind, payload)) }))
				defer server.Close()
				p := newNativeTextProvider(kind, "synthetic", "model", server.URL, server.Client())
				if _, err := p.Analyze(context.Background(), ProjectInput{Title: "标题", Brief: "合成简报"}); err == nil {
					t.Fatal("accepted invalid output")
				}
			})
		}
	}
}
func TestNativeTextNoRetriesRedirectsOrLeakedErrors(t *testing.T) {
	for _, status := range []int{302, 401, 429, 500} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var count atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				count.Add(1)
				w.Header().Set("Location", "/leak")
				w.WriteHeader(status)
				fmt.Fprint(w, "secret-provider-body")
			}))
			defer server.Close()
			p := newNativeTextProvider("anthropic-text", "synthetic", "model", server.URL, server.Client())
			_, err := p.Analyze(context.Background(), ProjectInput{Title: "标题", Brief: "合成简报"})
			if err == nil || strings.Contains(err.Error(), "secret-provider-body") || count.Load() != 1 {
				t.Fatalf("error/count %v %d", err, count.Load())
			}
		})
	}
	var count atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count.Add(1)
		select {
		case <-r.Context().Done():
		case <-time.After(100 * time.Millisecond):
		}
	}))
	defer server.Close()
	p := newNativeTextProvider("gemini-text", "synthetic", "../escape", server.URL, server.Client())
	if _, err := p.Analyze(context.Background(), ProjectInput{Title: "标题", Brief: "合成简报"}); err == nil || count.Load() != 0 {
		t.Fatal("invalid model contacted server")
	}
	p.model = "model"
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := p.Analyze(ctx, ProjectInput{Title: "标题", Brief: "合成简报"}); err == nil || count.Load() > 1 {
		t.Fatal("cancellation failed")
	}
}
func TestNativeProductionOrigins(t *testing.T) {
	for _, p := range []*NativeTextProvider{NewAnthropicCreativeProvider("synthetic", "model"), NewGeminiCreativeProvider("synthetic", "model")} {
		if p.client.Transport.(*http.Transport).Proxy != nil {
			t.Fatal("environment proxy enabled")
		}
		if p.client.CheckRedirect(nil, nil) != http.ErrUseLastResponse {
			t.Fatal("redirect enabled")
		}
	}
	if NewNativeTextProvider("unknown", "", "model") != nil {
		t.Fatal("unknown provider accepted")
	}
}

func TestNativeTextRejectsTruncatedAndOversizedEnvelopes(t *testing.T) {
	for _, kind := range []string{"anthropic-text", "gemini-text", "minimax-text"} {
		for _, raw := range []string{`{"stop_reason":"max_tokens","content":[{"type":"text","text":"{}"}]}`, `{"candidates":[{"finishReason":"SAFETY","content":{"parts":[{"text":"{}"}]}}]}`, strings.Repeat("x", (256<<10)+1)} {
			t.Run(kind, func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, raw) }))
				defer server.Close()
				p := newNativeTextProvider(kind, "synthetic", "model", server.URL, server.Client())
				if _, err := p.Analyze(context.Background(), ProjectInput{Title: "标题", Brief: "合成简报"}); err == nil {
					t.Fatal("accepted truncated, blocked or oversized envelope")
				}
			})
		}
	}
}

func TestMiniMaxNativeCompatibility(t *testing.T) {
	payload := `{"summary":"合成需求分析","questions":[],"suggestions":[]}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer synthetic" {
			t.Error("invalid MiniMax endpoint/auth")
		}
		var body map[string]any
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			t.Fatal("invalid body")
		}
		if body["model"] != "configured-model" || body["reasoning_split"] != true || body["max_completion_tokens"] != float64(6000) || body["response_format"] != nil {
			t.Error("MiniMax protocol controls missing or unsupported options sent")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"finish_reason": "stop", "message": map[string]string{"content": payload, "reasoning_content": "not persisted"}}}})
	}))
	defer server.Close()
	p := newNativeTextProvider("minimax-text", "synthetic", "configured-model", server.URL, server.Client())
	a, err := p.Analyze(context.Background(), ProjectInput{Title: "标题", Brief: "合成简报"})
	if err != nil || a.Provider != "minimax-text" {
		t.Fatalf("analysis %v %#v", err, a)
	}
	data, _ := json.Marshal(nativeTestScript())
	payload = string(data)
	script, err := p.Script(context.Background(), ProjectInput{Title: "标题", Brief: "合成简报"})
	if err != nil || len(script.Scenes) != 3 {
		t.Fatal(err)
	}
	payload = `{"shots":[{"id":"shot_1","scene_index":1,"description":"画面","shot_size":"wide","camera_movement":"static","duration_seconds":3,"dialogue":""}]}`
	if _, err = p.Storyboard(context.Background(), script); err != nil {
		t.Fatal(err)
	}
	payload = `<think>hidden</think>{"summary":"合成需求分析","questions":[],"suggestions":[]}`
	if _, err = p.Analyze(context.Background(), ProjectInput{Title: "标题", Brief: "合成简报"}); err == nil {
		t.Fatal("accepted reasoning mixed with JSON")
	}
	if NewMiniMaxTextProvider("synthetic", "model").endpoint != "https://api.minimax.io" {
		t.Fatal("nonofficial origin")
	}
}
