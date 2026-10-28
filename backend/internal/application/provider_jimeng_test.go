package application

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestJimengOfficialSignatureVector(t *testing.T) {
	// Oracle: unmodified base/sign.go + base/model.go from official SDK commit
	// 64e6e051a12441821178c93b8922db4e7158d3e1, GetSignRequest, synthetic keys only.
	body := []byte(`{"req_key":"synthetic","prompt":"hello","force_single":true}`)
	req, _ := http.NewRequest("POST", "https://visual.volcengineapi.com/?Version=2022-08-31&Action=CVSync2AsyncSubmitTask", bytes.NewReader(body))
	at, _ := time.Parse(time.RFC3339, "2026-10-23T12:34:56Z")
	signJimeng(req, body, "synthetic-access", "synthetic-secret", at)
	want := "HMAC-SHA256 Credential=synthetic-access/20261023/cn-north-1/cv/request, SignedHeaders=content-type;host;x-content-sha256;x-date, Signature=869a654e25433d4d32c2b01ce5388f1213b54542ff2f05d13c4319524e3bd32f"
	if req.Header.Get("Authorization") != want || req.Header.Get("X-Content-Sha256") != "5f17cb8ac3d1aa27141dd7df28832da9579be391a905e98aa02db536e9087f83" {
		t.Fatal("signature differs from official SDK oracle")
	}
}
func jimengInput() Input {
	return Input{Prompt: "合成协议测试", Kind: "general", Ratio: "1:1", Style: "natural"}
}
func TestJimengSubmitOncePollAndInlineImage(t *testing.T) {
	var imageData bytes.Buffer
	_ = png.Encode(&imageData, image.NewRGBA(image.Rect(0, 0, 8, 8)))
	b64 := base64.StdEncoding.EncodeToString(imageData.Bytes())
	var submits, polls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Query().Get("Version") != "2022-08-31" {
			t.Error("wrong protocol")
		}
		raw, _ := io.ReadAll(r.Body)
		if r.Header.Get("X-Content-Sha256") != jimengSHA(raw) || !strings.Contains(r.Header.Get("Authorization"), "/cn-north-1/cv/request") {
			t.Error("missing signature")
		}
		var in map[string]any
		_ = json.Unmarshal(raw, &in)
		if r.URL.Query().Get("Action") == "CVSync2AsyncSubmitTask" {
			submits.Add(1)
			if in["force_single"] != true || in["req_key"] != "synthetic" || in["image_urls"] != nil {
				t.Error("missing bounded submit")
			}
			fmt.Fprint(w, `{"code":10000,"data":{"task_id":"task-1"}}`)
			return
		}
		if r.URL.Query().Get("Action") != "CVSync2AsyncGetResult" || in["task_id"] != "task-1" || !strings.Contains(in["req_json"].(string), `"return_url":false`) {
			t.Error("invalid poll")
		}
		if polls.Add(1) == 1 {
			fmt.Fprint(w, `{"code":10000,"data":{"status":"generating"}}`)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 10000, "data": map[string]any{"status": "done", "binary_data_base64": []string{b64}}})
	}))
	defer server.Close()
	p := newJimengImageProvider("synthetic-access", "synthetic-secret", "synthetic", server.URL, server.Client())
	p.pollInterval = time.Millisecond
	result, err := p.Generate(context.Background(), jimengInput())
	if err != nil || result.Width != 8 || result.MIMEType != "image/png" {
		t.Fatalf("result %#v %v", result, err)
	}
	if submits.Load() != 1 || polls.Load() != 3 {
		t.Fatal("unexpected submission or polling count")
	}
}
func TestJimengRejectsUnsafeAndFailedResults(t *testing.T) {
	for _, result := range []string{`{"code":50413,"data":null,"message":"secret upstream body"}`, `{"code":10000,"data":null}`, `{"code":10000,"data":{"status":"done","image_urls":["http://127.0.0.1/leak"]}}`, `{"code":10000,"data":{"status":"done","binary_data_base64":["bad"]}}`, `{"code":10000,"data":{"status":"failed"}}`, `{"code":10000,"data":{"status":"not_found"}}`, `{"code":10000,"data":{"status":"expired"}}`, `{"code":10000,"data":{"status":"unknown"}}`} {
		t.Run(result, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if calls.Add(1) == 1 {
					fmt.Fprint(w, `{"code":10000,"data":{"task_id":"1"}}`)
					return
				}
				fmt.Fprint(w, result)
			}))
			defer server.Close()
			p := newJimengImageProvider("synthetic-access", "synthetic-secret", "synthetic", server.URL, server.Client())
			_, err := p.Generate(context.Background(), jimengInput())
			expectedCalls := int32(2)
			if strings.Contains(result, `"status":"done"`) {
				expectedCalls = 3
			}
			if err == nil || strings.Contains(err.Error(), "secret upstream") || calls.Load() != expectedCalls {
				t.Fatalf("invalid result accepted/retried: %v %d", err, calls.Load())
			}
		})
	}
}
func TestJimengNoSubmissionRetryOrRedirect(t *testing.T) {
	for _, status := range []int{302, 401, 429, 500} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Location", "/other")
				w.WriteHeader(status)
			}))
			defer server.Close()
			p := newJimengImageProvider("synthetic-access", "synthetic-secret", "synthetic", server.URL, server.Client())
			if _, err := p.Generate(context.Background(), jimengInput()); err == nil || calls.Load() != 1 {
				t.Fatalf("submission retry/redirect: %v %d", err, calls.Load())
			}
		})
	}
}
func TestJimengCancellationWhilePolling(t *testing.T) {
	var submits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("Action") == "CVSync2AsyncSubmitTask" {
			submits.Add(1)
			fmt.Fprint(w, `{"code":10000,"data":{"task_id":"1"}}`)
		} else {
			fmt.Fprint(w, `{"code":10000,"data":{"status":"in_queue"}}`)
		}
	}))
	defer server.Close()
	p := newJimengImageProvider("synthetic-access", "synthetic-secret", "synthetic", server.URL, server.Client())
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := p.Generate(ctx, jimengInput()); err == nil || submits.Load() != 1 {
		t.Fatal("cancellation or submit count failed")
	}
}
func TestJimengConfigurationGuard(t *testing.T) {
	p := NewJimengImageProvider("synthetic-access", "synthetic-secret", "")
	if _, err := p.Generate(context.Background(), jimengInput()); err == nil {
		t.Fatal("missing req_key allowed")
	}
	if p.endpoint != "https://visual.volcengineapi.com" || p.client.Transport.(*http.Transport).Proxy != nil || p.client.CheckRedirect(nil, nil) != http.ErrUseLastResponse {
		t.Fatal("unsafe production transport")
	}
}
