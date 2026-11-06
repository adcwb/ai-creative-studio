package application

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/projects"
)

func structureUpgradeContract(t *testing.T, s *FileStore) {
	t.Helper()
	p := boardProject(t, s)
	legacy := cloneProject(p)
	oldMedia, err := s.media.GetChecked(p.ID, p.SpaceID)
	if err != nil {
		t.Fatal(err)
	}
	beforeHistory, _ := s.ProjectHistory(p.SpaceID, p.ID)
	input := StructureUpgradeInput{ExpectedVersion: p.Version}
	p, err = s.UpgradeProjectStructure(p.SpaceID, p.ID, "structure-upgrade-key", input)
	if err != nil || p.StructureVersion != 2 || p.Script.StructureVersion != 2 || p.Version != legacy.Version+1 || p.ScriptVersion != legacy.ScriptVersion+1 || p.StoryboardStale {
		t.Fatal("atomic upgrade failed", p, err)
	}
	if p.Storyboard.SourceScriptVersion != p.ScriptVersion || p.Storyboard.Shots[0].SceneID != p.Script.Scenes[p.Storyboard.Shots[0].SceneIndex-1].ID || len(p.StructureMappings) != 0 {
		t.Fatal("source association or public mapping privacy", p)
	}
	s.mu.Lock()
	mappings := projects.CloneStructureMappings(s.data.Projects[p.ID].StructureMappings)
	s.mu.Unlock()
	if len(mappings) != 1 || mappings[0].SourceScriptVersion != legacy.ScriptVersion {
		t.Fatal("original mapping was not persisted", mappings)
	}
	history, _ := s.ProjectHistory(p.SpaceID, p.ID)
	if len(history) != len(beforeHistory)+2 || !reflect.DeepEqual(history[1].Script, legacy.Script) {
		t.Fatal("original pre-upgrade revision not retained", history)
	}
	// A replay returns the current project, and never overwrites later edits.
	draft := cloneScript(*p.Script)
	oldScene := draft.Scenes[0].ID
	oldChar := draft.Characters[0].ID
	draft.Characters[0].Name = "改名后的主角"
	draft.Scenes[0], draft.Scenes[1] = draft.Scenes[1], draft.Scenes[0]
	p, err = s.SaveProjectScript(p.SpaceID, p.ID, "structure-rename-reorder", ScriptSaveInput{ExpectedVersion: p.Version, Script: draft})
	if err != nil || !p.StoryboardStale || p.Script.Scenes[1].ID != oldScene || p.Script.Characters[0].ID != oldChar {
		t.Fatal("rename/reorder lost identity", err)
	}
	for _, scene := range p.Script.Scenes {
		for _, line := range scene.Dialogue {
			if line.CharacterID == oldChar && line.Character != "改名后的主角" {
				t.Fatal("display name did not follow identity")
			}
		}
	}
	current := cloneProject(p)
	p, err = s.UpgradeProjectStructure(p.SpaceID, p.ID, "structure-upgrade-key", input)
	if err != nil || !reflect.DeepEqual(p, current) {
		t.Fatal("upgrade replay overwrote edits", err)
	}
	if _, err = s.UpgradeProjectStructure(p.SpaceID, p.ID, "structure-upgrade-key", StructureUpgradeInput{ExpectedVersion: p.Version}); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatal("same key accepted changed body", err)
	}
	if _, err = s.UpgradeProjectStructure("team-demo", p.ID, "structure-cross-space", input); !errors.Is(err, ErrNotFound) {
		t.Fatal("cross-space source visible", err)
	}
	if _, err = s.SaveProjectScript(p.SpaceID, p.ID, "structure-old-client-write", ScriptSaveInput{ExpectedVersion: p.Version, Script: *legacy.Script}); err == nil {
		t.Fatal("old client stripped stable associations")
	}
	shots := append([]StoryboardShot(nil), p.Storyboard.Shots...)
	shots[0].Description = "只修改旧来源的画面描述"
	p, err = s.SaveProjectStoryboard(p.SpaceID, p.ID, "structure-edit-old-board", StoryboardSaveInput{ExpectedVersion: p.Version, Shots: shots})
	if err != nil || p.Storyboard.Shots[0].SceneID != current.Storyboard.Shots[0].SceneID || !p.StoryboardStale {
		t.Fatal("stale board rebound to reordered current scenes", err)
	}
	shots[0].SceneID = p.Script.Scenes[0].ID
	if _, err = s.SaveProjectStoryboard(p.SpaceID, p.ID, "structure-contradictory-board", StoryboardSaveInput{ExpectedVersion: p.Version, Shots: shots}); err == nil {
		t.Fatal("contradictory index and identity accepted")
	}
	cut := cloneScript(*p.Script)
	cut.Scenes = append(cut.Scenes[:1], cut.Scenes[2:]...)
	// The moved first source scene is now at index one. Its deletion from the
	// current draft must not delete the old board's pinned source association.
	p, err = s.SaveProjectScript(p.SpaceID, p.ID, "structure-delete-current-scene", ScriptSaveInput{ExpectedVersion: p.Version, Script: cut})
	if err != nil || p.Storyboard.SourceSceneIDs[0] != oldScene || !p.StoryboardStale {
		t.Fatal("deleting a current scene rewrote a historical board", err)
	}
	// Restore the original legacy snapshot using its pinned identities, not new IDs.
	var original ProjectRevision
	for _, r := range history {
		if r.Kind == "script" && r.ScriptVersion == legacy.ScriptVersion {
			original = r
		}
	}
	p, err = s.RestoreHistory(p.SpaceID, p.ID, original.ID, "structure-restore-original", ScriptGenerationInput{ExpectedVersion: p.Version})
	if err != nil || p.Script.Characters[0].ID != oldChar || p.Script.Scenes[0].ID != oldScene {
		t.Fatal("restored legacy revision lost original identity", err)
	}
	nowMedia, err := s.media.GetChecked(p.ID, p.SpaceID)
	if err != nil || !reflect.DeepEqual(nowMedia, oldMedia) {
		t.Fatal("identity upgrade changed media or old exports", err)
	}
	// Public/history views are detached from persisted arrays.
	p.Storyboard.SourceSceneIDs[0] = "external-mutation"
	loaded, _ := s.GetProject(p.SpaceID, p.ID)
	if loaded.Storyboard.SourceSceneIDs[0] == "external-mutation" {
		t.Fatal("source array escaped storage")
	}
}

