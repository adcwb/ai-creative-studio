package application

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestStreamingBlobsExceedLegacyInlineLimitWithoutTruncation(t *testing.T) {
	ctx := context.Background()
	key := blobKey("stream-tenant", "stream-project", mediaID()+".mp4")
	source := filepath.Join(t.TempDir(), "source")
	f, err := os.Create(source)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.Truncate(MaxBlobBytes + 65537); err != nil {
		t.Fatal(err)
	}
	f.Close()
	// A nonzero tail detects shortened copies and ranges ending at the old cap.
	f, err = os.OpenFile(source, os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.WriteAt([]byte("verified-tail"), MaxBlobBytes+100); err != nil {
		t.Fatal(err)
	}
	f.Close()
	info, err := localBlobInfo(ctx, key, source)
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"file", "s3-http-contract"} {
		t.Run(kind, func(t *testing.T) {
			var target StreamingBlobStore
			if kind == "file" {
				s, e := NewFileBlobStore(t.TempDir())
				if e != nil {
					t.Fatal(e)
				}
				defer s.Close()
				target = s
			} else {
				target = streamingS3Fixture(t, key)
			}
			r, e := os.Open(source)
			if e != nil {
				t.Fatal(e)
			}
			got, e := target.PutStream(ctx, info, r)
			r.Close()
			if e != nil || got != info {
				t.Fatal("stream upload", got, e)
			}
			if e = verifyBlobStream(ctx, target, info); e != nil {
				t.Fatal("full-object checksum", e)
			}
			part, e := target.OpenRange(ctx, key, MaxBlobBytes+100, int64(len("verified-tail")))
			if e != nil {
				t.Fatal(e)
			}
			data, e := io.ReadAll(part)
			part.Close()
			if e != nil || string(data) != "verified-tail" {
				t.Fatal("range beyond old cap", string(data), e)
			}
			m, e := openManagedBlobs(t.TempDir(), target)
			if e != nil {
				t.Fatal(e)
			}
			if e = m.putFile(ctx, key, source); e != nil {
				t.Fatal("immutable replay", e)
			}
			cache := filepath.Join(t.TempDir(), "cache")
			if _, e = m.materialize(ctx, key, cache); e != nil {
				t.Fatal(e)
			}
			actual, e := localBlobInfo(ctx, key, cache)
			if e != nil || actual != info {
				t.Fatal("materialization changed bytes", actual, e)
			}
			if _, e = readVerifiedBlob(ctx, target, info); !errors.Is(e, ErrBlobIntegrity) {
				t.Fatal("legacy inline callers must remain bounded", e)
			}
		})
	}
}

func TestStreamingBlobIntegrityFailureDoesNotPublishObject(t *testing.T) {
	key := blobKey("tenant", "project", mediaID())
	for _, s := range []StreamingBlobStore{func() *FileBlobStore {
		v, e := NewFileBlobStore(t.TempDir())
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() { v.Close() })
		return v
	}(), streamingS3Fixture(t, key)} {
		info := blobInfo(key, []byte("original"))
		if _, e := s.PutStream(context.Background(), info, strings.NewReader("tampered")); !errors.Is(e, ErrBlobIntegrity) {
			t.Fatal("corrupt source accepted", e)
		}
		if _, e := s.Stat(context.Background(), key); !errors.Is(e, ErrBlobNotFound) {
			t.Fatal("corrupt upload became visible", e)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, e := s.PutStream(ctx, info, bytes.NewReader([]byte("original"))); !errors.Is(e, context.Canceled) {
			t.Fatal("cancelled copy accepted", e)
		}
	}
}

func TestPostgresStreamingBlobMigrationRequiresOfflineSchemaUpgrade(t *testing.T) {
	db := postgresTestDB(t)
	ctx := context.Background()
	if _, err := db.Exec(`ALTER TABLE studio_blobs DROP CONSTRAINT studio_blobs_size_nonnegative; ALTER TABLE studio_blobs ADD CONSTRAINT studio_blobs_size_check CHECK(size>=0 AND size<=134217728)`); err != nil {
		t.Fatal(err)
	}
	if err := CheckPostgres(ctx, db); err == nil {
		t.Fatal("legacy object-size schema incorrectly reported ready")
	}
	if err := InitPostgres(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := CheckPostgres(ctx, db); err != nil {
		t.Fatal(err)
	}
	i := blobInfo(blobKey("pg-stream", "project", mediaID()), []byte("schema metadata fixture"))
	i.Size = MaxBlobBytes + 1
	if err := insertBlobInfo(ctx, db, i); err != nil {
		t.Fatal("upgraded schema retains inline-size quota", err)
	}
	if _, err := db.Exec(`UPDATE studio_blobs SET size=-1 WHERE key=$1`, i.Key); err == nil {
		t.Fatal("negative object size accepted")
	}
}

// An isolated HTTP contract fixture: bodies are streamed to disk, payload hashes
// checked, and conditional writes plus HTTP ranges actually exercised. It is
// not evidence of a real S3 service or its authorization implementation.
func streamingS3Fixture(t *testing.T, key string) *S3BlobStore {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "object")
	var mu sync.Mutex
	digest := ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.URL.Path != "/studio/"+key || r.URL.RawQuery != "" || !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 Credential=synthetic-access/") {
			http.Error(w, "invalid contract", 403)
			return
		}
		switch r.Method {
		case "PUT":
			if _, e := os.Stat(path); e == nil {
				w.WriteHeader(412)
				return
			}
			f, e := os.CreateTemp(dir, "upload-")
			if e != nil {
				t.Error(e)
				w.WriteHeader(500)
				return
			}
			defer os.Remove(f.Name())
			info, e := hashBlobStream(r.Context(), key, io.TeeReader(r.Body, f))
			f.Close()
			if e != nil || info.Size != r.ContentLength || info.SHA256 != r.Header.Get("X-Amz-Content-Sha256") || info.SHA256 != r.Header.Get("x-amz-meta-sha256") {
				w.WriteHeader(400)
				return
			}
			if e = os.Rename(f.Name(), path); e != nil {
				t.Error(e)
				w.WriteHeader(500)
				return
			}
			digest = info.SHA256
			w.WriteHeader(200)
		case "HEAD", "GET":
			f, e := os.Open(path)
			if os.IsNotExist(e) {
				w.WriteHeader(404)
				return
			}
			if e != nil {
				w.WriteHeader(500)
				return
			}
			defer f.Close()
			w.Header().Set("x-amz-meta-sha256", digest)
			http.ServeContent(w, r, "object", time.Unix(1, 0), f)
		default:
			w.WriteHeader(501)
		}
	}))
	t.Cleanup(srv.Close)
	s, e := NewS3BlobStore(S3BlobConfig{Endpoint: srv.URL, Bucket: "studio", Region: "us-east-1", AccessKey: "synthetic-access", SecretKey: "synthetic-secret", AllowLoopbackHTTP: true})
	if e != nil {
		t.Fatal(e)
	}
	return s
}
