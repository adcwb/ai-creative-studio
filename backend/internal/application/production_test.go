package application

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestProductionUploadReplayIdentifiesOriginalAsset(t *testing.T) {
	s := testStore(t)
	p := createProject(t, s)
	upload := func(key string) string {
		r := httptest.NewRequest("POST", "/api/v1/projects/"+p.ID+"/media/uploads?space_id="+p.SpaceID+"&filename=mask.png&role=mask", bytes.NewReader(syntheticPNG(0)))
		r.Header.Set("Content-Type", "image/png")
		r.Header.Set("Idempotency-Key", key)
		w := httptest.NewRecorder()
		Handler(s, "").ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		var result struct {
			UploadedAssetID string `json:"uploaded_asset_id"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if result.UploadedAssetID == "" {
			t.Fatal("upload did not identify its asset")
		}
		return result.UploadedAssetID
	}
	first := upload("first-mask-upload")
	second := upload("second-mask-upload")
	if first == second || upload("first-mask-upload") != first {
		t.Fatal("replay selected a later asset")
	}
}

func TestProductionDecisionHTTPAndRecovery(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	p := createProject(t, s)
	media, err := s.media.AddAsset(p.ID, p.SpaceID, "candidate-upload", "candidate", "shot", "", "", "uploaded", syntheticPNG(0), "image/png")
	if err != nil {
		t.Fatal(err)
	}
	path := "/projects/" + p.ID + "/media/decisions?space_id=" + p.SpaceID
	in := ProductionDecision{ExpectedVersion: media.Version, Action: "select", AssetID: media.Assets[0].ID, Text: "preferred framing"}
	body := projectJSON(t, in)
	selected := projectHTTP(t, s, "POST", path, "candidate-select", body, 200)
	projectHTTP(t, s, "POST", path, "candidate-select", body, 200)
	current := s.media.Get(p.ID, p.SpaceID)
	if len(current.Selections) != 1 || !reflect.DeepEqual(current.Timeline, media.Timeline) {
		t.Fatal("adoption changed timeline or was duplicated")
	}
	projectHTTP(t, s, "POST", path, "stale-selection", body, 409)
	projectHTTP(t, s, "POST", "/projects/"+p.ID+"/media/decisions?space_id=team-demo", "foreign-selection", body, 404)
	in.ExpectedVersion = current.Version
	in.AssetID = "foreign"
	projectHTTP(t, s, "POST", path, "missing-selection", projectJSON(t, in), 400)
	in.AssetID = media.Assets[0].ID
	in.ShotID = "missing-shot"
	projectHTTP(t, s, "POST", path, "missing-shot-test", projectJSON(t, in), 400)
	in.ShotID = ""
	in.Action = "review"
	in.Time = 2
	projectHTTP(t, s, "POST", path, "invalid-note-time", projectJSON(t, in), 400)
	in.Time = 0
	in.Text = "adjust the lighting"
	projectHTTP(t, s, "POST", path, "review-note-save", projectJSON(t, in), 200)
	current = s.media.Get(p.ID, p.SpaceID)
	in.Action = "resolve"
	in.ExpectedVersion = current.Version
	in.ReviewID = current.Reviews[0].ID
	in.Resolved = true
	projectHTTP(t, s, "POST", path, "review-note-resolve", projectJSON(t, in), 200)
	s.Close()
	s, err = OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	current = s.media.Get(p.ID, p.SpaceID)
	if !current.Reviews[0].Resolved || len(current.Selections) != 1 {
		t.Fatal("decisions were not persisted")
	}
	replayed := projectHTTP(t, s, "POST", path, "candidate-select", body, 200)
	if !reflect.DeepEqual(selected["selections"], replayed["selections"]) {
		t.Fatal("restart duplicated selection")
	}
}

func TestProductionManualStoryAndStandaloneShots(t *testing.T) {
	s := testStore(t)
	p, _, err := s.CreateProject("personal-demo", "standalone-create", ProjectInput{Title: "Sketch", Brief: "A standalone shot", Kind: "image"})
	if err != nil || p.Kind != "image" {
		t.Fatal(err, p)
	}
	shot := StoryboardShot{ID: "one", SceneIndex: 0, Description: "empty street", ShotSize: "wide", CameraMovement: "static", DurationSeconds: 3, Purpose: "establish location"}
	p, err = s.SaveProjectStoryboard(p.SpaceID, p.ID, "standalone-save", StoryboardSaveInput{ExpectedVersion: p.Version, Shots: []StoryboardShot{shot}})
	if err != nil || p.Script != nil {
		t.Fatal(err)
	}
	script := Script{Title: "Sketch", Logline: "Silent observation", Characters: []ScriptCharacter{}, Scenes: []ScriptScene{{Heading: "Street", Action: "Rain falls", Dialogue: []ScriptDialogue{}, EpisodeNumber: 2}}}
	p, err = s.SaveProjectScript(p.SpaceID, p.ID, "handwritten-save", ScriptSaveInput{ExpectedVersion: p.Version, Script: script})
	if err != nil || !p.StoryboardStale {
		t.Fatal(err)
	}
	if p.Script.Scenes[0].Dialogue == nil || p.Script.Characters == nil {
		t.Fatal("silent scripts must serialize arrays, not null")
	}
	shot.SceneIndex = 2
	_, err = s.SaveProjectStoryboard(p.SpaceID, p.ID, "missing-scene-save", StoryboardSaveInput{ExpectedVersion: p.Version, Shots: []StoryboardShot{shot}})
	if !errors.Is(err, ErrInvalidProjectInput) {
		t.Fatal("invalid source scene accepted", err)
	}
}