func TestFileStructureUpgradeAssociationsAndHistory(t *testing.T) {
	structureUpgradeContract(t, testStore(t))
}
func TestPostgresStructureUpgradeAssociationsAndHistory(t *testing.T) {
	structureUpgradeContract(t, postgresTestStore(t, postgresTestDB(t)))
}

func TestStructureUpgradeStaleSourceMissingAndAmbiguousFailClosed(t *testing.T) {
	for _, mode := range []string{"available", "missing", "ambiguous", "invalid-current"} {
		t.Run(mode, func(t *testing.T) {
			s := testStore(t)
			p := boardProject(t, s)
			old := cloneScript(*p.Script)
			draft := cloneScript(old)
			draft.Scenes[0], draft.Scenes[1] = draft.Scenes[1], draft.Scenes[0]
			p, err := s.SaveProjectScript(p.SpaceID, p.ID, "structure-old-reorder", ScriptSaveInput{ExpectedVersion: p.Version, Script: draft})
			if err != nil {
				t.Fatal(err)
			}
			s.mu.Lock()
			d := s.clone()
			if mode == "missing" {
				d.History[p.ID] = nil
			} else if mode == "ambiguous" {
				other := cloneRevision(d.History[p.ID][0])
				other.ID = "ambiguous-test-revision"
				other.Script.Title = "另一份矛盾的同版本旧稿"
				d.History[p.ID] = append(d.History[p.ID], other)
			} else if mode == "invalid-current" {
				corrupt := d.Projects[p.ID]
				corrupt.Script.Characters = nil
				d.Projects[p.ID] = corrupt
			}
			if err = s.commit(d); err != nil {
				t.Fatal(err)
			}
			s.mu.Unlock()
			before, _ := s.GetProject(p.SpaceID, p.ID)
			after, err := s.UpgradeProjectStructure(p.SpaceID, p.ID, "structure-stale-upgrade", StructureUpgradeInput{ExpectedVersion: p.Version})
			if mode != "available" {
				unchanged, _ := s.GetProject(p.SpaceID, p.ID)
				if err == nil || !reflect.DeepEqual(before, unchanged) {
					t.Fatal("ambiguous/missing history was guessed or partially upgraded", err)
				}
				s.mu.Lock()
				defer s.mu.Unlock()
				if len(s.data.Projects[p.ID].StructureMappings) != 0 {
					t.Fatal("failed upgrade persisted a partial source mapping")
				}
				if _, exists := s.data.ProjectReceipts[projectReceiptKey(p.SpaceID, "structure-stale-upgrade")]; exists {
					t.Fatal("failed upgrade accepted an idempotency receipt")
				}
				return
			}
			stableOld, _, _ := projects.CompileStructure(old, structureNamespace(p), 1)
			if err != nil || !after.StoryboardStale || after.Storyboard.Shots[0].SceneID != stableOld.Scenes[after.Storyboard.Shots[0].SceneIndex-1].ID {
				t.Fatal("stale source was derived from today's order", err)
			}
		})
	}
}

