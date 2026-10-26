package studio

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func googleTestWAV() []byte {
	b := make([]byte, 48)
	copy(b, "RIFF")
	binary.LittleEndian.PutUint32(b[4:], 40)
	copy(b[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(b[16:], 16)
	binary.LittleEndian.PutUint16(b[20:], 1)
	binary.LittleEndian.PutUint16(b[22:], 1)
	binary.LittleEndian.PutUint32(b[24:], 24000)
	binary.LittleEndian.PutUint32(b[28:], 48000)
	binary.LittleEndian.PutUint16(b[32:], 2)
	binary.LittleEndian.PutUint16(b[34:], 16)
	copy(b[36:], "data")
	binary.LittleEndian.PutUint32(b[40:], 4)
	return b
}
func googleTestOutput(kind, mime, data, uri string) any {
	return map[string]any{"status": "completed", "steps": []map[string]any{{"type": "user_input", "content": []map[string]string{{"type": "text", "text": "synthetic"}}}, {"type": "model_output", "content": []map[string]string{{"type": kind, "mime_type": mime, "data": data, "uri": uri}}}}}
}
func TestGoogleSpeechNativeWAV(t *testing.T) {
	wave := googleTestWAV()
	var count atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count.Add(1)
		if r.Method != "POST" || r.URL.Path != "/v1beta/interactions" || r.Header.Get("x-goog-api-key") != "synthetic" || r.Header.Get("Authorization") != "" || r.URL.RawQuery != "" {
			t.Error("wrong native transport")
		}
		var in map[string]any
		if json.NewDecoder(r.Body).Decode(&in) != nil {
			t.Error("invalid JSON")
		}
		format := in["response_format"].(map[string]any)
		if format["type"] != "audio" || format["mime_type"] != "audio/wav" || format["sample_rate"] != float64(24000) {
			t.Error("not WAV requested")
		}
		voice := in["generation_config"].(map[string]any)["speech_config"].([]any)[0].(map[string]any)["voice"]
		if voice != "Kore" || in["model"] != "speech-test" {
			t.Error("configured identity lost")
		}
		_ = json.NewEncoder(w).Encode(googleTestOutput("audio", "audio/wav", base64.StdEncoding.EncodeToString(wave), ""))
	}))
	defer server.Close()
	p := newGoogleMediaProvider("gemini-speech", "synthetic", "speech-test", "Kore", server.URL, server.Client(), nil)
	got, err := p.Speech(context.Background(), "synthetic hello")
	if err != nil || !bytes.Equal(got, wave) || count.Load() != 1 {
		t.Fatalf("WAV %v", err)
	}
}
func TestGoogleSpeechRejectsPCMInvalidWAVAndSDKShape(t *testing.T) {
	good := googleTestWAV()
	bad := append([]byte(nil), good...)
	binary.LittleEndian.PutUint32(bad[24:], 16000)
	cases := []any{googleTestOutput("audio", "audio/l16", base64.StdEncoding.EncodeToString(good), ""), googleTestOutput("audio", "audio/wav", base64.StdEncoding.EncodeToString([]byte("raw PCM")), ""), googleTestOutput("audio", "audio/wav", base64.StdEncoding.EncodeToString(bad), ""), map[string]any{"status": "completed", "output_audio": map[string]string{"data": base64.StdEncoding.EncodeToString(good)}}, googleTestOutput("audio", "audio/wav", "", "https://evil.invalid/a.wav")}
	for i, fixture := range cases {
		t.Run(string(rune('a'+i)), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _ = json.NewEncoder(w).Encode(fixture) }))
			defer server.Close()
			p := newGoogleMediaProvider("gemini-speech", "synthetic", "test", "Kore", server.URL, server.Client(), nil)
			if _, err := p.Speech(context.Background(), "hello"); err == nil {
				t.Fatal("invalid media accepted")
			}
		})
	}
	truncated := good[:len(good)-1]
	if googleWAV(truncated) {
		t.Fatal("truncated WAV accepted")
	}
}
func TestGoogleVideoFileLifecycleAndRestart(t *testing.T) {
	var submits, polls atomic.Int32
	media := []byte{0, 0, 0, 20, 'f', 't', 'y', 'p', 'i', 's', 'o', 'm'}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-goog-api-key") != "synthetic" {
			t.Error("missing key")
		}
		switch r.URL.Path {
		case "/v1beta/interactions":
			submits.Add(1)
			var in map[string]any
			_ = json.NewDecoder(r.Body).Decode(&in)
			format := in["response_format"].(map[string]any)
			if in["model"] != "gemini-omni-1.1-flash" || in["input"] != "synthetic sunset" || in["previous_interaction_id"] != nil || in["generation_config"] != nil || in["background"] != false || in["store"] != false || in["stream"] != false || format["duration"] != nil || format["type"] != "video" || format["delivery"] != "uri" || format["resolution"] != "720p" || format["aspect_ratio"] != "16:9" {
				t.Error("missing URI delivery")
			}
			_ = json.NewEncoder(w).Encode(googleTestOutput("video", "video/mp4", "", "https://generativelanguage.googleapis.com/v1beta/files/abc-123:download?alt=media"))
		case "/v1beta/files/abc-123":
			state := "ACTIVE"
			if polls.Add(1) == 1 {
				state = "PROCESSING"
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"name": "files/abc-123", "state": state, "mimeType": "video/mp4", "downloadUri": "http://127.0.0.1/ignored"})
		case "/v1beta/files/abc-123:download":
			if r.URL.RawQuery != "alt=media" {
				t.Error("wrong download path")
			}
			_, _ = w.Write(media)
		default:
			t.Error("unexpected route", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	p := newGoogleMediaProvider("gemini-video", "synthetic", "gemini-omni-1.1-flash", "", server.URL, server.Client(), nil)
	job, err := p.SubmitVideo(context.Background(), "synthetic sunset")
	if err != nil || job.ID != "abc-123" || job.Status != "queued" {
		t.Fatal(job, err)
	}
	job, err = p.PollVideo(context.Background(), job.ID)
	if err != nil || job.Status != "in_progress" {
		t.Fatal(job, err)
	}
	// A new adapter can resume using only the persisted file ID; no POST replay.
	restored := newGoogleMediaProvider("gemini-video", "synthetic", "gemini-omni-1.1-flash", "", server.URL, server.Client(), nil)
	job, err = restored.PollVideo(context.Background(), job.ID)
	if err != nil || job.Status != "completed" {
		t.Fatal(job, err)
	}
	b, err := restored.DownloadVideo(context.Background(), job.ID)
	if err != nil || !bytes.Equal(b, media) || submits.Load() != 1 {
		t.Fatal("download/replay", err)
	}
}
func TestGoogleVideoRejectsUnsafeURIAndIdentifiers(t *testing.T) {
	for _, uri := range []string{"https://evil.invalid/v1beta/files/abc", "http://generativelanguage.googleapis.com/v1beta/files/abc", "https://generativelanguage.googleapis.com.evil.invalid/v1beta/files/abc", "https://generativelanguage.googleapis.com/v1beta/files/abc?key=secret", "https://generativelanguage.googleapis.com/v1beta/files/%2e%2e", "files/../secret", "files/abc#x", "files/abc:download?alt=media"} {
		if _, err := googleVideoFileID(uri); err == nil {
			t.Fatal("unsafe URI", uri)
		}
	}
	for _, uri := range []string{"files/abc-123", "https://generativelanguage.googleapis.com/v1beta/files/abc-123", "https://generativelanguage.googleapis.com/v1beta/files/abc-123:download?alt=media"} {
		if id, err := googleVideoFileID(uri); err != nil || id != "abc-123" {
			t.Fatal("valid URI", uri, err)
		}
	}
	var count atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count.Add(1)
		_ = json.NewEncoder(w).Encode(googleTestOutput("video", "video/mp4", "", "https://evil.invalid/video"))
	}))
	defer server.Close()
	p := newGoogleMediaProvider("gemini-video", "synthetic", "model", "", server.URL, server.Client(), nil)
	if _, err := p.PollVideo(context.Background(), "../secret"); err == nil || count.Load() != 0 {
		t.Fatal("unsafe ID sent")
	}
	if _, err := p.SubmitVideo(context.Background(), "synthetic prompt"); err == nil || count.Load() != 1 {
		t.Fatal("unsafe response URI accepted")
	}
}
func TestGoogleMediaRedirectBoundariesAndErrors(t *testing.T) {
	var generated, redirected atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected.Add(1) }))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST":
			generated.Add(1)
			http.Redirect(w, r, target.URL, 307)
		case strings.HasSuffix(r.URL.Path, ":download"):
			http.Redirect(w, r, "http://127.0.0.1/private", 302)
		default:
			_ = json.NewEncoder(w).Encode(map[string]string{"name": "files/abc", "state": "ACTIVE", "mimeType": "video/mp4"})
		}
	}))
	defer server.Close()
	p := newGoogleMediaProvider("gemini-video", "synthetic", "model", "", server.URL, server.Client(), nil)
	if _, err := p.SubmitVideo(context.Background(), "synthetic prompt"); err == nil || generated.Load() != 1 || redirected.Load() != 0 {
		t.Fatal("generation redirect followed")
	}
	if _, err := p.DownloadVideo(context.Background(), "abc"); err == nil || redirected.Load() != 0 {
		t.Fatal("private redirect accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.SubmitVideo(ctx, "synthetic prompt"); err == nil || generated.Load() != 1 {
		t.Fatal("cancelled POST sent")
	}
	rate := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(429)
		_, _ = w.Write([]byte("private provider detail"))
	}))
	defer rate.Close()
	p = newGoogleMediaProvider("gemini-speech", "synthetic", "model", "Kore", rate.URL, rate.Client(), nil)
	_, err := p.Speech(context.Background(), "hello")
	var safe *ProviderError
	if !errors.As(err, &safe) || safe.Code != "provider_rate_limited" || strings.Contains(err.Error(), "private") {
		t.Fatal("unsafe error", err)
	}
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { time.Sleep(40 * time.Millisecond) }))
	defer slow.Close()
	p = newGoogleMediaProvider("gemini-video", "synthetic", "model", "", slow.URL, &http.Client{Timeout: 5 * time.Millisecond}, nil)
	_, err = p.SubmitVideo(context.Background(), "synthetic prompt")
	if !errors.As(err, &safe) || safe.Code != "provider_timeout" {
		t.Fatal("timeout", err)
	}
	huge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "999999999")
		_, _ = w.Write([]byte("{}"))
	}))
	defer huge.Close()
	p = newGoogleMediaProvider("gemini-speech", "synthetic", "model", "Kore", huge.URL, huge.Client(), nil)
	if _, err = p.Speech(context.Background(), "hello"); err == nil {
		t.Fatal("oversize accepted")
	}
}

