package studio

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
)

func boardFixture(t *testing.T) []StoryboardShot {
	t.Helper()
	p, e := NewStoryboardPipeline(context.Background(), MockStoryboardProvider{})
	if e != nil {
		t.Fatal(e)
	}
	shots, e := p.Invoke(context.Background(), scriptFixture(t))
	if e != nil {
		t.Fatal(e)
	}
	return shots
}
func boardProject(t *testing.T, s *FileStore) Project {
	t.Helper()
	p := makeScript(t, s, createProject(t, s))
	g, _, e := s.CreateStoryboardGeneration(p.SpaceID, p.ID, "board-fixture-key", ScriptGenerationInput{ExpectedVersion: p.Version})
	if e != nil {
		t.Fatal(e)
	}
	if e = s.FinishStoryboardGeneration(g, boardFixture(t), nil); e != nil {
		t.Fatal(e)
	}
	p, e = s.GetProject(p.SpaceID, p.ID)
	if e != nil {
		t.Fatal(e)
	}
	return p
}
func TestStoryboardVersionsStaleAndFailure(t *testing.T) {
	s, e := OpenStore(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	p := boardProject(t, s)
	if p.ScriptVersion != 1 || p.StoryboardStale || p.Storyboard.SourceScriptVersion != 1 || p.Version != 3 {
		t.Fatal(p)
	}
	p, e = s.UpdateProject(p.SpaceID, p.ID, "board-brief-update", ProjectUpdate{Title: p.Title, Brief: "新需求不直接使已有分镜过期", ExpectedVersion: p.Version})
	if e != nil || p.StoryboardStale {
		t.Fatal(e, p)
	}
	script := cloneScript(*p.Script)
	script.Scenes[0].Action = "[mock-fail] 合成失败场景"
	p, e = s.SaveProjectScript(p.SpaceID, p.ID, "board-script-update", ScriptSaveInput{Script: script, ExpectedVersion: p.Version})
	if e != nil || !p.StoryboardStale || p.ScriptVersion != 2 {
		t.Fatal(e, p)
	}
	input := StoryboardSaveInput{ExpectedVersion: p.Version, Shots: append([]StoryboardShot(nil), p.Storyboard.Shots...)}
	input.Shots[0].Description = "旧来源计划仍可编辑"
	p, e = s.SaveProjectStoryboard(p.SpaceID, p.ID, "board-save-stale", input)
	if e != nil || !p.StoryboardStale || p.Storyboard.SourceScriptVersion != 1 {
		t.Fatal(e, p)
	}
	before := *p.Storyboard
	version := p.Version
	g, _, e := s.CreateStoryboardGeneration(p.SpaceID, p.ID, "board-failed-run", ScriptGenerationInput{ExpectedVersion: p.Version})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.SaveProjectScript(p.SpaceID, p.ID, "blocked-script-save", ScriptSaveInput{Script: script, ExpectedVersion: p.Version}); !errors.Is(e, ErrGenerationActive) {
		t.Fatal(e)
	}
	if _, e = s.SaveProjectStoryboard(p.SpaceID, p.ID, "blocked-board-save", StoryboardSaveInput{ExpectedVersion: p.Version, Shots: input.Shots}); !errors.Is(e, ErrGenerationActive) {
		t.Fatal(e)
	}
	worker, ok, e := s.NextStoryboardGeneration()
	if e != nil || !ok || worker.SourceScript == nil || g.SourceScript != nil {
		t.Fatal(e)
	}
	pipeline, e := NewStoryboardPipeline(context.Background(), MockStoryboardProvider{})
	if e != nil {
		t.Fatal(e)
	}
	shots, runErr := pipeline.Invoke(context.Background(), *worker.SourceScript)
	if runErr == nil {
		t.Fatal("mock failure not executed")
	}
	if e = s.FinishStoryboardGeneration(worker, shots, runErr); e != nil {
		t.Fatal(e)
	}
	p, _ = s.GetProject(p.SpaceID, p.ID)
	if p.Version != version || !reflect.DeepEqual(*p.Storyboard, before) || p.LatestStoryboardGeneration.ErrorCode != "mock_storyboard_failed" {
		t.Fatal(p)
	}
	replay, e := s.SaveProjectStoryboard(p.SpaceID, p.ID, "board-save-stale", input)
	if e != nil || replay.Version != version {
		t.Fatal(e)
	}
}
func TestStoryboardRecoveryConcurrentAndIsolation(t *testing.T) {
	dir := t.TempDir()
	s, e := OpenStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	p := boardProject(t, s)
	copyP, _ := s.GetProject(p.SpaceID, p.ID)
	copyP.Storyboard.Shots[0].Description = "corrupt caller"
	fresh, _ := s.GetProject(p.SpaceID, p.ID)
	if fresh.Storyboard.Shots[0].Description == "corrupt caller" {
		t.Fatal("aliased plan")
	}
	var successes atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := s.SaveProjectStoryboard(p.SpaceID, p.ID, "concurrent-board-"+string(rune('a'+i)), StoryboardSaveInput{ExpectedVersion: p.Version, Shots: p.Storyboard.Shots})
			if err == nil {
				successes.Add(1)
			} else if !errors.Is(err, ErrVersionConflict) {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	if successes.Load() != 1 {
		t.Fatal(successes.Load())
	}
	p, _ = s.GetProject(p.SpaceID, p.ID)
	in := ScriptGenerationInput{ExpectedVersion: p.Version}
	g, _, e := s.CreateStoryboardGeneration(p.SpaceID, p.ID, "restart-board-key", in)
	if e != nil {
		t.Fatal(e)
	}
	running, ok, e := s.NextStoryboardGeneration()
	if e != nil || !ok {
		t.Fatal(e)
	}
	running.SourceScript.Scenes[0].Action = "mutated worker copy"
	s.Close()
	s, e = OpenStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	replay, created, e := s.CreateStoryboardGeneration(p.SpaceID, p.ID, "restart-board-key", in)
	if e != nil || created || replay.ID != g.ID || replay.Status != "queued" || replay.SourceScript != nil {
		t.Fatal(e, replay)
	}
	running, ok, e = s.NextStoryboardGeneration()
	if e != nil || !ok || running.SourceScript.Scenes[0].Action == "mutated worker copy" {
		t.Fatal(e)
	}
	if e = s.FinishStoryboardGeneration(running, boardFixture(t), nil); e != nil {
		t.Fatal(e)
	}
	if e = s.FinishStoryboardGeneration(running, boardFixture(t), nil); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	fresh, _ = s.GetProject(p.SpaceID, p.ID)
	if fresh.Version != p.Version+1 {
		t.Fatal(fresh.Version)
	}
}
func TestStoryboardMigrationAndCorruption(t *testing.T) {
	dir := t.TempDir()
	s, e := OpenStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	p := makeScript(t, s, createProject(t, s))
	s.Close()
	path := filepath.Join(dir, "state.json")
	raw, _ := os.ReadFile(path)
	var data map[string]any
	if e = json.Unmarshal(raw, &data); e != nil {
		t.Fatal(e)
	}
	data["version"] = 2
	delete(data, "storyboard_generations")
	projects := data["projects"].(map[string]any)
	delete(projects[p.ID].(map[string]any), "script_version")
	raw, _ = json.Marshal(data)
	if e = os.WriteFile(path, raw, 0600); e != nil {
		t.Fatal(e)
	}
	s, e = OpenStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	p, _ = s.GetProject(p.SpaceID, p.ID)
	if p.ScriptVersion != 1 || p.Script == nil {
		t.Fatal(p)
	}
	s.Close()
	raw, _ = os.ReadFile(path)
	json.Unmarshal(raw, &data)
	delete(data, "storyboard_generations")
	raw, _ = json.Marshal(data)
	os.WriteFile(path, raw, 0600)
	if broken, e := OpenStore(dir); e == nil {
		broken.Close()
		t.Fatal("missing v3 map accepted")
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(raw) {
		t.Fatal("corrupt file changed")
	}
}
func TestStoryboardValidationAndWriteFailure(t *testing.T) {
	shots := boardFixture(t)
	cases := []func([]StoryboardShot){func(s []StoryboardShot) { s[0].SceneIndex = 4 }, func(s []StoryboardShot) { s[0].ID = s[1].ID }, func(s []StoryboardShot) { s[0].ShotSize = "unknown" }, func(s []StoryboardShot) { s[0].CameraMovement = "unknown" }, func(s []StoryboardShot) { s[0].DurationSeconds = 0 }, func(s []StoryboardShot) { s[0].Description = " " }}
	for _, change := range cases {
		copy := append([]StoryboardShot(nil), shots...)
		change(copy)
		if _, e := NormalizeStoryboardShots(copy); e == nil {
			t.Fatal("invalid shot accepted")
		}
	}
	s, e := OpenStore(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	p := boardProject(t, s)
	original := s.path
	s.path = filepath.Join(t.TempDir(), "missing", "state.json")
	in := StoryboardSaveInput{ExpectedVersion: p.Version, Shots: shots}
	in.Shots[0].Description = "未落盘不应污染"
	if _, e = s.SaveProjectStoryboard(p.SpaceID, p.ID, "failed-board-save", in); e == nil {
		t.Fatal("write unexpectedly succeeded")
	}
	s.path = original
	fresh, _ := s.GetProject(p.SpaceID, p.ID)
	if fresh.Version != p.Version || fresh.Storyboard.Shots[0].Description == in.Shots[0].Description {
		t.Fatal("failed write mutated state")
	}
	if _, e = s.SaveProjectStoryboard(p.SpaceID, p.ID, "failed-board-save", in); e != nil {
		t.Fatal("failed write consumed receipt", e)
	}
}

func TestStoryboardHTTPRequiredFieldsAndGuards(t *testing.T) {
	s := testStore(t)
	p := createProject(t, s)
	path := "/projects/" + p.ID + "/storyboard-generations?space_id=personal-demo"
	projectErrorCode(t, projectHTTP(t, s, "POST", path, "board-no-script", `{"expected_version":1}`, 409), "script_missing")
	p = makeScript(t, s, p)
	g := projectHTTP(t, s, "POST", path, "board-http-create", projectJSON(t, ScriptGenerationInput{ExpectedVersion: p.Version}), 202)
	if _, ok := g["source_script"]; ok {
		t.Fatal("private source leaked")
	}
	projectErrorCode(t, projectHTTP(t, s, "POST", "/projects/"+p.ID+"/generations?space_id=personal-demo", "script-during-board", projectJSON(t, ScriptGenerationInput{ExpectedVersion: p.Version}), 409), "generation_active")
	next, _, e := s.NextStoryboardGeneration()
	if e != nil {
		t.Fatal(e)
	}
	if e = s.FinishStoryboardGeneration(next, boardFixture(t), nil); e != nil {
		t.Fatal(e)
	}
	p, _ = s.GetProject(p.SpaceID, p.ID)
	var body map[string]any
	json.Unmarshal([]byte(projectJSON(t, StoryboardSaveInput{ExpectedVersion: p.Version, Shots: p.Storyboard.Shots})), &body)
	shots := body["shots"].([]any)
	shot := shots[0].(map[string]any)
	delete(shot, "dialogue")
	savePath := "/projects/" + p.ID + "/storyboard?space_id=personal-demo"
	projectErrorCode(t, projectHTTP(t, s, "PUT", savePath, "board-http-missing", projectJSON(t, body), 400), "invalid_json")
	shot["dialogue"] = ""
	shot["unexpected"] = true
	projectErrorCode(t, projectHTTP(t, s, "PUT", savePath, "board-http-unknown", projectJSON(t, body), 400), "invalid_json")
	delete(shot, "unexpected")
	projectHTTP(t, s, "PUT", savePath, "board-http-empty", projectJSON(t, body), 200)
	projectErrorCode(t, projectHTTP(t, s, "PUT", "/projects/"+p.ID+"/storyboard?space_id=team-demo", "board-http-cross", projectJSON(t, body), 404), "not_found")
	projectErrorCode(t, projectHTTP(t, s, "PUT", savePath, "board-http-stale", projectJSON(t, body), 409), "version_conflict")
}
