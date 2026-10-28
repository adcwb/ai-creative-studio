package application

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// Only the deployment operator can enable the reviewed application. HTTP callers
// cannot supply hosts, app approval, encryption keys or client credentials.
type socialRuntime struct {
	photo                                   *socialPhotoBroker
	douyin                                  *douyinRuntime
	auth                                    *AuthStore
	m                                       *MediaStore
	client                                  *http.Client
	key                                     cipher.AEAD
	clientKey, clientSecret, redirect, base string
	approved                                bool
}
type socialToken struct {
	TokenType        string `json:"token_type"`
	AccessExpiresAt  int64  `json:"access_expires_at,omitempty"`
	RefreshExpiresAt int64  `json:"refresh_expires_at,omitempty"`
	RefreshAttempt   string `json:"refresh_attempt,omitempty"`
	Access           string `json:"access_token"`
	Refresh          string `json:"refresh_token"`
	OpenID           string `json:"open_id"`
	Scope            string `json:"scope"`
	Expires          int64  `json:"expires_in"`
	Error            string `json:"error"`
	RefreshExpires   int64  `json:"refresh_expires_in"`
}
type socialPending struct {
	User    string
	Binding string
	State   string
	Expires int64
}
type SocialCreator struct {
	Version         int      `json:"version,omitempty"`
	Username        string   `json:"creator_username"`
	Nickname        string   `json:"creator_nickname"`
	Privacy         []string `json:"privacy_level_options"`
	CommentDisabled bool     `json:"comment_disabled"`
	DuetDisabled    bool     `json:"duet_disabled"`
	StitchDisabled  bool     `json:"stitch_disabled"`
	MaxDuration     int      `json:"max_video_post_duration_sec"`
}

