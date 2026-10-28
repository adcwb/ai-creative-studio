package studio

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func authRequest(t *testing.T, h http.Handler, method, path, body, token, csrf string, want int) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.RemoteAddr = "127.0.0.1:1234"
	r.Header.Set("Origin", "http://localhost:5173")
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Idempotency-Key", "auth-test-operation")
	r.Header.Set("X-CSRF-Token", csrf)
	if token != "" {
		r.AddCookie(&http.Cookie{Name: "studio_session", Value: token})
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != want {
		t.Fatalf("%s %s: got %d want %d: %s", method, path, w.Code, want, w.Body.String())
	}
	return w
}
func TestAuthTenantRBACAndResourceIsolation(t *testing.T) {
	s := testStore(t)
	auth, err := OpenAuthStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a, ta, sa, err := auth.login("alice@example.test", "synthetic-password-A", true)
	if err != nil {
		t.Fatal(err)
	}
	b, tb, sb, err := auth.login("bob@example.test", "synthetic-password-B", true)
	if err != nil {
		t.Fatal(err)
	}
	oa, ob := auth.spaces(a.ID)[0]["id"], auth.spaces(b.ID)[0]["id"]
	h := SecureHandler(s, "http://localhost:5173", auth, AuthOptions{AllowRegistration: true})
	authRequest(t, h, "GET", "/api/v1/projects?space_id="+oa, "", "", "", 401)
	authRequest(t, h, "GET", "/api/v1/projects?space_id="+oa, "", tb, "", 403)
	authRequest(t, h, "GET", "/api/v1/projects?space_id=personal-demo", "", ta, "", 403)
	w := authRequest(t, h, "POST", "/api/v1/projects?space_id="+oa, `{"title":"A project","brief":"Synthetic story"}`, ta, sa.CSRF, 201)
	var p Project
	if err = json.Unmarshal(w.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/v1/projects/" + p.ID, "/api/v1/projects/" + p.ID + "/media", "/api/v1/projects/" + p.ID + "/media/assets/guess/file", "/api/v1/projects/" + p.ID + "/media/exports/guess/file", "/api/v1/projects/" + p.ID + "/media/exports/guess/subtitles", "/api/v1/projects/" + p.ID + "/generations", "/api/v1/projects/" + p.ID + "/storyboard-generations", "/api/v1/projects/" + p.ID + "/history", "/api/v1/tasks/guess", "/api/v1/tasks/guess/result"} {
		authRequest(t, h, "GET", path+"?space_id="+ob, "", tb, "", 404)
	}
	authRequest(t, h, "PUT", "/api/v1/projects/"+p.ID+"?space_id="+oa, `{}`, ta, "", 403)
	memberPath := "/api/v1/organizations/" + oa + "/members"
	authRequest(t, h, "PUT", memberPath, `{"email":"bob@example.test","role":"viewer"}`, ta, sa.CSRF, 200)
	authRequest(t, h, "GET", "/api/v1/projects/"+p.ID+"?space_id="+oa, "", tb, "", 200)
	authRequest(t, h, "POST", "/api/v1/projects?space_id="+oa, `{"title":"Forbidden","brief":"Synthetic story"}`, tb, sb.CSRF, 403)
	authRequest(t, h, "PUT", memberPath, `{"email":"bob@example.test","role":"admin"}`, tb, sb.CSRF, 403)
	authRequest(t, h, "PUT", memberPath, `{"email":"bob@example.test","role":"admin"}`, ta, sa.CSRF, 200)
	authRequest(t, h, "PUT", memberPath, `{"email":"alice@example.test","role":"remove"}`, tb, sb.CSRF, 403)
	authRequest(t, h, "PUT", memberPath, `{"email":"bob@example.test","role":"remove"}`, ta, sa.CSRF, 200)
	authRequest(t, h, "GET", "/api/v1/projects/"+p.ID+"?space_id="+oa, "", tb, "", 403)
	authRequest(t, h, "POST", "/api/v1/auth/logout", `{}`, ta, sa.CSRF, 200)
	authRequest(t, h, "GET", "/api/v1/projects?space_id="+oa, "", ta, "", 401)
}
func TestAuthCookieCSRFRecoveryAndDefaultMode(t *testing.T) {
	dir := t.TempDir()
	auth, err := OpenAuthStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	s := testStore(t)
	h := SecureHandler(s, "http://localhost:5173", auth, AuthOptions{AllowRegistration: true, SecureCookie: true})
	w := authRequest(t, h, "POST", "/api/v1/auth/register", `{"email":"account@example.test","password":"synthetic-password"}`, "", "", 200)
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || !cookies[0].Secure || cookies[0].SameSite != http.SameSiteLaxMode {
		t.Fatal("unsafe cookie", cookies)
	}
	var state struct {
		CSRF string `json:"csrf_token"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &state)
	token := cookies[0].Value
	b, err := os.ReadFile(filepath.Join(dir, "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "synthetic-password") || strings.Contains(string(b), token) {
		t.Fatal("stored plaintext password/session")
	}
	restored, err := OpenAuthStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, ok := restored.authenticate(token); !ok {
		t.Fatal("session lost on restart")
	}
	t.Run("write requires Origin", func(t *testing.T) {
		r := httptest.NewRequest("POST", "/api/v1/auth/logout", strings.NewReader(`{}`))
		r.AddCookie(cookies[0])
		r.Header.Set("X-CSRF-Token", state.CSRF)
		out := httptest.NewRecorder()
		h.ServeHTTP(out, r)
		if out.Code != 403 {
			t.Fatal("missing Origin accepted", out.Code)
		}
	})
	authRequest(t, h, "POST", "/api/v1/auth/logout", `{}`, token, "bad-csrf", 403)
	restored.mu.Lock()
	ss := restored.data.Sessions[authDigest(token)]
	ss.Expires = time.Now().Add(-time.Second)
	restored.data.Sessions[authDigest(token)] = ss
	restored.mu.Unlock()
	if _, _, ok := restored.authenticate(token); ok {
		t.Fatal("expired session accepted")
	}
	t.Setenv("AUTH_TRUSTED_PROXY_CIDRS", "")
	t.Setenv("MODEL_CONFIG_KEK_BASE64", "")
	t.Setenv("OIDC_ISSUER", "")
	t.Setenv("OIDC_CLIENT_ID", "")
	t.Setenv("OIDC_REDIRECT_URL", "")
	t.Setenv("OIDC_CLIENT_SECRET", "")
	t.Setenv("AUTH_MODE", "")
	t.Setenv("AUTH_ALLOW_REGISTRATION", "")
	t.Setenv("AUTH_COOKIE_SECURE", "")
	defaultHandler, err := HandlerFromEnv(s, "http://localhost:5173", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	authRequest(t, defaultHandler, "GET", "/api/v1/projects?space_id=personal-demo", "", "", "", 401)
	authRequest(t, defaultHandler, "POST", "/api/v1/auth/register", `{"email":"account@example.test","password":"synthetic-password"}`, "", "", 403)
	if err = os.WriteFile(filepath.Join(dir, "auth.json"), []byte(`{"version":1}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = OpenAuthStore(dir); err == nil {
		t.Fatal("corrupt auth store reset")
	}
}
func TestAuthLoginFailureThrottleAndCredentials(t *testing.T) {
	auth, err := OpenAuthStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err = auth.login("synthetic@example.test", "short", true); err == nil {
		t.Fatal("weak password accepted")
	}
	user, token, _, err := auth.login("synthetic@example.test", "synthetic-password", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err = auth.login(user.Email, "different-password", false); err == nil {
		t.Fatal("incorrect password accepted")
	}
	if _, _, _, err = auth.login(user.Email, "synthetic-password", false); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := auth.authenticate(token + "x"); ok {
		t.Fatal("invalid token accepted")
	}
	for i := 0; i < 20; i++ {
		if !auth.allowAttempt("127.0.0.1") {
			t.Fatal("premature throttle")
		}
	}
	if auth.allowAttempt("127.0.0.1") {
		t.Fatal("no login throttle")
	}
}

func TestAuthTrustedProxyAddressCannotBeSpoofed(t *testing.T) {
	trusted, err := parseTrustedAuthProxies("10.0.0.0/24")
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "/api/v1/auth/login", nil)
	r.RemoteAddr = "192.0.2.4:123"
	r.Header.Set("X-Real-IP", "198.51.100.1")
	if got := authClientIP(r, trusted); got != "192.0.2.4" {
		t.Fatal("untrusted forwarding header accepted", got)
	}
	r.RemoteAddr = "10.0.0.5:123"
	if got := authClientIP(r, trusted); got != "198.51.100.1" {
		t.Fatal("trusted client not resolved", got)
	}
	r.Header.Set("X-Forwarded-For", "203.0.113.9")
	r.Header.Set("X-Real-IP", "invalid,198.51.100.1")
	if got := authClientIP(r, trusted); got != "" {
		t.Fatal("invalid proxy IP accepted", got)
	}
	if _, err = parseTrustedAuthProxies("*"); err == nil {
		t.Fatal("invalid proxy configuration accepted")
	}
}

func TestAuthAuditRejectsMutationAndDoesNotLogCredentials(t *testing.T) {
	dir := t.TempDir()
	auth, err := OpenAuthStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	auth.models, err = OpenModelConfigStore(dir, []byte("synthetic-key-material-32-bytes!!"))
	if err == nil {
		t.Fatal("test key should be wrong length")
	}
	auth.models, err = OpenModelConfigStore(dir, []byte(strings.Repeat("s", 32)))
	if err != nil {
		t.Fatal(err)
	}
	user, token, session, err := auth.login("audit@example.test", "synthetic-audit-password", true)
	if err != nil {
		t.Fatal(err)
	}
	org := auth.spaces(user.ID)[0]["id"]
	store := testStore(t)
	ops, err := OpenOperations(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer ops.Close()
	h := ops.Wrap(SecureHandler(store, "http://localhost:5173", auth, AuthOptions{}))
	for i := 0; i < 20; i++ {
		authRequest(t, h, "GET", "/api/v1/projects?space_id="+org, "", "", "", 401)
	}
	contents, err := os.ReadFile(filepath.Join(dir, "audit.jsonl"))
	if err != nil || len(contents) != 0 {
		t.Fatal("anonymous request consumed audit capacity", err)
	}
	secret := "synthetic-audit-api-secret"
	body := map[string]any{"expected_version": 0, "entry": syntheticModel(), "credential": ModelCredentialInput{Action: "set", Secret: secret}}
	raw, _ := json.Marshal(body)
	authRequest(t, h, "PUT", "/api/v1/model-config?space_id="+org, string(raw), token, session.CSRF, 200)
	contents, err = os.ReadFile(filepath.Join(dir, "audit.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(contents), user.ID) || !strings.Contains(string(contents), org) {
		t.Fatal("missing trusted audit identity")
	}
	for _, sensitive := range []string{secret, token, session.CSRF, "synthetic-audit-password"} {
		if strings.Contains(string(contents), sensitive) {
			t.Fatal("credential leaked to audit")
		}
	}
	ops.mu.Lock()
	ops.failed = true
	ops.mu.Unlock()
	authRequest(t, h, "POST", "/api/v1/projects?space_id="+org, `{"title":"Blocked","brief":"Synthetic blocked mutation"}`, token, session.CSRF, 503)
	if len(store.ListProjects(org)) != 0 {
		t.Fatal("mutation executed without audit")
	}
}

func TestAuthLoginAuditFailureDoesNotCommit(t *testing.T) {
	auth, err := OpenAuthStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	checked := false
	_, _, _, err = auth.login("audit-new@example.test", "synthetic-password", true, func(user AuthUser) error { checked = user.ID != ""; return errAuthAudit })
	if !errors.Is(err, errAuthAudit) || !checked || len(auth.data.Users) != 0 || len(auth.data.Sessions) != 0 {
		t.Fatal("registration committed without durable audit", err)
	}
	_, _, _, err = auth.login("audit-new@example.test", "synthetic-password", true)
	if err != nil {
		t.Fatal(err)
	}
	sessions := len(auth.data.Sessions)
	checked = false
	_, _, _, err = auth.login("audit-new@example.test", "wrong-password", false, func(AuthUser) error { checked = true; return nil })
	if err == nil || checked {
		t.Fatal("invalid credentials generated trusted audit")
	}
	_, _, _, err = auth.login("audit-new@example.test", "synthetic-password", false, func(AuthUser) error { return errAuthAudit })
	if !errors.Is(err, errAuthAudit) || len(auth.data.Sessions) != sessions {
		t.Fatal("login committed without durable audit", err)
	}
}

func TestAuthSSOMemberRequiresImmutableID(t *testing.T) {
	auth, err := OpenAuthStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	owner, token, session, err := auth.login("owner@example.test", "synthetic-password", true)
	if err != nil {
		t.Fatal(err)
	}
	member, _, _, err := auth.loginSSO(SSOIdentity{Issuer: "https://issuer.test", Subject: "stable-subject", Email: "sso-member@example.test", EmailVerified: true}, true)
	if err != nil {
		t.Fatal(err)
	}
	org := auth.spaces(owner.ID)[0]["id"]
	h := SecureHandler(testStore(t), "http://localhost:5173", auth, AuthOptions{})
	path := "/api/v1/organizations/" + org + "/members"
	authRequest(t, h, "PUT", path, `{"email":"sso-member@example.test","role":"viewer"}`, token, session.CSRF, 403)
	authRequest(t, h, "PUT", path, `{"email":"sso-member@example.test","role":"viewer","user_id":"wrong-user"}`, token, session.CSRF, 403)
	authRequest(t, h, "PUT", path, `{"email":"sso-member@example.test","role":"viewer","user_id":"`+member.ID+`"}`, token, session.CSRF, 200)
}
