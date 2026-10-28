package application

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func photoFixture(t *testing.T, fn socialRoundTrip) (*socialRuntime, context.Context, string, string, SocialCreate) {
	t.Helper()
	dr, project, space, user, in := douyinFixture(t)
	m := dr.store.media
	p, e := m.normalizeSocialPhoto(project, space, "normalize-photo", photoNormalize{in.ExpectedVersion, in.AssetID, true})
	if e != nil {
		t.Fatal(e)
	}
	id := p.Assets[len(p.Assets)-1].ID
	r := testSocialRuntime(t, m, fn)
	r.auth = dr.auth
	r.photo = &socialPhotoBroker{runtime: r, store: dr.store, origin: "https://studio.example", verified: true, now: time.Now}
	m.mu.Lock()
	d := m.clone()
	k := mediaKey(space, project)
	p = mediaState(d, k)
	p.Social = &SocialState{Jobs: []SocialJob{}, Connections: []SocialConnection{{ID: "account", AccountKey: "synthetic-account-fingerprint", Platform: "tiktok", Status: "connected", Version: 1, ExpiresAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339)}}}
	private := socialPrivate(space, project, "token", "account")
	sealed, e := r.seal(private, socialToken{Access: "synthetic-access", Refresh: "synthetic-refresh", OpenID: "synthetic-openid", Scope: "video.publish,video.upload", Expires: 3600, RefreshExpires: 86400, TokenType: "Bearer"})
	if e != nil {
		t.Fatal(e)
	}
	d.Receipts[private] = mediaReceipt{SocialSecret: sealed}
	d.Projects[k] = p
	e = m.commit(d)
	m.mu.Unlock()
	if e != nil {
		t.Fatal(e)
	}
	ctx := context.WithValue(context.Background(), authScopeKey{}, authScope{UserID: user, SpaceID: space, Role: "owner"})
	ctx = context.WithValue(ctx, socialGuardKey{}, func() error {
		if !runtimeRoleAllowed(r.auth.role(user, space)) {
			return errRuntimeDenied
		}
		return nil
	})
	return r, ctx, project, space, SocialCreate{ExpectedVersion: p.Version, Platform: "tiktok", Mode: "direct", MediaType: "PHOTO", PhotoAssetIDs: []string{id}, PhotoCoverIndex: 0, Title: "Photo", Caption: "Description", Confirmed: true, ConnectionID: "account", PrivacyLevel: "SELF_ONLY", RightsConfirmed: true, PublicMediaConfirmed: true, IsAIGC: true}
}
func TestPhotoNormalizationPixelsConsentAndRecovery(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 2160, 1080))
	for y := 0; y < 1080; y++ {
		for x := 0; x < 2160; x++ {
			src.SetNRGBA(x, y, color.NRGBA{R: 255, A: 128})
		}
	}
	data, e := normalizedPhoto(src)
	if e != nil {
		t.Fatal(e)
	}
	out, e := jpeg.Decode(bytes.NewReader(data))
	if e != nil || out.Bounds().Dx() != 1080 || out.Bounds().Dy() != 540 {
		t.Fatal("actual normalized dimensions", e)
	}
	r, g, b, _ := out.At(500, 250).RGBA()
	if r < 60000 || g < 28000 || g > 37000 || b < 28000 || b > 37000 {
		t.Fatal("white matte", r, g, b)
	}
	dr, p, s, _, in := douyinFixture(t)
	m := dr.store.media
	args := photoNormalize{in.ExpectedVersion, in.AssetID, false}
	if _, e = m.normalizeSocialPhoto(p, s, "no-consent", args); e == nil {
		t.Fatal("no consent")
	}
	args.Confirmed = true
	result, e := m.normalizeSocialPhoto(p, s, "normalize", args)
	if e != nil {
		t.Fatal(e)
	}
	again, e := m.normalizeSocialPhoto(p, s, "normalize", args)
	if e != nil || len(again.Assets) != len(result.Assets) {
		t.Fatal("replay", e)
	}
	if _, e = m.normalizeSocialPhoto(p, "foreign", "foreign", args); e == nil {
		t.Fatal("foreign normalize")
	}
	if _, e = m.normalizeSocialPhoto(p, s, "stale", args); e == nil {
		t.Fatal("stale normalize")
	}
	if result.Assets[0].ID != in.AssetID || result.Assets[0].MIMEType != "image/png" || result.Assets[1].MIMEType != "image/jpeg" {
		t.Fatal("original overwritten")
	}
}
func TestPhotoProtocolBrokerUnknownAndRevocation(t *testing.T) {
	for _, mode := range []string{"direct", "draft"} {
		t.Run(mode, func(t *testing.T) {
			var urls []string
			creates := 0
			r, ctx, p, s, in := photoFixture(t, func(req *http.Request) (*http.Response, error) {
				if req.URL.Path == "/v2/post/publish/creator_info/query/" {
					return socialJSON(map[string]any{"error": map[string]string{"code": "ok"}, "data": SocialCreator{Privacy: []string{"SELF_ONLY"}}}), nil
				}
				if req.URL.Path != "/v2/post/publish/content/init/" {
					t.Fatal("unexpected photo endpoint", req.URL.Path)
				}
				creates++
				var body map[string]any
				if json.NewDecoder(req.Body).Decode(&body) != nil {
					t.Fatal("payload")
				}
				want := "DIRECT_POST"
				if mode == "draft" {
					want = "MEDIA_UPLOAD"
				}
				if body["media_type"] != "PHOTO" || body["post_mode"] != want || body["is_aigc"] != true {
					t.Fatal("photo top-level contract", body)
				}
				post := body["post_info"].(map[string]any)
				if _, ok := post["is_aigc"]; ok {
					t.Fatal("AIGC misplaced")
				}
				if post["title"] != "Photo" || post["description"] != "Description" {
					t.Fatal("photo text")
				}
				if mode == "draft" {
					if _, ok := post["privacy_level"]; ok {
						t.Fatal("draft direct fields")
					}
				}
				source := body["source_info"].(map[string]any)
				if source["source"] != "PULL_FROM_URL" || source["photo_cover_index"] != float64(0) {
					t.Fatal("source contract")
				}
				for _, v := range source["photo_images"].([]any) {
					urls = append(urls, v.(string))
				}
				return nil, errors.New("synthetic ambiguous init")
			})
			in.Mode = mode
			if mode == "draft" {
				in.PrivacyLevel = ""
			}
			v, e := r.m.createSocial(p, s, "photo-intent", in)
			if e != nil {
				t.Fatal(e)
			}
			job := v.Jobs[0]
			v, e = r.execute(ctx, p, s, job.ID, v.Version, false)
			if e != nil || v.Jobs[0].Status != "unknown" || creates != 1 || len(urls) != 1 {
				t.Fatal("unknown contract", e, v)
			}
			fetch := func(method, raw string) *httptest.ResponseRecorder {
				u, _ := url.Parse(raw)
				req := httptest.NewRequest(method, u.RequestURI(), nil)
				req.Header.Set("Range", "bytes=0-7")
				w := httptest.NewRecorder()
				r.photo.serve(w, req)
				return w
			}
			if w := fetch("GET", urls[0]); w.Code != 206 || w.Body.Len() != 8 {
				t.Fatal("photo Range", w.Code)
			}
			if w := fetch("HEAD", urls[0]); w.Code != 206 || w.Body.Len() != 0 {
				t.Fatal("photo HEAD", w.Code)
			}
			if _, e = r.execute(ctx, p, s, job.ID, v.Version, false); e == nil || creates != 1 {
				t.Fatal("ambiguous create repeated")
			}
			in.ExpectedVersion = v.Version
			in.Caption = "new caption"
			if _, e = r.m.createSocial(p, s, "duplicate", in); e == nil {
				t.Fatal("unknown source duplicated")
			}
			// Explicitly normalizing again cannot bypass same-byte media-set guard.
			normalized, e := r.m.normalizeSocialPhoto(p, s, "renormalize", photoNormalize{v.Version, r.m.Get(p, s).Assets[0].ID, true})
			if e != nil {
				t.Fatal(e)
			}
			in.ExpectedVersion = normalized.Version
			in.PhotoAssetIDs = []string{normalized.Assets[len(normalized.Assets)-1].ID}
			if _, e = r.m.createSocial(p, s, "renormalized-duplicate", in); e == nil {
				t.Fatal("equivalent JPEG bypassed duplicate guard")
			}
			current := r.m.Get(p, s)
			v, e = r.m.revokePhotoAccess(p, s, "revoke-photo", job.ID, current.Version)
			if e != nil {
				t.Fatal(e)
			}
			if w := fetch("GET", urls[0]); w.Code != 404 {
				t.Fatal("revoked URL accessible")
			}
			if v.Jobs[0].Status != "unknown" {
				t.Fatal("revocation erased unknown")
			}
			raw, e := os.ReadFile(filepath.Join(r.m.dir, "state.json"))
			if e != nil || bytes.Contains(raw, []byte("?cap=")) || bytes.Contains(raw, []byte("synthetic-access")) {
				t.Fatal("grant/token leaked on disk", e)
			}
		})
	}
}
func TestPhotoIntentAndFileBounds(t *testing.T) {
	r, _, p, s, in := photoFixture(t, func(*http.Request) (*http.Response, error) { t.Fatal("network forbidden"); return nil, nil })
	cases := []func(*SocialCreate){func(v *SocialCreate) { v.PhotoAssetIDs = nil }, func(v *SocialCreate) { v.PhotoAssetIDs = make([]string, 36) }, func(v *SocialCreate) { v.PhotoAssetIDs = append(v.PhotoAssetIDs, v.PhotoAssetIDs[0]) }, func(v *SocialCreate) { v.PhotoAssetIDs = []string{"foreign"} }, func(v *SocialCreate) { v.PhotoCoverIndex = 1 }, func(v *SocialCreate) { v.PublicMediaConfirmed = false }, func(v *SocialCreate) { v.Title = strings.Repeat("😀", 46) }, func(v *SocialCreate) { v.Caption = strings.Repeat("😀", 2001) }, func(v *SocialCreate) { v.MediaType = "VIDEO" }, func(v *SocialCreate) { v.AssetID = v.PhotoAssetIDs[0] }, func(v *SocialCreate) { v.AllowDuet = true }}
	for i, change := range cases {
		v := in
		v.PhotoAssetIDs = append([]string(nil), in.PhotoAssetIDs...)
		change(&v)
		if _, e := r.m.createSocial(p, s, "invalid-photo-"+string(rune('a'+i)), v); e == nil {
			t.Fatal("invalid photo accepted", i)
		}
	}
	if _, e := r.m.createSocial(p, "foreign", "foreign-tenant", in); e == nil {
		t.Fatal("foreign tenant accepted")
	}
	path, e := r.m.localBlob(s, p, in.PhotoAssetIDs[0])
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(path, syntheticPNG(1), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = r.m.createSocial(p, s, "mime-spoof", in); e == nil {
		t.Fatal("PNG masquerading as JPEG")
	}
}

func TestPhotoSuccessOrderGrantRecoveryAndSecureHTTP(t *testing.T) {
	var urls []string
	calls := 0
	status := "PROCESSING_DOWNLOAD"
	r, ctx, p, s, in := photoFixture(t, func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/v2/post/publish/creator_info/query/":
			return socialJSON(map[string]any{"error": map[string]string{"code": "ok"}, "data": SocialCreator{Privacy: []string{"SELF_ONLY"}}}), nil
		case "/v2/post/publish/content/init/":
			calls++
			var body struct {
				Source struct {
					Images []string `json:"photo_images"`
					Cover  int      `json:"photo_cover_index"`
				} `json:"source_info"`
			}
			if e := json.NewDecoder(req.Body).Decode(&body); e != nil {
				t.Fatal(e)
			}
			urls = body.Source.Images
			if body.Source.Cover != 1 || len(urls) != 2 {
				t.Fatal("order/cover contract", body)
			}
			return socialJSON(map[string]any{"error": map[string]string{"code": "ok"}, "data": map[string]string{"publish_id": "synthetic-photo-publish"}}), nil
		case "/v2/post/publish/status/fetch/":
			return socialJSON(map[string]any{"error": map[string]string{"code": "ok"}, "data": map[string]string{"status": status}}), nil
		default:
			t.Fatal("unexpected upstream", req.URL.Path)
			return nil, nil
		}
	})
	pic := image.NewRGBA(image.Rect(0, 0, 16, 16))
	for y := 0; y < 16; y++ {
		for x := 0; x < 16; x++ {
			pic.SetRGBA(x, y, color.RGBA{G: 255, A: 255})
		}
	}
	data, e := normalizedPhoto(pic)
	if e != nil {
		t.Fatal(e)
	}
	media, e := r.m.AddAsset(p, s, "second-photo", "green.jpg", "", "", "", "uploaded", data, "image/jpeg")
	if e != nil {
		t.Fatal(e)
	}
	in.ExpectedVersion = media.Version
	in.PhotoAssetIDs = append(in.PhotoAssetIDs, media.Assets[len(media.Assets)-1].ID)
	in.PhotoCoverIndex = 1
	v, e := r.m.createSocial(p, s, "two-photo-intent", in)
	if e != nil {
		t.Fatal(e)
	}
	id := v.Jobs[0].ID
	v, e = r.execute(ctx, p, s, id, v.Version, false)
	if e != nil || v.Jobs[0].Status != "processing" {
		t.Fatal("photo accepted != published", e)
	}
	for i, raw := range urls {
		u, _ := url.Parse(raw)
		var cap photoCapability
		if e = r.open(tiktokPhotoMediaPath, u.Query().Get("cap"), &cap); e != nil || cap.Asset != in.PhotoAssetIDs[i] {
			t.Fatal("source order changed", e)
		}
	}
	t.Setenv("SOCIAL_TIKTOK_APP_APPROVED", "true")
	t.Setenv("SOCIAL_TIKTOK_DIRECT_AUDITED", "true")
	t.Setenv("SOCIAL_TIKTOK_CLIENT_KEY", r.clientKey)
	t.Setenv("SOCIAL_TIKTOK_CLIENT_SECRET", "synthetic-only")
	t.Setenv("SOCIAL_TIKTOK_REDIRECT_URI", "https://studio.example/social-callback")
	t.Setenv("SOCIAL_KEK_BASE64", "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=")
	t.Setenv("SOCIAL_TIKTOK_PHOTO_URL_VERIFIED", "true")
	t.Setenv("SOCIAL_TIKTOK_PHOTO_PUBLIC_ORIGIN", r.photo.origin)
	h := SecureHandler(r.photo.store, "http://localhost:5173", r.auth, AuthOptions{})
	fetch := func(method, raw string) int {
		u, _ := url.Parse(raw)
		req := httptest.NewRequest(method, u.RequestURI(), nil)
		req.Header.Set("Origin", "http://localhost:5173")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w.Code
	}
	if code := fetch("GET", urls[0]); code != 200 {
		t.Fatal("secure public grant", code)
	}
	if code := fetch("HEAD", urls[0]); code != 200 {
		t.Fatal("secure HEAD", code)
	}
	if code := fetch("POST", urls[0]); code != 401 {
		t.Fatal("public POST bypass", code)
	}
	if code := fetch("GET", urls[0]+"&space_id=foreign"); code != 404 {
		t.Fatal("injected tenant", code)
	}
	v, e = r.execute(ctx, p, s, id, v.Version, true)
	if e != nil || v.Jobs[0].Status != "processing" {
		t.Fatal("poll", e)
	}
	if code := fetch("GET", urls[0]); code != 200 {
		t.Fatal("poll changed immutable grant", code)
	}
	old := r.photo.origin
	r.photo.origin = "https://other.example"
	u, _ := url.Parse(urls[0])
	w := httptest.NewRecorder()
	r.photo.serve(w, httptest.NewRequest("GET", u.RequestURI(), nil))
	if w.Code != 404 {
		t.Fatal("changed config revived grant")
	}
	r.photo.origin = old
	r.photo.now = func() time.Time { return time.Now().Add(66 * time.Minute) }
	w = httptest.NewRecorder()
	r.photo.serve(w, httptest.NewRequest("GET", u.RequestURI(), nil))
	if w.Code != 404 {
		t.Fatal("expired photo grant")
	}
	r.photo.now = time.Now
	r.m.mu.Lock()
	d := r.m.clone()
	k := mediaKey(s, p)
	state := d.Projects[k]
	state.Social.Jobs[0].NextPollAt = time.Now().Add(-time.Second).UTC().Format(time.RFC3339)
	d.Projects[k] = state
	e = r.m.commit(d)
	r.m.mu.Unlock()
	if e != nil {
		t.Fatal(e)
	}
	status = "PUBLISH_COMPLETE"
	v, e = r.execute(ctx, p, s, id, v.Version, true)
	if e != nil || v.Jobs[0].Status != "published" || calls != 1 {
		t.Fatal("publication status recovery", e)
	}
	if code := fetch("GET", urls[0]); code != 404 {
		t.Fatal("terminal grant remains public", code)
	}
}

