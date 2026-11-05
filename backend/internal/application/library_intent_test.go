package application

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/library"
	"github.com/adcwb/ai-creative-studio/backend/internal/persistence/libraryrepo"
)

func intentLibraryCall(t *testing.T, s *FileStore, method, path, key string, in any, status int) []byte {
	t.Helper()
	b, _ := json.Marshal(in)
	r := httptest.NewRequest(method, path, bytes.NewReader(b))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Idempotency-Key", key)
	w := httptest.NewRecorder()
	Handler(s, "").ServeHTTP(w, r)
	if w.Code != status {
		t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body.String())
	}
	return w.Body.Bytes()
}
func intentCards() map[string]libraryWrite {
	out := map[string]libraryWrite{"style": {Kind: "style", Card: library.Card{Name: "雨夜墨色", Style: &library.Style{Palette: "青灰与暖灯", Linework: "疏朗线条", Composition: "留白与门框", Lighting: "侧逆光", Texture: "纸面颗粒"}}, References: []libraryReferenceInput{}}, "voice": {Kind: "voice", Card: library.Card{Name: "小满的表演意图", Voice: &library.Voice{Timbre: "清亮但克制", Performance: "戒备，不急于表明身份", Pace: "句尾停顿", Pronunciation: "小满轻读"}}, References: []libraryReferenceInput{}}}
	for k, v := range out {
		v.Card.ProhibitedChanges = []string{}
		for i := 0; i < 31; i++ {
			v.Card.ProhibitedChanges = append(v.Card.ProhibitedChanges, fmt.Sprintf("不可变项 %d", i))
		}
		out[k] = v
	}
	return out
}
func libraryIntentContract(t *testing.T, s *FileStore) map[string]libraryrepo.Result {
	t.Helper()
	base := "/api/v1/library/assets"
	q := "?space_id=personal-demo"
	results := map[string]libraryrepo.Result{}
	for kind, input := range intentCards() {
		var original libraryrepo.Result
		_ = json.Unmarshal(intentLibraryCall(t, s, http.MethodPost, base+q, "intent-create-"+kind, input, 201), &original)
		if original.Asset.Kind != kind || len(original.Version.Card.ProhibitedChanges) != 31 {
			t.Fatal("typed intent or constraints lost")
		}
		results[kind] = original
		changed := input
		changed.ExpectedVersion = original.Version.ID
		changed.Card.Description = "后来修订，不应改掉历史"
		var next libraryrepo.Result
		_ = json.Unmarshal(intentLibraryCall(t, s, "POST", base+"/"+original.Asset.ID+"/versions"+q, "intent-next-"+kind, changed, 201), &next)
		if next.Version.Number != 2 {
			t.Fatal("append clock")
		}
		var replay libraryrepo.Result
		_ = json.Unmarshal(intentLibraryCall(t, s, "POST", base+q, "intent-create-"+kind, input, 200), &replay)
		if !reflect.DeepEqual(original, replay) {
			t.Fatal("original intent replay changed")
		}
		var page struct {
			Items []library.Asset `json:"items"`
		}
		_ = json.Unmarshal(intentLibraryCall(t, s, "GET", base+q+"&kind="+kind, "", nil, 200), &page)
		if len(page.Items) != 1 || page.Items[0].ID != original.Asset.ID {
			t.Fatal("kind filtering")
		}
		old, e := s.library.Version(context.Background(), "personal-demo", original.Asset.ID, original.Version.ID)
		if e != nil || !reflect.DeepEqual(old, original.Version) {
			t.Fatal("history", e)
		}
		intentLibraryCall(t, s, "GET", base+"/"+original.Asset.ID+"?space_id=team-demo", "", nil, 404)
		mixed := input
		mixed.Card.Character = &library.Character{}
		intentLibraryCall(t, s, "POST", base+q, "intent-mixed-"+kind, mixed, 400)
	}
	return results
}
func TestFileLibraryStyleVoiceIntentVersions(t *testing.T) { libraryIntentContract(t, testStore(t)) }
func TestPostgresLibraryStyleVoiceIntentVersions(t *testing.T) {
	libraryIntentContract(t, postgresTestStore(t, postgresTestDB(t)))
}

