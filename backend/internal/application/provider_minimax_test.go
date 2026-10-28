package application

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMiniMaxNativeSpeechContractAndFailures(t *testing.T) {
	calls := 0
	response := `{"base_resp":{"status_code":0},"data":{"status":2,"audio":"` + hex.EncodeToString(append([]byte("RIFFxxxxWAVE"), make([]byte, 40)...)) + `"}}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/t2a_v2" || r.Header.Get("Authorization") != "Bearer synthetic-key" {
			t.Error("request contract")
		}
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		if b["model"] != "test-model" || b["output_format"] != "hex" || b["stream"] != false {
			t.Error("body contract", b)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(response))
	}))
	defer server.Close()
	p := NewMiniMaxSpeechProvider("synthetic-key", "test-model", "test-voice")
	p.transport = newCreativeProvider("synthetic-key", server.URL, server.Client())
	if data, err := p.Speech(context.Background(), "测试配音"); err != nil || len(data) != 52 {
		t.Fatal(err)
	}
	for _, bad := range []string{`{"data":null,"base_resp":{"status_code":0}}`, `{"data":{"status":2,"audio":"https://127.0.0.1/private"},"base_resp":{"status_code":0}}`, `{"data":{"status":2,"audio":"abcd"},"base_resp":{"status_code":0}}`, `{"data":{"status":2,"audio":"00"},"base_resp":{"status_code":1000}}`} {
		response = bad
		before := calls
		if _, err := p.Speech(context.Background(), "测试"); err == nil || calls != before+1 {
			t.Fatal("must reject without retry", err)
		}
	}
	before := calls
	if _, err := p.Speech(context.Background(), ""); err == nil || calls != before {
		t.Fatal("input sent upstream")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.Speech(ctx, "测试"); err == nil {
		t.Fatal("cancel ignored")
	}
}
