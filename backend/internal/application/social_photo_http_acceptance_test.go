package application

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	_ "image/jpeg"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
)

// This is an independent HTTP boundary test: real secure middleware and persisted
// stores, synthetic account tokens, and an egress-denying transport. No actual
// OAuth, platform request or public media grant is performed here.
func TestPhotoHTTPAcceptancePosterTenantReplayAndRestart(t *testing.T) {
	originalTransport := http.DefaultTransport
	transport := originalTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = func(context.Context, string, string) (net.Conn, error) {
		t.Error("unexpected network in local PHOTO acceptance")
		return nil, errors.New("synthetic offline acceptance guard")
	}
	http.DefaultTransport = transport
	t.Cleanup(func() { http.DefaultTransport = originalTransport; transport.CloseIdleConnections() })
	for key, value := range map[string]string{
		"SOCIAL_TIKTOK_APP_APPROVED": "true", "SOCIAL_TIKTOK_DIRECT_AUDITED": "true",
		"SOCIAL_TIKTOK_CLIENT_KEY": "synthetic-client", "SOCIAL_TIKTOK_CLIENT_SECRET": "synthetic-secret",
		"SOCIAL_TIKTOK_REDIRECT_URI":       "https://studio.example/social-callback",
		"SOCIAL_KEK_BASE64":                base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef")),
		"SOCIAL_TIKTOK_PHOTO_URL_VERIFIED": "true", "SOCIAL_TIKTOK_PHOTO_PUBLIC_ORIGIN": "https://studio.example",
	} {
		t.Setenv(key, value)
	}
	dir := t.TempDir()
	store, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { store.Close() }()
	auth, err := OpenAuthStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	alice, aliceToken, aliceSession, err := auth.login("photo-owner@example.invalid", "Synthetic-photo-owner!", true)
	if err != nil {
		t.Fatal(err)
	}
	bob, bobToken, bobSession, err := auth.login("photo-other@example.invalid", "Synthetic-photo-other!", true)
	if err != nil {
		t.Fatal(err)
	}
	tenant := auth.spaces(alice.ID)[0]["id"]
	foreign := auth.spaces(bob.ID)[0]["id"]
	project, _, err := store.CreateProject(tenant, "photo-http-project", projectInput())
	if err != nil {
		t.Fatal(err)
	}
	handler := SecureHandler(store, "http://localhost:5173", auth, AuthOptions{})
	path := "/api/v1/projects/" + project.ID
	query := "?space_id=" + tenant
	call := func(method, path, key string, body any, token, csrf string, status int) []byte {
		t.Helper()
		raw, _ := json.Marshal(body)
		request := httptest.NewRequest(method, path, bytes.NewReader(raw))
		request.RemoteAddr = "127.0.0.1:1234"
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Origin", "http://localhost:5173")
		request.Header.Set("Idempotency-Key", key)
		request.Header.Set("X-CSRF-Token", csrf)
		if token != "" {
			request.AddCookie(&http.Cookie{Name: "studio_session", Value: token})
		}
		result := httptest.NewRecorder()
		handler.ServeHTTP(result, request)
		if result.Code != status {
			t.Fatalf("%s %s: status %d, want %d: %s", method, path, result.Code, status, result.Body.String())
		}
		return result.Body.Bytes()
	}
	doc := PosterDocument{Width: 256, Height: 256, Background: "#ffffff", Layers: []PosterLayer{{ID: "text", Kind: "text", Text: "Synthetic PHOTO poster", X: 8, Y: 8, Width: 240, Height: 80, FontSize: 18, Color: "#123456", Opacity: 1}}}
	var media MediaState
	if err = json.Unmarshal(call("PUT", path+"/media/poster"+query, "photo-http-poster", map[string]any{"expected_version": 1, "poster": doc}, aliceToken, aliceSession.CSRF, 200), &media); err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(call("POST", path+"/media/poster/exports"+query, "photo-http-render", map[string]any{"expected_version": media.Version}, aliceToken, aliceSession.CSRF, 200), &media); err != nil {
		t.Fatal(err)
	}
	poster := media.Assets[len(media.Assets)-1]
	originalPNG := call("GET", poster.FileURL, "", nil, aliceToken, "", 200)
	input := map[string]any{"expected_version": media.Version, "asset_id": poster.ID, "confirmed": true}
	normalizePath := path + "/social/photos/normalize"
	call("POST", normalizePath+query, "photo-http-anonymous", input, "", "", 401)
	call("POST", normalizePath+query, "photo-http-csrf", input, aliceToken, "", 403)
	call("POST", normalizePath+query, "photo-http-foreign", input, bobToken, bobSession.CSRF, 403)
	call("POST", normalizePath+"?space_id="+foreign, "photo-http-other-org", input, bobToken, bobSession.CSRF, 404)
	var normalized struct {
		MediaState
		NormalizedAssetID string `json:"normalized_asset_id"`
	}
	if err = json.Unmarshal(call("POST", normalizePath+query, "photo-http-normalize", input, aliceToken, aliceSession.CSRF, 200), &normalized); err != nil {
		t.Fatal(err)
	}
	if normalized.NormalizedAssetID == "" || len(normalized.Assets) != len(media.Assets)+1 {
		t.Fatal("normalization did not return exact new asset receipt")
	}
	jpeg, ok := mediaAsset(normalized.MediaState, normalized.NormalizedAssetID)
	if !ok || jpeg.MIMEType != "image/jpeg" || jpeg.Source != "social-photo-normalized" {
		t.Fatal("normalized JPEG missing")
	}
	data := call("GET", jpeg.FileURL, "", nil, aliceToken, "", 200)
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || format != "jpeg" || cfg.Width != 256 || cfg.Height != 256 {
		t.Fatal("actual normalized JPEG dimensions", cfg, format, err)
	}
	if !bytes.Equal(originalPNG, call("GET", poster.FileURL, "", nil, aliceToken, "", 200)) {
		t.Fatal("normalization mutated original poster")
	}
	call("POST", normalizePath+query, "photo-http-stale", input, aliceToken, aliceSession.CSRF, 409)
	upstreamCalls := 0
	runtime := testSocialRuntime(t, store.media, func(*http.Request) (*http.Response, error) {
		upstreamCalls++
		return socialJSON(socialToken{Access: "synthetic-access", Refresh: "synthetic-refresh", OpenID: "synthetic-photo-account", Scope: "video.publish,video.upload", Expires: 3600, RefreshExpires: 31536000, TokenType: "Bearer"}), nil
	})
	authorization, err := runtime.start(project.ID, tenant, alice.ID, "synthetic-session-binding", normalized.Version, "photo-http-seed-connect")
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ := url.Parse(authorization)
	view, err := runtime.complete(context.Background(), project.ID, tenant, alice.ID, "synthetic-session-binding", parsed.Query().Get("state"), "synthetic-code")
	if err != nil {
		t.Fatal(err)
	}
	intent := map[string]any{"expected_version": view.Version, "platform": "tiktok", "mode": "direct", "media_type": "PHOTO", "photo_asset_ids": []string{jpeg.ID}, "photo_cover_index": 0, "connection_id": view.Connections[0].ID, "privacy_level": "SELF_ONLY", "confirmed": true, "rights_confirmed": true, "public_media_confirmed": true, "is_aigc": true, "title": "Synthetic poster", "caption": "Synthetic acceptance only"}
	bad := map[string]any{}
	for key, value := range intent {
		bad[key] = value
	}
	bad["photo_asset_ids"] = []string{poster.ID}
	call("POST", path+"/social/jobs"+query, "photo-http-png-reject", bad, aliceToken, aliceSession.CSRF, 400)
	raw := call("POST", path+"/social/jobs"+query, "photo-http-create", intent, aliceToken, aliceSession.CSRF, 200)
	if err = json.Unmarshal(raw, &view); err != nil {
		t.Fatal(err)
	}
	if len(view.Jobs) != 1 || view.Jobs[0].Status != "prepared" || strings.Contains(string(raw), "synthetic-refresh") || strings.Contains(string(raw), "synthetic-access") {
		t.Fatal("unsafe PHOTO acceptance result")
	}
	var replay SocialView
	if err = json.Unmarshal(call("POST", path+"/social/jobs"+query, "photo-http-create", intent, aliceToken, aliceSession.CSRF, 200), &replay); err != nil || len(replay.Jobs) != 1 || replay.Jobs[0].ID != view.Jobs[0].ID {
		t.Fatal("PHOTO duplicate receipt", err)
	}
	bad["photo_asset_ids"] = []string{jpeg.ID}
	bad["caption"] = "changed under same key"
	call("POST", path+"/social/jobs"+query, "photo-http-create", bad, aliceToken, aliceSession.CSRF, 409)
	call("PUT", "/api/v1/organizations/"+tenant+"/members", "photo-http-member", map[string]string{"email": bob.Email, "role": "viewer"}, aliceToken, aliceSession.CSRF, 200)
	call("POST", normalizePath+query, "photo-http-viewer", map[string]any{"expected_version": view.Version, "asset_id": poster.ID, "confirmed": true}, bobToken, bobSession.CSRF, 403)
	call("POST", path+"/social/jobs"+query, "photo-http-viewer-job", intent, bobToken, bobSession.CSRF, 403)
	if upstreamCalls != 1 {
		t.Fatal("acceptance unexpectedly called upstream", upstreamCalls)
	}
	store.Close()
	store, err = OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	handler = SecureHandler(store, "http://localhost:5173", auth, AuthOptions{})
	var restored SocialView
	if err = json.Unmarshal(call("GET", path+"/social"+query, "", nil, aliceToken, "", 200), &restored); err != nil || len(restored.Jobs) != 1 || restored.Jobs[0].ID != view.Jobs[0].ID || restored.Jobs[0].Status != "prepared" {
		t.Fatal("PHOTO record restart recovery", err)
	}
	if !bytes.Equal(data, call("GET", jpeg.FileURL, "", nil, aliceToken, "", 200)) {
		t.Fatal("normalized photo changed after restart")
	}
	var receipt struct {
		NormalizedAssetID string `json:"normalized_asset_id"`
	}
	if err = json.Unmarshal(call("POST", normalizePath+query, "photo-http-normalize", input, aliceToken, aliceSession.CSRF, 200), &receipt); err != nil || receipt.NormalizedAssetID != jpeg.ID {
		t.Fatal("exact normalization receipt lost after restart", err)
	}
}

