package studio

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOperationsAuditPrivacyAndMetrics(t *testing.T) {
	dir := t.TempDir()
	ops, err := OpenOperations(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer ops.Close()
	handler := ops.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		SetAuditIdentity(r.Context(), "user-test", "org-test", "owner")
		if err := BeginAuditMutation(r.Context()); err != nil {
			t.Fatal(err)
		}
		w.WriteHeader(201)
	}))
	request := httptest.NewRequest("POST", "/private-project?secret=hidden", strings.NewReader("private prompt"))
	request.Header.Set("Cookie", "secret-session")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != 201 || response.Header().Get("X-Request-ID") == "" {
		t.Fatal(response.Code)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "audit.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"private-project", "hidden", "private prompt", "secret-session"} {
		if strings.Contains(string(raw), secret) {
			t.Fatal("audit contains private data")
		}
	}
	if !strings.Contains(string(raw), `"actor_id":"user-test"`) || !strings.Contains(string(raw), `"tenant_id":"org-test"`) {
		t.Fatal("trusted identity missing")
	}
	if strings.Count(string(raw), "\n") != 2 {
		t.Fatal("missing audit phases")
	}
	metrics := httptest.NewRecorder()
	ops.MetricsHandler().ServeHTTP(metrics, httptest.NewRequest("GET", "/metrics", nil))
	if !strings.Contains(metrics.Body.String(), `studio_http_requests_total{class="2xx"} 1`) {
		t.Fatal(metrics.Body.String())
	}
}
func TestOperationsFailureBlocksMutation(t *testing.T) {
	ops, err := OpenOperations(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	_ = ops.Close()
	called := false
	handler := ops.Wrap(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("POST", "/api/v1/projects", nil))
	if w.Code != 503 || called {
		t.Fatal("mutation passed failed audit")
	}
}

func TestOperationsFailureBlocksSSOCallback(t *testing.T) {
	ops, err := OpenOperations(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	_ = ops.Close()
	called := false
	w := httptest.NewRecorder()
	ops.Wrap(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true })).ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/auth/sso/callback", nil))
	if w.Code != 503 || called {
		t.Fatal("SSO callback bypassed audit")
	}
}

func TestOperationsRejectsAuditSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "private")
	if err := os.WriteFile(target, []byte("untouched"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, "audit.jsonl")); err != nil {
		t.Fatal(err)
	}
	if ops, err := OpenOperations(dir); err == nil {
		ops.Close()
		t.Fatal("symlink audit accepted")
	}
	raw, _ := os.ReadFile(target)
	if string(raw) != "untouched" {
		t.Fatal("symlink destination changed")
	}
}

func TestAnonymousAndReadTrafficDoesNotExhaustAudit(t *testing.T) {
	dir := t.TempDir()
	ops, err := OpenOperations(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer ops.Close()
	handler := ops.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			w.WriteHeader(401)
		} else {
			w.WriteHeader(200)
		}
	}))
	for i := 0; i < 1000; i++ {
		for _, method := range []string{"GET", "POST"} {
			handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(method, "/healthz?untrusted=ignored", nil))
		}
	}
	info, err := os.Stat(filepath.Join(dir, "audit.jsonl"))
	if err != nil || info.Size() != 0 {
		t.Fatal("anonymous/read traffic appended audit", info, err)
	}
	ops.mu.Lock()
	requests := ops.counts[2] + ops.counts[4]
	ops.mu.Unlock()
	if requests != 2000 {
		t.Fatal("traffic metrics not aggregated")
	}
}
func TestAuthorizedMutationChecksDurabilityBeforeExecution(t *testing.T) {
	ops, err := OpenOperations(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	mutated := false
	handler := ops.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		SetAuditIdentity(r.Context(), "user", "org", "owner")
		_ = ops.Close()
		if err := BeginAuditMutation(r.Context()); err != nil {
			http.Error(w, "audit unavailable", 503)
			return
		}
		mutated = true
	}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("POST", "/api/v1/projects", nil))
	if response.Code != 503 || mutated {
		t.Fatal("mutation passed failed durable start")
	}
}