func TestPhotoAccountRevokeAndReconnectNeverRevivesGrant(t *testing.T) {
	var raw string
	r, ctx, p, s, in := photoFixture(t, func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/v2/post/publish/creator_info/query/":
			return socialJSON(map[string]any{"error": map[string]string{"code": "ok"}, "data": SocialCreator{Privacy: []string{"SELF_ONLY"}}}), nil
		case "/v2/post/publish/content/init/":
			var body struct {
				Source struct {
					Images []string `json:"photo_images"`
				} `json:"source_info"`
			}
			if e := json.NewDecoder(req.Body).Decode(&body); e != nil {
				t.Fatal(e)
			}
			raw = body.Source.Images[0]
			return nil, errors.New("synthetic init unknown")
		case "/v2/oauth/revoke/":
			return socialJSON(map[string]string{}), nil
		case "/v2/oauth/token/":
			return socialJSON(socialToken{Access: "synthetic-reconnected-access", Refresh: "synthetic-reconnected-refresh", OpenID: "synthetic-openid", Scope: "video.publish,video.upload", Expires: 3600, RefreshExpires: 86400, TokenType: "Bearer"}), nil
		default:
			t.Fatal("unexpected endpoint", req.URL.Path)
			return nil, nil
		}
	})
	// Match the stable, tenant-scoped account fingerprint used by OAuth reauthorization.
	r.m.mu.Lock()
	d := r.m.clone()
	k := mediaKey(s, p)
	state := d.Projects[k]
	state.Social.Connections[0].AccountKey = authDigest(s + ":" + p + ":" + r.clientKey + ":synthetic-openid")
	d.Projects[k] = state
	e := r.m.commit(d)
	r.m.mu.Unlock()
	if e != nil {
		t.Fatal(e)
	}
	v, e := r.m.createSocial(p, s, "photo-revoke-intent", in)
	if e != nil {
		t.Fatal(e)
	}
	job := v.Jobs[0]
	v, e = r.execute(ctx, p, s, job.ID, v.Version, false)
	if e != nil {
		t.Fatal(e)
	}
	fetch := func() int {
		u, _ := url.Parse(raw)
		w := httptest.NewRecorder()
		r.photo.serve(w, httptest.NewRequest("GET", u.RequestURI(), nil))
		return w.Code
	}
	if fetch() != 200 {
		t.Fatal("original grant invalid")
	}
	// Routine token replacement must not silently destroy consented asynchronous pull.
	r.m.mu.Lock()
	d = r.m.clone()
	private := socialPrivate(s, p, "token", "account")
	var token socialToken
	if e = r.open(private, d.Receipts[private].SocialSecret, &token); e != nil {
		t.Fatal(e)
	}
	token.AccessExpiresAt = time.Now().Add(10 * time.Second).Unix()
	token.RefreshExpiresAt = time.Now().Add(time.Hour).Unix()
	token.Access = "synthetic-near-expiry-access"
	sealed, e := r.seal(private, token)
	if e != nil {
		t.Fatal(e)
	}
	d.Receipts[private] = mediaReceipt{SocialSecret: sealed}
	e = r.m.commit(d)
	r.m.mu.Unlock()
	if e != nil || fetch() != 200 {
		t.Fatal("normal token rotation broke consent", e)
	}

	refreshCtx := context.WithValue(ctx, socialRefreshAllowedKey{}, true)
	if _, e = r.authorizedToken(refreshCtx, p, s, "account", "video.publish"); e != nil || fetch() != 200 {
		t.Fatal("actual token refresh broke grant", e)
	}
	v = r.view(r.m.Get(p, s))
	v, e = r.revoke(ctx, p, s, "account", v.Version)
	if e != nil || fetch() != 404 {
		t.Fatal("account revocation did not stop source", e)
	}
	scope := ctx.Value(authScopeKey{}).(authScope)
	authURL, e := r.start(p, s, scope.UserID, "synthetic-binding", v.Version, "photo-reconnect")
	if e != nil {
		t.Fatal(e)
	}
	u, _ := url.Parse(authURL)
	v, e = r.complete(ctx, p, s, scope.UserID, "synthetic-binding", u.Query().Get("state"), "synthetic-code")
	if e != nil {
		t.Fatal(e)
	}
	if len(v.Connections) != 1 || v.Connections[0].ID != "account" || v.Connections[0].Status != "connected" {
		t.Fatal("fixture must reconnect same account identity", v.Connections)
	}
	if fetch() != 404 || !v.Jobs[0].PhotoAccessRevoked || v.Jobs[0].Status != "unknown" {
		t.Fatal("reauthorization revived old grant or unlocked unknown")
	}
}

