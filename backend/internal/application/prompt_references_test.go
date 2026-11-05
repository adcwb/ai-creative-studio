package application

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"github.com/adcwb/ai-creative-studio/backend/internal/modules/library"
	"github.com/adcwb/ai-creative-studio/backend/internal/modules/prompts"
	"github.com/adcwb/ai-creative-studio/backend/internal/persistence/libraryrepo"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPromptReferenceApplicationAtomicReplayIsolationAndSource(t *testing.T) {
	s := testStore(t)
	space := "personal-demo"
	source, _, e := s.CreateProject(space, "reference-origin", projectInput())
	if e != nil {
		t.Fatal(e)
	}
	target, _, e := s.CreateProject(space, "reference-target", projectInput())
	if e != nil {
		t.Fatal(e)
	}
	images := [][]byte{}
	ids := []string{}
	for i := 0; i < 2; i++ {
		img := image.NewRGBA(image.Rect(0, 0, 32, 32))
		img.Set(1, 1, color.RGBA{R: uint8(50 + i*100), A: 255})
		var b bytes.Buffer
		png.Encode(&b, img)
		images = append(images, b.Bytes())
		m, e := s.media.AddAsset(source.ID, space, "reference-image-"+string(rune('a'+i)), "Reference", "reference", "", "", "uploaded", b.Bytes(), "image/png")
		if e != nil {
			t.Fatal(e)
		}
		ids = append(ids, m.Assets[len(m.Assets)-1].ID)
	}
	h := Handler(s, "")
	call := func(method, path, key string, body any) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, bytes.NewReader(raw))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Idempotency-Key", key)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	input := libraryWrite{Kind: "character", References: []libraryReferenceInput{{ProjectID: source.ID, AssetID: ids[0], Role: "reference", ViewAngle: "front"}, {ProjectID: source.ID, AssetID: ids[1], Role: "reference", ViewAngle: "side"}}}
	input.Card.ProhibitedChanges = []string{}
	input.Card.Name = "Reference identity"
	input.Card.Character = &library.Character{}
	w := call("POST", "/api/v1/library/assets?space_id="+space, "reference-card", input)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	var card libraryrepo.Result
	json.Unmarshal(w.Body.Bytes(), &card)
	command := promptCommand()
	command.Metadata.Name = "Private recipe name must not leak"
	command.Recipe.ReferenceBindings = []prompts.ReferenceBinding{{AssetID: card.Asset.ID, VersionID: card.Version.ID, ReferenceID: card.Version.References[1].ID, Purpose: "style"}, {AssetID: card.Asset.ID, VersionID: card.Version.ID, ReferenceID: card.Version.References[0].ID, Purpose: "subject"}}
	body := struct {
		prompts.Metadata
		Version prompts.Recipe `json:"version"`
	}{command.Metadata, command.Recipe}
	w = call("POST", "/api/v1/prompt-templates?space_id="+space, "reference-recipe", body)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	var recipe prompts.Result
	json.Unmarshal(w.Body.Bytes(), &recipe)
	apply := promptApplyInput{TemplateID: recipe.Template.ID, VersionID: recipe.Version.ID, Expected: 1}
	path := "/api/v1/projects/" + target.ID + "/prompt-applications?space_id=" + space
	w = call("POST", path, "apply-reference-recipe", apply)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	var applied prompts.Application
	json.Unmarshal(w.Body.Bytes(), &applied)
	if len(applied.ReferenceAssetIDs) != 2 || applied.Source.ReferenceBindings[0].ReferenceID != command.Recipe.ReferenceBindings[0].ReferenceID {
		t.Fatal("order/source missing")
	}
	for i, id := range applied.ReferenceAssetIDs {
		path, _, e := s.media.file(target.ID, space, id, false, false)
		if e != nil {
			t.Fatal(e)
		}
		data, e := safeReadLocal(path, 4<<20)
		if e != nil || !bytes.Equal(data, images[1-i]) {
			t.Fatal("reference order bytes", e)
		}
	}
	w = call("POST", path, "apply-reference-recipe", apply)
	if w.Code != 200 {
		t.Fatal("application replay", w.Code, w.Body.String())
	}
	state := s.media.Get(target.ID, space)
	if len(state.Assets) != 2 || len(state.PromptApplications) != 1 {
		t.Fatal("duplicate application")
	}
	if w = call("POST", "/api/v1/projects/"+target.ID+"/prompt-applications?space_id=team-demo", "cross-reference-apply", apply); w.Code != 404 {
		t.Fatal("cross-space", w.Code)
	}
	sourceInput := &promptSourceInput{TemplateID: recipe.Template.ID, VersionID: recipe.Version.ID, Edited: true, ApplicationID: applied.ID}
	creative := CreativeJobInput{Kind: "image", ExpectedVersion: target.Version, Prompt: applied.Prompt, PromptSource: sourceInput, ReferenceAssetIDs: applied.ReferenceAssetIDs}
	request := httptest.NewRequest("POST", "/", nil)
	writer := httptest.NewRecorder()
	if !s.resolveCreativePrompt(writer, request, space, target.ID, "source-creative-key", &creative) {
		t.Fatal(writer.Code, writer.Body.String())
	}
	if creative.promptSnapshot.ReferenceBindings[0].VersionID != card.Version.ID || creative.ImageParameters.Ratio != "4:5" {
		t.Fatal("lost source or defaults")
	}
	creative.ReferenceAssetIDs = []string{applied.ReferenceAssetIDs[0]}
	writer = httptest.NewRecorder()
	if s.resolveCreativePrompt(writer, request, space, target.ID, "changed-reference-key", &creative) {
		t.Fatal("silently dropped recipe reference")
	}
	home := map[string]any{"prompt": recipe.Version.Body, "kind": "poster", "prompt_source": map[string]any{"template_id": recipe.Template.ID, "version_id": recipe.Version.ID, "edited": false}}
	w = call("POST", "/api/v1/tasks?space_id="+space, "reference-home-reject", home)
	if w.Code != 400 || len(s.List(space)) != 0 {
		t.Fatal("home ignored references", w.Code)
	}
}
func TestGatewayDeclaredReferenceCountAndMultipartOrder(t *testing.T) {
	one := syntheticCreativePNG()
	two := append([]byte(nil), one...)
	// Distinct metadata-free PNGs keep content order observable in the actual HTTP request.
	img := image.NewRGBA(image.Rect(0, 0, 32, 32))
	img.Set(0, 0, color.RGBA{B: 255, A: 255})
	var b bytes.Buffer
	png.Encode(&b, img)
	two = b.Bytes()
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/v1/images/edits" {
			t.Error("wrong endpoint")
		}
		if r.ParseMultipartForm(2<<20) != nil {
			t.Error("multipart")
			return
		}
		defer r.MultipartForm.RemoveAll()
		files := r.MultipartForm.File["image[]"]
		if len(files) != 2 {
			t.Error("references dropped", len(files))
			return
		}
		for i, expected := range [][]byte{one, two} {
			f, e := files[i].Open()
			if e != nil {
				t.Error(e)
				return
			}
			raw, _ := io.ReadAll(f)
			f.Close()
			if !bytes.Equal(raw, expected) {
				t.Error("order changed")
			}
		}
		if r.FormValue("size") == "" {
			t.Error("defaults omitted")
		}
		json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]string{"b64_json": base64.StdEncoding.EncodeToString(one)}}})
	}))
	defer upstream.Close()
	p := &gatewayImageProvider{api: newCreativeProvider("synthetic-key", upstream.URL+"/v1", upstream.Client()), model: GatewayModel{Model: "synthetic-image", Capabilities: []string{"image_edit"}, MaxReferenceImages: 2}}
	refs := []referenceMedia{{Data: one, MIME: "image/png", Role: "reference"}, {Data: two, MIME: "image/png", Role: "reference"}}
	if _, e := p.GenerateWithReferences(context.Background(), Input{Prompt: "Synthetic referenced image", Kind: "poster", Ratio: "4:5", Style: "illustration"}, refs); e != nil {
		t.Fatal(e)
	}
	p.model.MaxReferenceImages = 1
	if _, e := p.GenerateWithReferences(context.Background(), Input{Prompt: "Synthetic referenced image", Kind: "poster"}, refs); e == nil || calls != 1 {
		t.Fatal("unsupported count dispatched")
	}
}

