package studio

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type socialRoundTrip func(*http.Request) (*http.Response, error)

func (f socialRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func testSocialRuntime(t *testing.T, m *MediaStore, fn socialRoundTrip) *socialRuntime {
	t.Helper()
	block, _ := aes.NewCipher([]byte("0123456789abcdef0123456789abcdef"))
	key, _ := cipher.NewGCM(block)
	return &socialRuntime{m: m, key: key, approved: true, clientKey: "synthetic-client", clientSecret: "synthetic-secret", redirect: "https://studio.example/social-callback", base: "https://open.tiktokapis.com", client: &http.Client{Transport: fn}}
}
func socialJSON(v any) *http.Response {
	b, _ := json.Marshal(v)
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(b))), Header: http.Header{}}
}
func TestSocialExportIsolationCASRecovery(t *testing.T) {
	dir := t.TempDir()
	m, e := openMediaStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	p, e := m.AddAsset("p", "tenant-a", "upload-social", "sample", "", "", "", "uploaded", syntheticPNG(1), "image/png")
	if e != nil {
		t.Fatal(e)
	}
	in := SocialCreate{ExpectedVersion: p.Version, Platform: "xiaohongshu", Mode: "export", AssetID: p.Assets[0].ID, Confirmed: true}
	v, e := m.createSocial("p", "tenant-a", "social-export", in)
	if e != nil || v.Jobs[0].Status != "prepared" {
		t.Fatal(v, e)
	}
	again, e := m.createSocial("p", "tenant-a", "social-export", in)
	if e != nil || len(again.Jobs) != 1 {
		t.Fatal(e)
	}
	in.Caption = "must not auto-fill"
	if _, e = m.createSocial("p", "tenant-a", "social-export", in); !errors.Is(e, errMediaKey) {
		t.Fatal(e)
	}
	in.Caption = ""
	in.ExpectedVersion = v.Version
	if _, e = m.createSocial("p", "tenant-b", "foreign-key", in); e == nil {
		t.Fatal("tenant access")
	}
	in.ExpectedVersion = 1
	if _, e = m.createSocial("p", "tenant-a", "stale-key", in); !errors.Is(e, errMediaVersion) {
		t.Fatal(e)
	}
	m.close()
	m, e = openMediaStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer m.close()
	got := socialView(m.Get("p", "tenant-a"))
	if len(got.Jobs) != 1 || got.Jobs[0].ID != v.Jobs[0].ID {
		t.Fatal("lost local recovery")
	}
	cancel, e := m.cancelSocial("p", "tenant-a", "cancel-social", v.Jobs[0].ID, got.Version)
	if e != nil || cancel.Jobs[0].Status != "cancelled" {
		t.Fatal(e)
	}
}
func TestSocialOAuthEncryptedStateAndRevocation(t *testing.T) {
	m, e := openMediaStore(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer m.close()
	calls := 0
	r := testSocialRuntime(t, m, func(req *http.Request) (*http.Response, error) {
		calls++
		if req.URL.Path == "/v2/oauth/revoke/" {
			return nil, errors.New("synthetic timeout")
		}
		if req.URL.Host != "open.tiktokapis.com" || req.URL.Path != "/v2/oauth/token/" {
			t.Fatal("unexpected endpoint")
		}
		_ = req.ParseForm()
		if req.Form.Get("client_secret") != "synthetic-secret" || req.Form.Get("grant_type") != "authorization_code" {
			t.Fatal("bad token request")
		}
		return socialJSON(socialToken{Access: "synthetic-access", Refresh: "synthetic-refresh", OpenID: "synthetic-open-id", Scope: "video.publish,video.upload", Expires: 3600, RefreshExpires: 31536000, TokenType: "Bearer"}), nil
	})
	u, e := r.start("p", "tenant", "user", "session", 1, "oauth-start-key")
	if e != nil {
		t.Fatal(e)
	}
	againURL, replayErr := r.start("p", "tenant", "user", "session", 1, "oauth-start-key")
	if replayErr != nil || againURL != u {
		t.Fatal("OAuth start idempotency", replayErr)
	}
	auth, _ := url.Parse(u)
	state := auth.Query().Get("state")
	if state == "" || auth.Host != "www.tiktok.com" {
		t.Fatal("bad authorization URL")
	}
	if _, e = r.complete(context.Background(), "p", "tenant", "intruder", "session", state, "code"); e == nil || calls != 0 {
		t.Fatal("unbound OAuth")
	}
	if _, e = r.complete(context.Background(), "p", "tenant", "user", "new-session", state, "code"); e == nil || calls != 0 {
		t.Fatal("OAuth session binding bypass")
	}
	v, e := r.complete(context.Background(), "p", "tenant", "user", "session", state, "code")
	if e != nil || len(v.Connections) != 1 {
		t.Fatal(e)
	}
	if _, e = r.complete(context.Background(), "p", "tenant", "user", "session", state, "code"); e == nil || calls != 1 {
		t.Fatal("OAuth code replay")
	}
	conn := v.Connections[0]
	token, e := r.token("p", "tenant", conn.ID)
	if e != nil || token.Access != "synthetic-access" {
		t.Fatal(e)
	}
	if _, e = r.token("p", "foreign", conn.ID); e == nil {
		t.Fatal("cross-tenant token")
	}
	raw, _ := os.ReadFile(filepath.Join(m.dir, "state.json"))
	for _, secret := range []string{"synthetic-access", "synthetic-refresh", "synthetic-secret", "synthetic-open-id"} {
		if strings.Contains(string(raw), secret) {
			t.Fatal("plaintext token persisted")
		}
	}
	public, _ := json.Marshal(m.Get("p", "tenant"))
	if strings.Contains(string(public), "social_secret") || strings.Contains(string(public), "synthetic-access") {
		t.Fatal("token exposed")
	}
	v, e = r.revoke(context.Background(), "p", "tenant", conn.ID, v.Version)
	if e != nil || v.Connections[0].Status != "revoked_remote_unconfirmed" {
		t.Fatal(v, e)
	}
	if _, e = r.token("p", "tenant", conn.ID); e == nil {
		t.Fatal("revoked token resurrected")
	}
	if got := socialView(m.Get("p", "tenant")).Connections[0].Status; got != "revoked_remote_unconfirmed" {
		t.Fatal("lost unknown revoke outcome", got)
	}
}
func TestSocialClaimUnknownNoResendAndLeases(t *testing.T) {
	m, _ := openMediaStore(t.TempDir())
	defer m.close()
	r := testSocialRuntime(t, m, func(*http.Request) (*http.Response, error) { t.Fatal("unexpected network"); return nil, nil })
	in := SocialCreate{Platform: "tiktok", Mode: "direct", Confirmed: true, RightsConfirmed: true, ConnectionID: "account", PrivacyLevel: "SELF_ONLY"}
	p := mediaState(m.data, mediaKey("s", "p"))
	p.Social = &SocialState{Jobs: []SocialJob{{ID: "job", Platform: "tiktok", Mode: "direct", Status: "prepared", ConnectionID: "account", Version: 1, Intent: &in}}, Connections: []SocialConnection{}}
	m.data.Projects[mediaKey("s", "p")] = p
	_ = m.commit(m.data)
	job, e := r.claim("p", "s", "job", 1, false)
	if e != nil {
		t.Fatal(e)
	}
	v, e := r.finish("p", "s", "job", "unknown", "", "create_outcome_unknown", job.Attempt)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = r.claim("p", "s", "job", v.Version, false); e == nil {
		t.Fatal("unknown was replayed")
	}
	if _, e = r.claim("p", "s", "job", v.Version, true); e == nil {
		t.Fatal("unknown without id queried")
	}
	v, e = r.finish("p", "s", "job", "dispatching", "remote-id", "", job.Attempt)
	if e != nil {
		t.Fatal(e)
	}
	// Simulate an active invocation with a persisted lease.
	m.mu.Lock()
	d := m.clone()
	p = d.Projects[mediaKey("s", "p")]
	p.Social.Jobs[0].LeaseUntil = time.Now().Add(time.Minute).UTC().Format(time.RFC3339)
	d.Projects[mediaKey("s", "p")] = p
	_ = m.commit(d)
	m.mu.Unlock()
	if _, e = r.claim("p", "s", "job", v.Version, true); e == nil {
		t.Fatal("concurrent poll clobbered upload")
	}
	if _, e = r.finish("p", "s", "job", "published", "remote-id", "", "stale-attempt"); e == nil {
		t.Fatal("stale attempt overwrote outcome")
	}
}
func TestSocialRouteRequiresProjectAndRole(t *testing.T) { // Ownership is resolved before every job operation.
	m, _ := openMediaStore(t.TempDir())
	defer m.close()
	_ = httptest.NewRecorder()
	r := testSocialRuntime(t, m, nil)
	if _, e := r.start("p", "s", "u", "session", 2); !errors.Is(e, errMediaVersion) {
		t.Fatal(e)
	}
	if r.view(m.Get("p", "other")).Version != 1 {
		t.Fatal("foreign state")
	}
}
func TestSocialTikTokSyntheticLifecycleAndUnknownRecovery(t *testing.T) {
	for _, scenario := range []string{"direct", "draft", "timeout", "bad-upload", "revoked-poll", "actor-revoked"} {
		t.Run(scenario, func(t *testing.T) {
			m, e := openMediaStore(t.TempDir())
			if e != nil {
				t.Fatal(e)
			}
			defer m.close()
			file := filepath.Join(t.TempDir(), "synthetic.mp4")
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			if e = ffmpeg(ctx, "-f", "lavfi", "-i", "color=c=red:s=64x64:r=24:d=1", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-an", file); e != nil {
				t.Fatal(e)
			}
			data, _ := os.ReadFile(file)
			p, e := m.AddAsset("p", "s", "social-video", "synthetic.mp4", "", "", "", "uploaded", data, "video/mp4")
			if e != nil {
				t.Fatal(e)
			}
			creates, uploads, polls := 0, 0, 0
			r := testSocialRuntime(t, m, nil)
			actorOK := true
			r.client.Transport = socialRoundTrip(func(req *http.Request) (*http.Response, error) {
				switch req.URL.Path {
				case "/v2/oauth/token/":
					return socialJSON(socialToken{Access: "synthetic-access", Refresh: "synthetic-refresh", OpenID: "account", Scope: "video.publish,video.upload", Expires: 3600, RefreshExpires: 31536000, TokenType: "Bearer"}), nil
				case "/v2/post/publish/creator_info/query/":
					if scenario == "actor-revoked" {
						actorOK = false
					}
					return socialJSON(map[string]any{"data": SocialCreator{Username: "synthetic", Nickname: "Synthetic", Privacy: []string{"SELF_ONLY"}, MaxDuration: 60}, "error": map[string]string{"code": "ok"}}), nil
				case "/v2/post/publish/video/init/", "/v2/post/publish/inbox/video/init/":
					creates++
					if scenario == "timeout" {
						return nil, errors.New("synthetic unknown outcome")
					}
					var body map[string]json.RawMessage
					_ = json.NewDecoder(req.Body).Decode(&body)
					if scenario == "draft" {
						if body["post_info"] != nil {
							t.Fatal("draft wrongly supplied directpostinfo")
						}
					} else {
						var post map[string]any
						_ = json.Unmarshal(body["post_info"], &post)
						if post["privacy_level"] != "SELF_ONLY" || post["disable_comment"] != true || post["is_aigc"] != true {
							t.Fatal("unsafe post defaults")
						}
					}
					u := "https://open-upload.tiktokapis.com/video/?upload_token=synthetic"
					if scenario == "bad-upload" {
						u = "https://127.0.0.1/internal"
					}
					return socialJSON(map[string]any{"data": map[string]string{"publish_id": "synthetic-publish", "upload_url": u}, "error": map[string]string{"code": "ok"}}), nil
				case "/video/":
					uploads++
					if req.URL.Host != "open-upload.tiktokapis.com" || req.Method != "PUT" || req.Header.Get("Authorization") != "" || req.Header.Get("Content-Range") == "" {
						t.Fatal("upload protocol")
					}
					b, _ := io.ReadAll(req.Body)
					if len(b) != len(data) {
						t.Fatal("wrong source bytes")
					}
					return &http.Response{StatusCode: 201, Body: io.NopCloser(strings.NewReader(""))}, nil
				case "/v2/post/publish/status/fetch/":
					polls++
					status := "PUBLISH_COMPLETE"
					if scenario == "draft" {
						status = "SEND_TO_USER_INBOX"
					}
					return socialJSON(map[string]any{"data": map[string]string{"status": status}, "error": map[string]string{"code": "ok"}}), nil
				default:
					t.Fatal("unexpected endpoint", req.URL.Path)
					return nil, nil
				}
			})
			u, e := r.start("p", "s", "user", "session", p.Version)
			if e != nil {
				t.Fatal(e)
			}
			parsed, _ := url.Parse(u)
			v, e := r.complete(ctx, "p", "s", "user", "session", parsed.Query().Get("state"), "synthetic-code")
			if e != nil {
				t.Fatal(e)
			}
			in := SocialCreate{ExpectedVersion: v.Version, Platform: "tiktok", Mode: "direct", AssetID: p.Assets[0].ID, Confirmed: true, RightsConfirmed: true, ConnectionID: v.Connections[0].ID, PrivacyLevel: "SELF_ONLY", IsAIGC: true}
			if scenario == "draft" {
				in.Mode = "draft"
			}
			v, e = m.createSocial("p", "s", "create-social", in)
			if e != nil {
				t.Fatal(e)
			}
			if scenario == "actor-revoked" {
				r.auth = &AuthStore{}
				ctx = context.WithValue(ctx, socialGuardKey{}, func() error {
					if !actorOK {
						return errRuntimeDenied
					}
					return nil
				})
			}
			v, e = r.execute(ctx, "p", "s", v.Jobs[0].ID, v.Version, false)
			if e != nil {
				t.Fatal(e)
			}
			if scenario == "actor-revoked" {
				if creates != 0 || uploads != 0 {
					t.Fatal("revoked actor dispatched")
				}
				return
			}
			if scenario == "timeout" {
				if v.Jobs[0].Status != "unknown" || v.Jobs[0].PublishID != "" {
					t.Fatal(v.Jobs[0])
				}
				if _, e = r.execute(ctx, "p", "s", v.Jobs[0].ID, v.Version, false); e == nil {
					t.Fatal("replayed uncertain create")
				}
				in.ExpectedVersion = v.Version
				if _, e = m.createSocial("p", "s", "duplicate-intent", in); e == nil {
					t.Fatal("new-key duplicate bypass")
				}
				if creates != 1 {
					t.Fatal("replayed")
				}
				againURL, err := r.start("p", "s", "user", "session", v.Version)
				if err != nil {
					t.Fatal(err)
				}
				againParsed, _ := url.Parse(againURL)
				linked, err := r.complete(ctx, "p", "s", "user", "session", againParsed.Query().Get("state"), "new-code")
				if err != nil {
					t.Fatal(err)
				}
				if len(linked.Connections) != 1 || linked.Connections[0].ID != in.ConnectionID {
					t.Fatal("reconnect changed account identity")
				}
				in.ExpectedVersion = linked.Version
				if _, err = m.createSocial("p", "s", "duplicate-reconnect", in); err == nil {
					t.Fatal("reconnect bypassed unknown guard")
				}
				return
			}
			if scenario == "bad-upload" {
				if uploads != 0 || v.Jobs[0].Status != "unknown" || v.Jobs[0].PublishID == "" {
					t.Fatal("SSRF or identity lost")
				}
				return
			}
			if scenario == "revoked-poll" {
				m.mu.Lock()
				d := m.clone()
				state := d.Projects[mediaKey("s", "p")]
				state.Social.Connections[0].Status = "revoked"
				d.Projects[mediaKey("s", "p")] = state
				_ = m.commit(d)
				m.mu.Unlock()
				v, e = r.execute(ctx, "p", "s", v.Jobs[0].ID, v.Version, true)
				if e != nil || v.Jobs[0].Status != "unknown" {
					t.Fatal("expired recovery unlocked duplicate", e, v.Jobs[0])
				}
				in.ExpectedVersion = v.Version
				if _, e = m.createSocial("p", "s", "duplicate-recovery", in); e == nil {
					t.Fatal("duplicate after expired recovery")
				}
				return
			}
			v, e = r.execute(ctx, "p", "s", v.Jobs[0].ID, v.Version, true)
			want := "published"
			if scenario == "draft" {
				want = "draft"
			}
			if e != nil || v.Jobs[0].Status != want || creates != 1 || uploads != 1 || polls != 1 {
				t.Fatal(v.Jobs, e, creates, uploads, polls)
			}
		})
	}
}
func TestSocialHTTPAuthorization(t *testing.T) {
	s := testStore(t)
	auth, e := OpenAuthStore(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	a, ta, sa, e := auth.login("social-a@example.test", "synthetic-password-A", true)
	if e != nil {
		t.Fatal(e)
	}
	b, tb, sb, e := auth.login("social-b@example.test", "synthetic-password-B", true)
	if e != nil {
		t.Fatal(e)
	}
	oa, ob := auth.spaces(a.ID)[0]["id"], auth.spaces(b.ID)[0]["id"]
	p, _, e := s.CreateProject(oa, "social-project", projectInput())
	if e != nil {
		t.Fatal(e)
	}
	h := SecureHandler(s, "http://localhost:5173", auth, AuthOptions{})
	path := "/api/v1/projects/" + p.ID + "/social"
	authRequest(t, h, "GET", path+"?space_id="+oa, "", "", "", 401)
	authRequest(t, h, "GET", path+"?space_id="+ob, "", tb, "", 404)
	authRequest(t, h, "POST", path+"/jobs?space_id="+oa, `{}`, ta, "", 403)
	authRequest(t, h, "PUT", "/api/v1/organizations/"+oa+"/members", `{"email":"social-b@example.test","role":"viewer"}`, ta, sa.CSRF, 200)
	authRequest(t, h, "POST", path+"/jobs?space_id="+oa, `{}`, tb, sb.CSRF, 403)
	authRequest(t, h, "POST", path+"/connections/start?space_id="+oa, `{"expected_version":1,"platform":"tiktok"}`, tb, sb.CSRF, 403)
}
func TestPostgresSocialTwoInstancesEncryptionAndClaim(t *testing.T) {
	db := postgresTestDB(t)
	a, b := postgresTestStore(t, db), postgresTestStore(t, db)
	p, _, e := a.CreateProject("tenant-a", "social-project", projectInput())
	if e != nil {
		t.Fatal(e)
	}
	fn := socialRoundTrip(func(*http.Request) (*http.Response, error) {
		return socialJSON(socialToken{Access: "synthetic-pg-access", Refresh: "synthetic-pg-refresh", OpenID: "synthetic-pg-account", Scope: "video.publish,video.upload", Expires: 3600, RefreshExpires: 31536000, TokenType: "Bearer"}), nil
	})
	ra, rb := testSocialRuntime(t, a.media, fn), testSocialRuntime(t, b.media, fn)
	u, e := ra.start(p.ID, "tenant-a", "user", "session", 1)
	if e != nil {
		t.Fatal(e)
	}
	parsed, _ := url.Parse(u)
	v, e := rb.complete(context.Background(), p.ID, "tenant-a", "user", "session", parsed.Query().Get("state"), "synthetic-code")
	if e != nil {
		t.Fatal(e)
	}
	conn := v.Connections[0]
	if token, e := ra.token(p.ID, "tenant-a", conn.ID); e != nil || token.Access != "synthetic-pg-access" {
		t.Fatal("cross-instance token recovery", e)
	}
	if _, e := rb.token(p.ID, "tenant-b", conn.ID); e == nil {
		t.Fatal("cross-tenant PG token")
	}
	a.media.mu.Lock()
	d := a.media.clone()
	k := mediaKey("tenant-a", p.ID)
	media := mediaState(d, k)
	media.Social.Jobs = append(media.Social.Jobs, SocialJob{ID: "pg-job", Platform: "tiktok", Mode: "direct", Status: "prepared", ConnectionID: conn.ID, Version: 1})
	d.Projects[k] = media
	e = a.media.commit(d)
	a.media.mu.Unlock()
	if e != nil {
		t.Fatal(e)
	}
	job, e := ra.claim(p.ID, "tenant-a", "pg-job", v.Version, false)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = rb.claim(p.ID, "tenant-a", "pg-job", v.Version, false); !errors.Is(e, errMediaVersion) {
		t.Fatal("PG double claim", e)
	}
	v, e = ra.finish(p.ID, "tenant-a", "pg-job", "unknown", "", "synthetic-timeout", job.Attempt)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = rb.claim(p.ID, "tenant-a", "pg-job", v.Version, false); e == nil {
		t.Fatal("PG unknown replay")
	}
	public, _ := json.Marshal(b.media.Get(p.ID, "tenant-a"))
	if strings.Contains(string(public), "synthetic-pg-access") {
		t.Fatal("PG public secret leak")
	}
}
func TestSocialOAuthRevocationDuringExchangeAndAAD(t *testing.T) {
	m, _ := openMediaStore(t.TempDir())
	defer m.close()
	allowed := true
	r := testSocialRuntime(t, m, func(*http.Request) (*http.Response, error) {
		allowed = false
		return socialJSON(socialToken{Access: "synthetic-never-persist", Refresh: "synthetic-refresh", OpenID: "synthetic-account", Scope: "video.publish", Expires: 3600, RefreshExpires: 31536000, TokenType: "Bearer"}), nil
	})
	u, e := r.start("p", "s", "u", "session", 1)
	if e != nil {
		t.Fatal(e)
	}
	parsed, _ := url.Parse(u)
	r.auth = &AuthStore{}
	ctx := context.WithValue(context.Background(), socialGuardKey{}, func() error {
		if !allowed {
			return errRuntimeDenied
		}
		return nil
	})
	if _, e = r.complete(ctx, "p", "s", "u", "session", parsed.Query().Get("state"), "synthetic-code"); e == nil {
		t.Fatal("revoked actor linked account after exchange")
	}
	if len(socialState(m.Get("p", "s")).Connections) != 0 {
		t.Fatal("persisted revoked connection")
	}
	r.auth = nil
	sealed, e := r.seal("tenant-a:project:token", socialToken{Access: "synthetic"})
	if e != nil {
		t.Fatal(e)
	}
	var token socialToken
	if r.open("tenant-b:project:token", sealed, &token) == nil {
		t.Fatal("ciphertext moved across tenant")
	}
	r.clientKey = "different-approved-app"
	if r.open("tenant-a:project:token", sealed, &token) == nil {
		t.Fatal("ciphertext moved across app")
	}
}
func TestSocialUnknownRestartAndCooldown(t *testing.T) {
	dir := t.TempDir()
	m, _ := openMediaStore(dir)
	r := testSocialRuntime(t, m, nil)
	p := mediaState(m.data, mediaKey("s", "p"))
	p.Social = &SocialState{Jobs: []SocialJob{{ID: "job", Platform: "tiktok", Mode: "direct", Status: "prepared", Version: 1}}, Connections: []SocialConnection{}}
	m.data.Projects[mediaKey("s", "p")] = p
	_ = m.commit(m.data)
	j, e := r.claim("p", "s", "job", 1, false)
	if e != nil {
		t.Fatal(e)
	}
	v, e := r.finish("p", "s", "job", "unknown", "remote-id", "status_unavailable", j.Attempt)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = r.claim("p", "s", "job", v.Version, true); e == nil {
		t.Fatal("uncertainty cooldown bypass")
	}
	m.close()
	restored, e := openMediaStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer restored.close()
	r.m = restored
	if _, e = r.claim("p", "s", "job", v.Version, false); e == nil {
		t.Fatal("restart retried unknown create")
	}
	if restored.Get("p", "s").Social.Jobs[0].PublishID != "remote-id" {
		t.Fatal("lost reconciliation id")
	}
}
