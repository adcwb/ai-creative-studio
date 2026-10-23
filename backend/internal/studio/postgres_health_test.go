package studio

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPostgresReadinessRejectsOldColumnsWithoutDDL(t *testing.T) {
	db := postgresTestDB(t)
	ctx := context.Background()
	if err := CheckPostgres(ctx, db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("ALTER TABLE execution_jobs DROP COLUMN trace_parent"); err != nil {
		t.Fatal(err)
	}
	if CheckPostgres(ctx, db) == nil {
		t.Fatal("old schema accepted")
	}
	var present bool
	if err := db.QueryRow("SELECT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='execution_jobs' AND column_name='trace_parent')").Scan(&present); err != nil || present {
		t.Fatal("readiness silently repaired schema", err)
	}
	if err := InitPostgres(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := CheckPostgres(ctx, db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("ALTER TABLE studio_cost_entries RENAME COLUMN actual TO obsolete_actual"); err != nil {
		t.Fatal(err)
	}
	if CheckPostgres(ctx, db) == nil {
		t.Fatal("incomplete ledger accepted")
	}
}
func TestPostgresPersistenceFailureRecordedAsFailure(t *testing.T) {
	db := postgresTestDB(t)
	s := postgresTestStore(t, db)
	ops, err := OpenPostgresOperations(db)
	if err != nil {
		t.Fatal(err)
	}
	defer ops.Close()
	rec := httptest.NewRecorder()
	ops.Wrap(s.GuardPersistence(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		SetAuditIdentity(r.Context(), "actor", "tenant", "owner")
		if err := BeginAuditMutation(r.Context()); err != nil {
			t.Fatal(err)
		}
		s.pg.failures.Add(1)
		respond(w, 200, []string{})
	}))).ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/tasks", nil))
	if rec.Code != 503 {
		t.Fatal(rec.Code)
	}
	var status int
	if err := db.QueryRow("SELECT status FROM studio_audit_events WHERE request_id=$1 AND phase='finished'", rec.Header().Get("X-Request-ID")).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != 503 || ops.counts[5] != 1 || ops.counts[2] != 0 {
		t.Fatalf("false success: %d %v", status, ops.counts)
	}
}
