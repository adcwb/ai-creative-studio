package studio

import (
	"context"
	"database/sql"
	"github.com/adcwb/ai-creative-studio/backend/internal/jobqueue"
	"os"
	"path/filepath"
	"testing"
)

func TestPostgresBackupConnectionIsolation(t *testing.T) {
	for _, dsn := range []string{"postgres://u@localhost/a?sslmode=disable&dbname=host%3Devil", "postgres://u@localhost/db?service=existing", "postgres://u@localhost/db"} {
		if _, e := backupPostgresEnv(dsn, t.TempDir(), ""); e == nil {
			t.Fatal("unsafe URI accepted")
		}
	}
	env, e := backupPostgresEnv("postgres://synthetic:synthetic@localhost/test?sslmode=disable", t.TempDir(), "")
	if e != nil || backupDatabaseName(env) != "test" {
		t.Fatal(e)
	}
	if _, _, e = VerifyPostgresBackup(context.Background(), t.TempDir()); e == nil {
		t.Fatal("missing bundle accepted")
	}
}
func TestPostgresBackupRestore(t *testing.T) {
	dsn, target := os.Getenv("STUDIO_BACKUP_TEST_DSN"), os.Getenv("STUDIO_BACKUP_TARGET_DSN")
	if dsn == "" || target == "" {
		t.Skip("explicit synthetic source and EMPTY target DB URIs required")
	}
	ctx := context.Background()
	db, e := sql.Open("pgx", dsn)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if e = InitPostgres(ctx, db); e != nil {
		t.Fatal(e)
	}
	if _, e = db.ExecContext(ctx, jobqueue.Schema); e != nil {
		t.Fatal(e)
	}
	if _, e = db.ExecContext(ctx, `INSERT INTO execution_jobs(tenant_id,id,kind,idempotency_key,payload,state) VALUES('backup-tenant','unknown-job','image','unknown-job','{}','unknown')`); e != nil {
		t.Fatal(e)
	}
	if _, e = db.ExecContext(ctx, `INSERT INTO studio_cost_entries(record_key,tenant_id,entity_id,currency,reserved,actual,state,payload) VALUES('backup-cost','backup-tenant','unknown-cost','USD',100,0,'unknown','{"tenant":"backup-tenant","id":"unknown-cost","currency":"USD","reserved_minor":100,"actual_minor":0,"state":"unknown"}')`); e != nil {
		t.Fatal(e)
	}
	dir := t.TempDir()
	objects, e := NewFileBlobStore(filepath.Join(dir, "original"))
	if e != nil {
		t.Fatal(e)
	}
	defer objects.Close()
	info, e := objects.Put(ctx, blobKey("backup-tenant", "project", mediaID()), []byte("synthetic immutable asset"))
	if e != nil {
		t.Fatal(e)
	}
	if e = insertBlobInfo(ctx, db, info); e != nil {
		t.Fatal(e)
	}
	tools := PostgresBackupTools{Dump: os.Getenv("STUDIO_TEST_PG_DUMP"), Restore: os.Getenv("STUDIO_TEST_PG_RESTORE"), LibraryPath: os.Getenv("STUDIO_TEST_PG_LIBRARY_PATH")}
	bundle := filepath.Join(dir, "bundle")
	if _, e = BackupPostgresObjects(ctx, db, dsn, objects, bundle, tools, false); e == nil {
		t.Fatal("missing stop assertion accepted")
	}
	if _, e = db.ExecContext(ctx, `UPDATE execution_jobs SET state='leased',lease_until=clock_timestamp()+interval '1 hour'`); e != nil {
		t.Fatal(e)
	}
	if _, e = BackupPostgresObjects(ctx, db, dsn, objects, bundle, tools, true); e == nil {
		t.Fatal("active lease accepted")
	}
	if _, e = db.ExecContext(ctx, `UPDATE execution_jobs SET state='unknown',lease_until=NULL`); e != nil {
		t.Fatal(e)
	}
	report, e := BackupPostgresObjects(ctx, db, dsn, objects, bundle, tools, true)
	if e != nil {
		t.Fatal(e)
	}
	if report.Objects != 1 || !report.Verified {
		t.Fatal(report)
	}
	m, _, e := VerifyPostgresBackup(ctx, bundle)
	if e != nil || m.QueueStates["unknown"] != 1 || m.CostStates["unknown"] != 1 {
		t.Fatal(m, e)
	}
	archiveRoot := filepath.Join(dir, "encrypted")
	if e = os.Mkdir(archiveRoot, 0700); e != nil {
		t.Fatal(e)
	}
	syntheticKey := make([]byte, 32)
	for i := range syntheticKey {
		syntheticKey[i] = 77
	}
	sealed, e := SealBackup(ctx, bundle, archiveRoot, syntheticKey)
	if e != nil {
		t.Fatal(e)
	}
	decrypted := filepath.Join(dir, "decrypted")
	if _, e = OpenBackup(ctx, filepath.Join(archiveRoot, sealed.File), decrypted, syntheticKey); e != nil {
		t.Fatal(e)
	}
	restored, e := sql.Open("pgx", target)
	if e != nil {
		t.Fatal(e)
	}
	defer restored.Close()
	restored.SetMaxOpenConns(1)
	destination := filepath.Join(dir, "restored")
	if _, e = RestorePostgresBackup(ctx, restored, target, decrypted, destination, tools, true); e != nil {
		t.Fatal(e)
	}
	read, e := openBackupObjects(filepath.Join(destination, "objects"))
	if e != nil {
		t.Fatal(e)
	}
	defer read.Close()
	if _, e = readVerifiedBlob(ctx, read, info); e != nil {
		t.Fatal(e)
	}
	if _, e = RestorePostgresBackup(ctx, restored, target, bundle, filepath.Join(dir, "another"), tools, true); e == nil {
		t.Fatal("nonempty DB accepted")
	}
	if e = os.WriteFile(filepath.Join(bundle, "objects", info.Key), []byte("tamper"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, _, e = VerifyPostgresBackup(ctx, bundle); e == nil {
		t.Fatal("corrupt object accepted")
	}
	t.Log("real pg_dump exported snapshot + shared-file bundle + authenticated encryption/decryption + pg_restore verified; unknown queue/cost retained; active lease, nonempty DB and corruption rejected")
}
