package main

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

func TestMigrationExportNeverInitializesEmptyDatabase(t *testing.T) {
	dsn := os.Getenv("STUDIO_POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("requires disposable PostgreSQL")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal("invalid synthetic DSN")
	}
	admin := stdlib.OpenDB(*cfg)
	defer admin.Close()
	schema := fmt.Sprintf("migration_test_%d", time.Now().UnixNano())
	if _, err := admin.Exec("CREATE SCHEMA " + schema); err != nil {
		t.Fatal(err)
	}
	defer admin.Exec("DROP SCHEMA " + schema + " CASCADE")
	cfg.RuntimeParams["search_path"] = schema
	db := stdlib.OpenDB(*cfg)
	defer db.Close()
	ctx := context.Background()
	for _, mode := range []string{"export", "import"} {
		if prepareSchema(ctx, db, mode) == nil {
			t.Fatalf("%s initialized empty database", mode)
		}
	}
	var count int
	if err := db.QueryRow("SELECT count(*) FROM information_schema.tables WHERE table_schema=current_schema()").Scan(&count); err != nil || count != 0 {
		t.Fatal("schema mutated", count, err)
	}
	if err := prepareSchema(ctx, db, "init"); err != nil {
		t.Fatal(err)
	}
	if err := prepareSchema(ctx, db, "export"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("ALTER TABLE execution_jobs DROP COLUMN trace_parent"); err != nil {
		t.Fatal(err)
	}
	if prepareSchema(ctx, db, "export") == nil {
		t.Fatal("export repaired old schema")
	}
	if err := prepareSchema(ctx, db, "init"); err != nil {
		t.Fatal(err)
	}
}
