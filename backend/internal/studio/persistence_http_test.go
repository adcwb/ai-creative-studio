package studio

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPersistenceResponseNeverReturnsSuccessAfterFailedRefresh(t *testing.T) {
	s := &FileStore{pg: &postgresScope{}}
	rec := httptest.NewRecorder()
	w := &persistenceResponse{ResponseWriter: rec, store: s, epoch: 0}
	w.Header().Set("Set-Cookie", "synthetic-session")
	w.Header().Set("Content-Length", "2")
	s.pg.failures.Add(1)
	w.Write([]byte("[]"))
	w.Write([]byte("not a success"))
	if rec.Code != 503 || rec.Header().Get("Set-Cookie") != "" || strings.Contains(rec.Body.String(), "not a success") || !strings.Contains(rec.Body.String(), "storage_unavailable") {
		t.Fatal("failed read presented a successful response", rec.Code, rec.Body.String())
	}
}

func TestPostgresPersistenceGuardMidRequestFailure(t *testing.T) {
	db := postgresTestDB(t)
	s := postgresTestStore(t, db)
	w := httptest.NewRecorder()
	s.GuardPersistence(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		db.Close() // failure after preflight, before the domain list refresh
		respond(w, 200, s.List("synthetic-tenant"))
	})).ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/tasks", nil))
	if w.Code != 503 {
		t.Fatal("mid-request database failure became empty success", w.Code, w.Body.String())
	}
}