func TestPostgresPhotoAcceptanceTwoInstances(t *testing.T) {
	db := postgresTestDB(t)
	shared := t.TempDir()
	open := func() *FileStore {
		t.Helper()
		s := postgresTestStore(t, db)
		blob, err := NewFileBlobStore(shared)
		if err != nil {
			t.Fatal(err)
		}
		if err = configureBlobStorage(s, blob); err != nil {
			t.Fatal(err)
		}
		return s
	}
	first, second := open(), open()
	project, _, err := first.CreateProject("photo-tenant", "photo-pg-project", projectInput())
	if err != nil {
		t.Fatal(err)
	}
	p, err := first.media.SavePoster(project.ID, "photo-tenant", "save", 1, PosterDocument{Width: 256, Height: 256, Background: "#135790", Layers: []PosterLayer{}})
	if err != nil {
		t.Fatal(err)
	}
	p, err = first.media.ExportPoster(project.ID, "photo-tenant", "render", p.Version)
	if err != nil {
		t.Fatal(err)
	}
	original := p.Assets[0]
	input := photoNormalize{ExpectedVersion: p.Version, AssetID: original.ID, Confirmed: true}
	p, err = second.media.normalizeSocialPhoto(project.ID, "photo-tenant", "normalize", input)
	if err != nil {
		t.Fatal("second instance must materialize original from shared blob", err)
	}
	asset, err := second.media.AssetForReceipt(project.ID, "photo-tenant", "normalize")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = first.media.normalizeSocialPhoto(project.ID, "photo-tenant", "normalize", input); err != nil {
		t.Fatal("cross-instance exact receipt", err)
	}
	if _, err = first.media.normalizeSocialPhoto(project.ID, "photo-tenant", "stale", input); !errors.Is(err, errMediaVersion) {
		t.Fatal("stale CAS accepted", err)
	}
	if _, err = second.media.normalizeSocialPhoto(project.ID, "foreign-tenant", "foreign", photoNormalize{ExpectedVersion: 1, AssetID: original.ID, Confirmed: true}); err == nil {
		t.Fatal("foreign normalization accepted")
	}
	runtime := testSocialRuntime(t, first.media, func(*http.Request) (*http.Response, error) {
		return socialJSON(socialToken{Access: "synthetic-pg-access", Refresh: "synthetic-pg-refresh", OpenID: "synthetic-pg-account", Scope: "video.publish,video.upload", Expires: 3600, RefreshExpires: 31536000, TokenType: "Bearer"}), nil
	})
	authURL, err := runtime.start(project.ID, "photo-tenant", "actor", "session", p.Version, "connect")
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ := url.Parse(authURL)
	view, err := runtime.complete(context.Background(), project.ID, "photo-tenant", "actor", "session", parsed.Query().Get("state"), "synthetic-code")
	if err != nil {
		t.Fatal(err)
	}
	in := SocialCreate{ExpectedVersion: view.Version, Platform: "tiktok", Mode: "direct", MediaType: "PHOTO", PhotoAssetIDs: []string{asset.ID}, ConnectionID: view.Connections[0].ID, PrivacyLevel: "SELF_ONLY", Confirmed: true, RightsConfirmed: true, PublicMediaConfirmed: true, Title: "Synthetic poster"}
	view, err = second.media.createSocial(project.ID, "photo-tenant", "photo-create", in)
	if err != nil || len(view.Jobs) != 1 {
		t.Fatal("second instance PHOTO create", err)
	}
	replay, err := first.media.createSocial(project.ID, "photo-tenant", "photo-create", in)
	if err != nil || len(replay.Jobs) != 1 || replay.Jobs[0].ID != view.Jobs[0].ID {
		t.Fatal("PHOTO durable cross-instance receipt", err)
	}
	third := open()
	restored := socialView(third.media.Get(project.ID, "photo-tenant"))
	if len(restored.Jobs) != 1 || restored.Jobs[0].ID != view.Jobs[0].ID || restored.Jobs[0].Status != "prepared" {
		t.Fatal("fresh instance PHOTO recovery")
	}
	recovered, err := third.media.AssetForReceipt(project.ID, "photo-tenant", "normalize")
	if err != nil || recovered.ID != asset.ID {
		t.Fatal("fresh instance normalization receipt", err)
	}
	path, err := third.media.localBlob("photo-tenant", project.ID, asset.ID)
	if err != nil {
		t.Fatal("fresh cache materialization", err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	img, format, err := image.Decode(file)
	if err != nil || format != "jpeg" || img.Bounds().Dx() != 256 || img.Bounds().Dy() != 256 {
		t.Fatal("recovered JPEG decode", err)
	}
	if _, err = third.media.localBlob("foreign-tenant", project.ID, asset.ID); err == nil {
		t.Fatal("cross-tenant shared blob leak")
	}
	if len(third.media.Get(project.ID, "foreign-tenant").Assets) != 0 {
		t.Fatal("cross-tenant PHOTO state leak")
	}
}