func libraryManyReferenceContract(t *testing.T, s *FileStore) {
	t.Helper()
	space := "personal-demo"
	source, _, e := s.CreateProject(space, "many-ref-source", ProjectInput{Title: "来源", Brief: "真实合成图片引用的来源"})
	if e != nil {
		t.Fatal(e)
	}
	target, _, e := s.CreateProject(space, "many-ref-target", ProjectInput{Title: "目标", Brief: "逐份复制，不限制累计素材数"})
	if e != nil {
		t.Fatal(e)
	}
	png := syntheticCreativePNG()
	input := libraryWrite{Kind: "character", Card: library.Card{Name: "多参考人物", Character: &library.Character{}, ProhibitedChanges: []string{}}}
	for i := 0; i < 25; i++ {
		state, e := s.media.AddAsset(source.ID, space, fmt.Sprintf("ref-upload-%d", i), fmt.Sprintf("视角%d", i), "reference", "", "", "uploaded", png, "image/png")
		if e != nil {
			t.Fatal(e)
		}
		input.References = append(input.References, libraryReferenceInput{ProjectID: source.ID, AssetID: state.Assets[len(state.Assets)-1].ID, Role: "reference", ViewAngle: fmt.Sprintf("视角%d", i)})
	}
	var saved libraryrepo.Result
	_ = json.Unmarshal(intentLibraryCall(t, s, "POST", "/api/v1/library/assets?space_id="+space, "many-ref-card", input, 201), &saved)
	if len(saved.Version.References) != 25 {
		t.Fatal("old reference count gate")
	}
	binding := libraryBindingInput{ExpectedVersion: 1, AssetID: saved.Asset.ID, VersionID: saved.Version.ID, Usage: "character"}
	path := "/api/v1/projects/" + target.ID + "/library-bindings?space_id=" + space
	// A late corrupt source must leave all target metadata and the receipt absent.
	last := saved.Version.References[24]
	original, e := s.media.localBlob(space, "library", last.ID)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(original, []byte("corrupt"), 0600); e != nil {
		t.Fatal(e)
	}
	intentLibraryCall(t, s, "POST", path, "many-ref-bind", binding, 500)
	untouched := s.media.Get(target.ID, space)
	if len(untouched.Assets) != 0 || len(untouched.LibraryBindings) != 0 || untouched.Version != 1 {
		t.Fatal("partial metadata exposed")
	}
	if e = os.WriteFile(original, png, 0600); e != nil {
		t.Fatal(e)
	}
	var media MediaState
	_ = json.Unmarshal(intentLibraryCall(t, s, "POST", path, "many-ref-bind", binding, 201), &media)
	if len(media.Assets) != 25 || len(media.LibraryBindings) != 1 || len(media.LibraryBindings[0].MediaAssetIDs) != 25 {
		t.Fatal("copy truncated")
	}
	sum := sha256.Sum256(png)
	for _, asset := range media.Assets {
		path, _, e := s.media.file(target.ID, space, asset.ID, false, false)
		if e != nil {
			t.Fatal(e)
		}
		data, e := safeReadLocal(path, 4<<20)
		h := sha256.Sum256(data)
		if e != nil || hex.EncodeToString(h[:]) != hex.EncodeToString(sum[:]) {
			t.Fatal("copied bytes", e)
		}
	}
	intentLibraryCall(t, s, "POST", path, "many-ref-bind", binding, 200)
	if len(s.media.Get(target.ID, space).LibraryBindings) != 1 {
		t.Fatal("duplicate binding after replay")
	}
	voice := intentCards()["voice"]
	voice.References = input.References[:1]
	intentLibraryCall(t, s, "POST", "/api/v1/library/assets?space_id="+space, "image-is-not-voice", voice, 400)
}
func TestFileLibraryReferenceCountAndAtomicCopy(t *testing.T) {
	libraryManyReferenceContract(t, testStore(t))
}
func TestPostgresLibraryReferenceCountAndAtomicCopy(t *testing.T) {
	libraryManyReferenceContract(t, postgresTestStore(t, postgresTestDB(t)))
}

