package studio

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func douyinFixture(t *testing.T) (*douyinRuntime, string, string, string, douyinPrepare) {
	t.Helper()
	s := testStore(t)
	auth, e := OpenAuthStore(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	user, _, _, e := auth.login("douyin@example.test", "synthetic-password-A", true)
	if e != nil {
		t.Fatal(e)
	}
	space := auth.spaces(user.ID)[0]["id"]
	p, _, e := s.CreateProject(space, "create-douyin", ProjectInput{Title: "Synthetic handoff", Brief: "Synthetic source only"})
	if e != nil {
		t.Fatal(e)
	}
	state, e := s.media.AddAsset(p.ID, space, "douyin-image", "synthetic.png", "", "", "", "uploaded", syntheticPNG(1), "image/png")
	if e != nil {
		t.Fatal(e)
	}
	block, _ := aes.NewCipher([]byte("0123456789abcdef0123456789abcdef"))
	key, _ := cipher.NewGCM(block)
	now := time.Now().UTC().Truncate(time.Second)
	r := &douyinRuntime{store: s, auth: auth, key: key, now: func() time.Time { return now }, config: DouyinHandoffConfig{Approved: true, Scopes: []string{"h5.share", "aweme.share", "open.get.ticket"}, ClientKey: "synthetic-client", Ticket: "synthetic-ticket", TicketExpiresAt: now.Add(time.Hour), MediaPrefix: "https://studio.example/api/v1/social-media/"}}
	return r, p.ID, space, user.ID, douyinPrepare{ExpectedVersion: state.Version, AssetID: state.Assets[0].ID, Title: "Synthetic", Confirmed: true, RightsConfirmed: true}
}
func douyinSource(t *testing.T, p douyinPrepared) string {
	t.Helper()
	u, e := url.Parse(p.URL)
	if e != nil || u.Scheme != "snssdk1128" {
		t.Fatal("invalid handoff")
	}
	source := u.Query().Get("image_path")
	if source == "" {
		source = u.Query().Get("video_path")
	}
	u, e = url.Parse(source)
	if e != nil || u.Host != "studio.example" || u.Path != douyinMediaPath {
		t.Fatal("invalid broker URL")
	}
	return u.RequestURI()
}
func douyinFetch(r *douyinRuntime, method, path, rangeHeader string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	if rangeHeader != "" {
		req.Header.Set("Range", rangeHeader)
	}
	w := httptest.NewRecorder()
	r.serve(w, req)
	return w
}
func TestDouyinRuntimeCapabilityRangeRevocationAndReplay(t *testing.T) {
	r, project, space, user, in := douyinFixture(t)
	out, e := r.prepare(project, space, user, "prepare-handoff", in)
	if e != nil {
		t.Fatal(e)
	}
	source := douyinSource(t, out)
	replay, e := r.prepare(project, space, user, "prepare-handoff", in)
	if e != nil || replay.URL != out.URL || replay.JobID != out.JobID {
		t.Fatal("unstable replay", e)
	}
	changed := in
	changed.Title = "changed"
	if _, e = r.prepare(project, space, user, "prepare-handoff", changed); !errors.Is(e, errMediaKey) {
		t.Fatal("no key conflict", e)
	}
	w := douyinFetch(r, "GET", source, "bytes=0-7")
	if w.Code != 206 || w.Body.Len() != 8 || !strings.HasPrefix(w.Header().Get("Content-Range"), "bytes 0-7/") || w.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatal(w.Code, w.Header())
	}
	if w = douyinFetch(r, "HEAD", source, ""); w.Code != 200 || w.Body.Len() != 0 {
		t.Fatal("HEAD", w.Code)
	}
	if w = douyinFetch(r, "GET", source, "bytes=99999999-"); w.Code != 416 {
		t.Fatal("range not bounded", w.Code)
	}
	if w = douyinFetch(r, "GET", source+"&space_id=foreign", ""); w.Code != 404 {
		t.Fatal("unexpected query", w.Code)
	}
	v, e := r.change(project, space, user, "ack-handoff", out.JobID, "ack", out.Version)
	if e != nil || v.Jobs[0].Status != "handed_off" {
		t.Fatal(e)
	}
	if w = douyinFetch(r, "GET", source, ""); w.Code != 200 {
		t.Fatal("ack must not break media fetch", w.Code)
	}
	_, e = r.change(project, space, user, "revoke-handoff", out.JobID, "revoke", v.Version)
	if e != nil {
		t.Fatal(e)
	}
	for _, method := range []string{"GET", "HEAD"} {
		if w = douyinFetch(r, method, source, "bytes=0-7"); w.Code != 404 {
			t.Fatal("revoked grant", method, w.Code)
		}
	}
	if _, e = r.prepare(project, space, user, "prepare-handoff", in); e == nil {
		t.Fatal("replay revived revoked capability")
	}
	disk, e := os.ReadFile(filepath.Join(r.store.media.dir, "state.json"))
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(string(disk), "synthetic-ticket") || strings.Contains(string(disk), "snssdk1128") || strings.Contains(string(disk), "?cap=") {
		t.Fatal("plaintext credential/grant persisted")
	}
}
func TestDouyinRuntimeOwnershipExpiryRoleAndConsent(t *testing.T) {
	r, project, space, user, in := douyinFixture(t)
	denied := in
	denied.RightsConfirmed = false
	if _, e := r.prepare(project, space, user, "without-consent", denied); e == nil {
		t.Fatal("missing rights accepted")
	}
	if _, e := r.prepare(project, "foreign", user, "foreign-tenant", in); e == nil {
		t.Fatal("foreign tenant")
	}
	foreign := in
	foreign.AssetID = "foreign-asset"
	if _, e := r.prepare(project, space, user, "foreign-asset", foreign); e == nil {
		t.Fatal("foreign asset")
	}
	if _, e := r.prepare(project, space, user, "revoked-session", in, func() bool { return false }); e == nil {
		t.Fatal("revoked session")
	}
	out, e := r.prepare(project, space, user, "prepare-expiry", in)
	if e != nil {
		t.Fatal(e)
	}
	source := douyinSource(t, out)
	// Auth membership is consulted on every public fetch, without a browser cookie.
	r.auth.mu.Lock()
	d := r.auth.clone()
	org := d.Organizations[space]
	org.Members[user] = "viewer"
	d.Organizations[space] = org
	if e = r.auth.commit(d); e != nil {
		t.Fatal(e)
	}
	r.auth.mu.Unlock()
	if w := douyinFetch(r, "GET", source, ""); w.Code != 404 {
		t.Fatal("revoked role", w.Code)
	}
	r.auth.mu.Lock()
	d = r.auth.clone()
	org = d.Organizations[space]
	org.Members[user] = "owner"
	d.Organizations[space] = org
	_ = r.auth.commit(d)
	r.auth.mu.Unlock()
	clock := r.now()
	r.now = func() time.Time { return clock.Add(6 * time.Minute) }
	if w := douyinFetch(r, "GET", source, ""); w.Code != 404 {
		t.Fatal("expired grant", w.Code)
	}
	r.now = func() time.Time { return clock }
	u, _ := url.Parse(source)
	token := u.Query().Get("cap")
	tampered := token[:len(token)-3] + "xxx"
	if w := douyinFetch(r, "GET", douyinMediaPath+"?cap="+tampered, ""); w.Code != 404 {
		t.Fatal("tampered grant")
	}
	// Wrong purpose and cross-project claims cannot authorize a different file.
	capability, _ := r.seal("wrong-purpose", douyinCapability{space, project, user, out.JobID, clock.Add(time.Minute).Unix()})
	if w := douyinFetch(r, "GET", douyinMediaPath+"?cap="+capability, ""); w.Code != 404 {
		t.Fatal("purpose binding")
	}
}
func TestDouyinRuntimeAuthBypassIsExactAndDefaultClosed(t *testing.T) {
	s := testStore(t)
	auth, e := OpenAuthStore(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	h := SecureHandler(s, "http://localhost:5173", auth, AuthOptions{})
	for _, tc := range []struct {
		method, path string
		code         int
	}{{"GET", douyinMediaPath + "?cap=invalid", 404}, {"HEAD", douyinMediaPath + "?cap=invalid", 404}, {"POST", douyinMediaPath, 401}, {"GET", douyinMediaPath + "/other", 401}, {"GET", "/api/v1/projects/guess/media", 401}} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(tc.method, tc.path, nil)
		req.Header.Set("Origin", "http://localhost:5173")
		h.ServeHTTP(w, req)
		if w.Code != tc.code {
			t.Fatalf("%s %s: %d != %d", tc.method, tc.path, w.Code, tc.code)
		}
	}
	r := douyinRuntimeFromEnv(s)
	if r.ready() {
		t.Fatal("unconfigured ready")
	}
	b, _ := json.Marshal(socialView(s.media.Get("p", "tenant")))
	if strings.Contains(string(b), "handoff_url") {
		t.Fatal("capability exposed in listing")
	}
}

