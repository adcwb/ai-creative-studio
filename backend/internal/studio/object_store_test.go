package studio

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

type s3RoundTrip func(*http.Request) (*http.Response, error)

func (fn s3RoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return fn(r) }
func TestS3PresignOfficialVector(t *testing.T) {
	// AWS documentation's public synthetic credentials and published signature.
	config := S3ReferenceConfig{Region: "us-east-1", AccessKey: "AKIAIOSFODNN7EXAMPLE", SecretKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"}
	target, _ := url.Parse("https://examplebucket.s3.amazonaws.com/test.txt")
	got := s3Presign(config, target, "GET", time.Date(2013, 5, 24, 0, 0, 0, 0, time.UTC), 86400)
	parsed, _ := url.Parse(got)
	if parsed.Query().Get("X-Amz-Signature") != "aeeed9bbccd4d02ee5c0109b86d86835f995330da4c265957d157751f604d404" {
		t.Fatal("signature differs from AWS reference")
	}
}
func TestS3ReferenceBridgeControlledUpload(t *testing.T) {
	bridge, err := NewS3ReferenceBridge(S3ReferenceConfig{Endpoint: "https://objects.example.com", Bucket: "test-bucket", Region: "us-east-1", AccessKey: "synthetic-key", SecretKey: "synthetic-secret", Authorized: true})
	if err != nil {
		t.Fatal(err)
	}
	var input bytes.Buffer
	if err = png.Encode(&input, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	calls := 0
	uploadedPath := ""
	bridge.client = &http.Client{Transport: s3RoundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		uploadedPath = r.URL.Path
		if r.Method != "PUT" || r.Header.Get("Content-Type") != "image/png" || r.Header.Get("x-amz-acl") != "" || r.GetBody != nil {
			t.Fatal("unsafe request")
		}
		if r.URL.Host != "objects.example.com" || r.URL.Query().Get("X-Amz-Expires") != "60" {
			t.Fatal("wrong destination or expiry")
		}
		raw, _ := io.ReadAll(r.Body)
		if _, err := png.Decode(bytes.NewReader(raw)); err != nil {
			t.Fatal(err)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(""))}, nil
	})}
	signed, err := bridge.ReferenceURL(context.Background(), input.Bytes(), "image/png")
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ := url.Parse(signed)
	if calls != 1 || parsed.Path != uploadedPath || !strings.HasPrefix(parsed.Path, "/test-bucket/references/") || parsed.Query().Get("X-Amz-Expires") != "300" {
		t.Fatal("unexpected bridge URL")
	}
	if _, err = bridge.ReferenceURL(context.Background(), []byte("not a png"), "image/png"); err == nil || calls != 1 {
		t.Fatal("invalid image uploaded")
	}
	bridge.client = &http.Client{Transport: s3RoundTrip(func(*http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 302, Body: io.NopCloser(strings.NewReader(""))}, nil
	})}
	if _, err = bridge.ReferenceURL(context.Background(), input.Bytes(), "image/png"); err == nil || calls != 2 {
		t.Fatal("redirect/retry accepted")
	}
}
func TestS3ReferenceBridgeConfigurationAndSSRF(t *testing.T) {
	config := S3ReferenceConfig{Endpoint: "https://objects.example.com", Bucket: "test-bucket", Region: "us-east-1", AccessKey: "synthetic-key", SecretKey: "synthetic-secret"}
	if _, err := NewS3ReferenceBridge(config); err == nil {
		t.Fatal("missing consent accepted")
	}
	config.Authorized = true
	for _, endpoint := range []string{"http://objects.example.com", "https://user:pass@objects.example.com", "https://objects.example.com/path", "https://objects.example.com?key=value", "https://objects.example.com:8443"} {
		config.Endpoint = endpoint
		if _, err := NewS3ReferenceBridge(config); err == nil {
			t.Fatal("invalid endpoint", endpoint)
		}
	}
	config.Endpoint = "https://127.0.0.1"
	bridge, err := NewS3ReferenceBridge(config)
	if err != nil {
		t.Fatal(err)
	}
	var input bytes.Buffer
	_ = png.Encode(&input, image.NewRGBA(image.Rect(0, 0, 2, 2)))
	if _, err = bridge.ReferenceURL(context.Background(), input.Bytes(), "image/png"); err == nil {
		t.Fatal("private destination accepted")
	}
	t.Setenv("REFERENCE_BRIDGE_ENABLED", "")
	if bridge, err := ConfigureReferenceBridgeFromEnv(); err != nil || bridge != nil {
		t.Fatal("bridge enabled by default")
	}
}
