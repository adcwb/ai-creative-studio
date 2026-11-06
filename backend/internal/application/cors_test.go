package application

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// Open CORS belongs only to explicit, unauthenticated demo mode. Secure mode's
// trusted-source and authenticated CSRF regressions live in auth_security_test.
func TestDemoCORSAllowsAllOrigins(t *testing.T) {
	h := Handler(testStore(t), "http://localhost:5173")
	for _, origin := range []string{"http://localhost:5173", "http://127.0.0.1:3000", "https://another.example", "null"} {
		for _, method := range []string{http.MethodGet, http.MethodOptions} {
			r := httptest.NewRequest(method, "/api/v1/spaces", nil)
			r.Header.Set("Origin", origin)
			if method == http.MethodOptions {
				r.Header.Set("Access-Control-Request-Method", "PATCH")
				r.Header.Set("Access-Control-Request-Headers", "content-type, idempotency-key, x-csrf-token")
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			want := 200
			if method == http.MethodOptions {
				want = 204
			}
			if w.Code != want || w.Header().Get("Access-Control-Allow-Origin") != origin || w.Header().Get("Access-Control-Allow-Credentials") != "true" {
				t.Fatalf("demo CORS %s %s: %d", method, origin, w.Code)
			}
		}
	}
}
