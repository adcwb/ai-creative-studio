package application

import (
	"encoding/json"
	"github.com/adcwb/ai-creative-studio/backend/internal/platform/deployconfig"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestSecureOriginRejectsCrossSourceReadsAndWrites(t *testing.T) {
	a, err := OpenAuthStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	owner, token, ss, err := a.login("owner@example.test", "synthetic-password", true)
	if err != nil {
		t.Fatal(err)
	}
	member, _, _, err := a.login("member@example.test", "synthetic-password", true)
	if err != nil {
		t.Fatal(err)
	}
	team := testTeamSpace(t, a, owner.ID)
	t.Setenv("STUDIO_CONFIG_FILE", filepath.Join(t.TempDir(), "config.json"))
	config, err := deployconfig.Open()
	if err != nil {
		t.Fatal(err)
	}
	report, _ := config.Report()
	h := SecureHandler(testStore(t), "https://studio.example.test", a, AuthOptions{SystemConfig: config, SystemAdminUserIDs: []string{owner.ID}, AllowedOrigins: []string{"https://trusted.example.test"}})
	for _, origin := range []string{"https://evil.example.test", "https://studio.example.test.evil.test", "null", "https://studio.example.test/path", "https://studio.example.test?x=1"} {
		for _, route := range []struct{ method, path, body string }{
			{"GET", "/api/v1/auth/session", ""}, {"GET", "/api/v1/system-config", ""}, {"OPTIONS", "/api/v1/system-config", ""},
			{"PUT", "/api/v1/organizations/" + team + "/members", `{"email":"member@example.test","user_id":"` + member.ID + `","role":"admin","expected_version":1}`},
			{"PATCH", "/api/v1/system-config", `{"expected_version":"` + report.Version + `","changes":{"CONTENT_MODERATION_MODE":"required"}}`},
		} {
			r := httptest.NewRequest(route.method, route.path, strings.NewReader(route.body))
			r.Header.Set("Origin", origin)
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("X-CSRF-Token", ss.CSRF)
			r.Header.Set("X-Forwarded-Host", "studio.example.test")
			r.AddCookie(&http.Cookie{Name: "studio_session", Value: token})
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != 403 || w.Header().Get("Access-Control-Allow-Origin") != "" || w.Header().Get("Access-Control-Allow-Credentials") != "" || strings.Contains(w.Body.String(), ss.CSRF) {
				t.Fatalf("untrusted origin accepted %s %s %d", origin, route.path, w.Code)
			}
		}
	}
	if a.role(member.ID, team) != "" {
		t.Fatal("cross-origin member mutation")
	}
	after, _ := config.Report()
	if after.Version != report.Version {
		t.Fatal("cross-origin configuration mutation")
	}
	for _, origin := range []string{"https://studio.example.test", "https://trusted.example.test"} {
		preflight := httptest.NewRequest("OPTIONS", "/api/v1/system-config", nil)
		preflight.Header.Set("Origin", origin)
		preflight.Header.Set("Access-Control-Request-Method", "PATCH")
		pw := httptest.NewRecorder()
		h.ServeHTTP(pw, preflight)
		if pw.Code != 204 || pw.Header().Get("Access-Control-Allow-Origin") != origin || !strings.Contains(pw.Header().Get("Access-Control-Allow-Methods"), "PATCH") {
			t.Fatal("trusted PATCH preflight failed")
		}

		r := httptest.NewRequest("GET", "/api/v1/auth/session", nil)
		r.Header.Set("Origin", origin)
		r.AddCookie(&http.Cookie{Name: "studio_session", Value: token})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 200 || w.Header().Get("Access-Control-Allow-Origin") != origin || !strings.Contains(w.Body.String(), ss.CSRF) {
			t.Fatal("explicit trusted origin failed", origin, w.Code)
		}
	}
}
func TestAdminEmailClaimDoesNotGrantDeploymentRights(t *testing.T) {
	a, err := OpenAuthStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// An account squatted before this upgrade must also fail closed.
	user, token, _, err := a.login("reserved@example.test", "synthetic-password", true)
	if err != nil {
		t.Fatal(err)
	}
	h := SecureHandler(testStore(t), "http://localhost:5173", a, AuthOptions{AllowRegistration: true, ReservedAdminEmails: []string{"reserved@example.test", "new-reserved@example.test"}})
	w := authRequest(t, h, "GET", "/api/v1/auth/session", "", token, "", 200)
	var session map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &session)
	if session["deployment_admin"] != false {
		t.Fatal("unverified email became administrator")
	}
	authRequest(t, h, "GET", "/api/v1/system-config", "", token, "", 403)
	authRequest(t, h, "POST", "/api/v1/auth/register", `{"email":"new-reserved@example.test","password":"synthetic-password"}`, "", "", 403)
	sso, ssoToken, _, err := a.loginSSO(SSOIdentity{Issuer: "https://issuer.example.test", Subject: "stable-test-subject", Email: "new-reserved@example.test", EmailVerified: true}, true)
	if err != nil || sso.ID == "" {
		t.Fatal("SSO fixture", err)
	}
	sw := authRequest(t, h, "GET", "/api/v1/auth/session", "", ssoToken, "", 200)
	var state map[string]any
	_ = json.Unmarshal(sw.Body.Bytes(), &state)
	if state["deployment_admin"] != false {
		t.Fatal("verified SSO email implicitly became administrator")
	}
	if user.ID == "" {
		t.Fatal("fixture missing identity")
	}
}
func TestMemberEmailAloneNeverAuthorizesUnverifiedAccount(t *testing.T) {
	a, err := OpenAuthStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	owner, token, ss, _ := a.login("owner@example.test", "synthetic-password", true)
	claimed, _, _, _ := a.login("victim@example.test", "synthetic-password", true)
	team := testTeamSpace(t, a, owner.ID)
	path := "/api/v1/organizations/" + team + "/members"
	h := SecureHandler(testStore(t), "http://localhost:5173", a, AuthOptions{})
	authRequest(t, h, "PUT", path, `{"email":"victim@example.test","role":"admin"}`, token, ss.CSRF, 403)
	authRequest(t, h, "PUT", path, `{"email":"victim@example.test","role":"admin","user_id":"wrong"}`, token, ss.CSRF, 403)
	if a.role(claimed.ID, team) != "" {
		t.Fatal("email claim silently enrolled member")
	}
	// Only an explicit identity selected by the owner can be added.
	authRequest(t, h, "PUT", path, `{"email":"victim@example.test","role":"editor","user_id":"`+claimed.ID+`"}`, token, ss.CSRF, 200)
}

