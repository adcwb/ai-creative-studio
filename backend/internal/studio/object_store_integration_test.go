package studio

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

// This test is opt-in and only accepts a loopback MinIO launched by the harness.
// Production bridge DNS restrictions are not relaxed by any environment setting.
func TestS3LocalContainerIntegration(t *testing.T) {
	endpoint := os.Getenv("STUDIO_TEST_S3_ENDPOINT")
	if endpoint == "" {
		t.Skip("run scripts/s3_integration.py for isolated MinIO")
	}
	local, err := url.Parse(endpoint)
	if err != nil || local.Scheme != "http" || local.Hostname() != "127.0.0.1" {
		t.Fatal("only isolated loopback fixture accepted")
	}
	config := S3ReferenceConfig{Endpoint: "https://objects.example.test", Bucket: "studio-bridge-test", Region: "us-east-1", AccessKey: os.Getenv("STUDIO_TEST_S3_ACCESS"), SecretKey: os.Getenv("STUDIO_TEST_S3_SECRET"), Authorized: true}
	bridge, err := NewS3ReferenceBridge(config)
	if err != nil {
		t.Fatal(err)
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	defer transport.CloseIdleConnections()
	client := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }, Transport: s3RoundTrip(func(r *http.Request) (*http.Response, error) {
		copy := r.Clone(r.Context())
		target := *r.URL
		target.Scheme = local.Scheme
		target.Host = local.Host
		copy.URL = &target
		copy.Host = r.URL.Host
		return transport.RoundTrip(copy)
	})}
	bridge.client = client
	bucket, _ := url.Parse(config.Endpoint + "/" + config.Bucket)
	create, _ := http.NewRequest("PUT", s3Presign(config, bucket, "PUT", time.Now(), 60), nil)
	response, err := client.Do(create)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatalf("create bucket: %d %s", response.StatusCode, raw)
	}
	var input bytes.Buffer
	if err = png.Encode(&input, image.NewRGBA(image.Rect(0, 0, 3, 2))); err != nil {
		t.Fatal(err)
	}
	signed, err := bridge.ReferenceURL(context.Background(), input.Bytes(), "image/png")
	if err != nil {
		t.Fatal(err)
	}
	response, err = client.Get(signed)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatalf("GET: %d", response.StatusCode)
	}
	decoded, err := png.Decode(bytes.NewReader(raw))
	if err != nil || decoded.Bounds().Dx() != 3 || decoded.Bounds().Dy() != 2 {
		t.Fatal("invalid round-trip PNG", err)
	}
	target, _ := url.Parse(signed)
	target.RawQuery = ""
	expired := s3Presign(config, target, "GET", time.Now().Add(-10*time.Minute), 300)
	response, err = client.Get(expired)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 403 {
		t.Fatalf("expired URL accepted: %d", response.StatusCode)
	}
	modified := strings.Replace(signed, "/references/", "/other/", 1)
	response, err = client.Get(modified)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 403 {
		t.Fatalf("object substitution accepted: %d", response.StatusCode)
	}
	// Anonymous object reads are denied; no public ACL was configured.
	response, err = client.Get(target.String())
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 403 {
		t.Fatalf("anonymous object readable: %d", response.StatusCode)
	}
}
