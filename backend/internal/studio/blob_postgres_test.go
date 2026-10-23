package studio

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestBlobPostgresIndependentInstances(t *testing.T) {
	dsn := os.Getenv("STUDIO_BLOB_TEST_DSN")
	if dsn == "" {
		t.Skip("explicit synthetic PostgreSQL DSN required")
	}
	ctx := context.Background()
	db, e := sql.Open("pgx", dsn)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	if _, e = db.ExecContext(ctx, BlobSchema); e != nil {
		t.Fatal(e)
	}
	dir := t.TempDir()
	one, e := NewFileBlobStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer one.Close()
	two, e := NewFileBlobStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer two.Close()
	a := &managedBlobs{db: db, store: one, entries: map[string]BlobInfo{}}
	b := &managedBlobs{db: db, store: two, entries: map[string]BlobInfo{}}
	key := blobKey("pg-tenant-"+mediaID(), "project", mediaID())
	payload := []byte("shared immutable object")
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, m := range []*managedBlobs{a, b} {
		wg.Add(1)
		go func(m *managedBlobs) { defer wg.Done(); errs <- m.put(ctx, key, payload) }(m)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	if e = b.put(ctx, key, []byte("replace")); e == nil {
		t.Fatal("immutable cross-instance overwrite accepted")
	}
	for _, m := range []*managedBlobs{a, b} {
		cache := filepath.Join(t.TempDir(), "input")
		path, e := m.materialize(ctx, key, cache)
		if e != nil {
			t.Fatal(e)
		}
		got, e := os.ReadFile(path)
		if e != nil || !bytes.Equal(got, payload) {
			t.Fatal("independent cache mismatch")
		}
	}
	c := &managedBlobs{db: db, store: two, entries: map[string]BlobInfo{}}
	if got, e := c.read(ctx, key); e != nil || !bytes.Equal(got, payload) {
		t.Fatal("restart did not see DB manifest")
	}
	if _, e = db.ExecContext(ctx, "UPDATE studio_blobs SET size=-1 WHERE key=$1", key); e == nil {
		t.Fatal("DB size constraint bypassed")
	}
	export := t.TempDir()
	if e = ExportBlobManifest(ctx, export, db); e != nil {
		t.Fatal(e)
	}
	if e = ImportBlobManifest(ctx, export, db); e != nil {
		t.Fatal(e)
	}
}
