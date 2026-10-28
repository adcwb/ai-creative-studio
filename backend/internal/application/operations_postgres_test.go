package application

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func TestPostgresAuditDurabilityPrivacyAndFailure(t *testing.T) {
	dsn := os.Getenv("STUDIO_AUDIT_TEST_DSN")
	if dsn == "" {
		t.Skip("requires explicitly configured disposable PostgreSQL")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal("test database open failed")
	}
	defer db.Close()
	if _, err = db.Exec(PostgresAuditSchema); err != nil {
		t.Fatal(err)
	}
	ops, err := OpenPostgresOperations(db)
	if err != nil {
		t.Fatal(err)
	}
	defer ops.Close()
	mutated := false
	handler := ops.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		SetAuditIdentity(r.Context(), "synthetic-actor", "synthetic-tenant", "owner")
		if err := BeginAuditMutation(r.Context()); err != nil {
			http.Error(w, "audit unavailable", 503)
			return
		}
		mutated = true
		w.WriteHeader(201)
	}))
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/secret-path?code=secret-code", strings.NewReader("secret-prompt"))
	r.Header.Set("Authorization", "Bearer secret-value")
	handler.ServeHTTP(w, r)
	if w.Code != 201 || !mutated {
		t.Fatal("durable audit rejected valid mutation")
	}
	id := w.Header().Get("X-Request-ID")
	defer db.Exec("DELETE FROM studio_audit_events WHERE request_id=$1", id)
	// Read through an independent connection to prove the event committed.
	other, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal("second connection failed")
	}
	defer other.Close()
	var count int
	var raw string
	if err = other.QueryRow("SELECT count(*),string_agg(row_to_json(e)::text,'') FROM studio_audit_events e WHERE request_id=$1", id).Scan(&count, &raw); err != nil {
		t.Fatal(err)
	}
	if count != 2 || !strings.Contains(raw, "synthetic-tenant") {
		t.Fatal("audit phases not shared across connections")
	}
	for _, secret := range []string{"secret-path", "secret-code", "secret-prompt", "secret-value"} {
		if strings.Contains(raw, secret) {
			t.Fatal("audit leaked sensitive data")
		}
	}
	// A failed database write must precede (and prevent) the business action.
	broken, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	failedOps, err := OpenPostgresOperations(broken)
	if err != nil {
		t.Fatal(err)
	}
	defer failedOps.Close()
	broken.Close()
	mutated = false
	w = httptest.NewRecorder()
	failedOps.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		SetAuditIdentity(r.Context(), "synthetic-actor", "synthetic-tenant", "owner")
		if err := BeginAuditMutation(r.Context()); err != nil {
			http.Error(w, "audit unavailable", 503)
			return
		}
		mutated = true
	})).ServeHTTP(w, httptest.NewRequest("POST", "/", nil))
	if w.Code != 503 || mutated {
		t.Fatal("database audit failure allowed mutation")
	}
}