func TestBootstrapIdentityIsOfflineAndDoesNotOverwriteAccounts(t *testing.T) {
	dir := t.TempDir()
	user, err := BootstrapLocalAccount(dir, nil, "offline@example.test", "synthetic-password")
	if err != nil {
		t.Fatal(err)
	}
	a, err := OpenAuthStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(a.data.Sessions) != 0 {
		t.Fatal("bootstrap left an authenticated session")
	}
	if _, err = BootstrapLocalAccount(dir, nil, "offline@example.test", "different-synthetic-password"); err == nil {
		t.Fatal("bootstrap overwrote an existing identity")
	}
	again, _, _, err := a.login("offline@example.test", "synthetic-password", false)
	if err != nil || again.ID != user.ID {
		t.Fatal("offline identity not durable", err)
	}
}

func TestFourRoleHTTPPermissionMatrix(t *testing.T) {
	a, err := OpenAuthStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	type actor struct {
		user    AuthUser
		token   string
		session authSession
	}
	actors := map[string]actor{}
	for _, role := range []string{"owner", "admin", "editor", "viewer"} {
		u, tok, ss, e := a.login(role+"-matrix@example.test", "synthetic-password", true)
		if e != nil {
			t.Fatal(e)
		}
		actors[role] = actor{u, tok, ss}
	}
	team := testTeamSpace(t, a, actors["owner"].user.ID)
	a.mu.Lock()
	d := a.clone()
	o := d.Organizations[team]
	for role, v := range actors {
		if role != "owner" {
			o.Members[v.user.ID] = role
		}
	}
	d.Organizations[team] = o
	err = a.commit(d)
	a.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("STUDIO_CONFIG_FILE", filepath.Join(t.TempDir(), "config.json"))
	config, err := deployconfig.Open()
	if err != nil {
		t.Fatal(err)
	}
	h := SecureHandler(testStore(t), "http://localhost:5173", a, AuthOptions{SystemConfig: config, SystemAdminUserIDs: []string{actors["owner"].user.ID}})
	for _, role := range []string{"owner", "admin", "editor", "viewer"} {
		v := actors[role]
		t.Run(role, func(t *testing.T) {
			for _, path := range []string{"/api/v1/projects?space_id=" + team, "/api/v1/model-usage?space_id=" + team, "/api/v1/organizations/" + team + "/members", "/api/v1/organizations/" + team + "/access"} {
				authRequest(t, h, "GET", path, "", v.token, "", 200)
			}
			manage := 403
			if role == "owner" || role == "admin" {
				manage = 200
			}
			authRequest(t, h, "GET", "/api/v1/model-management?space_id="+team, "", v.token, "", manage)
			global := 403
			if role == "owner" {
				global = 200
			}
			authRequest(t, h, "GET", "/api/v1/system-config", "", v.token, "", global)
			authRequest(t, h, "GET", "/api/v1/projects?space_id=foreign-tenant", "", v.token, "", 403)
			req := httptest.NewRequest("POST", "/api/v1/projects?space_id="+team, strings.NewReader(`{"title":"Matrix project","brief":"Synthetic role matrix"}`))
			req.Header.Set("Origin", "http://localhost:5173")
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-CSRF-Token", v.session.CSRF)
			req.Header.Set("Idempotency-Key", "matrix-"+role)
			req.AddCookie(&http.Cookie{Name: "studio_session", Value: v.token})
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			expected := 201
			if role == "viewer" {
				expected = 403
			}
			if w.Code != expected {
				t.Fatalf("workspace write: %d %s", w.Code, w.Body.String())
			}
			if a.can(v.user.ID, team, "models.execute") != (role != "viewer") {
				t.Fatal("runtime execution role mismatch")
			}
		})
	}
}
func TestPostgresAuthenticationFailureNeverUsesCachedAdmin(t *testing.T) {
	db := postgresTestDB(t)
	a, err := OpenPostgresAuthStore(t.TempDir(), db)
	if err != nil {
		t.Fatal(err)
	}
	user, token, _, err := a.login("cached-admin@example.test", "synthetic-password", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, ok := a.authenticate(token); !ok {
		t.Fatal("fixture session")
	}
	if _, err = db.Exec(`ALTER TABLE studio_space_memberships RENAME TO unavailable_memberships`); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := a.authenticate(token); ok {
		t.Fatal("failed relational authorization reused cached session")
	}
	if a.role(user.ID, "unknown") != "" || len(a.spaces(user.ID)) != 0 || a.can(user.ID, "unknown", "workspace.read") {
		t.Fatal("failed storage leaked cached authorization")
	}
	h := SecureHandler(testStore(t), "http://localhost:5173", a, AuthOptions{SystemAdminUserIDs: []string{user.ID}})
	authRequest(t, h, "GET", "/api/v1/system-config", "", token, "", 503)
}