func TestStableGenerationBindsServerIdentitiesAndRejectsInvalidOutput(t *testing.T) {
	s := testStore(t)
	p, _, err := s.CreateProject("personal-demo", "structure-new-project", ProjectInput{StructureVersion: 2, Title: "新版身份项目", Brief: "确定性模型合成测试"})
	if err != nil {
		t.Fatal(err)
	}
	g, _, err := s.CreateScriptGeneration(p.SpaceID, p.ID, "structure-generate-first", ScriptGenerationInput{ExpectedVersion: p.Version})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.FinishScriptGeneration(g, scriptFixture(t), nil); err != nil {
		t.Fatal(err)
	}
	p, _ = s.GetProject(p.SpaceID, p.ID)
	if p.Script.StructureVersion != 2 || p.Script.Scenes[0].ID == "" || p.Script.Characters[0].ID == "" {
		t.Fatal("accepted generated content has no identities")
	}
	bg, _, err := s.CreateStoryboardGeneration(p.SpaceID, p.ID, "structure-generate-board", ScriptGenerationInput{ExpectedVersion: p.Version})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.FinishStoryboardGeneration(bg, boardFixture(t), nil); err != nil {
		t.Fatal(err)
	}
	p, _ = s.GetProject(p.SpaceID, p.ID)
	before := cloneProject(p)
	if p.Storyboard.StructureVersion != 2 || p.Storyboard.SourceSceneIDs[0] != p.Script.Scenes[0].ID {
		t.Fatal("generated board lost source identities")
	}
	g, _, _ = s.CreateScriptGeneration(p.SpaceID, p.ID, "structure-invalid-output", ScriptGenerationInput{ExpectedVersion: p.Version})
	bad := cloneScript(*p.Script)
	bad.Scenes[1].ID = bad.Scenes[0].ID
	if err = s.FinishScriptGeneration(g, bad, nil); err != nil {
		t.Fatal(err)
	}
	p, _ = s.GetProject(p.SpaceID, p.ID)
	if p.LatestGeneration.Status != "failed" || p.Version != before.Version || !reflect.DeepEqual(p.Script, before.Script) || !reflect.DeepEqual(p.Storyboard, before.Storyboard) {
		t.Fatal("invalid model output replaced formal content")
	}
}

func TestStructureUpgradeHTTPAndNoReadAssignment(t *testing.T) {
	s := testStore(t)
	p := makeScript(t, s, createProject(t, s))
	q := "?space_id=" + p.SpaceID
	path := "/projects/" + p.ID
	read := projectHTTP(t, s, "GET", path+q, "", "", 200)
	if _, exists := read["structure_version"]; exists {
		t.Fatal("GET silently assigned new structure")
	}
	projectHTTP(t, s, "POST", path+"/structure-upgrades"+q, "http-structure-upgrade", projectJSON(t, StructureUpgradeInput{p.Version}), 200)
	after := projectHTTP(t, s, "GET", path+q, "", "", 200)
	if after["structure_version"] != float64(2) || after["structure_mappings"] != nil {
		t.Fatal("public upgrade contract", after)
	}
	projectHTTP(t, s, "POST", path+"/structure-upgrades?space_id=team-demo", "http-structure-cross", projectJSON(t, StructureUpgradeInput{p.Version}), 404)
	projectHTTP(t, s, "POST", path+"/structure-upgrades"+q, "http-structure-forged", `{"expected_version":1,"user_id":"owner"}`, 400)
}