func TestPostgresLibraryIntentOfflineRoundTrip(t *testing.T) {
	ctx := context.Background()
	db := postgresTestDB(t)
	dir := t.TempDir()
	file, e := OpenStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	original := libraryIntentContract(t, file)
	if e = file.Close(); e != nil {
		t.Fatal(e)
	}
	if _, e = ImportPostgresData(ctx, dir, db); e != nil {
		t.Fatal(e)
	}
	pg := postgresTestStore(t, db)
	for kind, input := range intentCards() {
		var r libraryrepo.Result
		_ = json.Unmarshal(intentLibraryCall(t, pg, "POST", "/api/v1/library/assets?space_id=personal-demo", "intent-create-"+kind, input, 200), &r)
		if !reflect.DeepEqual(original[kind], r) {
			t.Fatal("PG intent receipt changed")
		}
	}
	dst := filepath.Join(t.TempDir(), "restored")
	if _, e = ExportPostgresData(ctx, dst, db); e != nil {
		t.Fatal(e)
	}
	restored, e := OpenStore(dst)
	if e != nil {
		t.Fatal(e)
	}
	defer restored.Close()
	for kind, input := range intentCards() {
		var r libraryrepo.Result
		_ = json.Unmarshal(intentLibraryCall(t, restored, "POST", "/api/v1/library/assets?space_id=personal-demo", "intent-create-"+kind, input, 200), &r)
		if !reflect.DeepEqual(original[kind], r) {
			t.Fatal("file intent receipt changed")
		}
	}
}

func TestLibraryMetadataWorksetRejectsBeforePublishing(t *testing.T) {
	s := testStore(t)
	in := intentCards()["style"]
	in.Card.ProhibitedChanges = nil
	for i := 0; i < 1000; i++ {
		in.Card.ProhibitedChanges = append(in.Card.ProhibitedChanges, fmt.Sprintf("%s%d", string(bytes.Repeat([]byte("<"), 210)), i))
	}
	encoded, _ := json.Marshal(in)
	body := bytes.ReplaceAll(encoded, []byte(`\u003c`), []byte("<"))
	if len(body) >= 1<<20 {
		t.Fatal("fixture must fit inbound parser")
	}
	r := httptest.NewRequest("POST", "/api/v1/library/assets?space_id=personal-demo", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Idempotency-Key", "large-manifest")
	w := httptest.NewRecorder()
	Handler(s, "").ServeHTTP(w, r)
	if w.Code != 413 || !bytes.Contains(w.Body.Bytes(), []byte("library_request_too_large")) {
		t.Fatal("metadata bound", w.Code, w.Body.String())
	}
	items, e := s.library.List(context.Background(), "personal-demo", libraryrepo.Query{Limit: 20})
	if e != nil || len(items) != 0 {
		t.Fatal("failed payload published", e)
	}
}
func TestPostgresLibraryKindUpgradeIsExplicitAndPreservesOldRows(t *testing.T) {
	ctx := context.Background()
	db := postgresTestDB(t)
	s := postgresTestStore(t, db)
	old := libraryWrite{Kind: "character", Card: library.Card{Name: "旧资产", Character: &library.Character{}, ProhibitedChanges: []string{}}, References: []libraryReferenceInput{}}
	var original libraryrepo.Result
	_ = json.Unmarshal(intentLibraryCall(t, s, "POST", "/api/v1/library/assets?space_id=personal-demo", "old-kind-before-upgrade", old, 201), &original)
	if _, e := db.Exec(`ALTER TABLE studio_library_assets DROP CONSTRAINT studio_library_kind_v2; ALTER TABLE studio_library_assets ADD CONSTRAINT studio_library_assets_kind_check CHECK(kind IN ('character','product','scene'))`); e != nil {
		t.Fatal(e)
	}
	if e := CheckPostgres(ctx, db); e == nil {
		t.Fatal("old kind schema advertised ready")
	}
	var stillOld bool
	if e := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM pg_constraint WHERE connamespace=current_schema()::regnamespace AND conname='studio_library_assets_kind_check')`).Scan(&stillOld); e != nil || !stillOld {
		t.Fatal("readiness mutated schema", e)
	}
	for i := 0; i < 2; i++ {
		if e := InitPostgres(ctx, db); e != nil {
			t.Fatal("initializer", e)
		}
	}
	if e := CheckPostgres(ctx, db); e != nil {
		t.Fatal("upgraded schema not ready", e)
	}
	current, e := s.library.Get(ctx, "personal-demo", original.Asset.ID)
	if e != nil || !reflect.DeepEqual(current, original) {
		t.Fatal("upgrade rewrote old content", e)
	}
	libraryIntentContract(t, s)
}
