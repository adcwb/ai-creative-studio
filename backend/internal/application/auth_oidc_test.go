package application

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"golang.org/x/oauth2"
)

func TestOIDCVerifiedLoginAndRejectedClaims(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	signing, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key}, (&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", "synthetic-key"))
	if err != nil {
		t.Fatal(err)
	}
	nonce, challenge := "", ""
	calls := 0
	mutation := ""
	var issuer *httptest.Server
	issuer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			_ = json.NewEncoder(w).Encode(map[string]any{"issuer": issuer.URL, "authorization_endpoint": issuer.URL + "/authorize", "token_endpoint": issuer.URL + "/token", "jwks_uri": issuer.URL + "/keys", "id_token_signing_alg_values_supported": []string{"RS256"}})
		case "/keys":
			_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &key.PublicKey, KeyID: "synthetic-key", Algorithm: "RS256", Use: "sig"}}})
		case "/token":
			calls++
			_ = r.ParseForm()
			if oauth2.S256ChallengeFromVerifier(r.Form.Get("code_verifier")) != challenge || r.Form.Get("grant_type") != "authorization_code" {
				http.Error(w, "invalid proof key", 400)
				return
			}
			claims := map[string]any{"iss": issuer.URL, "sub": "synthetic-subject", "aud": "studio-test", "exp": time.Now().Add(time.Minute).Unix(), "iat": time.Now().Unix(), "nonce": nonce, "email": "oidc@example.test", "email_verified": true}
			switch mutation {
			case "nonce":
				claims["nonce"] = "wrong"
			case "audience":
				claims["aud"] = "other"
			case "expired":
				claims["exp"] = time.Now().Add(-time.Minute).Unix()
			case "issuer":
				claims["iss"] = "https://other.test"
			case "subject":
				claims["sub"] = ""
			case "unverified":
				claims["email_verified"] = false
			case "azp":
				claims["aud"] = []string{"studio-test", "other"}
				claims["azp"] = "other"
			}
			raw, _ := json.Marshal(claims)
			signed, _ := signing.Sign(raw)
			serialized, _ := signed.CompactSerialize()
			if mutation == "signature" {
				parts := strings.Split(serialized, ".")
				parts[2] = strings.Repeat("a", len(parts[2]))
				serialized = strings.Join(parts, ".")
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "synthetic-unused-access", "token_type": "Bearer", "id_token": serialized})
		default:
			http.NotFound(w, r)
		}
	}))
	defer issuer.Close()
	oidcLogin, err := newOIDCLogin(context.Background(), issuer.URL, "studio-test", "", "http://localhost:5173/api/v1/auth/sso/callback", issuer.Client(), true)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	auth, err := OpenAuthStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	h := SecureHandler(testStore(t), "http://localhost:5173", auth, AuthOptions{AllowRegistration: true, OIDC: oidcLogin})
	start := func() (string, *http.Cookie) {
		w := authRequest(t, h, "POST", "/api/v1/auth/sso/start", `{}`, "", "", 200)
		var response map[string]string
		_ = json.Unmarshal(w.Body.Bytes(), &response)
		u, err := url.Parse(response["authorization_url"])
		if err != nil {
			t.Fatal(err)
		}
		nonce = u.Query().Get("nonce")
		challenge = u.Query().Get("code_challenge")
		if u.Query().Get("code_challenge_method") != "S256" || nonce == "" {
			t.Fatal("missing PKCE or nonce")
		}
		return u.Query().Get("state"), w.Result().Cookies()[0]
	}
	callback := func(state string, cookie *http.Cookie, want int) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "/api/v1/auth/sso/callback?state="+state+"&code=synthetic-code", nil)
		if cookie != nil {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("callback %s got %d want %d: %s", mutation, w.Code, want, w.Body.String())
		}
		return w
	}
	state, cookie := start()
	callback(state, nil, 400)
	if calls != 0 {
		t.Fatal("unbound callback exchanged code")
	}
	w := callback(state, cookie, 303)
	if w.Header().Get("Location") != "http://localhost:5173/" {
		t.Fatal("open redirect")
	}
	callback(state, cookie, 400)
	if calls != 1 {
		t.Fatal("state replay exchanged code")
	}
	found := false
	for _, c := range w.Result().Cookies() {
		if c.Name == "studio_session" {
			if _, _, ok := auth.authenticate(c.Value); !ok {
				t.Fatal("missing SSO session")
			}
			found = true
		}
	}
	if !found {
		t.Fatal("no session cookie")
	}
	if _, err = OpenAuthStore(dir); err != nil {
		t.Fatal("SSO state recovery", err)
	}
	for _, bad := range []string{"nonce", "audience", "expired", "issuer", "subject", "unverified", "azp", "signature"} {
		mutation = bad
		state, cookie = start()
		callback(state, cookie, 401)
	}
	mutation = ""
	state, cookie = start()
	oidcLogin.mu.Lock()
	p := oidcLogin.pending[state]
	p.Expires = time.Now().Add(-time.Second)
	oidcLogin.pending[state] = p
	oidcLogin.mu.Unlock()
	callback(state, cookie, 400)
	if _, err = newOIDCLogin(context.Background(), issuer.URL, "studio-test", "", "http://localhost/callback", issuer.Client(), false); err == nil {
		t.Fatal("insecure production issuer accepted")
	}
}
func TestOIDCDoesNotLinkEmailAndDisallowsPasswordLogin(t *testing.T) {
	auth, err := OpenAuthStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, err = auth.login("local@example.test", "synthetic-password", true)
	if err != nil {
		t.Fatal(err)
	}
	identity := SSOIdentity{Issuer: "https://issuer.test", Subject: "external-subject", Email: "local@example.test", EmailVerified: true}
	if _, _, _, err = auth.loginSSO(identity, true); err == nil {
		t.Fatal("SSO silently claimed local email")
	}
	identity.Email = "invalid email"
	if _, _, _, err = auth.loginSSO(identity, true); err == nil {
		t.Fatal("invalid SSO email accepted")
	}
	identity.Email = "sso@example.test"
	if _, _, _, err = auth.loginSSO(identity, false); err == nil {
		t.Fatal("SSO registration bypass")
	}
	user, _, _, err := auth.loginSSO(identity, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err = auth.login(user.Email, "synthetic-password", false); err == nil {
		t.Fatal("SSO account accepted password")
	}
	_, oldToken, _, err := auth.loginSSO(identity, false)
	if err != nil {
		t.Fatal(err)
	}
	identity.Email = "changed@example.test"
	updated, _, _, err := auth.loginSSO(identity, false)
	if err != nil || updated.ID != user.ID || updated.Email != identity.Email {
		t.Fatal("verified email change not applied", updated, err)
	}
	if _, _, ok := auth.authenticate(oldToken); ok {
		t.Fatal("old SSO directory session survived email change")
	}
	identity.Email = "local@example.test"
	if _, _, _, err = auth.loginSSO(identity, false); err == nil {
		t.Fatal("SSO email change merged local account")
	}

}
