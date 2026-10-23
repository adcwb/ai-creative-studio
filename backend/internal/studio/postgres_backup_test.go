package studio

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

// This is a real pg_dump/pg_restore drill against a disposable synthetic DB.
// Explicit binary/test DSN configuration is required; no existing credentials or
// standard PG environment are forwarded to subprocesses.
func TestPostgresNativeBackupRestore(t *testing.T) {
	bin := os.Getenv("STUDIO_POSTGRES_BIN_DIR")
	if bin == "" {
		t.Skip("STUDIO_POSTGRES_BIN_DIR not set; native backup restore not exercised")
	}
	db := postgresTestDB(t)
	store := postgresTestStore(t, db)
	task, _, err := store.Create("synthetic-backup", "backup-key", Input{Prompt: "synthetic backup image", Kind: "poster"})
	if err != nil {
		t.Fatal(err)
	}
	if err = store.budget.SetLimit("synthetic-backup", "USD", 300); err != nil {
		t.Fatal(err)
	}
	if _, err = store.budget.Reserve("backup-cost", "synthetic-backup", "synthetic", "synthetic", "USD", 100); err != nil {
		t.Fatal(err)
	}
	if err = store.budget.Resolve("backup-cost", "synthetic-backup", "USD", "unknown", 0, ""); err != nil {
		t.Fatal(err)
	}
	var schema string
	if err = db.QueryRow(`SELECT current_schema()`).Scan(&schema); err != nil {
		t.Fatal(err)
	}
	cfg, err := pgx.ParseConfig(os.Getenv("STUDIO_POSTGRES_TEST_DSN"))
	if err != nil {
		t.Fatal("invalid synthetic backup configuration")
	}
	admin := stdlib.OpenDB(*cfg)
	defer admin.Close()
	name := "restore_" + authRandom(8)
	if _, err = admin.Exec(`CREATE DATABASE ` + name); err != nil {
		t.Fatal("test account requires disposable database creation for restore drill")
	}
	defer admin.Exec(`DROP DATABASE ` + name + ` WITH (FORCE)`)
	dump := filepath.Join(t.TempDir(), "studio.dump")
	run := func(command string, args ...string) {
		t.Helper()
		cmd := exec.Command(filepath.Join(bin, command), args...)
		cmd.Env = []string{"PATH=" + bin + ":/usr/bin:/bin", "HOME=/nonexistent", "PGSERVICEFILE=/dev/null", "PGPASSFILE=/dev/null", "LD_LIBRARY_PATH=" + os.Getenv("STUDIO_POSTGRES_LIB_DIR")}
		if err := cmd.Run(); err != nil {
			t.Fatalf("%s failed; connection details suppressed: %v", command, err)
		}
	}
	run("pg_dump", "--no-password", "--format=custom", "--no-owner", "--no-acl", "--schema="+schema, "--file="+dump, "--dbname="+os.Getenv("STUDIO_POSTGRES_TEST_DSN"))
	// Use separate command flags, never construct a password-bearing DSN or log it.
	run("pg_restore", "--no-password", "--no-owner", "--no-acl", "--exit-on-error", "--host="+cfg.Host, "--port="+itoaPort(cfg.Port), "--username="+cfg.User, "--dbname="+name, dump)
	cfg.Database = name
	cfg.RuntimeParams["search_path"] = schema
	restoredDB := stdlib.OpenDB(*cfg)
	defer restoredDB.Close()
	restored := postgresTestStore(t, restoredDB)
	if got, err := restored.Get("synthetic-backup", task.ID); err != nil || got.Key != "backup-key" {
		t.Fatal("restored task/idempotency mismatch", err)
	}
	entries, err := restored.budget.Entries("synthetic-backup", "USD")
	if err != nil || len(entries) != 1 || entries[0].State != "unknown" {
		t.Fatal("restored unknown cost mismatch", err)
	}
	// Compare canonical domain metadata, including task queue identities.
	for name, template := range postgresTemplates() {
		a, _ := db.BeginTx(context.Background(), nil)
		rawA, _, e := readPostgresScopeTx(context.Background(), a, name, template)
		a.Rollback()
		if e != nil {
			t.Fatal(e)
		}
		b, _ := restoredDB.BeginTx(context.Background(), nil)
		rawB, _, e := readPostgresScopeTx(context.Background(), b, name, template)
		b.Rollback()
		if e != nil {
			t.Fatal(e)
		}
		var va, vb any
		json.Unmarshal(rawA, &va)
		json.Unmarshal(rawB, &vb)
		if !reflect.DeepEqual(va, vb) {
			t.Fatal("native restore metadata mismatch", name)
		}
	}
}
func itoaPort(port uint16) string { // keep command construction simple and typed
	const digits = "0123456789"
	if port == 0 {
		return "0"
	}
	b := make([]byte, 0, 5)
	for port > 0 {
		b = append([]byte{digits[port%10]}, b...)
		port /= 10
	}
	return string(b)
}