func TestDouyinRuntimeConfiguredHTTPAndBounds(t *testing.T) {
	r, project, space, user, in := douyinFixture(t)
	// Only synthetic environment settings; nothing is sent to this example host.
	t.Setenv("SOCIAL_DOUYIN_H5_APPROVED", "true")
	t.Setenv("SOCIAL_DOUYIN_TICKET_FETCH_ENABLED", "false")
	t.Setenv("SOCIAL_DOUYIN_PUBLIC_ORIGIN", "https://studio.example")
	t.Setenv("SOCIAL_DOUYIN_MEDIA_KEY_BASE64", "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=")
	t.Setenv("SOCIAL_DOUYIN_CLIENT_KEY", "synthetic-client")
	t.Setenv("SOCIAL_DOUYIN_TICKET", "synthetic-ticket")
	t.Setenv("SOCIAL_DOUYIN_TICKET_EXPIRES_AT", time.Now().Add(time.Hour).UTC().Format(time.RFC3339))
	t.Setenv("SOCIAL_DOUYIN_SCOPES", "h5.share aweme.share open.get.ticket")
	_, token, session, e := r.auth.login("douyin@example.test", "synthetic-password-A", false)
	if e != nil {
		t.Fatal(e)
	}
	h := SecureHandler(r.store, "http://localhost:5173", r.auth, AuthOptions{})
	body, _ := json.Marshal(in)
	path := "/api/v1/projects/" + project + "/social/douyin/prepare?space_id=" + space
	w := authRequest(t, h, "POST", path, string(body), token, session.CSRF, 200)
	var out douyinPrepared
	if e = json.Unmarshal(w.Body.Bytes(), &out); e != nil {
		t.Fatal(e)
	}
	source := douyinSource(t, out)
	req := httptest.NewRequest("GET", source, nil)
	req.Header.Set("Range", "bytes=0-7")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 206 || w.Body.Len() != 8 {
		t.Fatal("configured sessionless range", w.Code)
	}
	authRequest(t, h, "POST", path, string(body), token, "", 403)
	bad := in
	bad.ExpectedVersion = out.Version
	bad.AssetID = "foreign"
	raw, _ := json.Marshal(bad)
	req = httptest.NewRequest("POST", path, strings.NewReader(string(raw)))
	req.Header.Set("Origin", "http://localhost:5173")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "foreign-http-key")
	req.Header.Set("X-CSRF-Token", session.CSRF)
	req.AddCookie(&http.Cookie{Name: "studio_session", Value: token})
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 404 {
		t.Fatal("foreign asset HTTP", w.Code)
	}
	for _, origin := range []string{"http://studio.example", "https://127.0.0.1", "https://localhost", "https://host.local", "https://studio.example/path", "https://user:secret@studio.example", "https://studio.example?redirect=evil"} {
		t.Setenv("SOCIAL_DOUYIN_PUBLIC_ORIGIN", origin)
		if douyinRuntimeFromEnv(r.store).ready() {
			t.Fatal("unsafe origin accepted")
		}
	}
	// Existing source ownership is verified before any file is resolved. A larger
	// synthetic file cannot be offered as a legal image, even with a valid ID.
	large := filepath.Join(r.store.media.dir, in.AssetID)
	if e = os.Truncate(large, 21<<20); e != nil {
		t.Fatal(e)
	}
	in.ExpectedVersion = out.Version
	if _, e = r.prepare(project, space, user, "oversize-image", in); e == nil {
		t.Fatal("oversize image accepted")
	}
}

