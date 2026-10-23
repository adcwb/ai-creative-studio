package studio

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCreativeRegistryConfigurationFailsClosed(t *testing.T) {
	for _, name := range []string{"openai-text", "anthropic-text", "gemini-text", "ark-text", "minimax-text"} {
		p, c := configureCreative(name, "synthetic-key", "test-model", "", "")
		if p == nil || !c.Ready || c.Model != "test-model" {
			t.Fatal(name, c)
		}
		if p, c = configureCreative(name, "synthetic-key", "", "", ""); p != nil || c.Ready {
			t.Fatal("missing model accepted", name)
		}
		if p, c = configureCreative(name, "", "test-model", "", ""); p != nil || c.Ready {
			t.Fatal("missing credential accepted", name)
		}
	}
	_, c := configureCreative("secret-not-a-provider", "synthetic-key", "test-model", "", "")
	if c.Ready || c.Provider != "unavailable" {
		t.Fatal("unknown config exposed", c)
	}
	t.Setenv("SPEECH_PROVIDER", "")
	p, c := ConfigureSpeechFromEnv()
	if p != nil || c.Ready {
		t.Fatal("nil speech offered as ready")
	}
	t.Setenv("VIDEO_PROVIDER", "")
	p, c = ConfigureVideoFromEnv()
	if p != nil || c.Ready {
		t.Fatal("nil video offered as ready")
	}
}
func TestArkCompatibleTextContract(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		if r.URL.Path != "/chat/completions" || b["model"] != "test-endpoint" || b["max_completion_tokens"] != float64(6000) {
			t.Error("ark contract", b)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]any{"content": `{"summary":"测试需求摘要","questions":[],"suggestions":[]}`}}}})
	}))
	defer server.Close()
	core := newCreativeProvider("synthetic-key", server.URL, server.Client())
	core.SetModels("test-endpoint", "", "")
	p := &arkTextProvider{core}
	a, e := p.Analyze(context.Background(), ProjectInput{Title: "测试", Brief: "测试故事需求"})
	if e != nil || a.Provider != "ark-text" || calls != 1 {
		t.Fatal(a, e)
	}
	if _, e = p.Speech(context.Background(), "测试"); e == nil || calls != 1 {
		t.Fatal("inherited unsupported media endpoint")
	}
}

func TestRealImageQueueLimitPreservesIdempotentReplay(t *testing.T) {
	caps := Capabilities{Provider: "openai-images", Mode: "real", Model: OpenAIModel, Ready: true}
	s, e := OpenStore(t.TempDir(), caps)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	in := Input{Kind: "general", Prompt: "synthetic queue limit", Ratio: "16:9", Style: "natural"}
	for _, key := range []string{"limit-one", "limit-two", "limit-three", "limit-four"} {
		if _, _, e = s.Create("personal-demo", key, in); e != nil {
			t.Fatal(e)
		}
	}
	if _, _, e = s.Create("personal-demo", "limit-five", in); e != ErrProviderNotReady {
		t.Fatal("unbounded costly queue", e)
	}
	if _, created, e := s.Create("personal-demo", "limit-one", in); e != nil || created {
		t.Fatal("rate limit blocked safe replay", e)
	}
}