func TestGoogleVideoStatesAndNoPrematureDownload(t *testing.T) {
	for _, state := range []string{"PROCESSING", "FAILED", "UNKNOWN"} {
		t.Run(state, func(t *testing.T) {
			var downloads atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, ":download") {
					downloads.Add(1)
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]string{"name": "files/abc", "state": state, "mimeType": "video/mp4"})
			}))
			defer server.Close()
			p := newGoogleMediaProvider("gemini-video", "synthetic", "model", "", server.URL, server.Client(), nil)
			job, err := p.PollVideo(context.Background(), "abc")
			if state == "UNKNOWN" {
				if err == nil {
					t.Fatal("unknown state accepted")
				}
			} else if err != nil || (state == "FAILED" && job.Status != "failed") || (state == "PROCESSING" && job.Status != "in_progress") {
				t.Fatal(job, err)
			}
			if _, err = p.DownloadVideo(context.Background(), "abc"); err == nil || downloads.Load() != 0 {
				t.Fatal("downloaded before ACTIVE")
			}
		})
	}
	for _, p := range []*GoogleMediaProvider{NewGoogleSpeechProvider("synthetic", "model", "Kore"), NewGoogleVideoProvider("synthetic", "model")} {
		if p.client.Transport.(*http.Transport).Proxy != nil || p.endpoint != "https://generativelanguage.googleapis.com" {
			t.Fatal("unsafe production transport")
		}
	}
}
