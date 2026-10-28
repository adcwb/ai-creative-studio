package application

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func refreshContext() context.Context {
	return context.WithValue(context.Background(), socialRefreshAllowedKey{}, true)
}
func seedRefresh(t *testing.T, r *socialRuntime, project, space string) string {
	t.Helper()
	r.m.mu.Lock()
	defer r.m.mu.Unlock()
	d := r.m.clone()
	k := mediaKey(space, project)
	p := mediaState(d, k)
	token := socialToken{Access: "synthetic-old-access", Refresh: "synthetic-old-refresh", OpenID: "synthetic-account", Scope: "video.publish,video.upload", TokenType: "Bearer", Expires: 3600, RefreshExpires: 31536000, AccessExpiresAt: time.Now().Add(30 * time.Second).Unix(), RefreshExpiresAt: time.Now().Add(time.Hour).Unix()}
	id := "synthetic-connection"
	c := SocialConnection{ID: id, Platform: "tiktok", Status: "connected", AccountKey: authDigest(space + ":" + project + ":" + r.clientKey + ":" + token.OpenID), Version: 1, ExpiresAt: time.Unix(token.AccessExpiresAt, 0).UTC().Format(time.RFC3339)}
	p.Social = &SocialState{Connections: []SocialConnection{c}, Jobs: []SocialJob{}}
	p.Version++
	d.Projects[k] = p
	private := socialPrivate(space, project, "token", id)
	sealed, e := r.seal(private, token)
	if e != nil {
		t.Fatal(e)
	}
	d.Receipts[private] = mediaReceipt{SocialSecret: sealed}
	if e = r.m.commit(d); e != nil {
		t.Fatal(e)
	}
	return id
}
func rotatedSocialToken() socialToken {
	return socialToken{Access: "synthetic-new-access", Refresh: "synthetic-rotated-refresh", OpenID: "synthetic-account", Scope: "video.publish,video.upload", TokenType: "Bearer", Expires: 86400, RefreshExpires: 31536000}
}
func TestSocialRefreshRotationAndAbsoluteRecovery(t *testing.T) {
	dir := t.TempDir()
	m, _ := openMediaStore(dir)
	defer m.close()
	calls := 0
	r := testSocialRuntime(t, m, func(req *http.Request) (*http.Response, error) {
		calls++
		_ = req.ParseForm()
		if req.Method != "POST" || req.URL.String() != "https://open.tiktokapis.com/v2/oauth/token/" || req.Header.Get("Content-Type") != "application/x-www-form-urlencoded" || req.Form.Get("grant_type") != "refresh_token" || req.Form.Get("refresh_token") != "synthetic-old-refresh" || req.Form.Get("client_secret") != "synthetic-secret" {
			t.Fatal("refresh contract")
		}
		return socialJSON(rotatedSocialToken()), nil
	})
	id := seedRefresh(t, r, "p", "s")
	before := time.Now().Unix()
	token, e := r.authorizedToken(refreshContext(), "p", "s", id, "video.publish")
	if e != nil || token.Refresh != "synthetic-rotated-refresh" || token.AccessExpiresAt < before+86400 || token.RefreshExpiresAt < before+31536000 {
		t.Fatal(token, e)
	}
	_, e = r.authorizedToken(refreshContext(), "p", "s", id, "video.upload")
	if e != nil || calls != 1 {
		t.Fatal("unnecessary second refresh", e)
	}
	raw, _ := os.ReadFile(filepath.Join(m.dir, "state.json"))
	if strings.Contains(string(raw), "synthetic-rotated-refresh") || strings.Contains(string(raw), "synthetic-new-access") {
		t.Fatal("plaintext rotation")
	}
	restored, e := openMediaStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer restored.close()
	r.m = restored
	again, e := r.authorizedToken(refreshContext(), "p", "s", id, "")
	if e != nil || again.RefreshExpiresAt != token.RefreshExpiresAt || calls != 1 {
		t.Fatal("restart extended/reissued refresh", e)
	}
}
func TestSocialRefreshUnknownInvalidGrantAndExpiry(t *testing.T) {
	for _, scenario := range []string{"timeout", "invalid_grant", "expired", "legacy", "wrong-account", "wrong-type", "scope-expansion", "scope-reduction", "access-ttl", "refresh-ttl"} {
		t.Run(scenario, func(t *testing.T) {
			m, _ := openMediaStore(t.TempDir())
			defer m.close()
			calls := 0
			r := testSocialRuntime(t, m, func(*http.Request) (*http.Response, error) {
				calls++
				switch scenario {
				case "timeout":
					return nil, errors.New("synthetic lost result")
				case "invalid_grant":
					return &http.Response{StatusCode: 400, Body: io.NopCloser(strings.NewReader(`{"error":"invalid_grant"}`))}, nil
				}
				next := rotatedSocialToken()
				if scenario == "access-ttl" {
					next.Expires = 86401
				}
				if scenario == "refresh-ttl" {
					next.RefreshExpires = 31536001
				}
				if scenario == "wrong-account" {
					next.OpenID = "other"
				}
				if scenario == "wrong-type" {
					next.TokenType = "unknown"
				}
				if scenario == "scope-expansion" {
					next.Scope += " ,user.info.basic"
				}
				if scenario == "scope-reduction" {
					next.Scope = "video.upload"
				}
				return socialJSON(next), nil
			})
			id := seedRefresh(t, r, "p", "s")
			if scenario == "expired" || scenario == "legacy" {
				m.mu.Lock()
				d := m.clone()
				private := socialPrivate("s", "p", "token", id)
				var old socialToken
				_ = r.open(private, d.Receipts[private].SocialSecret, &old)
				old.RefreshExpiresAt = 0
				if scenario == "expired" {
					old.RefreshExpiresAt = time.Now().Add(-time.Hour).Unix()
				}
				sealed, _ := r.seal(private, old)
				d.Receipts[private] = mediaReceipt{SocialSecret: sealed}
				_ = m.commit(d)
				m.mu.Unlock()
			}
			_, e := r.authorizedToken(refreshContext(), "p", "s", id, "video.publish")
			if e == nil {
				t.Fatal("unsafe refresh accepted")
			}
			state := m.Get("p", "s").Social.Connections[0].Status
			want := "refresh_unknown"
			if scenario == "expired" || scenario == "legacy" || scenario == "invalid_grant" {
				want = "expired"
			}
			if scenario == "scope-reduction" {
				want = "connected"
			}
			if state != want {
				t.Fatal(state, want)
			}
			_, _ = r.authorizedToken(refreshContext(), "p", "s", id, "video.publish")
			if calls > 1 {
				t.Fatal("blind refresh replay")
			}
			if want == "expired" {
				m.mu.Lock()
				_, exists := m.data.Receipts[socialPrivate("s", "p", "token", id)]
				m.mu.Unlock()
				if exists {
					t.Fatal("invalid refresh credential retained")
				}
			}
		})
	}
}
func TestSocialRefreshReadOnlyAndCrashQuarantine(t *testing.T) {
	m, _ := openMediaStore(t.TempDir())
	defer m.close()
	calls := 0
	r := testSocialRuntime(t, m, func(*http.Request) (*http.Response, error) { calls++; return socialJSON(rotatedSocialToken()), nil })
	id := seedRefresh(t, r, "p", "s")
	r.auth = &AuthStore{}
	read := context.WithValue(context.Background(), socialGuardKey{}, func() error { return nil })
	before := m.Get("p", "s").Version
	if _, e := r.authorizedToken(read, "p", "s", id, ""); e == nil || calls != 0 || m.Get("p", "s").Version != before {
		t.Fatal("GET changed token")
	}
	r.auth = nil
	_, attempt, version, e := r.claimSocialRefresh(refreshContext(), "p", "s", id, "")
	if e != nil || attempt == "" {
		t.Fatal(e)
	}
	if _, e = r.authorizedToken(refreshContext(), "p", "s", id, ""); e == nil || calls != 0 {
		t.Fatal("crashed refresh replay")
	}
	m.mu.Lock()
	d := m.clone()
	p := mediaState(d, mediaKey("s", "p"))
	p.Social.Connections[0].RefreshLeaseUntil = time.Now().Add(-time.Minute).Format(time.RFC3339)
	d.Projects[mediaKey("s", "p")] = p
	_ = m.commit(d)
	m.mu.Unlock()
	if r.view(m.Get("p", "s")).Connections[0].Status != "refresh_unknown" {
		t.Fatal("crash not exposed")
	}
	if _, e = r.finishSocialRefresh("p", "s", id, "wrong-attempt", version, "connected", rotatedSocialToken()); e == nil {
		t.Fatal("unfenced completion")
	}
}
func TestSocialRefreshRevocationAndReconnectFence(t *testing.T) {
	for _, reconnect := range []bool{false, true} {
		m, _ := openMediaStore(t.TempDir())
		r := testSocialRuntime(t, m, nil)
		id := seedRefresh(t, r, "p", "s")
		r.client.Transport = socialRoundTrip(func(req *http.Request) (*http.Response, error) {
			if req.URL.Path == "/v2/oauth/revoke/" {
				return socialJSON(map[string]any{}), nil
			}
			if reconnect {
				m.mu.Lock()
				d := m.clone()
				p := mediaState(d, mediaKey("s", "p"))
				p.Social.Connections[0].Status = "connected"
				p.Social.Connections[0].Version++
				next := rotatedSocialToken()
				next.Access = "synthetic-reconnected"
				stampSocialToken(&next, time.Now())
				private := socialPrivate("s", "p", "token", id)
				sealed, _ := r.seal(private, next)
				d.Receipts[private] = mediaReceipt{SocialSecret: sealed}
				p.Social.Connections[0].ExpiresAt = time.Unix(next.AccessExpiresAt, 0).Format(time.RFC3339)
				d.Projects[mediaKey("s", "p")] = p
				_ = m.commit(d)
				m.mu.Unlock()
			} else {
				_, e := r.revoke(context.Background(), "p", "s", id, m.Get("p", "s").Version)
				if e != nil {
					t.Fatal(e)
				}
			}
			return socialJSON(rotatedSocialToken()), nil
		})
		if _, e := r.authorizedToken(refreshContext(), "p", "s", id, ""); e == nil {
			t.Fatal("late refresh overwrote account change")
		}
		if reconnect {
			got, e := r.token("p", "s", id)
			if e != nil || got.Access != "synthetic-reconnected" {
				t.Fatal("reconnected token replaced")
			}
		} else if _, e := r.token("p", "s", id); e == nil {
			t.Fatal("revoked credentials resurrected")
		}
		m.close()
	}
}
func TestPostgresSocialRefreshOneFlightAndDurableRotation(t *testing.T) {
	db := postgresTestDB(t)
	a, b := postgresTestStore(t, db), postgresTestStore(t, db)
	project, _, e := a.CreateProject("tenant-a", "refresh-project", projectInput())
	if e != nil {
		t.Fatal(e)
	}
	var calls atomic.Int32
	fn := socialRoundTrip(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		time.Sleep(20 * time.Millisecond)
		return socialJSON(rotatedSocialToken()), nil
	})
	ra, rb := testSocialRuntime(t, a.media, fn), testSocialRuntime(t, b.media, fn)
	id := seedRefresh(t, ra, project.ID, "tenant-a")
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r := ra
			if i%2 == 1 {
				r = rb
			}
			_, _ = r.authorizedToken(refreshContext(), project.ID, "tenant-a", id, "video.publish")
		}(i)
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatal("duplicate refresh issuance", calls.Load())
	}
	token, e := rb.authorizedToken(refreshContext(), project.ID, "tenant-a", id, "video.publish")
	if e != nil || token.Refresh != "synthetic-rotated-refresh" {
		t.Fatal("PG rotation not shared", e)
	}
	if _, e = rb.authorizedToken(refreshContext(), project.ID, "other-tenant", id, ""); e == nil {
		t.Fatal("cross tenant refresh")
	}
	public, _ := json.Marshal(b.media.Get(project.ID, "tenant-a"))
	if strings.Contains(string(public), "synthetic-rotated-refresh") {
		t.Fatal("public refresh leak")
	}
}
func TestSocialRefreshPreDispatchRevocationAndActorGuard(t *testing.T) {
	m, _ := openMediaStore(t.TempDir())
	defer m.close()
	calls := 0
	r := testSocialRuntime(t, m, func(*http.Request) (*http.Response, error) { calls++; return socialJSON(rotatedSocialToken()), nil })
	id := seedRefresh(t, r, "p", "s")
	old, attempt, version, e := r.claimSocialRefresh(refreshContext(), "p", "s", id, "")
	if e != nil {
		t.Fatal(e)
	}
	m.mu.Lock()
	d := m.clone()
	p := mediaState(d, mediaKey("s", "p"))
	p.Social.Connections[0].Status = "revoked"
	p.Social.Connections[0].Version++
	d.Projects[mediaKey("s", "p")] = p
	_ = m.commit(d)
	m.mu.Unlock()
	if _, _, e = r.exchangeSocialRefresh(refreshContext(), old, func() bool { return r.socialRefreshActive("p", "s", id, attempt, version) }); e == nil || calls != 0 {
		t.Fatal("refresh sent after revoked claim")
	}
	r.auth = &AuthStore{}
	ctx := context.WithValue(refreshContext(), socialGuardKey{}, func() error { return errRuntimeDenied })
	if _, e = r.authorizedToken(ctx, "p", "s", id, ""); e == nil || calls != 0 {
		t.Fatal("revoked actor renewed")
	}
}
func TestSocialRevokeLateCompletionPreservesReconnect(t *testing.T) {
	m, _ := openMediaStore(t.TempDir())
	defer m.close()
	r := testSocialRuntime(t, m, nil)
	id := seedRefresh(t, r, "p", "s")
	r.client.Transport = socialRoundTrip(func(*http.Request) (*http.Response, error) {
		m.mu.Lock()
		d := m.clone()
		p := mediaState(d, mediaKey("s", "p"))
		p.Social.Connections[0].Status = "connected"
		p.Social.Connections[0].Version++
		d.Projects[mediaKey("s", "p")] = p
		next := rotatedSocialToken()
		stampSocialToken(&next, time.Now())
		private := socialPrivate("s", "p", "token", id)
		sealed, _ := r.seal(private, next)
		d.Receipts[private] = mediaReceipt{SocialSecret: sealed}
		_ = m.commit(d)
		m.mu.Unlock()
		return socialJSON(map[string]any{}), nil
	})
	v, e := r.revoke(context.Background(), "p", "s", id, m.Get("p", "s").Version)
	if e != nil || v.Connections[0].Status != "connected" {
		t.Fatal("late revoke clobbered reconnect", e)
	}
	if m.Get("p", "s").Social.Connections[0].Version != 3 {
		t.Fatal("wrong reconnect version")
	}
}
func TestSocialRefreshCommitLossQuarantinesAfterRestart(t *testing.T) {
	dir := t.TempDir()
	m, _ := openMediaStore(dir)
	calls := 0
	r := testSocialRuntime(t, m, nil)
	id := seedRefresh(t, r, "p", "s")
	saved := m.dir + "-saved"
	r.client.Transport = socialRoundTrip(func(*http.Request) (*http.Response, error) {
		calls++
		if e := os.Rename(m.dir, saved); e != nil {
			t.Fatal(e)
		}
		return socialJSON(rotatedSocialToken()), nil
	})
	if _, e := r.authorizedToken(refreshContext(), "p", "s", id, ""); e == nil {
		t.Fatal("lost commit reported success")
	}
	if e := os.Rename(saved, m.dir); e != nil {
		t.Fatal(e)
	}
	m.close()
	restored, e := openMediaStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer restored.close()
	r.m = restored
	if _, e = r.authorizedToken(refreshContext(), "p", "s", id, ""); e == nil || calls != 1 {
		t.Fatal("ambiguous commit replayed refresh")
	}
}
func TestPostgresSocialRefreshUnknownSurvivesAnotherAPI(t *testing.T) {
	db := postgresTestDB(t)
	a, b := postgresTestStore(t, db), postgresTestStore(t, db)
	project, _, e := a.CreateProject("tenant-a", "unknown-refresh-project", projectInput())
	if e != nil {
		t.Fatal(e)
	}
	var calls atomic.Int32
	fn := socialRoundTrip(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return nil, errors.New("synthetic lost rotated token")
	})
	ra, rb := testSocialRuntime(t, a.media, fn), testSocialRuntime(t, b.media, fn)
	id := seedRefresh(t, ra, project.ID, "tenant-a")
	if _, e = ra.authorizedToken(refreshContext(), project.ID, "tenant-a", id, ""); e == nil {
		t.Fatal("uncertain refresh succeeded")
	}
	if _, e = rb.authorizedToken(refreshContext(), project.ID, "tenant-a", id, ""); e == nil || calls.Load() != 1 {
		t.Fatal("anotherAPI repeated uncertain grant")
	}
	c := postgresTestStore(t, db)
	rc := testSocialRuntime(t, c.media, fn)
	if _, e = rc.authorizedToken(refreshContext(), project.ID, "tenant-a", id, ""); e == nil || calls.Load() != 1 {
		t.Fatal("freshAPI repeated uncertain grant")
	}
	v, e := rb.revoke(context.Background(), project.ID, "tenant-a", id, b.media.Get(project.ID, "tenant-a").Version)
	if e != nil || v.Connections[0].Status != "revoked_remote_unconfirmed" {
		t.Fatal("unknown rotation revoke status", e)
	}
	if _, e = rc.token(project.ID, "tenant-a", id); e == nil {
		t.Fatal("PG revocation not shared")
	}
}
func TestSocialRefreshInvalidGrantTombstonesPhotoAccess(t *testing.T) {
	m, _ := openMediaStore(t.TempDir())
	defer m.close()
	r := testSocialRuntime(t, m, func(*http.Request) (*http.Response, error) {
		return socialJSON(socialToken{Error: "invalid_grant"}), nil
	})
	id := seedRefresh(t, r, "p", "s")
	m.mu.Lock()
	d := m.clone()
	p := mediaState(d, mediaKey("s", "p"))
	p.Social.Jobs = append(p.Social.Jobs, SocialJob{ID: "photo", ConnectionID: id, MediaType: "PHOTO", Status: "unknown", PhotoGrantID: "synthetic-grant"})
	d.Projects[mediaKey("s", "p")] = p
	_ = m.commit(d)
	m.mu.Unlock()
	_, _ = r.authorizedToken(refreshContext(), "p", "s", id, "")
	job := m.Get("p", "s").Social.Jobs[0]
	if !job.PhotoAccessRevoked || job.Status != "unknown" {
		t.Fatal("invalidgrant grantrevival or dedupe reset")
	}
}
func TestSocialRefreshActorRevokedDuringResponse(t *testing.T) {
	m, _ := openMediaStore(t.TempDir())
	defer m.close()
	allowed := true
	calls := 0
	r := testSocialRuntime(t, m, func(*http.Request) (*http.Response, error) {
		calls++
		allowed = false
		return socialJSON(rotatedSocialToken()), nil
	})
	id := seedRefresh(t, r, "p", "s")
	r.auth = &AuthStore{}
	ctx := context.WithValue(refreshContext(), socialGuardKey{}, func() error {
		if !allowed {
			return errRuntimeDenied
		}
		return nil
	})
	if _, e := r.authorizedToken(ctx, "p", "s", id, ""); e == nil || calls != 1 {
		t.Fatal("revoked actor accepted rotated credentials")
	}
	if m.Get("p", "s").Social.Connections[0].Status != "refresh_unknown" {
		t.Fatal("revoked refresh not quarantined")
	}
	allowed = true
	if _, e := r.authorizedToken(ctx, "p", "s", id, ""); e == nil || calls != 1 {
		t.Fatal("unknownrefresh replay afterpermissionrestore")
	}
}

