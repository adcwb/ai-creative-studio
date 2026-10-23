package studio

import (
	"context"
	"crypto/subtle"
	"errors"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

type oidcPending struct {
	Nonce, Verifier, Binding string
	Expires                  time.Time
}
type OIDCLogin struct {
	mu       sync.Mutex
	pending  map[string]oidcPending
	config   oauth2.Config
	verifier *oidc.IDTokenVerifier
	client   *http.Client
	issuer   string
}

func newOIDCLogin(ctx context.Context, issuer, clientID, secret, callback string, client *http.Client, allowLocal bool) (*OIDCLogin, error) {
	validURL := func(raw string) bool {
		u, e := url.Parse(raw)
		return e == nil && u.Host != "" && u.User == nil && u.Fragment == "" && (u.Scheme == "https" || (allowLocal && u.Scheme == "http" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost")))
	}
	if !validURL(issuer) || !validURL(callback) || clientID == "" {
		return nil, errors.New("OIDC requires HTTPS issuer/callback and an explicit client ID")
	}
	ctx = oidc.ClientContext(ctx, client)
	provider, err := oidc.NewProvider(ctx, issuer)
	if err != nil {
		return nil, errors.New("OIDC discovery failed")
	}
	endpoint := provider.Endpoint()
	var metadata struct {
		JWKS string `json:"jwks_uri"`
	}
	if err = provider.Claims(&metadata); err != nil {
		return nil, errors.New("invalid OIDC discovery")
	}
	if !validURL(endpoint.AuthURL) || !validURL(endpoint.TokenURL) || !validURL(metadata.JWKS) {
		return nil, errors.New("OIDC endpoints must use HTTPS")
	}
	return &OIDCLogin{pending: map[string]oidcPending{}, config: oauth2.Config{ClientID: clientID, ClientSecret: secret, Endpoint: endpoint, RedirectURL: callback, Scopes: []string{oidc.ScopeOpenID, "email"}}, verifier: provider.Verifier(&oidc.Config{ClientID: clientID, SupportedSigningAlgs: []string{"RS256", "ES256"}}), client: client, issuer: issuer}, nil
}
func oidcFromEnv() (*OIDCLogin, error) {
	issuer := os.Getenv("OIDC_ISSUER")
	if issuer == "" {
		if os.Getenv("OIDC_CLIENT_ID") != "" || os.Getenv("OIDC_REDIRECT_URL") != "" {
			return nil, errors.New("incomplete OIDC configuration")
		}
		return nil, nil
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	client := &http.Client{Timeout: 10 * time.Second, Transport: transport, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return newOIDCLogin(ctx, issuer, os.Getenv("OIDC_CLIENT_ID"), os.Getenv("OIDC_CLIENT_SECRET"), os.Getenv("OIDC_REDIRECT_URL"), client, false)
}
func (o *OIDCLogin) start(w http.ResponseWriter, r *http.Request, secure bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	now := time.Now()
	for k, p := range o.pending {
		if now.After(p.Expires) {
			delete(o.pending, k)
		}
	}
	if len(o.pending) >= 1000 {
		problem(w, 429, "sso_limit", "SSO请求过多，请稍后再试")
		return
	}
	state := authRandom(32)
	p := oidcPending{Nonce: authRandom(32), Verifier: oauth2.GenerateVerifier(), Binding: authRandom(32), Expires: now.Add(5 * time.Minute)}
	o.pending[state] = p
	http.SetCookie(w, &http.Cookie{Name: "studio_oidc", Value: p.Binding, Path: "/api/v1/auth/sso", Secure: secure, HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: 300})
	location := o.config.AuthCodeURL(state, oauth2.S256ChallengeOption(p.Verifier), oidc.Nonce(p.Nonce))
	respond(w, 200, map[string]string{"authorization_url": location})
}
func (o *OIDCLogin) callback(w http.ResponseWriter, r *http.Request, auth *AuthStore, options AuthOptions, origin string) {
	state := r.URL.Query().Get("state")
	cookie, err := r.Cookie("studio_oidc")
	if err != nil || len(state) != 64 {
		problem(w, 400, "sso_state_denied", "SSO会话无效，请重新开始")
		return
	}
	o.mu.Lock()
	p, ok := o.pending[state]
	if ok && subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(p.Binding)) == 1 {
		delete(o.pending, state)
	} else {
		ok = false
	}
	o.mu.Unlock()
	if !ok || time.Now().After(p.Expires) || r.URL.Query().Get("code") == "" {
		problem(w, 400, "sso_state_denied", "SSO会话无效，请重新开始")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "studio_oidc", Value: "", Path: "/api/v1/auth/sso", Secure: options.SecureCookie, HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: -1})
	ctx, cancel := context.WithTimeout(oidc.ClientContext(r.Context(), o.client), 15*time.Second)
	defer cancel()
	token, err := o.config.Exchange(ctx, r.URL.Query().Get("code"), oauth2.VerifierOption(p.Verifier))
	if err != nil {
		problem(w, 401, "sso_failed", "SSO验证失败，请重新登录")
		return
	}
	raw, ok := token.Extra("id_token").(string)
	if !ok || len(raw) > 64<<10 {
		problem(w, 401, "sso_failed", "SSO验证失败")
		return
	}
	verified, err := o.verifier.Verify(ctx, raw)
	if err != nil || verified.Subject == "" || len(verified.Subject) > 255 || subtle.ConstantTimeCompare([]byte(verified.Nonce), []byte(p.Nonce)) != 1 {
		problem(w, 401, "sso_failed", "SSO验证失败")
		return
	}
	var claims struct {
		Email           string `json:"email"`
		Verified        bool   `json:"email_verified"`
		AuthorizedParty string `json:"azp"`
	}
	if err = verified.Claims(&claims); err != nil || !claims.Verified || claims.Email == "" || (len(verified.Audience) > 1 && claims.AuthorizedParty != o.config.ClientID) || (claims.AuthorizedParty != "" && claims.AuthorizedParty != o.config.ClientID) {
		problem(w, 401, "sso_failed", "SSO验证失败")
		return
	}
	_, sessionToken, session, err := auth.loginSSO(SSOIdentity{Issuer: o.issuer, Subject: verified.Subject, Email: claims.Email, EmailVerified: true}, options.AllowRegistration)
	if err != nil {
		problem(w, 403, "sso_account_denied", "SSO账号未开放注册或与现有本地账号冲突；不会自动关联邮箱")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "studio_session", Value: sessionToken, Path: "/", Secure: options.SecureCookie, HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: 43200, Expires: session.Expires})
	http.Redirect(w, r, strings.TrimSuffix(origin, "/")+"/", http.StatusSeeOther)
}
func (s *AuthStore) loginSSO(identity SSOIdentity, allowRegistration bool) (AuthUser, string, authSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !identity.EmailVerified || identity.Issuer == "" || identity.Subject == "" || len(identity.Email) > 254 {
		return AuthUser{}, "", authSession{}, errors.New("invalid SSO identity")
	}
	var account authAccount
	found := false
	for _, u := range s.data.Users {
		if u.Issuer == identity.Issuer && u.Subject == identity.Subject {
			account = u
			found = true
			break
		}
	}
	d := s.clone()
	if !found {
		if !allowRegistration || len(d.Users) >= 1000 {
			return AuthUser{}, "", authSession{}, errors.New("SSO registration disabled")
		}
		email := strings.ToLower(strings.TrimSpace(identity.Email))
		for _, u := range d.Users {
			if u.Email == email {
				return AuthUser{}, "", authSession{}, errors.New("email collision requires explicit account linking")
			}
		}
		account = authAccount{AuthUser: AuthUser{ID: authRandom(16), Email: email}, Issuer: identity.Issuer, Subject: identity.Subject}
		d.Users[account.ID] = account
		id := "org-" + authRandom(16)
		d.Organizations[id] = AuthOrganization{ID: id, Name: "我的工作空间", OwnerID: account.ID, Members: map[string]string{account.ID: "owner"}}
	}
	now := time.Now()
	count := 0
	for k, ss := range d.Sessions {
		if now.After(ss.Expires) {
			delete(d.Sessions, k)
		} else if ss.UserID == account.ID {
			count++
		}
	}
	if count >= 20 || len(d.Sessions) >= 10000 {
		return AuthUser{}, "", authSession{}, errors.New("session limit reached")
	}
	token := authRandom(32)
	session := authSession{UserID: account.ID, CSRF: authRandom(32), Expires: now.Add(12 * time.Hour)}
	d.Sessions[authDigest(token)] = session
	if err := s.commit(d); err != nil {
		return AuthUser{}, "", authSession{}, err
	}
	return account.AuthUser, token, session, nil
}
