package studio

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
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

func TestBlobPostgresMetadataFailureLeavesUnreachableOrphan(t *testing.T) {
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
	if e = InitPostgres(ctx, db); e != nil {
		t.Fatal(e)
	}
	s, e := OpenPostgresStore(t.TempDir(), db)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	remote, calls := fakeS3Blobs(t)
	if e = configureBlobStorage(s, remote); e != nil {
		t.Fatal(e)
	}
	tenant := "failure-" + mediaID()
	project := mediaID()
	s.media.pg.beforeCommit = func(context.Context, *sql.Tx) error { return fmt.Errorf("synthetic transaction failure") }
	if _, e = s.media.AddAsset(project, tenant, "same-key", "asset", "", "", "", "synthetic", syntheticPNG(0), "image/png"); e == nil {
		t.Fatal("accepted metadata transaction failure")
	}
	s.media.pg.beforeCommit = nil
	if got := s.media.Get(project, tenant); len(got.Assets) != 0 {
		t.Fatal("failed transaction published media")
	}
	var orphanKey string
	if e = db.QueryRowContext(ctx, "SELECT key FROM studio_blobs WHERE tenant_hash=$1", s3Hash(tenant)).Scan(&orphanKey); e != nil {
		t.Fatal(e)
	}
	orphanID := filepath.Base(orphanKey)
	before := *calls
	if _, _, e = s.media.file(project, tenant, orphanID, false, false); e == nil {
		t.Fatal("unreferenced blob reachable")
	}
	if *calls != before {
		t.Fatal("orphan download touched storage before ownership check")
	}
	p, e := s.media.AddAsset(project, tenant, "same-key", "asset", "", "", "", "synthetic", syntheticPNG(1), "image/png")
	if e != nil {
		t.Fatal(e)
	}
	if len(p.Assets) != 1 || p.Assets[0].ID == orphanID {
		t.Fatal("retry reused orphan identity")
	}
	got, e := s.blobs.read(ctx, orphanKey)
	if e != nil || !bytes.Equal(got, syntheticPNG(0)) {
		t.Fatal("orphan overwritten")
	}
}
