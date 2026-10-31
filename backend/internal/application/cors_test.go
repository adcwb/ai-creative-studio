package application

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCORSAllowsAllOrigins(t *testing.T) {
	s := testStore(t)
	auth, err := OpenAuthStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	user, token, session, err := auth.login("cors@example.test", "synthetic-password", true)
	if err != nil {
		t.Fatal(err)
	}
	space := auth.spaces(user.ID)[0]["id"]
	handlers := map[string]http.Handler{
		"demo":   Handler(s, "http://localhost:5173"),
		"secure": SecureHandler(s, "http://localhost:5173", auth, AuthOptions{}),
	}
	for mode, h := range handlers {
		t.Run(mode, func(t *testing.T) {
			for _, origin := range []string{"http://localhost:5173", "http://127.0.0.1:3000", "https://another.example", "null"} {
				t.Run(origin, func(t *testing.T) {
					for _, method := range []string{http.MethodGet, http.MethodOptions} {
						r := httptest.NewRequest(method, "/api/v1/spaces", nil)
						r.Header.Set("Origin", origin)
						if method == http.MethodOptions {
							r.Header.Set("Access-Control-Request-Method", "PATCH")
							r.Header.Set("Access-Control-Request-Headers", "content-type, idempotency-key, x-csrf-token")
						} else {
							r.AddCookie(&http.Cookie{Name: "studio_session", Value: token})
						}
						w := httptest.NewRecorder()
						h.ServeHTTP(w, r)
						want := http.StatusOK
						if method == http.MethodOptions {
							want = http.StatusNoContent
							if w.Header().Get("Access-Control-Allow-Methods") != "GET, POST, PUT, PATCH, OPTIONS" || w.Header().Get("Access-Control-Allow-Headers") != "Content-Type, Idempotency-Key, X-CSRF-Token" {
								t.Fatal("preflight omitted supported methods or headers", w.Header())
							}
						}
						if w.Code != want || w.Header().Get("Access-Control-Allow-Origin") != origin || w.Header().Get("Access-Control-Allow-Credentials") != "true" || w.Header().Get("Vary") != "Origin" {
							t.Fatalf("%s: status=%d headers=%v body=%s", method, w.Code, w.Header(), w.Body.String())
						}
					}
					if mode == "secure" {
						path := "/api/v1/projects?space_id=" + space
						for _, check := range []struct {
							token, csrf string
							status      int
						}{{"", "", http.StatusUnauthorized}, {token, "", http.StatusForbidden}, {token, "bad-csrf", http.StatusForbidden}, {token, session.CSRF, http.StatusCreated}} {
							r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"title":"CORS project","brief":"Synthetic story"}`))
							r.Header.Set("Origin", origin)
							r.Header.Set("Content-Type", "application/json")
							r.Header.Set("Idempotency-Key", "cors-"+authDigest(origin))
							r.Header.Set("X-CSRF-Token", check.csrf)
							r.AddCookie(&http.Cookie{Name: "studio_session", Value: check.token})
							w := httptest.NewRecorder()
							h.ServeHTTP(w, r)
							if w.Code != check.status || w.Header().Get("Access-Control-Allow-Origin") != origin {
								t.Fatalf("cross-origin write: got %d want %d: %s", w.Code, check.status, w.Body.String())
							}
						}
					}
				})
			}
		})
	}
}
