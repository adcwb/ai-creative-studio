package libraryrepo_test

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"os"
	"sync"
	"testing"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/library"
	"github.com/adcwb/ai-creative-studio/backend/internal/persistence/libraryrepo"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

// Each run owns an isolated schema; no existing application tables are touched.
func libraryTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("STUDIO_POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("STUDIO_POSTGRES_TEST_DSN is required for real PostgreSQL verification")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal("invalid test DSN")
	}
	admin := stdlib.OpenDB(*cfg)
	t.Cleanup(func() { admin.Close() })
	var entropy [12]byte
	if _, err = rand.Read(entropy[:]); err != nil {
		t.Fatal(err)
	}
	schema := "library_test_" + hex.EncodeToString(entropy[:])
	if _, err = admin.Exec("CREATE SCHEMA " + schema); err != nil {
		t.Fatal("create isolated test schema:", err)
	}
	t.Cleanup(func() {
		if _, e := admin.Exec("DROP SCHEMA " + schema + " CASCADE"); e != nil {
			t.Error("cleanup test schema:", e)
		}
	})
	cfg.RuntimeParams["search_path"] = schema
	db := stdlib.OpenDB(*cfg)
	t.Cleanup(func() { db.Close() })
	if _, err = db.Exec(libraryrepo.Schema); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestPostgresLibraryCASReplayAndMigration(t *testing.T) {
	db := libraryTestDB(t)
	ctx := context.Background()
	source := t.TempDir()
	file, err := libraryrepo.Open(source, nil)
	if err != nil {
		t.Fatal(err)
	}
	first := result("actor", "v1", "space", 1)
	if _, err = file.Mutate(ctx, "space", "create-key", "create", "", first, false); err != nil {
		t.Fatal(err)
	}
	// Failed migration must not leave partially imported identities or receipts.
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = libraryrepo.ImportFiles(ctx, tx, source); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	var n int
	if err = db.QueryRow("SELECT count(*) FROM studio_library_assets").Scan(&n); err != nil || n != 0 {
		t.Fatal("rollback left rows", n, err)
	}
	tx, err = db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = libraryrepo.ImportFiles(ctx, tx, source); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	store, err := libraryrepo.Open("", db)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	out := make(chan error, 2)
	for _, id := range []string{"v2", "v3"} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			_, e := store.Mutate(ctx, "space", id, id, "v1", result("actor", id, "space", 2), false)
			out <- e
		}(id)
	}
	wg.Wait()
	close(out)
	wins, conflicts := 0, 0
	for e := range out {
		if e == nil {
			wins++
		} else if errors.Is(e, library.ErrConflict) {
			conflicts++
		} else {
			t.Fatal(e)
		}
	}
	if wins != 1 || conflicts != 1 {
		t.Fatalf("wins=%d conflicts=%d", wins, conflicts)
	}
	if _, e := store.Get(ctx, "other", "actor"); !errors.Is(e, libraryrepo.ErrNotFound) {
		t.Fatal("cross-space read", e)
	}
	original, ok, e := store.Replay(ctx, "space", "create-key", "create")
	if e != nil || !ok || original.Version.ID != "v1" {
		t.Fatal("original receipt lost", e)
	}
	current, e := store.Get(ctx, "space", "actor")
	if e != nil {
		t.Fatal(e)
	}
	current.Asset.Status = "archived"
	if _, e = store.Mutate(ctx, "space", "archive-key", "archive", current.Version.ID, current, true); e != nil {
		t.Fatal(e)
	}
	destination := t.TempDir()
	tx, e = db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = libraryrepo.ExportFiles(ctx, tx, destination); e != nil {
		tx.Rollback()
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	exported, e := libraryrepo.Open(destination, nil)
	if e != nil {
		t.Fatal(e)
	}
	got, e := exported.Get(ctx, "space", "actor")
	if e != nil || got.Asset.Status != "archived" || got.Version.ID != current.Version.ID {
		t.Fatal("export lost head", e)
	}
	if old, e := exported.Version(ctx, "space", "actor", "v1"); e != nil || old.SHA256 != first.Version.SHA256 {
		t.Fatal("export lost history", e)
	}
	if _, ok, e = exported.Replay(ctx, "space", "create-key", "create"); e != nil || !ok {
		t.Fatal("export lost receipt", e)
	}
}
