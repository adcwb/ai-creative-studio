package application

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/library"
	"github.com/adcwb/ai-creative-studio/backend/internal/persistence/libraryrepo"
)

func TestLibraryHTTPVersionReplayAndIsolation(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	h := Handler(s, "")
	call := func(method, path, key string, body any) *httptest.ResponseRecorder {
		b, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, bytes.NewReader(b))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Idempotency-Key", key)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	in := libraryWrite{Kind: "character"}
	in.Card.Name = "主角"
	in.Card.Character = &library.Character{}
	in.Card.ProhibitedChanges = []string{}
	in.References = []libraryReferenceInput{}
	w := call("POST", "/api/v1/library/assets?space_id=personal-demo", "library-create", in)
	if w.Code != 201 {
		t.Fatalf("create %d %s", w.Code, w.Body.String())
	}
	var result libraryrepo.Result
	if json.Unmarshal(w.Body.Bytes(), &result) != nil {
		t.Fatal("response")
	}
	url := "/api/v1/library/assets/" + result.Asset.ID
	if w = call("GET", url+"?space_id=team-demo", "", nil); w.Code != 404 {
		t.Fatalf("tenant %d", w.Code)
	}
	in.ExpectedVersion = result.Version.ID
	in.Card.Name = "主角新版"
	if w = call("POST", url+"/versions?space_id=personal-demo", "library-update", in); w.Code != 201 {
		t.Fatalf("update %d %s", w.Code, w.Body.String())
	}
	if w = call("POST", url+"/versions?space_id=personal-demo", "library-conflict", in); w.Code != 409 {
		t.Fatalf("CAS %d", w.Code)
	}
	in.ExpectedVersion = ""
	in.Card.Name = "主角"
	w = call("POST", "/api/v1/library/assets?space_id=personal-demo", "library-create", in)
	var replay libraryrepo.Result
	_ = json.Unmarshal(w.Body.Bytes(), &replay)
	if w.Code != 200 || replay.Version.ID != result.Version.ID {
		t.Fatalf("replay %d", w.Code)
	}
}

func TestLibraryBindingPinsCopiedReference(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	source, _, err := s.CreateProject("personal-demo", "source-project", ProjectInput{Title: "来源", Brief: "来源项目说明"})
	if err != nil {
		t.Fatal(err)
	}
	target, _, err := s.CreateProject("personal-demo", "target-project", ProjectInput{Title: "目标", Brief: "目标项目说明"})
	if err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rect(0, 0, 32, 32))
	img.Set(1, 1, color.RGBA{R: 200, A: 255})
	var b bytes.Buffer
	if png.Encode(&b, img) != nil {
		t.Fatal("PNG")
	}
	state, err := s.media.AddAsset(source.ID, "personal-demo", "image-source", "参考", "reference", "", "", "uploaded", b.Bytes(), "image/png")
	if err != nil {
		t.Fatal(err)
	}
	h := Handler(s, "")
	input := libraryWrite{Kind: "character", References: []libraryReferenceInput{{ProjectID: source.ID, AssetID: state.Assets[0].ID, Role: "reference", ViewAngle: "front"}}}
	input.Card.Name = "角色"
	input.Card.Character = &library.Character{}
	input.Card.ProhibitedChanges = []string{}
	body, _ := json.Marshal(input)
	r := httptest.NewRequest("POST", "/api/v1/library/assets?space_id=personal-demo", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Idempotency-Key", "library-with-reference")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 201 {
		t.Fatalf("create %d %s", w.Code, w.Body.String())
	}
	var a libraryrepo.Result
	_ = json.Unmarshal(w.Body.Bytes(), &a)
	// A later card version must use the managed library copy even if the
	// originating project image is gone. Its existing project binding stays v1.
	originalPath, _, err := s.media.file(source.ID, "personal-demo", state.Assets[0].ID, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(originalPath); err != nil {
		t.Fatal(err)
	}
	input.ExpectedVersion = a.Version.ID
	input.Card.Character.Costume = "新外套"
	input.References = []libraryReferenceInput{{LibraryVersionID: a.Version.ID, LibraryReferenceID: a.Version.References[0].ID, Role: "reference", ViewAngle: "front"}}
	body, _ = json.Marshal(input)
	r = httptest.NewRequest("POST", "/api/v1/library/assets/"+a.Asset.ID+"/versions?space_id=personal-demo", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Idempotency-Key", "retain-library-reference")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 201 {
		t.Fatalf("retain reference %d %s", w.Code, w.Body.String())
	}
	var next libraryrepo.Result
	if json.Unmarshal(w.Body.Bytes(), &next) != nil || next.Version.References[0].ID != a.Version.References[0].ID {
		t.Fatal("reference identity changed")
	}
	bind := libraryBindingInput{ExpectedVersion: 1, AssetID: a.Asset.ID, VersionID: a.Version.ID, Usage: "character"}
	body, _ = json.Marshal(bind)
	r = httptest.NewRequest("POST", "/api/v1/projects/"+target.ID+"/library-bindings?space_id=personal-demo", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Idempotency-Key", "bind-reference")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 201 {
		t.Fatalf("bind %d %s", w.Code, w.Body.String())
	}
	p := s.media.Get(target.ID, "personal-demo")
	if len(p.LibraryBindings) != 1 || len(p.Assets) != 1 || p.LibraryBindings[0].VersionID != a.Version.ID {
		t.Fatal("missing binding")
	}
	if p.Assets[0].ID == state.Assets[0].ID || p.Assets[0].ID == a.Version.References[0].ID {
		t.Fatal("not project owned")
	}
	stored, err := s.library.Version(context.Background(), "personal-demo", a.Asset.ID, a.Version.ID)
	if err != nil || len(stored.References) != 1 {
		t.Fatal("missing reference")
	}
	path, mime, err := s.media.file(target.ID, "personal-demo", p.Assets[0].ID, false, false)
	if err != nil || mime != "image/png" {
		t.Fatal(err)
	}
	copied, err := safeReadLocal(path, 4<<20)
	if err != nil || !bytes.Equal(copied, b.Bytes()) {
		t.Fatal("changed bytes", err)
	}
}

