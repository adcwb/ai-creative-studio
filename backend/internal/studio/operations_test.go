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
