package seriesrepo

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/series"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

func testDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("STUDIO_POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("STUDIO_POSTGRES_TEST_DSN required for real PostgreSQL")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal("invalid fixture DSN")
	}
	admin := stdlib.OpenDB(*cfg)
	t.Cleanup(func() { admin.Close() })
	var entropy [12]byte
	if _, err = rand.Read(entropy[:]); err != nil {
		t.Fatal(err)
	}
	name := "series_test_" + hex.EncodeToString(entropy[:])
	if _, err = admin.Exec("CREATE SCHEMA " + name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec("DROP SCHEMA " + name + " CASCADE"); err != nil {
			t.Error(err)
		}
	})
	cfg.RuntimeParams["search_path"] = name
	db := stdlib.OpenDB(*cfg)
	t.Cleanup(func() { db.Close() })
	if _, err = db.Exec(Schema); err != nil {
		t.Fatal(err)
	}
	return db
}
func TestPostgresDirectory201PaginationReorderAndIsolation(t *testing.T) {
	db := testDB(t)
	s, _ := Open("", db)
	directoryContract(t, s)
}
func TestPostgresEpisodeAndCoreProjectRollback(t *testing.T) {
	db := testDB(t)
	s, _ := Open("", db)
	_, season := fixture(t, s)
	if _, err := db.Exec(`CREATE TABLE fixture_projects(space_id text,id text,title text,PRIMARY KEY(space_id,id))`); err != nil {
		t.Fatal(err)
	}
	c := command("episode", season.ID, "atomic-episode")
	// A caller must not create an orphan episode with the convenience API.
	if _, err := s.Mutate(ctx, testActor, "atomic-episode", c, nil); !errors.Is(err, ErrStorage) {
		t.Fatal("standalone SQL episode accepted")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	n, err := s.MutateTx(ctx, tx, testActor, "atomic-episode", c)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(`INSERT INTO fixture_projects VALUES($1,$2,$3)`, n.Space, n.ProjectID, n.Title); err != nil {
		t.Fatal(err)
	}
	// A failure in another core participant must roll back node, ordinal and receipt.
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Get(ctx, testActor.Space, "episode", c.ID); !errors.Is(err, series.ErrNotFound) {
		t.Fatal("rolled back episode visible", err)
	}
	parent, _ := s.Get(ctx, testActor.Space, "season", season.ID)
	if parent.NextOrdinal != 1 || parent.Version != 1 {
		t.Fatal("rolled back parent changed")
	}
	tx, err = db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	n, err = s.MutateTx(ctx, tx, testActor, "atomic-episode", c)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(`INSERT INTO fixture_projects VALUES($1,$2,$3)`, n.Space, n.ProjectID, n.Title); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = db.QueryRow(`SELECT count(*) FROM fixture_projects WHERE id=$1`, n.ID).Scan(&count); err != nil || count != 1 {
		t.Fatal("core project not atomic", err)
	}
	if _, err = s.ByProject(ctx, testActor.Space, n.ProjectID); err != nil {
		t.Fatal(err)
	}
}
func fileDigests(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out[path] = series.SHA(string(b))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}
func TestPostgresMigrationPreservesCurrentEntitiesReceiptsAndSource(t *testing.T) {
	db := testDB(t)
	dir := t.TempDir()
	source, _ := Open(dir, nil)
	root, season := fixture(t, source)
	for _, key := range []string{"episode-one", "episode-two"} {
		create(t, source, command("episode", season.ID, key), key)
	}
	n, _ := source.Get(ctx, testActor.Space, "season", season.ID)
	move := series.Command{Operation: "move", Kind: "episode", ID: series.NewID(testActor, "create", "episode-one"), Expected: 1, ExpectedParent: n.Version, Ordinal: 7}
	if _, err := source.Mutate(ctx, testActor, "move-seven", move, nil); err != nil {
		t.Fatal(err)
	}
	before := fileDigests(t, dir)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	count, err := ImportFiles(ctx, tx, dir)
	if err != nil || count != 4 {
		t.Fatalf("import %d: %v", count, err)
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	after := fileDigests(t, dir)
	if series.Digest(before) != series.Digest(after) {
		t.Fatal("source changed on rollback")
	}
	tx, _ = db.BeginTx(ctx, nil)
	count, err = ImportFiles(ctx, tx, dir)
	if err != nil || count != 4 {
		t.Fatal(count, err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	pg, _ := Open("", db)
	if replay, err := pg.Mutate(ctx, testActor, "move-seven", move, nil); err != nil || replay.Ordinal != 7 {
		t.Fatal("SQL receipt not preserved", err)
	}
	export := filepath.Join(t.TempDir(), "series")
	tx, _ = db.BeginTx(ctx, nil)
	count, err = ExportFiles(ctx, tx, export)
	if err != nil || count != 4 {
		t.Fatal(count, err)
	}
	tx.Rollback()
	restored, _ := Open(export, nil)
	if replay, err := restored.Mutate(ctx, testActor, "move-seven", move, nil); err != nil || replay.Ordinal != 7 {
		t.Fatal("file receipt not preserved", err)
	}
	if replay, err := restored.Mutate(ctx, testActor, "root-key", command("series", "", "root-key"), nil); err != nil || replay.ID != root.ID {
		t.Fatal("create receipt lost", err)
	}
	next := create(t, restored, command("episode", season.ID, "next-eight"), "next-eight")
	if next.Ordinal != 8 {
		t.Fatal("counter not preserved")
	}
	if series.Digest(before) != series.Digest(fileDigests(t, dir)) {
		t.Fatal("read-only migration changed source")
	}
	tx, _ = db.BeginTx(ctx, nil)
	if _, err = ImportFiles(ctx, tx, dir); err == nil {
		t.Fatal("nonempty destination overwritten")
	}
	tx.Rollback()
}
func TestMigrationRefusesUnfinishedEpisodeAndDatabaseRejectsIdentityTampering(t *testing.T) {
	db := testDB(t)
	dir := t.TempDir()
	s, _ := Open(dir, nil)
	_, season := fixture(t, s)
	c := command("episode", season.ID, "pending-migrate")
	if _, err := s.Mutate(ctx, testActor, "pending-migrate", c, func(series.Node) error { return errors.New("interrupted") }); err == nil {
		t.Fatal("expected failure")
	}
	before := fileDigests(t, dir)
	tx, _ := db.BeginTx(ctx, nil)
	if _, err := ImportFiles(ctx, tx, dir); err == nil {
		t.Fatal("unfinished episode migrated")
	}
	tx.Rollback()
	if series.Digest(before) != series.Digest(fileDigests(t, dir)) {
		t.Fatal("migration repaired source implicitly")
	}
	pg, _ := Open("", db)
	root, _ := fixture(t, pg)
	b, _ := json.Marshal(root)
	var payload map[string]any
	json.Unmarshal(b, &payload)
	payload["id"] = "wrong"
	b, _ = json.Marshal(payload)
	if _, err := db.Exec(`UPDATE studio_series_nodes SET payload=$1 WHERE id=$2`, b, root.ID); err == nil {
		t.Fatal("root payload identity bypassed constraint")
	}
}
