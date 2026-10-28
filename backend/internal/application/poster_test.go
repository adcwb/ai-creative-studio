package application

import (
	"bytes"
	"encoding/json"
	"errors"
	"image/png"
	"os"
	"testing"
)

func TestPosterSaveExportReplayIsolation(t *testing.T) {
	m, err := openMediaStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer m.close()
	p, err := m.AddAsset("p", "s", "logo-upload", "logo", "logo", "", "", "uploaded", syntheticPNG(0), "image/png")
	if err != nil {
		t.Fatal(err)
	}
	doc := PosterDocument{Width: 256, Height: 256, Background: "#123456", Layers: []PosterLayer{{ID: "logo", Kind: "logo", AssetID: p.Assets[0].ID, X: 0, Y: 0, Width: 64, Height: 64, Opacity: 1}, {ID: "text", Kind: "text", Text: "Hello %{eif:1:2}\n海报", X: 64, Y: 64, Width: 180, Height: 120, FontSize: 18, Color: "#ffffff", Opacity: 1}}}
	saved, err := m.SavePoster("p", "s", "save-poster", p.Version, doc)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.SavePoster("p", "other", "foreign", 1, doc); !errors.Is(err, errMediaInput) {
		t.Fatalf("foreign image: %v", err)
	}
	if _, err = m.SavePoster("p", "s", "stale", p.Version, doc); !errors.Is(err, errMediaVersion) {
		t.Fatal(err)
	}
	if _, err = m.SavePoster("p", "s", "save-poster", p.Version, doc); err != nil {
		t.Fatal(err)
	}
	result, err := m.ExportPoster("p", "s", "export-poster", saved.Version)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Assets) != 2 {
		t.Fatal("export asset missing")
	}
	a := result.Assets[1]
	path, _, err := m.file("p", "s", a.ID, false, false)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() != 256 || img.Bounds().Dy() != 256 {
		t.Fatal(img.Bounds())
	}
	r, g, bl, _ := img.At(250, 250).RGBA()
	if r>>8 != 0x12 || g>>8 != 0x34 || bl>>8 != 0x56 {
		t.Fatalf("background %x %x %x", r, g, bl)
	}
	r, g, bl, _ = img.At(20, 20).RGBA()
	if r>>8 == 0x12 && g>>8 == 0x34 && bl>>8 == 0x56 {
		t.Fatal("logo not rendered")
	}
	r, g, bl, _ = img.At(230, 175).RGBA()
	if r>>8 != 0x12 || g>>8 != 0x34 || bl>>8 != 0x56 {
		t.Fatal("empty text rectangle must stay transparent")
	}
	changed := 0
	for y := 70; y < 180; y++ {
		for x := 65; x < 240; x++ {
			r, g, b, _ := img.At(x, y).RGBA()
			if r>>8 != 0x12 || g>>8 != 0x34 || b>>8 != 0x56 {
				changed++
			}
		}
	}
	if changed < 20 {
		t.Fatal("text not rendered")
	}
	replay, err := m.ExportPoster("p", "s", "export-poster", saved.Version)
	if err != nil || len(replay.Assets) != 2 {
		t.Fatalf("replay %v", err)
	}
	if _, _, err = m.file("p", "other", a.ID, false, false); !errors.Is(err, ErrNotFound) {
		t.Fatal("cross-tenant export")
	}
	if _, _, err = m.file("other", "s", a.ID, false, false); !errors.Is(err, ErrNotFound) {
		t.Fatal("cross-project export")
	}
}
func TestPosterValidation(t *testing.T) {
	valid := PosterDocument{Width: 256, Height: 256, Background: "#ffffff", Layers: []PosterLayer{{ID: "a", Kind: "text", Text: "hi", Width: 100, Height: 40, FontSize: 20, Color: "#000000", Opacity: 1}}}
	if err := validatePoster(MediaState{}, valid); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*PosterDocument){func(d *PosterDocument) { d.Width = 4096 }, func(d *PosterDocument) { d.Background = "red:evil" }, func(d *PosterDocument) { d.Layers[0].Text = "a\x00" }, func(d *PosterDocument) { d.Layers[0].X = 250 }, func(d *PosterDocument) { d.Layers[0].Color = "red:evil" }, func(d *PosterDocument) { d.Layers[0].Opacity = 2 }, func(d *PosterDocument) { d.Layers = append(d.Layers, d.Layers[0]) }} {
		d := valid
		d.Layers = append([]PosterLayer{}, valid.Layers...)
		mutate(&d)
		if err := validatePoster(MediaState{}, d); !errors.Is(err, errMediaInput) {
			t.Fatalf("accepted %#v", d)
		}
	}
}

