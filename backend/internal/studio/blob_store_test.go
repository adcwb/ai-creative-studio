package studio

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
)

func fakeS3Blobs(t *testing.T) (*S3BlobStore, *int) {
	t.Helper()
	var mu sync.Mutex
	objects := map[string][]byte{}
	hashes := map[string]string{}
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		if !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 Credential=synthetic-access/") || !strings.Contains(r.Header.Get("Authorization"), "x-amz-content-sha256;x-amz-date") || r.URL.RawQuery != "" {
			t.Error("missing header SigV4 or credential in URL")
			w.WriteHeader(403)
			return
		}
		if !strings.HasPrefix(r.URL.Path, "/studio/") {
			w.WriteHeader(404)
			return
		}
		b, ok := objects[r.URL.Path]
		switch r.Method {
		case "PUT":
			body, _ := io.ReadAll(r.Body)
			if r.Header.Get("X-Amz-Content-Sha256") != s3Hash(string(body)) || !strings.Contains(r.Header.Get("Authorization"), "if-none-match;") || !strings.Contains(r.Header.Get("Authorization"), "x-amz-meta-sha256") {
				t.Error("unsigned put precondition/hash")
			}
			if r.Header.Get("If-None-Match") != "*" {
				t.Error("mutable put")
			}
			if ok {
				w.WriteHeader(412)
				return
			}
			objects[r.URL.Path] = body
			hashes[r.URL.Path] = r.Header.Get("X-Amz-Meta-Sha256")
			w.WriteHeader(200)
		case "HEAD":
			if !ok {
				w.WriteHeader(404)
				return
			}
			w.Header().Set("Content-Length", strconv.Itoa(len(b)))
			w.Header().Set("X-Amz-Meta-Sha256", hashes[r.URL.Path])
		case "GET":
			if !ok {
				w.WriteHeader(404)
				return
			}
			if rng := r.Header.Get("Range"); rng != "" {
				var start, end int
				if _, e := fmt.Sscanf(rng, "bytes=%d-%d", &start, &end); e != nil {
					w.WriteHeader(416)
					return
				}
				if start < 0 || end >= len(b) {
					w.WriteHeader(416)
					return
				}
				w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(b)))
				w.Header().Set("Content-Length", strconv.Itoa(end-start+1))
				w.WriteHeader(206)
				w.Write(b[start : end+1])
				return
			}
			w.Write(b)
		case "DELETE":
			delete(objects, r.URL.Path)
			delete(hashes, r.URL.Path)
			w.WriteHeader(204)
		}
	}))
	t.Cleanup(srv.Close)
	s, e := NewS3BlobStore(S3BlobConfig{Endpoint: srv.URL, Bucket: "studio", Region: "us-east-1", AccessKey: "synthetic-access", SecretKey: "synthetic-secret", AllowLoopbackHTTP: true})
	if e != nil {
		t.Fatal(e)
	}
	return s, &calls
}
func TestBlobStoreContract(t *testing.T) {
	local, e := NewFileBlobStore(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer local.Close()
	remote, _ := fakeS3Blobs(t)
	for name, store := range map[string]BlobStore{"file": local, "s3-fixture": remote} {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			key := blobKey("tenant-a", "project", strings.Repeat("a", 32))
			b := []byte("verified contents")
			info, e := store.Put(ctx, key, b)
			if e != nil {
				t.Fatal(e)
			}
			if info != blobInfo(key, b) {
				t.Fatal(info)
			}
			if got, e := store.Stat(ctx, key); e != nil || got != info {
				t.Fatalf("stat %v %v", got, e)
			}
			if _, e = store.Put(ctx, key, b); e != nil {
				t.Fatal(e)
			}
			if _, e = store.Put(ctx, key, []byte("overwrite")); e == nil {
				t.Fatal("immutable blob overwritten")
			}
			r, e := store.OpenRange(ctx, key, 2, 4)
			if e != nil {
				t.Fatal(e)
			}
			part, e := io.ReadAll(r)
			r.Close()
			if e != nil || string(part) != "rifi" {
				t.Fatalf("range %q %v", part, e)
			}
			if _, e = store.Stat(ctx, blobKey("tenant-b", "project", strings.Repeat("a", 32))); e == nil {
				t.Fatal("cross tenant key resolved")
			}
			for _, bad := range []string{"../x", "/etc/passwd", key + "?x=1", "a%2fb"} {
				if _, e = store.Put(ctx, bad, b); e == nil {
					t.Fatal("invalid key accepted")
				}
			}
			if e = store.Delete(ctx, key); e != nil {
				t.Fatal(e)
			}
			if _, e = store.Stat(ctx, key); e == nil {
				t.Fatal("delete failed")
			}
		})
	}
}
func TestBlobConfigAndIntegrity(t *testing.T) {
	for _, endpoint := range []string{"http://localhost:9000", "http://169.254.169.254", "https://user:pass@example.com", "https://example.com/path", "https://example.com?secret=x"} {
		if _, e := NewS3BlobStore(S3BlobConfig{Endpoint: endpoint, Bucket: "studio", Region: "us-east-1", AccessKey: "synthetic", SecretKey: "synthetic", AllowLoopbackHTTP: true}); e == nil {
			t.Fatalf("accepted %s", endpoint)
		}
	}
	s, e := NewFileBlobStore(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	ctx := context.Background()
	key := blobKey("a", "p", strings.Repeat("a", 32))
	info, e := s.Put(ctx, key, []byte("abc"))
	if e != nil {
		t.Fatal(e)
	}
	info.SHA256 = strings.Repeat("0", 64)
	if _, e = readVerifiedBlob(ctx, s, info); e == nil {
		t.Fatal("hash mismatch accepted")
	}
}
func TestBlobMediaMigrationAndRollback(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, e := OpenStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	p, e := s.media.AddAsset("p", "tenant", "key", "image", "", "", "", "synthetic", syntheticPNG(0), "image/png")
	if e != nil {
		t.Fatal(e)
	}
	id := p.Assets[0].ID
	remote, calls := fakeS3Blobs(t)
	if _, e = MigrateBlobStorage(ctx, dir, remote, false); e == nil {
		t.Fatal("migration bypassed active lock")
	}
	if e = configureBlobStorage(s, remote); e == nil {
		t.Fatal("unmigrated storage enabled")
	}
	s.Close()
	report, e := MigrateBlobStorage(ctx, dir, remote, false)
	if e != nil || report.Objects != 1 {
		t.Fatalf("migration %+v %v", report, e)
	}
	if e = os.Remove(filepath.Join(dir, "media", id)); e != nil {
		t.Fatal(e)
	}
	s, e = OpenStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	if e = configureBlobStorage(s, remote); e != nil {
		t.Fatal(e)
	}
	before := *calls
	if _, _, e = s.media.file("p", "other", id, false, false); e == nil {
		t.Fatal("cross-tenant media resolved")
	}
	if *calls != before {
		t.Fatal("unauthorized object request")
	}
	path, _, e := s.media.file("p", "tenant", id, false, false)
	if e != nil {
		t.Fatal(e)
	}
	b, e := os.ReadFile(path)
	if e != nil || !bytes.Equal(b, syntheticPNG(0)) {
		t.Fatal("materialization mismatch")
	}
	p, e = s.media.AddAsset("p", "tenant", "key2", "image2", "", "", "", "synthetic", syntheticPNG(1), "image/png")
	if e != nil {
		t.Fatal(e)
	}
	id2 := p.Assets[1].ID
	s.Close()
	os.Remove(filepath.Join(dir, "media", id2))
	report, e = MigrateBlobStorage(ctx, dir, remote, true)
	if e != nil || report.Objects != 2 {
		t.Fatalf("rollback %+v %v", report, e)
	}
	b, e = os.ReadFile(filepath.Join(dir, "media", id2))
	if e != nil || !bytes.Equal(b, syntheticPNG(1)) {
		t.Fatal("rollback omitted post-switch write")
	}
	os.WriteFile(filepath.Join(dir, "media", id2), []byte("conflict"), 0600)
	if _, e = MigrateBlobStorage(ctx, dir, remote, true); e == nil {
		t.Fatal("rollback overwrote conflict")
	}
}
func TestBlobMediaExportRemoteRoundtrip(t *testing.T) {
	if _, e := os.Stat("/usr/bin/ffmpeg"); e != nil {
		t.Skip("ffmpeg unavailable")
	}
	s, e := OpenStore(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	remote, _ := fakeS3Blobs(t)
	if e = configureBlobStorage(s, remote); e != nil {
		t.Fatal(e)
	}
	p, e := s.media.Demo("p", "tenant", "demo", 1, nil)
	if e != nil {
		t.Fatal(e)
	}
	for _, a := range p.Assets {
		os.Remove(filepath.Join(s.media.dir, a.ID))
	}
	x, e := s.media.StartExport("p", "tenant", "export", p.Version)
	if e != nil {
		t.Fatal(e)
	}
	s.media.wg.Wait()
	p = s.media.Get("p", "tenant")
	if p.Exports[0].Status != "succeeded" {
		t.Fatalf("export %+v", p.Exports[0])
	}
	for _, ext := range []string{".mp4", ".srt", ".vtt"} {
		os.Remove(filepath.Join(s.media.dir, x.ID+ext))
		path, e := s.media.localBlob("tenant", "p", x.ID+ext)
		if e != nil {
			t.Fatal(e)
		}
		st, e := os.Stat(path)
		if e != nil || st.Size() == 0 {
			t.Fatalf("export missing %s", ext)
		}
	}
}

func TestBlobPNGResultRoundtrip(t *testing.T) {
	dir := t.TempDir()
	s := realStore(t, dir)
	remote, calls := fakeS3Blobs(t)
	if e := configureBlobStorage(s, remote); e != nil {
		t.Fatal(e)
	}
	task, _, e := s.Create("personal-demo", "blob-png", realInput())
	if e != nil {
		t.Fatal(e)
	}
	result := realResult(t)
	if e = s.Finish(task, result, nil); e != nil {
		t.Fatal(e)
	}
	s.Close()
	if e = os.Remove(filepath.Join(dir, "results", task.ID+".png")); e != nil {
		t.Fatal(e)
	}
	s = realStore(t, dir)
	defer s.Close()
	if e = configureBlobStorage(s, remote); e != nil {
		t.Fatal(e)
	}
	before := *calls
	if _, e = s.ReadResult("other", task.ID); e == nil {
		t.Fatal("cross tenant PNG")
	}
	if *calls != before {
		t.Fatal("unauthorized storage access")
	}
	got, e := s.ReadResult("personal-demo", task.ID)
	if e != nil || !bytes.Equal(got.Data, result.Data) {
		t.Fatalf("remote PNG %v", e)
	}
}