func TestStructureUpgradeRestartKeepsOriginalReceiptAndMappings(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	p := boardProject(t, s)
	in := StructureUpgradeInput{p.Version}
	accepted, err := s.UpgradeProjectStructure(p.SpaceID, p.ID, "structure-response-lost", in)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	replay, err := s.UpgradeProjectStructure(p.SpaceID, p.ID, "structure-response-lost", in)
	if err != nil || !reflect.DeepEqual(accepted, replay) {
		t.Fatal("restart changed accepted upgrade", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.data.Projects[p.ID].StructureMappings) != 1 {
		t.Fatal("restart lost original mapping")
	}
}

func TestPostgresStructureUpgradeTwoAPIInstancesReplayOneTransaction(t *testing.T) {
	db := postgresTestDB(t)
	a, b := postgresTestStore(t, db), postgresTestStore(t, db)
	p := boardProject(t, a)
	var wg sync.WaitGroup
	for _, store := range []*FileStore{a, b} {
		wg.Add(1)
		go func(s *FileStore) {
			defer wg.Done()
			v, err := s.UpgradeProjectStructure(p.SpaceID, p.ID, "structure-two-api-key", StructureUpgradeInput{p.Version})
			if err != nil || v.Version != p.Version+1 {
				t.Errorf("concurrent replay: version %d, error %v", v.Version, err)
			}
		}(store)
	}
	wg.Wait()
	first, _ := a.GetProject(p.SpaceID, p.ID)
	second, _ := b.GetProject(p.SpaceID, p.ID)
	if !reflect.DeepEqual(first, second) || first.Version != p.Version+1 {
		t.Fatal("API caches disagree on stable identity transaction")
	}
}

func TestStructureUpgradeSecureRolesCSRFRevocationAndSourceScope(t *testing.T) {
	s := testStore(t)
	auth, err := OpenAuthStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	owner, token, session, err := auth.login("structure-owner@example.invalid", "Synthetic-structure-owner-password!", true)
	if err != nil {
		t.Fatal(err)
	}
	_, viewerToken, viewerSession, err := auth.login("structure-viewer@example.invalid", "Synthetic-structure-viewer-password!", true)
	if err != nil {
		t.Fatal(err)
	}
	space := testTeamSpace(t, auth, owner.ID)
	p, _, err := s.CreateProject(space, "structure-owned-project", projectInput())
	if err != nil {
		t.Fatal(err)
	}
	h := SecureHandler(s, "http://localhost:5173", auth, AuthOptions{AllowRegistration: true})
	path := "/api/v1/projects/" + p.ID + "/structure-upgrades?space_id=" + space
	body := `{"expected_version":1}`
	authRequest(t, h, "POST", path, body, token, "", 403)
	authRequest(t, h, "POST", path, body, viewerToken, viewerSession.CSRF, 403)
	authRequest(t, h, "PUT", "/api/v1/organizations/"+space+"/members", `{"email":"structure-viewer@example.invalid","role":"viewer"}`, token, session.CSRF, 200)
	authRequest(t, h, "POST", path, body, viewerToken, viewerSession.CSRF, 403)
	w := authRequest(t, h, "POST", path, body, token, session.CSRF, 200)
	var accepted Project
	if err = json.Unmarshal(w.Body.Bytes(), &accepted); err != nil || accepted.StructureVersion != 2 {
		t.Fatal("authorized upgrade failed", err)
	}
	authRequest(t, h, "PUT", "/api/v1/organizations/"+space+"/members", `{"email":"structure-viewer@example.invalid","role":"remove"}`, token, session.CSRF, 200)
	authRequest(t, h, "POST", path, body, viewerToken, viewerSession.CSRF, 403)
}

func TestStableIndependentStoryboardSurvivesRestartWithoutScript(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	p, _, err := s.CreateProject("personal-demo", "independent-structure-project", ProjectInput{StructureVersion: 2, Title: "先画镜头", Brief: "没有剧本时先建立独立镜头"})
	if err != nil {
		t.Fatal(err)
	}
	shots := boardFixture(t)
	for i := range shots {
		shots[i].SceneIndex = 0
	}
	p, err = s.SaveProjectStoryboard(p.SpaceID, p.ID, "independent-structure-board", StoryboardSaveInput{ExpectedVersion: p.Version, Shots: shots})
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = OpenStore(dir)
	if err != nil {
		t.Fatal("valid independent board rejected on restart", err)
	}
	defer s.Close()
	loaded, err := s.GetProject(p.SpaceID, p.ID)
	if err != nil || !reflect.DeepEqual(p, loaded) || loaded.Storyboard.SourceScriptVersion != 0 || loaded.Storyboard.StructureVersion != 2 {
		t.Fatal("independent board lost", err)
	}
}

func TestPostgresStructureOfflineRoundTripPreservesOriginalMappings(t *testing.T) {
	db := postgresTestDB(t)
	dir := t.TempDir()
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	p := boardProject(t, s)
	in := StructureUpgradeInput{p.Version}
	p, err = s.UpgradeProjectStructure(p.SpaceID, p.ID, "structure-round-trip", in)
	if err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	original := cloneProject(s.data.Projects[p.ID])
	s.mu.Unlock()
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ImportPostgresData(context.Background(), dir, db); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if err != nil || string(before) != string(after) {
		t.Fatal("offline import changed original source file", err)
	}
	pg := postgresTestStore(t, db)
	pg.mu.Lock()
	imported := cloneProject(pg.data.Projects[p.ID])
	pg.mu.Unlock()
	if !reflect.DeepEqual(original, imported) {
		t.Fatal("import lost project identities or sealed mappings")
	}
	restoredDir := filepath.Join(t.TempDir(), "restored")
	if _, err = ExportPostgresData(context.Background(), restoredDir, db); err != nil {
		t.Fatal(err)
	}
	restored, err := OpenStore(restoredDir)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	replay, err := restored.UpgradeProjectStructure(p.SpaceID, p.ID, "structure-round-trip", in)
	if err != nil || !reflect.DeepEqual(p, replay) {
		t.Fatal("export lost original receipt or rebound identities", err)
	}
	restored.mu.Lock()
	defer restored.mu.Unlock()
	if !reflect.DeepEqual(original, restored.data.Projects[p.ID]) {
		t.Fatal("export lost immutable legacy source mapping")
	}
}

func TestStructureUpgradeWithoutCharactersRestoresItsOriginalMapping(t *testing.T) {
	s := testStore(t)
	p := createProject(t, s)
	script := scriptFixture(t)
	script.Characters = nil
	for i := range script.Scenes {
		script.Scenes[i].Dialogue = nil
	}
	p, err := s.SaveProjectScript(p.SpaceID, p.ID, "silent-legacy-script", ScriptSaveInput{ExpectedVersion: p.Version, Script: script})
	if err != nil {
		t.Fatal(err)
	}
	p, err = s.UpgradeProjectStructure(p.SpaceID, p.ID, "silent-script-upgrade", StructureUpgradeInput{p.Version})
	if err != nil {
		t.Fatal(err)
	}
	oldID := p.Script.Scenes[0].ID
	history, err := s.ProjectHistory(p.SpaceID, p.ID)
	if err != nil || len(history) != 1 {
		t.Fatal("missing original silent script", err)
	}
	p, err = s.RestoreHistory(p.SpaceID, p.ID, history[0].ID, "silent-script-restore", ScriptGenerationInput{ExpectedVersion: p.Version})
	if err != nil || p.Script.Scenes[0].ID != oldID {
		t.Fatal("empty cast changed the original mapping", err)
	}
}

func TestStructureFieldsPreserveLegacyRequestFingerprints(t *testing.T) {
	// Fixed pre-structure-protocol receipts: adding optional fields must not
	// invalidate accepted requests from a previous binary after an upgrade.
	for _, test := range []struct {
		operation, id, digest string
		body                  any
	}{
		{"create", "", "042948147489c0679c116e77175a3f64f4b5d7a1d06b69746de457801e980116", ProjectInput{Title: "Legacy title", Brief: "Legacy brief"}},
		{"save", "legacy-project", "de12b5a79437015704184cafd5cdb16f98b7c991283fdf6e6becefb38be4365e", ScriptSaveInput{ExpectedVersion: 1, Script: Script{Title: "Legacy title", Logline: "Legacy story", Characters: []ScriptCharacter{}, Scenes: []ScriptScene{{Heading: "Legacy scene", Action: "Legacy action", Dialogue: []ScriptDialogue{}}}}}},
		{"storyboard-save", "legacy-project", "e8f7a93404ce6766e525bbffccbd47efa46d4bb5218c558e76042ff778570aaa", StoryboardSaveInput{ExpectedVersion: 2, Shots: []StoryboardShot{{ID: "legacy-shot", SceneIndex: 0, Description: "Legacy shot", ShotSize: "medium", CameraMovement: "static", DurationSeconds: 3}}}},
	} {
		digest, err := projectFingerprint(test.operation, test.id, test.body)
		if err != nil || digest != test.digest {
			t.Errorf("legacy %s receipt changed: %s, %v", test.operation, digest, err)
		}
	}
}
