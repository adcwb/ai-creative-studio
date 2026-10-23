package studio

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestOpenAIEditMultipartLocalReference(t *testing.T) {
	var output bytes.Buffer
	if err := png.Encode(&output, image.NewRGBA(image.Rect(0, 0, 1024, 1024))); err != nil {
		t.Fatal(err)
	}
	reference := syntheticCreativePNG()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/images/edits" {
			t.Error(r.Method, r.URL.Path)
		}
		if err := r.ParseMultipartForm(18 << 20); err != nil {
			t.Fatal(err)
		}
		defer r.MultipartForm.RemoveAll()
		if r.FormValue("model") != "synthetic-model" || r.FormValue("size") != "1024x1024" || r.FormValue("n") != "1" {
			t.Error("wrong multipart fields")
		}
		f, h, err := r.FormFile("image[]")
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		b, _ := io.ReadAll(f)
		if !bytes.Equal(b, reference) || h.Header.Get("Content-Type") != "image/png" {
			t.Error("reference was not sent inline")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"b64_json": base64.StdEncoding.EncodeToString(output.Bytes())}}})
	}))
	defer server.Close()
	p := newOpenAIProvider("synthetic", server.URL+"/images/generations", server.Client())
	p.model = "synthetic-model"
	result, err := p.GenerateWithReference(context.Background(), Input{Kind: "general", Prompt: "合成角色参考的测试分镜", Ratio: "1:1", Style: "natural"}, reference, "image/png")
	if err != nil || result.Width != 1024 {
		t.Fatal(result.Width, err)
	}
}
func TestOpenAIEditRejectsUnsafeReferenceAndNeverRetries(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Location", "http://127.0.0.1:9/private")
		w.WriteHeader(429)
		_, _ = w.Write([]byte("upstream secret"))
	}))
	defer server.Close()
	p := newOpenAIProvider("synthetic", server.URL+"/images/generations", server.Client())
	in := Input{Kind: "general", Prompt: "合成角色参考测试", Ratio: "1:1", Style: "natural"}
	for _, sample := range []struct {
		data []byte
		mime string
	}{{[]byte(`<svg onload="bad"/>`), "image/svg+xml"}, {[]byte("not png"), "image/png"}, {syntheticCreativePNG(), "image/jpeg"}, {make([]byte, maxPNGBytes+1), "image/png"}} {
		if _, err := p.GenerateWithReference(context.Background(), in, sample.data, sample.mime); err == nil {
			t.Fatal("invalid reference accepted")
		}
	}
	if calls.Load() != 0 {
		t.Fatal("invalid input sent to upstream")
	}
	if _, err := p.GenerateWithReference(context.Background(), in, syntheticCreativePNG(), "image/png"); err == nil {
		t.Fatal("429 accepted")
	}
	if calls.Load() != 1 {
		t.Fatal("generation retried")
	}
}
