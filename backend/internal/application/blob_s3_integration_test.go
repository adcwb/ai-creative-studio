package application

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

// An explicit loopback-only, independently installed S3 service. Credentials are
// synthetic constants; this test never reads real S3 credentials or profiles.
func TestBlobS3Service(t *testing.T) {
	endpoint := os.Getenv("STUDIO_S3_SERVICE_ENDPOINT")
	if endpoint == "" {
		t.Skip("explicit synthetic loopback S3 service required")
	}
	store, e := NewS3BlobStore(S3BlobConfig{Endpoint: endpoint, Bucket: "studio", Region: "us-east-1", AccessKey: "synthetic-access", SecretKey: "synthetic-secret", AllowLoopbackHTTP: true})
	if e != nil {
		t.Fatal(e)
	}
	if store.endpoint.Scheme != "http" {
		t.Fatal("integration requires isolated loopback HTTP service")
	}
	ctx := context.Background()
	key := blobKey("integration-"+mediaID(), "contract", mediaID())
	data := []byte("independent S3 service fixture")
	contractOK := t.Run("metadata_range_auth_delete", func(t *testing.T) {
		info, e := store.Put(ctx, key, data)
		if e != nil {
			t.Fatal(e)
		}
		got, e := store.Stat(ctx, key)
		if e != nil || got != info {
			t.Fatalf("HEAD metadata: %+v %v", got, e)
		}
		if got, e := readVerifiedBlob(ctx, store, info); e != nil || !bytes.Equal(got, data) {
			t.Fatalf("GET integrity: %v", e)
		}
		r, e := store.OpenRange(ctx, key, 2, 6)
		if e != nil {
			t.Errorf("range contract: %v", e)
		} else {
			part, err := io.ReadAll(r)
			r.Close()
			if err != nil || !bytes.Equal(part, data[2:8]) {
				t.Error("range response mismatch")
			}
		}

		wrong := *store
		wrong.config.SecretKey = "deliberately-wrong-synthetic-secret"
		denied, err := wrong.request(ctx, "HEAD", key, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		closeBlobResponse(denied)
		t.Logf("wrong-signature status=%d", denied.StatusCode)
		if denied.StatusCode != 403 && denied.StatusCode != 401 {
			t.Errorf("wrong signature must be rejected: status %d", denied.StatusCode)
		}
		req, e := http.NewRequestWithContext(ctx, "GET", endpoint+"/studio/"+key, nil)
		if e != nil {
			t.Fatal(e)
		}
		resp, e := store.client.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		closeBlobResponse(resp)
		t.Logf("anonymous status=%d", resp.StatusCode)
		if resp.StatusCode != 403 && resp.StatusCode != 401 {
			t.Errorf("anonymous read status %d", resp.StatusCode)
		}
		if e = store.Delete(ctx, key); e != nil {
			t.Fatal(e)
		}
		if _, e = store.Stat(ctx, key); !errors.Is(e, ErrBlobNotFound) {
			t.Fatalf("DELETE result: %v", e)
		}
	})
	immutableOK := t.Run("immutable_condition", func(t *testing.T) {
		info, e := store.Put(ctx, key, data)
		if e != nil {
			t.Fatal(e)
		}
		defer store.Delete(ctx, key)
		if _, e = store.Put(ctx, key, data); e != nil {
			t.Fatalf("idempotent conditional PUT: %v", e)
		}
		var status atomic.Int64
		original := store.client.Transport
		store.client.Transport = s3RoundTrip(func(r *http.Request) (*http.Response, error) {
			response, err := original.RoundTrip(r)
			if err == nil && r.Method == "PUT" {
				status.Store(int64(response.StatusCode))
			}
			return response, err
		})
		defer func() { store.client.Transport = original }()
		_, putErr := store.Put(ctx, key, []byte("must never overwrite"))
		r, readErr := store.OpenRange(ctx, key, 0, -1)
		if readErr != nil {
			t.Fatal(readErr)
		}
		actual, readErr := io.ReadAll(io.LimitReader(r, MaxBlobBytes+1))
		r.Close()
		if readErr != nil {
			t.Fatal(readErr)
		}
		t.Logf("conditional PUT status=%d before_sha256=%s after_sha256=%s", status.Load(), info.SHA256, s3Hash(string(actual)))
		if !errors.Is(putErr, ErrBlobIntegrity) {
			t.Errorf("service violated immutable If-None-Match contract: %v", putErr)
		}
		if !bytes.Equal(actual, data) {
			t.Error("failed precondition modified existing object")
		}

	})
	if !contractOK || !immutableOK {
		return
	}
	t.Run("media", func(t *testing.T) {
		s, e := OpenStore(t.TempDir())
		if e != nil {
			t.Fatal(e)
		}
		defer s.Close()
		if e = configureBlobStorage(s, store); e != nil {
			t.Fatal(e)
		}
		project, tenant := mediaID(), "service-"+mediaID()
		p, e := s.media.Demo(project, tenant, "demo", 1, nil)
		if e != nil {
			t.Fatal(e)
		}
		for _, a := range p.Assets {
			if e = os.Remove(filepath.Join(s.media.dir, a.ID)); e != nil {
				t.Fatal(e)
			}
		}
		x, e := s.media.StartExport(project, tenant, "export", p.Version)
		if e != nil {
			t.Fatal(e)
		}
		s.media.wg.Wait()
		state := s.media.Get(project, tenant)
		if state.Exports[0].Status != "succeeded" {
			t.Fatalf("real service render: %+v", state.Exports[0])
		}
		for _, ext := range []string{".mp4", ".srt", ".vtt"} {
			if e = os.Remove(filepath.Join(s.media.dir, x.ID+ext)); e != nil {
				t.Fatal(e)
			}
			path, e := s.media.localBlob(tenant, project, x.ID+ext)
			if e != nil {
				t.Fatal(e)
			}
			if st, e := os.Stat(path); e != nil || st.Size() == 0 {
				t.Fatalf("artifact missing %s", ext)
			}
		}
	})
}