func TestLibrarySecureRolesAndSpaceIsolation(t *testing.T) {
	s := testStore(t)
	auth, err := OpenAuthStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	owner, ownerToken, ownerSession, err := auth.login("library-owner@example.test", "synthetic-owner-password", true)
	if err != nil {
		t.Fatal(err)
	}
	viewer, viewerToken, viewerSession, err := auth.login("library-viewer@example.test", "synthetic-viewer-password", true)
	if err != nil {
		t.Fatal(err)
	}
	space, other := auth.spaces(owner.ID)[0]["id"], auth.spaces(viewer.ID)[0]["id"]
	h := SecureHandler(s, "http://localhost:5173", auth, AuthOptions{AllowRegistration: true})
	base := "/api/v1/library/assets"
	body := `{"kind":"character","card":{"name":"Actor","description":"","prohibited_changes":[],"character":{}},"references":[]}`
	authRequest(t, h, "GET", base+"?space_id="+space, "", "", "", 401)
	authRequest(t, h, "POST", base+"?space_id="+space, body, ownerToken, "", 403)
	w := authRequest(t, h, "POST", base+"?space_id="+space, body, ownerToken, ownerSession.CSRF, 201)
	var created libraryrepo.Result
	if err = json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	authRequest(t, h, "GET", base+"/"+created.Asset.ID+"?space_id="+other, "", viewerToken, "", 404)
	authRequest(t, h, "GET", base+"/"+created.Asset.ID+"/versions/"+created.Version.ID+"?space_id="+other, "", viewerToken, "", 404)
	authRequest(t, h, "PUT", "/api/v1/organizations/"+space+"/members", `{"email":"library-viewer@example.test","role":"viewer"}`, ownerToken, ownerSession.CSRF, 200)
	authRequest(t, h, "GET", base+"/"+created.Asset.ID+"?space_id="+space, "", viewerToken, "", 200)
	authRequest(t, h, "POST", base+"?space_id="+space, body, viewerToken, viewerSession.CSRF, 403)
	authRequest(t, h, "POST", base+"/"+created.Asset.ID+"/archive?space_id="+space, `{"expected_asset_version_id":"`+created.Version.ID+`"}`, viewerToken, viewerSession.CSRF, 403)
}