func TestPosterHTTPAuthorization(t *testing.T) {
	s := testStore(t)
	auth, err := OpenAuthStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a, ta, sa, err := auth.login("poster-a@example.test", "synthetic-password-A", true)
	if err != nil {
		t.Fatal(err)
	}
	b, tb, sb, err := auth.login("poster-b@example.test", "synthetic-password-B", true)
	if err != nil {
		t.Fatal(err)
	}
	oa, ob := auth.spaces(a.ID)[0]["id"], auth.spaces(b.ID)[0]["id"]
	h := SecureHandler(s, "http://localhost:5173", auth, AuthOptions{AllowRegistration: true})
	w := authRequest(t, h, "POST", "/api/v1/projects?space_id="+oa, `{"title":"Poster","brief":"Synthetic poster test"}`, ta, sa.CSRF, 201)
	var p Project
	if err = json.Unmarshal(w.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/projects/" + p.ID + "/media/poster"
	body := `{"expected_version":1,"poster":{"width":256,"height":256,"background":"#ffffff","layers":[]}}`
	authRequest(t, h, "PUT", path+"?space_id="+oa, body, "", "", 401)
	authRequest(t, h, "PUT", path+"?space_id="+oa, body, ta, "", 403)
	authRequest(t, h, "PUT", path+"?space_id="+oa, body, tb, sb.CSRF, 403)
	authRequest(t, h, "PUT", path+"?space_id="+ob, body, tb, sb.CSRF, 404)
	authRequest(t, h, "POST", path+"/exports?space_id="+ob, `{"expected_version":1}`, tb, sb.CSRF, 404)
	authRequest(t, h, "PUT", path+"?space_id="+oa, body, ta, sa.CSRF, 200)
	authRequest(t, h, "PUT", "/api/v1/organizations/"+oa+"/members", `{"email":"poster-b@example.test","role":"viewer"}`, ta, sa.CSRF, 200)
	authRequest(t, h, "PUT", path+"?space_id="+oa, body, tb, sb.CSRF, 403)
	authRequest(t, h, "POST", path+"/exports?space_id="+oa, `{"expected_version":2}`, tb, sb.CSRF, 403)
}
func TestPosterRestartRecovery(t *testing.T) {
	dir := t.TempDir()
	m, err := openMediaStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	doc := PosterDocument{Width: 256, Height: 256, Background: "#246810", Layers: []PosterLayer{}}
	saved, err := m.SavePoster("p", "s", "save-original", 1, doc)
	if err != nil {
		t.Fatal(err)
	}
	result, err := m.ExportPoster("p", "s", "export-original", saved.Version)
	if err != nil {
		t.Fatal(err)
	}
	m.close()
	restored, err := openMediaStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.close()
	got, err := restored.ExportPoster("p", "s", "export-original", saved.Version)
	if err != nil || len(got.Assets) != 1 || got.Assets[0].ID != result.Assets[0].ID || got.Poster.Background != doc.Background {
		t.Fatalf("restore replay: %#v %v", got, err)
	}
}

func TestPostgresPosterCASAndIsolation(t *testing.T) {
	db := postgresTestDB(t)
	first, second := postgresTestStore(t, db), postgresTestStore(t, db)
	a, b := first.media, second.media
	project, _, err := first.CreateProject("tenant-a", "poster-project", projectInput())
	if err != nil {
		t.Fatal(err)
	}
	doc := PosterDocument{Width: 256, Height: 256, Background: "#135790", Layers: []PosterLayer{}}
	saved, err := a.SavePoster(project.ID, "tenant-a", "save-original", 1, doc)
	if err != nil {
		t.Fatal(err)
	}
	if got := b.Get(project.ID, "tenant-a"); got.Poster == nil || got.Poster.Background != doc.Background {
		t.Fatal("second instance cannot read saved poster")
	}
	if _, err = b.SavePoster(project.ID, "tenant-a", "stale-edit", 1, doc); !errors.Is(err, errMediaVersion) {
		t.Fatal("stale second-instance edit accepted", err)
	}
	if got := b.Get(project.ID, "tenant-b"); got.Poster != nil {
		t.Fatal("cross-tenant poster leaked")
	}
	got, err := b.SavePoster(project.ID, "tenant-a", "save-original", 1, doc)
	if err != nil || got.Version != saved.Version {
		t.Fatal("cross-instance replay failed", err)
	}
}
