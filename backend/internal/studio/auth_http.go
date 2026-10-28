package studio

import (
	"context"
	"crypto/subtle"
	"errors"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
	"unicode/utf8"
)

var errAuthAudit = errors.New("authentication audit unavailable")

type authScopeKey struct{}
type authScope struct{ UserID, SpaceID, Role string }
type AuthOptions struct {
	AllowRegistration bool
	SecureCookie      bool
	OIDC              *OIDCLogin
	TrustedProxies    []*net.IPNet
}

// SSOAuthenticator is a trusted server-side adapter boundary, never a client identity header.
// An implementation must validate issuer, audience, signature, expiry, nonce and PKCE before
// returning a subject. The default service exposes no SSO callback until an adapter is wired.
type SSOAuthenticator interface {
	Exchange(context.Context, string, string, string) (SSOIdentity, error)
}
type SSOIdentity struct {
	Issuer, Subject, Email string
	EmailVerified          bool
}

// HandlerFromEnv defaults to authenticated mode. Legacy demos require explicit opt-in.
func HandlerFromEnv(repo Repository, origin, dir string) (http.Handler, error) {
	mode := os.Getenv("AUTH_MODE")
	if mode == "demo" {
		return Handler(repo, origin), nil
	}
	if mode != "" && mode != "secure" {
		return nil, errors.New("AUTH_MODE must be secure or demo")
	}
	var s *AuthStore
	var err error
	if fs, ok := repo.(*FileStore); ok && fs.pg != nil {
		s, err = OpenPostgresAuthStore(dir, fs.pg.db)
	} else {
		s, err = OpenAuthStore(dir)
	}
	if err != nil {
		return nil, err
	}
	modelKey, err := modelKeyFromEnv()
	if err != nil {
		return nil, err
	}
	if s.pg != nil {
		s.models, err = OpenPostgresModelConfigStore(dir, s.pg.db, modelKey)
	} else {
		s.models, err = OpenModelConfigStore(dir, modelKey)
	}
	for i := range modelKey {
		modelKey[i] = 0
	}
	if err != nil {
		return nil, err
	}
	oidcLogin, err := oidcFromEnv()
	if err != nil {
		return nil, err
	}
	if oidcLogin != nil && s.pg != nil {
		oidcLogin.db = s.pg.db
	}
	trustedProxies, err := parseTrustedAuthProxies(os.Getenv("AUTH_TRUSTED_PROXY_CIDRS"))
	if err != nil {
		return nil, err
	}
	secure := os.Getenv("AUTH_COOKIE_SECURE") != "false"
	return SecureHandler(repo, origin, s, AuthOptions{AllowRegistration: os.Getenv("AUTH_ALLOW_REGISTRATION") == "true", SecureCookie: secure, OIDC: oidcLogin, TrustedProxies: trustedProxies}), nil
}
func authWrite(r *http.Request) bool {
	return r.Method != "GET" && r.Method != "HEAD" && r.Method != "OPTIONS"
}
func SecureHandler(repo Repository, origin string, auth *AuthStore, options AuthOptions) http.Handler {
	if s, ok := repo.(*FileStore); ok {
		ConfigureManagedRuntime(s, auth)
	}
	next := Handler(repo, origin)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if o := r.Header.Get("Origin"); o != "" {
			if o != origin {
				problem(w, 403, "origin_denied", "不允许的浏览器来源")
				return
			}
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Idempotency-Key, X-CSRF-Token")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, OPTIONS")
		}
		if r.Method == "OPTIONS" {
			w.WriteHeader(204)
			return
		}
		if r.URL.Path == "/healthz" && r.Method == "GET" {
			respond(w, 200, map[string]string{"status": "ok", "mode": "secure"})
			return
		}
		// This exact route verifies an encrypted expiring grant and current membership.
		if r.URL.Path == douyinMediaPath && (r.Method == "GET" || r.Method == "HEAD") {
			next.ServeHTTP(w, r)
			return
		}
		cookie, _ := r.Cookie("studio_session")
		token := ""
		if cookie != nil {
			token = cookie.Value
		}
		user, session, logged := auth.authenticate(token)
		sessionResponse := func() map[string]any {
			out := map[string]any{"mode": "secure", "authenticated": logged, "registration_enabled": options.AllowRegistration, "sso_available": options.OIDC != nil}
			if logged {
				out["user"] = user
				out["csrf_token"] = session.CSRF
				out["spaces"] = auth.spaces(user.ID)
			}
			return out
		}
		if r.URL.Path == "/api/v1/auth/session" && r.Method == "GET" {
			respond(w, 200, sessionResponse())
			return
		}
		if options.OIDC != nil && r.URL.Path == "/api/v1/auth/sso/callback" && r.Method == "GET" {
			options.OIDC.callback(w, r, auth, options, origin)
			return
		}
		if authWrite(r) && r.Header.Get("Origin") != origin {
			problem(w, 403, "origin_required", "写操作需要可信 Origin")
			return
		}
		if options.OIDC != nil && r.URL.Path == "/api/v1/auth/sso/start" && r.Method == "POST" {
			options.OIDC.start(w, r, options.SecureCookie)
			return
		}
		if (r.URL.Path == "/api/v1/auth/login" || r.URL.Path == "/api/v1/auth/register") && r.Method == "POST" {
			register := strings.HasSuffix(r.URL.Path, "/register")
			if register && !options.AllowRegistration {
				problem(w, 403, "registration_disabled", "注册未开放")
				return
			}
			ip := authClientIP(r, options.TrustedProxies)
			if ip == "" {
				problem(w, 400, "client_address_invalid", "客户端地址无效")
				return
			}
			var err error
			if !auth.allowAttempt(ip) {
				problem(w, 429, "auth_rate_limit", "尝试过于频繁，请稍后再试")
				return
			}
			var in struct {
				Email    string `json:"email"`
				Password string `json:"password"`
			}
			if !decode(w, r, &in) {
				return
			}
			var newToken string
			user, newToken, session, err = auth.login(in.Email, in.Password, register, func(candidate AuthUser) error {
				SetAuditIdentity(r.Context(), candidate.ID, "", "")
				if err := BeginAuditMutation(r.Context()); err != nil {
					return errAuthAudit
				}
				return nil
			})
			if err != nil {
				if errors.Is(err, errAuthAudit) {
					problem(w, 503, "audit_unavailable", "审计不可用，已拒绝登录变更")
					return
				}
				problem(w, 401, "authentication_failed", "登录或注册失败，请检查输入（密码需12–128字节）")
				return
			}
			logged = true
			SetAuditIdentity(r.Context(), user.ID, "", "")
			// Rotate the authenticated browser session; old sessions on other devices remain bounded.
			if token != "" {
				auth.mu.Lock()
				d := auth.clone()
				delete(d.Sessions, authDigest(token))
				err = auth.commit(d)
				auth.mu.Unlock()
				if err != nil {
					problem(w, 503, "auth_storage_error", "会话保存失败")
					return
				}
			}
			http.SetCookie(w, &http.Cookie{Name: "studio_session", Value: newToken, Path: "/", HttpOnly: true, Secure: options.SecureCookie, SameSite: http.SameSiteLaxMode, MaxAge: 43200, Expires: session.Expires})
			respond(w, 200, sessionResponse())
			return
		}
		if logged {
			SetAuditIdentity(r.Context(), user.ID, "", "")
		}
		if !logged {
			problem(w, 401, "authentication_required", "请先登录")
			return
		}
		if authWrite(r) && subtle.ConstantTimeCompare([]byte(r.Header.Get("X-CSRF-Token")), []byte(session.CSRF)) != 1 {
			problem(w, 403, "csrf_denied", "会话校验失败，请刷新后重试")
			return
		}
		if r.URL.Path == "/api/v1/auth/logout" && r.Method == "POST" {
			if !beginAuthAudit(w, r) {
				return
			}
			auth.mu.Lock()
			d := auth.clone()
			delete(d.Sessions, authDigest(token))
			err := auth.commit(d)
			auth.mu.Unlock()
			if err != nil {
				problem(w, 503, "auth_storage_error", "注销保存失败，请重试")
				return
			}
			http.SetCookie(w, &http.Cookie{Name: "studio_session", Value: "", Path: "/", HttpOnly: true, Secure: options.SecureCookie, SameSite: http.SameSiteLaxMode, MaxAge: -1, Expires: time.Unix(1, 0)})
			respond(w, 200, map[string]bool{"authenticated": false})
			return
		}
		if r.URL.Path == "/api/v1/spaces" && r.Method == "GET" {
			respond(w, 200, auth.spaces(user.ID))
			return
		}
		if r.URL.Path == "/api/v1/organizations" && r.Method == "POST" {
			if !beginAuthAudit(w, r) {
				return
			}
			auth.createOrganization(w, r, user)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/v1/organizations/") {
			auth.members(w, r, user)
			return
		}
		// Every data route requires an explicit organization membership. Capability/catalog
		// endpoints are account-readable but convey no tenant data. No client field grants identity.
		publicMetadata := r.Method == "GET" && (r.URL.Path == "/api/v1/templates" || r.URL.Path == "/api/v1/capabilities" || r.URL.Path == "/api/v1/creative-registry" || r.URL.Path == "/api/v1/creative-capabilities" || r.URL.Path == "/api/v1/creative-media-capabilities" || r.URL.Path == "/api/v1/alignment-capabilities")
		space := r.URL.Query().Get("space_id")
		role := auth.role(user.ID, space)
		if !publicMetadata && role == "" {
			problem(w, 403, "tenant_denied", "没有此工作空间的访问权限")
			return
		}
		if authWrite(r) && role == "viewer" {
			problem(w, 403, "role_denied", "只读成员不能修改内容")
			return
		}
		SetAuditIdentity(r.Context(), user.ID, space, role)
		if authWrite(r) && !beginAuthAudit(w, r) {
			return
		}
		if r.URL.Path == "/api/v1/model-config" || strings.HasPrefix(r.URL.Path, "/api/v1/model-config/") {
			s := authScope{UserID: user.ID, SpaceID: space, Role: role}
			auth.models.serve(w, r, s)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), authScopeKey{}, authScope{UserID: user.ID, SpaceID: space, Role: role})))
	})
}
func (s *AuthStore) createOrganization(w http.ResponseWriter, r *http.Request, user AuthUser) {
	var in struct {
		Name string `json:"name"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if utf8.RuneCountInString(in.Name) < 1 || utf8.RuneCountInString(in.Name) > 80 || strings.ContainsAny(in.Name, "\x00\r\n") {
		problem(w, 400, "invalid_input", "空间名称需为1–80字")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	count := 0
	for _, o := range s.data.Organizations {
		if o.OwnerID == user.ID {
			count++
		}
	}
	if count >= 20 || len(s.data.Organizations) >= 10000 {
		problem(w, 429, "organization_limit", "空间数量达到上限")
		return
	}
	d := s.clone()
	id := "org-" + authRandom(16)
	d.Organizations[id] = AuthOrganization{ID: id, Name: in.Name, OwnerID: user.ID, Members: map[string]string{user.ID: "owner"}}
	if err := s.commit(d); err != nil {
		problem(w, 503, "auth_storage_error", "空间保存失败")
		return
	}
	respond(w, 201, map[string]string{"id": id, "name": in.Name, "type": "team", "owner_id": user.ID, "role": "owner"})
}
func (s *AuthStore) members(w http.ResponseWriter, r *http.Request, user AuthUser) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/v1/organizations/"), "/")
	if len(parts) != 2 || parts[1] != "members" {
		problem(w, 404, "not_found", "接口不存在")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	org, exists := s.data.Organizations[parts[0]]
	role := org.Members[user.ID]
	if !exists || role == "" {
		problem(w, 403, "tenant_denied", "没有此工作空间的访问权限")
		return
	}
	if role != "owner" && role != "admin" {
		problem(w, 403, "role_denied", "仅管理员可管理成员")
		return
	}
	SetAuditIdentity(r.Context(), user.ID, org.ID, role)
	if r.Method == "GET" {
		members := []map[string]string{}
		for uid, role := range org.Members {
			u := s.data.Users[uid]
			members = append(members, map[string]string{"id": uid, "email": u.Email, "role": role})
		}
		respond(w, 200, members)
		return
	}
	if r.Method != "PUT" {
		problem(w, 405, "method_not_allowed", "方法不支持")
		return
	}
	var in struct {
		Email  string `json:"email"`
		Role   string `json:"role"`
		UserID string `json:"user_id,omitempty"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Role != "admin" && in.Role != "editor" && in.Role != "viewer" && in.Role != "remove" {
		problem(w, 400, "invalid_input", "角色无效")
		return
	}
	uid := ""
	for id, u := range s.data.Users {
		if u.Email == strings.ToLower(strings.TrimSpace(in.Email)) {
			uid = id
			break
		}
	}
	if uid == "" {
		problem(w, 404, "not_found", "该账号不存在，请先注册")
		return
	}
	if in.UserID != "" && in.UserID != uid {
		problem(w, 403, "member_identity_mismatch", "账号ID与当前邮箱不匹配")
		return
	}
	if s.data.Users[uid].Issuer != "" && in.UserID != uid {
		problem(w, 403, "sso_identity_confirmation_required", "SSO成员变更需要提供对方分享的稳定账号ID，不能仅凭邮箱授权")
		return
	}
	current := org.Members[uid]
	if current == "owner" || (role == "admin" && (current == "admin" || in.Role == "admin")) {
		problem(w, 403, "role_denied", "不能修改此角色")
		return
	}
	if len(org.Members) >= 200 && current == "" && in.Role != "remove" {
		problem(w, 429, "member_limit", "成员数量达到上限")
		return
	}
	if !beginAuthAudit(w, r) {
		return
	}
	d := s.clone()
	changed := d.Organizations[org.ID]
	if in.Role == "remove" {
		delete(changed.Members, uid)
	} else {
		changed.Members[uid] = in.Role
	}
	d.Organizations[org.ID] = changed
	if err := s.commit(d); err != nil {
		problem(w, 503, "auth_storage_error", "成员保存失败")
		return
	}
	respond(w, 200, map[string]bool{"updated": true})
}

func beginAuthAudit(w http.ResponseWriter, r *http.Request) bool {
	if err := BeginAuditMutation(r.Context()); err != nil {
		problem(w, 503, "audit_unavailable", "审计不可用，已拒绝写入")
		return false
	}
	return true
}
func parseTrustedAuthProxies(raw string) ([]*net.IPNet, error) {
	out := []*net.IPNet{}
	if strings.TrimSpace(raw) == "" {
		return out, nil
	}
	for _, value := range strings.Split(raw, ",") {
		_, network, err := net.ParseCIDR(strings.TrimSpace(value))
		if err != nil || len(out) >= 32 {
			return nil, errors.New("invalid AUTH_TRUSTED_PROXY_CIDRS")
		}
		out = append(out, network)
	}
	return out, nil
}
func authClientIP(r *http.Request, trusted []*net.IPNet) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return ""
	}
	remote := net.ParseIP(host)
	if remote == nil {
		return ""
	}
	for _, network := range trusted {
		if network.Contains(remote) {
			forwarded := net.ParseIP(r.Header.Get("X-Real-IP"))
			if forwarded == nil {
				return ""
			}
			return forwarded.String()
		}
	}
	return remote.String()
}
