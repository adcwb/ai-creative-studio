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
	"strings"
	"sync/atomic"
	"testing"
)

func miniMaxTestPNG() []byte {
	var b bytes.Buffer
	_ = png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 256, 256)))
	return b.Bytes()
}
func TestMiniMaxImageProtocolAndReference(t *testing.T) {
	data := miniMaxTestPNG()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/v1/image_generation" || r.Header.Get("Authorization") != "Bearer synthetic" {
			t.Error("invalid image endpoint/auth")
		}
		var body map[string]any
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			t.Error("invalid JSON")
		}
		if body["model"] != "image-01" || body["response_format"] != "base64" || body["n"] != float64(1) || body["prompt_optimizer"] != false {
			t.Error("missing image bounds")
		}
		n := calls.Add(1)
		if n == 1 {
			if body["aspect_ratio"] != "1:1" || body["subject_reference"] != nil {
				t.Error("unexpected text-only request")
			}
		} else {
			if body["width"] != float64(1024) || body["height"] != float64(1280) || body["aspect_ratio"] != nil {
				t.Error("wrong 4:5 dimensions")
			}
			refs, ok := body["subject_reference"].([]any)
			if !ok || len(refs) != 1 {
				t.Error("missing reference")
			} else {
				ref := refs[0].(map[string]any)
				if ref["type"] != "character" || ref["image_file"] != "data:image/png;base64,"+base64.StdEncoding.EncodeToString(data) {
					t.Error("wrong subject reference")
				}
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"base_resp": map[string]int{"status_code": 0}, "data": map[string]any{"image_base64": []string{base64.StdEncoding.EncodeToString(data)}}})
	}))
	defer server.Close()
	p := newMiniMaxImageProvider("synthetic", "image-01", server.URL, server.Client())
	in := jimengInput()
	out, err := p.Generate(context.Background(), in)
	if err != nil || out.Width != 256 || out.MIMEType != "image/png" {
		t.Fatalf("image %v %#v", err, out)
	}
	in.Ratio = "4:5"
	if _, err = p.GenerateWithReference(context.Background(), in, data, "image/png"); err != nil {
		t.Fatal(err)
	}
	before := calls.Load()
	p.model = "image-01-live"
	if _, err = p.Generate(context.Background(), in); err == nil {
		t.Fatal("unsupported ratio silently changed")
	}
	in.Ratio = "1:1"
	if _, err = p.GenerateWithReference(context.Background(), in, []byte("bad"), "image/png"); err == nil {
		t.Fatal("bad reference accepted")
	}
	if calls.Load() != before {
		t.Fatal("invalid input contacted upstream")
	}
}
func TestMiniMaxImageRejectsBusinessErrorsNullAndURLs(t *testing.T) {
	for _, body := range []string{`null`, `{}`, `{"base_resp":null,"data":{"image_base64":["abc"]}}`, `{"base_resp":{},"data":{"image_base64":["abc"]}}`, `{"base_resp":{"status_code":1008},"data":{"image_base64":["abc"]}}`, `{"base_resp":{"status_code":0},"data":null}`, `{"base_resp":{"status_code":0},"data":{"image_urls":["http://127.0.0.1/private"]}}`, `{"base_resp":{"status_code":0},"data":{"image_base64":["bad"]}}`, `{"base_resp":{"status_code":0},"data":{"image_base64":["a","b"]}}`} {
		t.Run(body, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); fmt.Fprint(w, body) }))
			defer server.Close()
			p := newMiniMaxImageProvider("synthetic", "image-01", server.URL, server.Client())
			if _, err := p.Generate(context.Background(), jimengInput()); err == nil || calls.Load() != 1 {
				t.Fatal("bad image result accepted or retried")
			}
		})
	}
}
func TestMiniMaxVideoLifecycleReferenceAndCancel(t *testing.T) {
	state := "queued"
	deletes := 0
	submits := 0
	downloads := 0
	data := miniMaxTestPNG()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer synthetic" {
			t.Error("missing auth")
		}
		switch r.Method {
		case "POST":
			submits++
			if r.URL.Path != "/v2/video_generation" {
				t.Error("wrong submit path")
			}
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["model"] != "MiniMax-H3-Lite" || body["duration"] != float64(4) || body["resolution"] != "480P" {
				t.Error("unbounded submission")
			}
			content := body["content"].([]any)
			if submits == 1 {
				if len(content) != 1 || body["ratio"] != "16:9" {
					t.Error("wrong t2v")
				}
			} else {
				if len(content) != 2 || body["ratio"] != "adaptive" {
					t.Error("wrong first frame")
				}
				ref := content[1].(map[string]any)
				if ref["role"] != "first_frame" || ref["image_url"].(map[string]any)["url"] != "data:image/png;base64,"+base64.StdEncoding.EncodeToString(data) {
					t.Error("wrong image data")
				}
			}
			fmt.Fprint(w, `{"task_id":"123"}`)
		case "GET":
			if r.URL.Path != "/v2/query/video_generation/123" {
				t.Error("wrong query path")
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"task": map[string]any{"id": "123", "status": state, "content": map[string]string{"url": "https://cdn.example.test/generated.mp4"}}})
		case "DELETE":
			deletes++
			if r.URL.Path != "/v2/video_generation/123" {
				t.Error("wrong delete path")
			}
			fmt.Fprint(w, `{"task_id":"123","action":"cancelled","status":"cancelled"}`)
		default:
			t.Error("unexpected request")
		}
	}))
	defer server.Close()
	p := newMiniMaxVideoProvider("synthetic", "MiniMax-H3-Lite", server.URL, server.Client(), func(_ context.Context, url string) ([]byte, error) {
		downloads++
		if url != "https://cdn.example.test/generated.mp4" {
			t.Error("incorrect download")
		}
		return []byte("0000ftyp0000synthetic"), nil
	})
	job, err := p.SubmitVideo(context.Background(), "合成测试视频")
	if err != nil || job.ID != "123" || job.Status != "queued" {
		t.Fatalf("submit %v %#v", err, job)
	}
	if _, err = p.SubmitVideoWithImage(context.Background(), "合成测试视频", data, "image/png"); err != nil {
		t.Fatal(err)
	}
	for native, want := range map[string]string{"queued": "queued", "running": "in_progress", "succeeded": "completed", "failed": "failed", "cancelled": "failed"} {
		state = native
		v, e := p.PollVideo(context.Background(), "123")
		if e != nil || v.Status != want {
			t.Fatalf("state %s %v %#v", native, e, v)
		}
	}
	state = "succeeded"
	if _, err = p.DownloadVideo(context.Background(), "123"); err != nil || downloads != 1 {
		t.Fatal(err)
	}
	if p.CancelVideo(context.Background(), "123") == nil || deletes != 0 {
		t.Fatal("terminal record deletion attempted")
	}
	state = "running"
	if p.CancelVideo(context.Background(), "123") == nil || deletes != 0 {
		t.Fatal("running cancellation attempted")
	}
	state = "queued"
	if err = p.CancelVideo(context.Background(), "123"); err != nil || deletes != 1 {
		t.Fatalf("queued cancel %v %d", err, deletes)
	}
}
func TestMiniMaxVideoRejectsMalformedAndBusinessFailures(t *testing.T) {
	for _, body := range []string{`null`, `{}`, `{"task":null}`, `{"task":{"id":"999","status":"queued"}}`, `{"task":{"id":"123","status":"invented"}}`, `{"type":"error","error":{"message":"secret-body"},"task":{"id":"123","status":"queued"}}`, `{"base_resp":{"status_code":1008},"task":{"id":"123","status":"queued"}}`, `{"base_resp":null,"task":{"id":"123","status":"queued"}}`} {
		t.Run(body, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) }))
			defer server.Close()
			p := newMiniMaxVideoProvider("synthetic", "model", server.URL, server.Client(), nil)
			if _, err := p.PollVideo(context.Background(), "123"); err == nil || strings.Contains(err.Error(), "secret-body") {
				t.Fatal("bad response accepted or leaked")
			}
		})
	}
}
func TestMiniMaxMediaNoRetriesRedirectOrUnsafeDownload(t *testing.T) {
	for _, status := range []int{302, 401, 429, 500} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Location", "/leak")
				w.WriteHeader(status)
			}))
			defer server.Close()
			ip := newMiniMaxImageProvider("synthetic", "image-01", server.URL, server.Client())
			vp := newMiniMaxVideoProvider("synthetic", "model", server.URL, server.Client(), nil)
			if _, err := ip.Generate(context.Background(), jimengInput()); err == nil {
				t.Fatal("accepted HTTP failure")
			}
			if _, err := vp.SubmitVideo(context.Background(), "合成视频"); err == nil || calls.Load() != 2 {
				t.Fatal("retried/redirected submission")
			}
		})
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"task":{"id":"123","status":"succeeded","content":{"url":"http://127.0.0.1/private"}}}`)
	}))
	defer server.Close()
	vp := newMiniMaxVideoProvider("synthetic", "model", server.URL, server.Client(), safeMediaDownload)
	if _, err := vp.DownloadVideo(context.Background(), "123"); err == nil {
		t.Fatal("unsafe URL accepted")
	}
	if _, err := vp.PollVideo(context.Background(), "../private"); err == nil {
		t.Fatal("unsafe task id accepted")
	}
}
func TestMiniMaxVideoDoesNotClaimDeleteAsCancel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			fmt.Fprint(w, `{"task":{"id":"123","status":"queued"}}`)
		} else {
			fmt.Fprint(w, `{"task_id":"123","action":"deleted","status":"deleted"}`)
		}
	}))
	defer server.Close()
	p := newMiniMaxVideoProvider("synthetic", "model", server.URL, server.Client(), nil)
	if p.CancelVideo(context.Background(), "123") == nil {
		t.Fatal("claimed deletion as remote cancellation")
	}
}

func TestMiniMaxMediaCancelledContextAndInvalidVideoBytes(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		fmt.Fprint(w, `{"task":{"id":"123","status":"succeeded","content":{"url":"https://cdn.example.test/video.mp4"}}}`)
	}))
	defer server.Close()
	p := newMiniMaxVideoProvider("synthetic", "model", server.URL, server.Client(), func(context.Context, string) ([]byte, error) { return []byte("not video"), nil })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.SubmitVideo(ctx, "合成视频"); err == nil || calls.Load() != 0 {
		t.Fatal("cancelled submission sent")
	}
	if _, err := p.DownloadVideo(context.Background(), "123"); err == nil {
		t.Fatal("invalid video bytes accepted")
	}
	imageProvider := NewMiniMaxImageProvider("synthetic", "image-01")
	videoProvider := NewMiniMaxVideoProvider("synthetic", "model")
	for _, api := range []*OpenAICreativeProvider{imageProvider.api, videoProvider.api} {
		if api.endpoint != "https://api.minimax.io" || api.client.Transport.(*http.Transport).Proxy != nil || api.client.CheckRedirect(nil, nil) != http.ErrUseLastResponse {
			t.Fatal("unsafe production transport")
		}
	}
}