func TestPhotoActorRevokedDuringCreatorDoesNotExposeSources(t *testing.T) {
	var r *socialRuntime
	var ctx context.Context
	var p, s string
	var in SocialCreate
	calls := 0
	r, ctx, p, s, in = photoFixture(t, func(req *http.Request) (*http.Response, error) {
		calls++
		if req.URL.Path != "/v2/post/publish/creator_info/query/" {
			t.Fatal("revoked actor sent publication")
		}
		scope := ctx.Value(authScopeKey{}).(authScope)
		r.auth.mu.Lock()
		d := r.auth.clone()
		org := d.Organizations[s]
		org.Members[scope.UserID] = "viewer"
		d.Organizations[s] = org
		e := r.auth.commit(d)
		r.auth.mu.Unlock()
		if e != nil {
			t.Fatal(e)
		}
		return socialJSON(map[string]any{"error": map[string]string{"code": "ok"}, "data": SocialCreator{Privacy: []string{"SELF_ONLY"}}}), nil
	})
	v, e := r.m.createSocial(p, s, "photo-actor-intent", in)
	if e != nil {
		t.Fatal(e)
	}
	v, e = r.execute(ctx, p, s, v.Jobs[0].ID, v.Version, false)
	if e != nil || calls != 1 || v.Jobs[0].PhotoExpiresAt != "" || v.Jobs[0].Status != "failed" {
		t.Fatal("actor revocation boundary", e, v)
	}
}