type fixtureDouyinTicket struct {
	calls   int
	expires time.Time
}

func (p *fixtureDouyinTicket) Ready() bool { return true }
func (p *fixtureDouyinTicket) Ticket(ctx context.Context) (string, time.Time, error) {
	p.calls++
	guard, ok := ctx.Value(douyinTicketGuardKey{}).(func() bool)
	if !ok || !guard() {
		return "", time.Time{}, errSocialUnavailable
	}
	return "synthetic-auto-ticket", p.expires, nil
}
func TestDouyinRuntimeFetchOnlyAfterConsentAndPreflight(t *testing.T) {
	r, project, space, user, in := douyinFixture(t)
	provider := &fixtureDouyinTicket{expires: r.now().Add(time.Hour)}
	r.tickets = provider
	r.config.Ticket = ""
	r.config.TicketExpiresAt = time.Time{}
	if !r.ready() || provider.calls != 0 {
		t.Fatal("readiness fetched ticket")
	}
	invalid := in
	invalid.AssetID = "foreign"
	if _, e := r.prepare(project, space, user, "foreign-auto", invalid); e == nil || provider.calls != 0 {
		t.Fatal("foreign source fetched ticket")
	}
	invalid = in
	invalid.Confirmed = false
	if _, e := r.prepare(project, space, user, "unconfirmed-auto", invalid); e == nil || provider.calls != 0 {
		t.Fatal("unconfirmed fetched ticket")
	}
	out, e := r.prepare(project, space, user, "approved-auto", in, func() bool { return true })
	if e != nil || provider.calls != 1 {
		t.Fatal(e, provider.calls)
	}
	replay, e := r.prepare(project, space, user, "approved-auto", in)
	if e != nil || replay.URL != out.URL || provider.calls != 1 {
		t.Fatal("replay made external request", e, provider.calls)
	}
	if w := douyinFetch(r, "GET", douyinSource(t, out), ""); w.Code != 200 || provider.calls != 1 {
		t.Fatal("broker fetched ticket", w.Code, provider.calls)
	}
}
