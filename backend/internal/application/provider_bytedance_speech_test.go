package application

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestByteDanceNativeSpeechStreamAndFailure(t *testing.T) {
	calls := 0
	status := 200
	audio := append([]byte("RIFFxxxxWAVE"), make([]byte, 40)...)
	response := `{"code":0,"data":"` + base64.StdEncoding.EncodeToString(audio[:10]) + `"}` + "\n" + `{"code":0,"data":"` + base64.StdEncoding.EncodeToString(audio[10:]) + `"}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("X-Api-Key") != "synthetic" || r.Header.Get("X-Api-Resource-Id") != "test-resource" || r.Header.Get("Authorization") != "" || len(r.Header.Get("X-Api-Request-Id")) != 36 {
			t.Error("wrong independent auth")
		}
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		if b["req_params"] == nil {
			t.Error("wrong request")
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(response))
	}))
	defer server.Close()
	p := NewByteDanceSpeechProvider("synthetic", "test-resource", "test-voice")
	p.endpoint = server.URL
	p.client = server.Client()
	if b, e := p.Speech(context.Background(), "合成测试"); e != nil || len(b) != len(audio) {
		t.Fatal(e)
	}
	for _, bad := range []string{`{"code":0,"data":""}`, `{"code":0,"data":"invalid-base64"}`, `{"code":0,"data":"` + base64.StdEncoding.EncodeToString(audio) + `"}` + "\n" + `{"code":500,"message":"secret-upstream"}`, `{"code":0,"data":"` + base64.StdEncoding.EncodeToString(audio) + `"}` + "\n{"} {
		response = bad
		before := calls
		if _, e := p.Speech(context.Background(), "合成测试"); e == nil || calls != before+1 {
			t.Fatal("partial/error stream accepted or retried", e)
		}
	}
	status = 429
	before := calls
	if _, e := p.Speech(context.Background(), "合成测试"); e == nil || calls != before+1 {
		t.Fatal("rate limit retried")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := p.Speech(ctx, "合成测试"); e == nil {
		t.Fatal("cancel ignored")
	}
}
