package studio

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

func postgresTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("STUDIO_POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("STUDIO_POSTGRES_TEST_DSN not configured; real PostgreSQL integration not run")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal("invalid test PostgreSQL configuration")
	}
	admin := stdlib.OpenDB(*cfg)
	schema := "test_" + authRandom(8)
	if _, err = admin.Exec(`CREATE SCHEMA ` + schema); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	cfg.RuntimeParams["search_path"] = schema
	db := stdlib.OpenDB(*cfg)
	t.Cleanup(func() { db.Close(); admin.Exec(`DROP SCHEMA ` + schema + ` CASCADE`); admin.Close() })
	if err = InitPostgres(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	return db
}
func postgresTestStore(t *testing.T, db *sql.DB) *FileStore {
	t.Helper()
	s, err := OpenPostgresStore(t.TempDir(), db)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestPostgresTwoInstancesDomainAndBudget(t *testing.T) {
	db := postgresTestDB(t)
	a, b := postgresTestStore(t, db), postgresTestStore(t, db)
	input := Input{Prompt: "synthetic concurrency image", Kind: "poster"}
	var wg sync.WaitGroup
	errs := make(chan error, 20)
	ids := make(chan string, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s := a
			if i%2 == 1 {
				s = b
			}
			task, _, err := s.Create("tenant-a", "same-idempotency-key", input)
			if err != nil {
				errs <- err
			} else {
				ids <- task.ID
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	close(ids)
	for err := range errs {
		t.Fatal(err)
	}
	var id string
	for got := range ids {
		if id != "" && id != got {
			t.Fatal("duplicate task identity")
		}
		id = got
	}
	if _, err := b.Get("tenant-b", id); err != ErrNotFound {
		t.Fatal("cross tenant read", err)
	}
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM execution_jobs`).Scan(&n); err != nil || n != 1 {
		t.Fatal("queue enqueue not atomic/idempotent", n, err)
	}
	if err := a.budget.SetLimit("tenant-a", "USD", 100); err != nil {
		t.Fatal(err)
	}
	errs = make(chan error, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s := a
			if i%2 == 1 {
				s = b
			}
			_, err := s.budget.Reserve(fmt.Sprint(i), "tenant-a", "synthetic", "synthetic", "USD", 30)
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	success := 0
	for err := range errs {
		if err == nil {
			success++
		}
	}
	if success != 3 {
		t.Fatalf("budget overspend/race: %d successful reservations", success)
	}
	entries, err := b.budget.Entries("tenant-a", "USD")
	if err != nil || len(entries) != 3 {
		t.Fatal(entries, err)
	}
	// A foreign tenant cannot reference another tenant's task even through SQL.
	_, err = db.Exec(`INSERT INTO studio_image_assets(record_key,tenant_id,entity_id,parent_id,payload) VALUES('bad','tenant-b','bad',$1,'{}')`, id)
	if err == nil {
		t.Fatal("cross tenant FK accepted")
	}
}
func TestPostgresAuthRevocationOIDCAndRateShared(t *testing.T) {
	db := postgresTestDB(t)
	a, err := OpenPostgresAuthStore(t.TempDir(), db)
	if err != nil {
		t.Fatal(err)
	}
	b, err := OpenPostgresAuthStore(t.TempDir(), db)
	if err != nil {
		t.Fatal(err)
	}
	u, token, _, err := a.login("synthetic@example.test", "synthetic-only-password", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, ok := b.authenticate(token); !ok {
		t.Fatal("shared session unavailable")
	}
	org := a.spaces(u.ID)[0]["id"]
	if b.role(u.ID, org) != "owner" {
		t.Fatal("shared ownership missing")
	}
	a.mu.Lock()
	d := a.clone()
	delete(d.Sessions, authDigest(token))
	err = a.commit(d)
	a.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if _, _, ok := b.authenticate(token); ok {
		t.Fatal("revoked session resurrected from cache")
	}
	a.mu.Lock()
	d = a.clone()
	o := d.Organizations[org]
	delete(o.Members, u.ID)
	d.Organizations[org] = o
	err = a.commit(d)
	a.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if b.role(u.ID, org) != "" {
		t.Fatal("revoked member survived refresh")
	}
	p := oidcPending{Nonce: "synthetic-nonce", Verifier: "synthetic-verifier", Binding: "synthetic-binding", Expires: time.Now().Add(time.Minute)}
	if err := postgresPutOIDC(db, "synthetic-state", p); err != nil {
		t.Fatal(err)
	}
	if _, ok := postgresTakeOIDC(db, "synthetic-state", "wrong"); ok {
		t.Fatal("bad binding")
	}
	if _, ok := postgresTakeOIDC(db, "synthetic-state", p.Binding); !ok {
		t.Fatal("callback missing")
	}
	if _, ok := postgresTakeOIDC(db, "synthetic-state", p.Binding); ok {
		t.Fatal("callback replay")
	}
	for i := 0; i < 20; i++ {
		s := a
		if i%2 == 1 {
			s = b
		}
		if !s.allowAttempt("synthetic-address") {
			t.Fatal("premature shared limit")
		}
	}
	if b.allowAttempt("synthetic-address") {
		t.Fatal("shared rate limit bypassed")
	}
}
func TestPostgresModelRevocationAndNoSecretsInColumns(t *testing.T) {
	db := postgresTestDB(t)
	key := bytes.Repeat([]byte{33}, 32)
	a, err := OpenPostgresModelConfigStore(t.TempDir(), db, key)
	if err != nil {
		t.Fatal(err)
	}
	b, err := OpenPostgresModelConfigStore(t.TempDir(), db, key)
	if err != nil {
		t.Fatal(err)
	}
	in := syntheticModel()
	r, err := a.update("tenant-a", 0, in, ModelCredentialInput{Action: "set", Secret: "synthetic-private-secret"})
	if err != nil {
		t.Fatal(err)
	}
	in.ID = r.Entries[0].ID
	if v, err := b.validate("tenant-a", in.ID); err != nil || v["credential_status"] != "encrypted" {
		t.Fatal(v, err)
	}
	if _, err := a.update("tenant-a", 1, in, ModelCredentialInput{Action: "revoke"}); err != nil {
		t.Fatal(err)
	}
	b.mu.Lock()
	ciphertext := b.data.Tenants["tenant-a"].Entries[in.ID].Ciphertext
	b.mu.Unlock()
	if ciphertext != "" {
		t.Fatal("revoked credential resurrected")
	}
	var leak bool
	if err = db.QueryRow(`SELECT EXISTS(SELECT 1 FROM studio_model_tenants WHERE payload::text LIKE '%synthetic-private-secret%')`).Scan(&leak); err != nil || leak {
		t.Fatal("plaintext secret persisted", err)
	}
}

func TestPostgresOfflineMigrationRoundTrip(t *testing.T) {
	db := postgresTestDB(t)
	source := t.TempDir()
	file, err := OpenStore(source)
	if err != nil {
		t.Fatal(err)
	}
	task, _, err := file.Create("personal-demo", "migration-key", Input{Prompt: "synthetic migration image", Kind: "poster"})
	if err != nil {
		t.Fatal(err)
	}
	if err = file.budget.SetLimit("personal-demo", "CNY", 500); err != nil {
		t.Fatal(err)
	}
	if _, err = file.budget.Reserve("cost-synthetic", "personal-demo", "synthetic", "synthetic", "CNY", 100); err != nil {
		t.Fatal(err)
	}
	if _, err = ImportPostgresData(context.Background(), source, db); err == nil {
		t.Fatal("accepted live source")
	}
	if err = file.Close(); err != nil {
		t.Fatal(err)
	}
	report, err := ImportPostgresData(context.Background(), source, db)
	if err != nil {
		t.Fatal(err)
	}
	if report.Records["core.tasks"] != 1 {
		t.Fatal(report)
	}
	pg := postgresTestStore(t, db)
	if _, err = pg.Get("personal-demo", task.ID); err != nil {
		t.Fatal(err)
	}
	entries, err := pg.budget.Entries("personal-demo", "CNY")
	if err != nil || len(entries) != 1 || entries[0].State != "unknown" {
		t.Fatal(entries, err)
	}
	if _, err = ImportPostgresData(context.Background(), source, db); err == nil {
		t.Fatal("nonempty import accepted")
	}
	target := t.TempDir() + "/restored"
	if _, err = ExportPostgresData(context.Background(), target, db); err != nil {
		t.Fatal(err)
	}
	restored, err := OpenStore(target)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if got, err := restored.Get("personal-demo", task.ID); err != nil || got.Key != "migration-key" {
		t.Fatal(got, err)
	}
	if _, err = ExportPostgresData(context.Background(), target, db); err == nil {
		t.Fatal("export overwrote existing data")
	}
}

func TestPostgresAcceptanceReservationRollbackAndCancel(t *testing.T) {
	db := postgresTestDB(t)
	s := postgresTestStore(t, db)
	if err := s.budget.SetLimit("tenant-a", "USD", 100); err != nil {
		t.Fatal(err)
	}
	in := Input{Prompt: "synthetic governed acceptance", Kind: "poster", ModelRequest: ModelRequest{selection: &RuntimeSelection{TenantID: "tenant-a", ActorID: "synthetic", ModelID: "synthetic-config", Version: 1, Capability: "image", Currency: "USD", MaximumMinor: 60}, selectedCaps: Capabilities{Provider: "synthetic-provider", Model: "synthetic-model", Mode: "real", Ready: true}}}
	task, _, err := s.Create("tenant-a", "accept-one", in)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.Create("tenant-a", "accept-two", in); err == nil {
		t.Fatal("over budget acceptance succeeded")
	}
	if len(s.List("tenant-a")) != 1 {
		t.Fatal("failed admission left domain task")
	}
	var n int
	if err = db.QueryRow(`SELECT count(*) FROM execution_jobs`).Scan(&n); err != nil || n != 1 {
		t.Fatal("failed admission left queue job", err)
	}
	entries, err := s.budget.Entries("tenant-a", "USD")
	if err != nil || len(entries) != 1 || entries[0].State != "reserved" || entries[0].Reserved != 60 {
		t.Fatal(entries, err)
	}
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = CancelQueuedPostgresTx(context.Background(), tx, "tenant-a", task.ID, "image"); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	entries, err = s.budget.Entries("tenant-a", "USD")
	if err != nil || entries[0].State != "released" {
		t.Fatal(entries, err)
	}
}
func TestPostgresFailureClearsCacheAndSignalsResponseGuard(t *testing.T) {
	db := postgresTestDB(t)
	s := postgresTestStore(t, db)
	task, _, err := s.Create("tenant-a", "failure-key", Input{Prompt: "synthetic failure test", Kind: "poster"})
	if err != nil {
		t.Fatal(err)
	}
	before := s.PersistenceFailureEpoch()
	if _, err = db.Exec(`ALTER TABLE studio_image_tasks RENAME TO hidden_tasks`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Get("tenant-a", task.ID); err == nil {
		t.Fatal("cached data exposed on database failure")
	}
	if s.PersistenceFailureEpoch() <= before {
		t.Fatal("failure hidden from response guard")
	}
}
func TestPostgresConstraintsExistInEachSchema(t *testing.T) {
	first := postgresTestDB(t)
	second := postgresTestDB(t)
	for _, db := range []*sql.DB{first, second} {
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM pg_constraint WHERE connamespace=current_schema()::regnamespace AND conname IN ('studio_image_assets_task_fk','studio_sessions_user_fk','studio_cost_bounds')`).Scan(&n); err != nil || n != 3 {
			t.Fatal("cross-schema constraint initialization skipped", n, err)
		}
	}
}

func TestPostgresMigrationRejectsPartialOrSymlinkSource(t *testing.T) {
	db := postgresTestDB(t)
	source := t.TempDir()
	if err := os.WriteFile(source+"/store.lock", nil, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ImportPostgresData(context.Background(), source, db); err == nil {
		t.Fatal("missing core metadata accepted")
	}
	if err := os.Symlink("/dev/null", source+"/state.json"); err != nil {
		t.Fatal(err)
	}
	if _, err := ImportPostgresData(context.Background(), source, db); err == nil {
		t.Fatal("symlink metadata accepted")
	}
}