func TestSocialCreatorPOSTRejectsBodyBeforeRefresh(t *testing.T) {
	m, _ := openMediaStore(t.TempDir())
	defer m.close()
	calls := 0
	r := testSocialRuntime(t, m, func(*http.Request) (*http.Response, error) { calls++; return socialJSON(rotatedSocialToken()), nil })
	auth, e := OpenAuthStore(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	user, cookie, session, e := auth.login("refresh-body@example.test", "synthetic-password", true)
	if e != nil {
		t.Fatal(e)
	}
	space := auth.spaces(user.ID)[0]["id"]
	r.auth = auth
	id := seedRefresh(t, r, "p", space)
	mux := http.NewServeMux()
	r.routes(mux, &FileStore{media: m}, "/api/v1/projects/{id}/social", func(w http.ResponseWriter, req *http.Request, write bool) (string, string, bool) {
		return projectWriteScope(w, req)
	})
	for _, body := range []string{`{"unexpected":true}`, `{`, `null`} {
		req := httptest.NewRequest("POST", "/api/v1/projects/p/social/connections/"+id+"/creator?space_id="+space, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", "refresh-body-check")
		req.Header.Set("Origin", "http://localhost:5173")
		req.Header.Set("X-CSRF-Token", session.CSRF)
		req.AddCookie(&http.Cookie{Name: "studio_session", Value: cookie})
		req = req.WithContext(context.WithValue(req.Context(), authScopeKey{}, authScope{UserID: user.ID, SpaceID: space, Role: "owner"}))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		if w.Code != 400 || calls != 0 {
			t.Fatal("invalid creator body reached credential exchange", w.Code, calls)
		}
	}
}