func applyPromptForTest(t *testing.T, s *FileStore, a prompts.Actor, recipe prompts.Result, key string) (Project, prompts.Application) {
	t.Helper()
	project, _, e := s.CreateProject(a.Space, key+"-project", projectInput())
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(promptApplyInput{TemplateID: recipe.Template.ID, VersionID: recipe.Version.ID, Expected: 1})
	r := httptest.NewRequest("POST", "/api/v1/projects/"+project.ID+"/prompt-applications", bytes.NewReader(raw))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Idempotency-Key", key)
	r = r.WithContext(context.WithValue(r.Context(), authScopeKey{}, authScope{SpaceID: a.Space, UserID: a.User, Role: "editor"}))
	w := httptest.NewRecorder()
	Handler(s, "").ServeHTTP(w, r)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	var out prompts.Application
	if json.Unmarshal(w.Body.Bytes(), &out) != nil {
		t.Fatal("application response")
	}
	return project, out
}
func TestPromptApplicationPostgresSecondAPI(t *testing.T) {
	db := postgresTestDB(t)
	a, b := postgresTestStore(t, db), postgresTestStore(t, db)
	actor := prompts.Actor{Space: "prompt-app-pg", User: "owner", Writable: true}
	recipe, e := a.prompts.Mutate(context.Background(), actor, "77777777777777777777777777777777", "pg-app-recipe", promptCommand())
	if e != nil {
		t.Fatal(e)
	}
	project, app := applyPromptForTest(t, a, actor, recipe, "pg-prompt-application")
	state := b.media.Get(project.ID, actor.Space)
	if len(state.PromptApplications) != 1 || state.PromptApplications[0].ID != app.ID || state.Version != 2 {
		t.Fatal("metadata/receipt was not committed across APIs")
	}
}