func socialRuntimeFromEnv(m *MediaStore) *socialRuntime {
	r := &socialRuntime{m: m, base: "https://open.tiktokapis.com"}
	// Disabled is inert: no credentials are read and no network calls are made.
	if os.Getenv("SOCIAL_TIKTOK_APP_APPROVED") != "true" || os.Getenv("SOCIAL_TIKTOK_DIRECT_AUDITED") != "true" {
		return r
	}
	r.clientKey = os.Getenv("SOCIAL_TIKTOK_CLIENT_KEY")
	r.clientSecret = os.Getenv("SOCIAL_TIKTOK_CLIENT_SECRET")
	r.redirect = os.Getenv("SOCIAL_TIKTOK_REDIRECT_URI")
	u, e := url.Parse(r.redirect)
	if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "/social-callback" {
		return r
	}
	raw, e := base64.StdEncoding.DecodeString(os.Getenv("SOCIAL_KEK_BASE64"))
	if e != nil || len(raw) != 32 {
		return r
	}
	block, e := aes.NewCipher(raw)
	if e != nil {
		return r
	}
	r.key, e = cipher.NewGCM(block)
	if e != nil {
		return r
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = nil
	r.client = &http.Client{Transport: tr, Timeout: 60 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	r.approved = r.clientKey != "" && r.clientSecret != ""
	return r
}
func (r *socialRuntime) ready() bool {
	return r != nil && r.approved && r.key != nil && r.client != nil
}
func (r *socialRuntime) view(p MediaState) SocialView {
	v := socialView(p)
	if r.photo != nil && r.photo.ready() {
		v.Platforms[0].PhotoStatus = "available"
	}
	v.Connections = append([]SocialConnection(nil), v.Connections...)
	if v.Connections == nil {
		v.Connections = []SocialConnection{}
	}
	for i, c := range v.Connections {
		if c.Status == "refreshing" {
			until, e := time.Parse(time.RFC3339, c.RefreshLeaseUntil)
			if e != nil || !time.Now().Before(until) {
				v.Connections[i].Status = "refresh_unknown"
			}
		}
	}
	v.Jobs = append([]SocialJob(nil), v.Jobs...)
	if v.Jobs == nil {
		v.Jobs = []SocialJob{}
	}
	if r.ready() {
		v.Platforms[0].ConnectionStatus = "configured"
		v.Platforms[0].DirectStatus = "available"
		v.Platforms[0].HandoffStatus = "available"
		v.Platforms[0].ReasonCode = "approved_application_account_required"
	}
	if r != nil && r.douyin != nil && r.douyin.ready() {
		v.Platforms[1].HandoffStatus = "available"
		v.Platforms[1].ConnectionStatus = "configured"
		v.Platforms[1].ReasonCode = "approved_h5_user_confirmation_required"
	}
	for i := range v.Jobs {
		if v.Jobs[i].Status == "dispatching" {
			v.Jobs[i].Status = "unknown"
		}
	}
	return v
}
func (r *socialRuntime) seal(id string, v any) (string, error) {
	if !r.ready() {
		return "", errSocialUnavailable
	}
	b, e := json.Marshal(v)
	if e != nil {
		return "", e
	}
	n := make([]byte, r.key.NonceSize())
	if _, e = rand.Read(n); e != nil {
		return "", e
	}
	return base64.StdEncoding.EncodeToString(r.key.Seal(n, n, b, []byte(id+":"+r.clientKey+":"+r.redirect))), nil
}
func (r *socialRuntime) open(id, s string, v any) error {
	if !r.ready() {
		return errSocialUnavailable
	}
	b, e := base64.StdEncoding.DecodeString(s)
	if e != nil || len(b) < r.key.NonceSize() {
		return errSocialUnavailable
	}
	n := r.key.NonceSize()
	plain, e := r.key.Open(nil, b[:n], b[n:], []byte(id+":"+r.clientKey+":"+r.redirect))
	if e != nil {
		return errSocialUnavailable
	}
	return json.Unmarshal(plain, v)
}
func socialPrivate(space, project, kind, id string) string {
	return mediaKey(space, project) + ":social:" + kind + ":" + id
}
func (r *socialRuntime) request(ctx context.Context, path, token string, body any, out any) error {
	if e := r.checkActor(ctx); e != nil {
		return e
	}
	var data []byte
	var content string
	switch v := body.(type) {
	case url.Values:
		data = []byte(v.Encode())
		content = "application/x-www-form-urlencoded"
	default:
		var err error
		data, err = json.Marshal(body)
		if err != nil {
			return err
		}
		content = "application/json; charset=UTF-8"
	}
	req, e := http.NewRequestWithContext(ctx, "POST", r.base+path, bytes.NewReader(data))
	if e != nil {
		return errors.New("social request invalid")
	}
	req.Header.Set("Content-Type", content)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, e := r.client.Do(req)
	if e != nil {
		return errors.New("social request outcome unknown")
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusTooManyRequests {
		return socialRateLimit{socialRetryDelay(res.Header.Get("Retry-After"), time.Now())}
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return errors.New("social upstream request rejected")
	}
	b, e := io.ReadAll(io.LimitReader(res.Body, 128<<10))
	if e != nil || len(b) >= 128<<10 {
		return errors.New("social response invalid")
	}
	if out == nil {
		return nil
	}
	if json.Unmarshal(b, out) != nil {
		return errors.New("social response invalid")
	}
	return nil
}
func (r *socialRuntime) start(project, space, user, binding string, version int, requestKeys ...string) (string, error) {
	if !r.ready() {
		return "", errSocialUnavailable
	}
	r.m.mu.Lock()
	defer r.m.mu.Unlock()
	d := r.m.clone()
	p := mediaState(d, mediaKey(space, project))
	requestKey := ""
	digest := ""
	receiptID := ""
	if len(requestKeys) > 0 {
		requestKey = requestKeys[0]
		digest = mediaDigest([]any{"social-oauth-start", version, user, binding})
		receiptID = mediaKey(space, project) + ":" + requestKey
		if existing, ok := d.Receipts[receiptID]; ok {
			if existing.Digest != digest {
				return "", errMediaKey
			}
			var prior socialPending
			if r.open(receiptID, existing.SocialSecret, &prior) != nil || prior.Expires < time.Now().Unix() {
				return "", mediaInvalid("authorization attempt expired; start explicitly again")
			}
			return r.authorizationURL(prior.State), nil
		}
	}
	if p.Version != version {
		return "", errMediaVersion
	}
	state := authRandom(32)
	id := socialPrivate(space, project, "pending", state)
	// One pending flow per user/project; overwrite invalidates a prior attempt.
	for k, v := range d.Receipts {
		if strings.HasPrefix(k, socialPrivate(space, project, "pending", "")) {
			var old socialPending
			if r.open(k, v.SocialSecret, &old) == nil && (old.User == user || old.Expires < time.Now().Unix()) {
				delete(d.Receipts, k)
			}
		}
	}
	pendingCount := 0
	for k := range d.Receipts {
		if strings.HasPrefix(k, socialPrivate(space, project, "pending", "")) {
			pendingCount++
		}
	}
	if pendingCount >= 100 {
		return "", errMediaLimit
	}
	enc, e := r.seal(id, socialPending{User: user, Binding: binding, State: state, Expires: time.Now().Add(5 * time.Minute).Unix()})
	if e != nil {
		return "", e
	}
	d.Receipts[id] = mediaReceipt{SocialSecret: enc}
	if requestKey != "" {
		sealed, sealErr := r.seal(receiptID, socialPending{User: user, Binding: binding, State: state, Expires: time.Now().Add(5 * time.Minute).Unix()})
		if sealErr != nil {
			return "", sealErr
		}
		d.Receipts[receiptID] = mediaReceipt{Digest: digest, SocialSecret: sealed}
	}
	if e = r.m.commit(d); e != nil {
		return "", e
	}
	return r.authorizationURL(state), nil
}
func (r *socialRuntime) authorizationURL(state string) string {
	q := url.Values{"client_key": {r.clientKey}, "response_type": {"code"}, "scope": {"video.publish,video.upload"}, "redirect_uri": {r.redirect}, "state": {state}, "disable_auto_auth": {"1"}}
	return "https://www.tiktok.com/v2/auth/authorize/?" + q.Encode()
}
func (r *socialRuntime) complete(ctx context.Context, project, space, user, binding, state, code string) (SocialView, error) {
	if !r.ready() || len(state) > 128 || code == "" || len(code) > 4096 {
		return SocialView{}, errSocialUnavailable
	}
	id := socialPrivate(space, project, "pending", state)
	r.m.mu.Lock()
	d := r.m.clone()
	var p socialPending
	e := r.open(id, d.Receipts[id].SocialSecret, &p)
	if e != nil || p.User != user || p.Binding != binding || p.Expires < time.Now().Unix() {
		r.m.mu.Unlock()
		return SocialView{}, mediaInvalid("OAuth state expired or invalid")
	}
	delete(d.Receipts, id)
	e = r.m.commit(d)
	r.m.mu.Unlock()
	if e != nil {
		return SocialView{}, e
	}
	// Consume state before exchange. Timeout does not replay a one-use code.
	var token socialToken
	e = r.request(ctx, "/v2/oauth/token/", "", url.Values{"client_key": {r.clientKey}, "client_secret": {r.clientSecret}, "code": {code}, "grant_type": {"authorization_code"}, "redirect_uri": {r.redirect}}, &token)
	if e != nil || !validSocialToken(token) {
		return SocialView{}, mediaInvalid("OAuth exchange failed; reconnect explicitly")
	}
	if e = r.checkActor(ctx); e != nil {
		return SocialView{}, e
	}
	r.m.mu.Lock()
	defer r.m.mu.Unlock()
	d = r.m.clone()
	k := mediaKey(space, project)
	media := mediaState(d, k)
	ss := socialState(media)
	stampSocialToken(&token, time.Now())
	accountKey := authDigest(space + ":" + project + ":" + r.clientKey + ":" + token.OpenID)
	existing := false
	for _, c := range ss.Connections {
		if c.AccountKey == accountKey {
			existing = true
		}
	}
	if !existing && len(ss.Connections) >= 20 {
		return SocialView{}, errMediaLimit
	}
	connection := SocialConnection{AccountKey: accountKey, ID: mediaID(), Platform: "tiktok", Status: "connected", DisplayName: "TikTok " + accountKey[:8], Version: 1, ExpiresAt: time.Now().Add(time.Duration(token.Expires) * time.Second).UTC().Format(time.RFC3339)}
	replaced := false
	for i, c := range ss.Connections {
		if c.AccountKey == connection.AccountKey {
			connection.ID = c.ID
			for j := range ss.Jobs {
				if ss.Jobs[j].ConnectionID == c.ID && ss.Jobs[j].MediaType == "PHOTO" {
					ss.Jobs[j].PhotoAccessRevoked = true
				}
			}
			connection.Version = c.Version + 1
			ss.Connections[i] = connection
			replaced = true
			break
		}
	}
	if !replaced {
		ss.Connections = append(ss.Connections, connection)
	}
	id = socialPrivate(space, project, "token", connection.ID)
	enc, e := r.seal(id, token)
	if e != nil {
		return SocialView{}, e
	}
	d.Receipts[id] = mediaReceipt{SocialSecret: enc}

	media.Social = &ss
	media.Version++
	d.Projects[k] = media
	return r.view(media), r.m.commit(d)
}
func socialScope(scopes, want string) bool {
	for _, s := range strings.Split(scopes, ",") {
		if s == want {
			return true
		}
	}
	return false
}
func (r *socialRuntime) token(project, space, id string) (socialToken, error) {
	r.m.mu.Lock()
	defer r.m.mu.Unlock()
	d := r.m.clone()
	p := mediaState(d, mediaKey(space, project))
	var token socialToken
	for _, c := range socialState(p).Connections {
		if c.ID == id && c.Status == "connected" {
			until, e := time.Parse(time.RFC3339, c.ExpiresAt)
			if e != nil || !time.Now().Before(until) {
				return token, mediaInvalid("account token expired; refresh or reconnect")
			}
			k := socialPrivate(space, project, "token", id)
			e = r.open(k, d.Receipts[k].SocialSecret, &token)
			if e == nil && token.AccessExpiresAt > 0 && time.Now().Unix() >= token.AccessExpiresAt {
				return socialToken{}, errSocialUnavailable
			}
			return token, e
		}
	}
	return token, errSocialUnavailable
}
func (r *socialRuntime) creator(ctx context.Context, project, space, id string) (SocialCreator, error) {
	t, e := r.authorizedToken(ctx, project, space, id, "")
	ctx = context.WithValue(ctx, socialConnectionKey{}, socialConnectionScope{project, space, id})
	if e != nil {
		return SocialCreator{}, e
	}
	var result struct {
		Data  SocialCreator `json:"data"`
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	e = r.request(ctx, "/v2/post/publish/creator_info/query/", t.Access, map[string]any{}, &result)
	if e != nil || result.Error.Code != "ok" || len(result.Data.Privacy) == 0 {
		return SocialCreator{}, errors.New("creator information unavailable")
	}
	result.Data.Version = r.m.Get(project, space).Version
	return result.Data, nil
}
func (r *socialRuntime) revoke(ctx context.Context, project, space, id string, version int) (SocialView, error) {
	r.m.mu.Lock()
	d := r.m.clone()
	k := mediaKey(space, project)
	p := mediaState(d, k)
	if p.Version != version {
		r.m.mu.Unlock()
		return SocialView{}, errMediaVersion
	}
	ss := socialState(p)
	var token socialToken
	found := false
	revokedVersion := 0
	for i := range ss.Connections {
		if ss.Connections[i].ID == id {
			found = true
			ss.Connections[i].Status = "revoked_remote_unconfirmed"
			ss.Connections[i].Version++
			revokedVersion = ss.Connections[i].Version
		}
	}
	if !found {
		r.m.mu.Unlock()
		return SocialView{}, ErrNotFound
	}
	for i := range ss.Jobs {
		if ss.Jobs[i].ConnectionID == id && ss.Jobs[i].MediaType == "PHOTO" {
			ss.Jobs[i].PhotoAccessRevoked = true
		}
	}
	secretID := socialPrivate(space, project, "token", id)
	_ = r.open(secretID, d.Receipts[secretID].SocialSecret, &token)
	delete(d.Receipts, secretID)
	p.Social = &ss
	p.Version++
	d.Projects[k] = p
	e := r.m.commit(d)
	r.m.mu.Unlock()
	if e != nil {
		return SocialView{}, e
	}
	// Local revocation is durable first. Never resurrect credentials on provider failure.
	if token.Access != "" {
		e = r.request(ctx, "/v2/oauth/revoke/", "", url.Values{"client_key": {r.clientKey}, "client_secret": {r.clientSecret}, "token": {token.Access}}, nil)
	}
	if e != nil {
		return r.view(p), nil
	}
	r.m.mu.Lock()
	defer r.m.mu.Unlock()
	d = r.m.clone()
	p = mediaState(d, k)
	ss = socialState(p)
	for i := range ss.Connections {
		if ss.Connections[i].ID == id {
			if ss.Connections[i].Status != "revoked_remote_unconfirmed" || ss.Connections[i].Version != revokedVersion {
				return r.view(p), nil
			}
			ss.Connections[i].Status = "revoked"
		}
	}
	p.Social = &ss
	p.Version++
	d.Projects[k] = p
	return r.view(p), r.m.commit(d)
}
func (r *socialRuntime) routes(mux *http.ServeMux, s *FileStore, base string, scope func(http.ResponseWriter, *http.Request, bool) (string, string, bool)) {
	admin := func(w http.ResponseWriter, req *http.Request) (string, bool) {
		a, ok := req.Context().Value(authScopeKey{}).(authScope)
		if !ok || (a.Role != "owner" && a.Role != "admin") {
			problem(w, 403, "role_denied", "Organization administrator required to connect accounts")
			return "", false
		}
		return a.UserID, true
	}
	mux.HandleFunc("POST "+base+"/connections/start", func(w http.ResponseWriter, req *http.Request) {
		req = r.actorRequest(req)
		space, key, ok := scope(w, req, true)
		if !ok {
			return
		}
		user, ok := admin(w, req)
		if !ok {
			return
		}
		var in struct {
			ExpectedVersion int    `json:"expected_version"`
			Platform        string `json:"platform"`
		}
		if !decodeProject(w, req, &in) {
			return
		}
		if in.Platform != "tiktok" {
			socialError(w, errSocialUnavailable)
			return
		}
		u, e := r.start(req.PathValue("id"), space, user, socialSessionBinding(req), in.ExpectedVersion, key)
		if e != nil {
			socialError(w, e)
			return
		}
		respond(w, 200, map[string]string{"authorization_url": u})
	})
	mux.HandleFunc("POST "+base+"/connections/complete", func(w http.ResponseWriter, req *http.Request) {
		req = r.actorRequest(req)
		space, _, ok := scope(w, req, true)
		if !ok {
			return
		}
		user, ok := admin(w, req)
		if !ok {
			return
		}
		var in struct {
			State string `json:"state"`
			Code  string `json:"code"`
		}
		if !decodeProject(w, req, &in) {
			return
		}
		v, e := r.complete(req.Context(), req.PathValue("id"), space, user, socialSessionBinding(req), in.State, in.Code)
		if e != nil {
			problem(w, 400, "social_oauth_failed", "Authorization failed or expired; reconnect explicitly")
			return
		}
		respond(w, 200, v)
	})
	mux.HandleFunc("GET "+base+"/connections/{connection_id}/creator", func(w http.ResponseWriter, req *http.Request) {
		req = r.actorRequest(req)
		space, _, ok := scope(w, req, false)
		if !ok {
			return
		}
		v, e := r.creator(req.Context(), req.PathValue("id"), space, req.PathValue("connection_id"))
		if e != nil {
			socialError(w, e)
			return
		}
		respond(w, 200, v)
	})
	mux.HandleFunc("POST "+base+"/connections/{connection_id}/creator", func(w http.ResponseWriter, req *http.Request) {
		req = r.actorRequest(req)
		space, _, ok := scope(w, req, true)
		if !ok {
			return
		}
		var in struct{}
		if !decodeProject(w, req, &in) {
			return
		}
		v, e := r.creator(req.Context(), req.PathValue("id"), space, req.PathValue("connection_id"))
		if e != nil {
			socialError(w, e)
			return
		}
		respond(w, 200, v)
	})
	mux.HandleFunc("POST "+base+"/connections/{connection_id}/revoke", func(w http.ResponseWriter, req *http.Request) {
		req = r.actorRequest(req)
		space, _, ok := scope(w, req, true)
		if !ok {
			return
		}
		if _, ok = admin(w, req); !ok {
			return
		}
		var in struct {
			ExpectedVersion int `json:"expected_version"`
		}
		if !decodeProject(w, req, &in) {
			return
		}
		v, e := r.revoke(req.Context(), req.PathValue("id"), space, req.PathValue("connection_id"), in.ExpectedVersion)
		if e != nil {
			socialError(w, e)
			return
		}
		respond(w, 200, v)
	})
	for _, op := range []string{"dispatch", "refresh"} {
		operation := op
		mux.HandleFunc("POST "+base+"/jobs/{job_id}/"+operation, func(w http.ResponseWriter, req *http.Request) {
			req = r.actorRequest(req)
			space, _, ok := scope(w, req, true)
			if !ok {
				return
			}
			var in struct {
				ExpectedVersion int `json:"expected_version"`
			}
			if !decodeProject(w, req, &in) {
				return
			}
			v, e := r.execute(req.Context(), req.PathValue("id"), space, req.PathValue("job_id"), in.ExpectedVersion, operation == "refresh")
			if e != nil {
				socialError(w, e)
				return
			}
			respond(w, 200, v)
		})
	}
}

type socialGuardKey struct{}

func (r *socialRuntime) actorRequest(req *http.Request) *http.Request {
	if r.auth == nil {
		return req
	}
	scope, _ := req.Context().Value(authScopeKey{}).(authScope)
	cookie, _ := req.Cookie("studio_session")
	token := ""
	if cookie != nil {
		token = cookie.Value
	}
	admin := strings.Contains(req.URL.Path, "/connections/") && !strings.HasSuffix(req.URL.Path, "/creator")
	guard := func() error {
		user, _, ok := r.auth.authenticate(token)
		role := r.auth.role(scope.UserID, scope.SpaceID)
		if !ok || user.ID != scope.UserID || !runtimeRoleAllowed(role) || (admin && role != "owner" && role != "admin") {
			return errRuntimeDenied
		}
		return nil
	}
	ctx := context.WithValue(req.Context(), socialGuardKey{}, guard)
	ctx = context.WithValue(ctx, socialRefreshAllowedKey{}, authWrite(req))
	return req.WithContext(ctx)
}

type socialConnectionKey struct{}
type socialConnectionScope struct{ project, space, id string }

func (r *socialRuntime) checkActor(ctx context.Context) error {
	if c, ok := ctx.Value(socialConnectionKey{}).(socialConnectionScope); ok && c.id != "" {
		if _, e := r.token(c.project, c.space, c.id); e != nil {
			return e
		}
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if r.auth == nil {
		return nil
	}
	guard, ok := ctx.Value(socialGuardKey{}).(func() error)
	if !ok {
		return errRuntimeDenied
	}
	return guard()
}

func socialSessionBinding(r *http.Request) string {
	c, e := r.Cookie("studio_session")
	if e != nil {
		return ""
	}
	return authDigest(c.Value)
}

type socialRateLimit struct{ Delay time.Duration }

func (e socialRateLimit) Error() string { return "social provider rate limit" }
func socialRetryDelay(value string, now time.Time) time.Duration {
	d := time.Minute
	if seconds, e := strconv.ParseInt(value, 10, 64); e == nil && seconds > 0 {
		if seconds > 3600 {
			seconds = 3600
		}
		d = time.Duration(seconds) * time.Second
	} else if until, e := http.ParseTime(value); e == nil {
		d = until.Sub(now)
	}
	if d < time.Minute {
		return time.Minute
	}
	if d > time.Hour {
		return time.Hour
	}
	return d
}
