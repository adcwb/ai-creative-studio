package studio

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
)

func TestArkVideoNativeProtocol(t *testing.T) {
	status := "queued"
	posts, deletes, downloads := 0, 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer synthetic-test-key" {
			t.Error("auth missing")
		}
		switch r.Method {
		case "POST":
			posts++
			var body map[string]any
			if json.NewDecoder(r.Body).Decode(&body) != nil {
				t.Error("json")
			}
			content := body["content"].([]any)[0].(map[string]any)
			if body["model"] != "explicit-model" || body["duration"] != float64(5) || content["type"] != "text" || content["text"] != "synthetic prompt" {
				t.Errorf("bad request %#v", body)
			}
			_, _ = w.Write([]byte(`{"id":"cgt-test-001"}`))
		case "GET":
			if r.URL.Path != "/contents/generations/tasks/cgt-test-001" {
				t.Error(r.URL.Path)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "cgt-test-001", "status": status, "content": map[string]string{"video_url": "https://cdn.example.test/synthetic.mp4"}})
		case "DELETE":
			deletes++
			_, _ = w.Write([]byte(`{}`))
		default:
			t.Error(r.Method)
		}
	}))
	defer server.Close()
	p := newArkVideoProvider("synthetic-test-key", "explicit-model", server.URL, server.Client(), func(_ context.Context, url string) ([]byte, error) {
		downloads++
		if url != "https://cdn.example.test/synthetic.mp4" {
			t.Error(url)
		}
		return []byte("0000ftypisomSynthetic"), nil
	})
	ctx := context.Background()
	job, err := p.SubmitVideo(ctx, "synthetic prompt")
	if err != nil || job.ID != "cgt-test-001" || job.Status != "queued" || posts != 1 {
		t.Fatalf("%+v %v", job, err)
	}
	if err = p.CancelVideo(ctx, job.ID); err != nil || deletes != 1 {
		t.Fatal(err)
	}
	status = "running"
	job, err = p.PollVideo(ctx, job.ID)
	if err != nil || job.Status != "in_progress" {
		t.Fatal(err)
	}
	if p.CancelVideo(ctx, job.ID) == nil || deletes != 1 {
		t.Fatal("deleted running task")
	}
	status = "succeeded"
	job, err = p.PollVideo(ctx, job.ID)
	if err != nil || job.Status != "completed" {
		t.Fatal(err)
	}
	if _, err = p.DownloadVideo(ctx, job.ID); err != nil || downloads != 1 {
		t.Fatal(err)
	}
	if p.CancelVideo(ctx, job.ID) == nil || deletes != 1 {
		t.Fatal("deleted successful task")
	}
	if _, err = p.PollVideo(ctx, "../../metadata"); err == nil {
		t.Fatal("invalid id")
	}
	if _, err = p.Speech(ctx, "test"); err == nil {
		t.Fatal("unsupported capability")
	}
}
func TestArkVideoNoRetryOnFailure(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(429)
		_, _ = w.Write([]byte(`secret remote body`))
	}))
	defer server.Close()
	p := newArkVideoProvider("synthetic", "explicit-model", server.URL, server.Client(), nil)
	_, err := p.SubmitVideo(context.Background(), "test synthetic video")
	if err == nil || calls != 1 || strings.Contains(err.Error(), "secret") {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
}
func TestSafeMediaDownloadDestination(t *testing.T) {
	ctx := context.Background()
	public := func(context.Context, string, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("8.8.8.8")}, nil
	}
	for _, raw := range []string{"http://example.com/video.mp4", "https://user:pass@example.com/v", "https://example.com:8080/v", "file:///etc/passwd", "https://localhost/v", "https://metadata.internal/v", "https://example.com/v#fragment"} {
		if _, _, err := downloadDestination(ctx, raw, public); err == nil {
			t.Errorf("accepted %s", raw)
		}
	}
	for _, ip := range []string{"127.0.0.1", "10.0.0.1", "172.16.0.1", "192.168.1.2", "169.254.169.254", "100.100.100.200", "0.0.0.0", "224.1.1.1", "240.1.1.1", "::1", "::ffff:127.0.0.1", "fe80::1", "fc00::1", "2001:db8::1", "64:ff9b::a00:1", "2002:7f00:1::"} {
		if publicDownloadIP(netip.MustParseAddr(ip)) {
			t.Error("accepted " + ip)
		}
	}
	mixed := func(context.Context, string, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("8.8.8.8"), netip.MustParseAddr("127.0.0.1")}, nil
	}
	if _, _, err := downloadDestination(ctx, "https://cdn.example.test/video.mp4", mixed); err == nil {
		t.Fatal("accepted mixed DNS")
	}
	u, ip, err := downloadDestination(ctx, "https://cdn.example.test/video.mp4?signature=synthetic", public)
	if err != nil || u.Hostname() != "cdn.example.test" || ip.String() != "8.8.8.8" {
		t.Fatal(err)
	}
	called := ""
	tr := pinnedDownloadTransport(ip, func(_ context.Context, _, address string) (net.Conn, error) {
		called = address
		return nil, errors.New("synthetic stop")
	})
	_, _ = tr.DialContext(ctx, "tcp", "rebound.example:443")
	if called != "8.8.8.8:443" || tr.Proxy != nil {
		t.Fatalf("not pinned: %s", called)
	}
}
func TestArkVideoFirstFrameConditioning(t *testing.T) {
	calls := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var body struct {
			Content []struct {
				Type     string `json:"type"`
				Role     string `json:"role"`
				ImageURL struct {
					URL string `json:"url"`
				} `json:"image_url"`
			} `json:"content"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil || len(body.Content) != 2 || body.Content[1].Role != "first_frame" || !strings.HasPrefix(body.Content[1].ImageURL.URL, "data:image/png;base64,") {
			t.Error("missing image conditioning")
		}
		_, _ = w.Write([]byte(`{"id":"cgt-reference-test"}`))
	}))
	defer s.Close()
	p := newArkVideoProvider("synthetic", "explicit-model", s.URL, s.Client(), nil)
	if _, err := p.SubmitVideoWithImage(context.Background(), "synthetic prompt", syntheticPNG(0), "image/png"); err != nil || calls != 1 {
		t.Fatal(err)
	}
	if _, err := p.SubmitVideoWithImage(context.Background(), "synthetic prompt", []byte("bad"), "image/png"); err == nil || calls != 1 {
		t.Fatal("invalid first frame accepted")
	}
}
