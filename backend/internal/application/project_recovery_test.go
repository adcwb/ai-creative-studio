package application

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestProjectSnapshotV1UpgradePreservesImagesAssetsAndKeys(t *testing.T) {
	dir := t.TempDir()
	s, e := OpenStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	mock, _, e := s.Create("personal-demo", "legacy-mock-key", Input{Prompt: "历史模拟图像", Kind: "general", Ratio: "1:1"})
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Finish(mock, svgResult(`<svg width="800" height="800">legacy</svg>`, "1:1"), nil); e != nil {
		t.Fatal(e)
	}
	mockAsset, e := s.SaveAsset("personal-demo", mock.ID)
	if e != nil {
		t.Fatal(e)
	}
	s.Close()
	s = realStore(t, dir)
	real, _, e := s.Create("personal-demo", "legacy-real-key", realInput())
	if e != nil {
		t.Fatal(e)
	}
	png := realResult(t)
	if e = s.Finish(real, png, nil); e != nil {
		t.Fatal(e)
	}
	realAsset, e := s.SaveAsset("personal-demo", real.ID)
	if e != nil {
		t.Fatal(e)
	}
	s.Close()
	path := filepath.Join(dir, "state.json")
	encoded, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	var legacy map[string]json.RawMessage
	if e = json.Unmarshal(encoded, &legacy); e != nil {
		t.Fatal(e)
	}
	legacy["version"] = json.RawMessage(`1`)
	delete(legacy, "projects")
	delete(legacy, "script_generations")
	delete(legacy, "project_receipts")
	delete(legacy, "storyboard_generations")
	before, _ := json.Marshal(legacy)
	if e = os.WriteFile(path, before, 0600); e != nil {
		t.Fatal(e)
	}
	s, e = OpenStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	for _, item := range []struct {
		task  Task
		key   string
		asset Asset
	}{{mock, "legacy-mock-key", mockAsset}, {real, "legacy-real-key", realAsset}} {
		got, created, e := s.Create("personal-demo", item.key, item.task.Input)
		if e != nil || created || got.ID != item.task.ID || got.Provider != item.task.Provider || got.Model != item.task.Model {
			t.Fatal("legacy key rebound/lost", e, got)
		}
		asset, e := s.SaveAsset("personal-demo", got.ID)
		if e != nil || asset != item.asset {
			t.Fatal("legacy asset changed", e)
		}
	}
	result, e := s.ReadResult("personal-demo", real.ID)
	if e != nil || !bytes.Equal(result.Data, png.Data) {
		t.Fatal("legacy PNG changed", e)
	}
	result, e = s.ReadResult("personal-demo", mock.ID)
	if e != nil || string(result.Data) != `<svg width="800" height="800">legacy</svg>` {
		t.Fatal("legacy SVG changed", e)
	}
	migrated, _ := os.ReadFile(path)
	var state map[string]any
	if e = json.Unmarshal(migrated, &state); e != nil {
		t.Fatal(e)
	}
	if state["version"] != float64(3) {
		t.Fatal("snapshot not migrated", state["version"])
	}
	for _, field := range []string{"projects", "script_generations", "project_receipts"} {
		value, ok := state[field].(map[string]any)
		if !ok || len(value) != 0 {
			t.Fatal("migration map invalid", field)
		}
	}
}
func TestProjectSnapshotV2MissingFieldsNeverResetsStore(t *testing.T) {
	for _, field := range []string{"tasks", "assets", "projects", "script_generations", "project_receipts"} {
		for _, missing := range []bool{true, false} {
			t.Run(field+map[bool]string{true: "-missing", false: "-null"}[missing], func(t *testing.T) {
				state := map[string]any{"version": 2, "tasks": map[string]any{}, "assets": map[string]any{}, "projects": map[string]any{}, "script_generations": map[string]any{}, "project_receipts": map[string]any{}}
				if missing {
					delete(state, field)
				} else {
					state[field] = nil
				}
				original := []byte(projectJSON(t, state))
				dir := t.TempDir()
				path := filepath.Join(dir, "state.json")
				if e := os.WriteFile(path, original, 0600); e != nil {
					t.Fatal(e)
				}
				s, e := OpenStore(dir)
				if e == nil {
					s.Close()
					t.Fatal("invalid v2 accepted")
				}
				after, e := os.ReadFile(path)
				if e != nil || !bytes.Equal(original, after) {
					t.Fatal("invalid v2 evidence overwritten", e)
				}
			})
		}
	}
}
func TestProjectGenerationAndSaveReceiptsSurviveRestart(t *testing.T) {
	dir := t.TempDir()
	s, e := OpenStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	p := createProject(t, s)
	g, _, e := s.CreateScriptGeneration("personal-demo", p.ID, "restart-generation", ScriptGenerationInput{ExpectedVersion: 1})
	if e != nil {
		t.Fatal(e)
	}
	running, ok, e := s.NextScriptGeneration()
	if e != nil || !ok || running.ID != g.ID {
		t.Fatal("did not dispatch generation", e)
	}
	s.Close()
	s, e = OpenStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	replay, created, e := s.CreateScriptGeneration("personal-demo", p.ID, "restart-generation", ScriptGenerationInput{ExpectedVersion: 1})
	if e != nil || created || replay.ID != g.ID || replay.Status != "queued" || replay.SourceBrief != p.Brief {
		t.Fatal("generation restart/replay", e, replay)
	}
	p, _ = s.GetProject("personal-demo", p.ID)
	if p.Version != 1 || p.Script != nil {
		t.Fatal("running changed content")
	}
	pipeline, e := NewScriptPipeline(context.Background(), MockScriptProvider{})
	if e != nil {
		t.Fatal(e)
	}
	result, e := pipeline.Invoke(context.Background(), ProjectInput{Title: replay.SourceTitle, Brief: replay.SourceBrief})
	if e != nil {
		t.Fatal(e)
	}
	if e = s.FinishScriptGeneration(replay, result, nil); e != nil {
		t.Fatal(e)
	}
	p, _ = s.GetProject("personal-demo", p.ID)
	savedInput := ScriptSaveInput{Script: *p.Script, ExpectedVersion: 2}
	savedInput.Script.Scenes[0].Action = "重启后依然存在的人工编辑。"
	saved, e := s.SaveProjectScript("personal-demo", p.ID, "restart-save-key", savedInput)
	if e != nil || saved.Version != 3 {
		t.Fatal(e)
	}
	s.Close()
	s, e = OpenStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	again, e := s.SaveProjectScript("personal-demo", p.ID, "restart-save-key", savedInput)
	if e != nil || again.Version != 3 || !reflect.DeepEqual(again.Script, saved.Script) {
		t.Fatal("save receipt not persistent", e)
	}
	gens, e := s.ListScriptGenerations("personal-demo", p.ID)
	if e != nil || len(gens) != 1 || gens[0].Status != "succeeded" {
		t.Fatal("generation history lost", e)
	}
	againCreated, created, e := s.CreateProject("personal-demo", "project-fixture-key", projectInput())
	if e != nil || created || againCreated.ID != p.ID || againCreated.Version != 3 {
		t.Fatal("project receipt not persistent")
	}
}
func TestProjectReceiptDomainAndSpaceIsolation(t *testing.T) {
	s := testStore(t)
	p := createProject(t, s)
	other, _, e := s.CreateProject("personal-demo", "other-project-key", ProjectInput{Title: "第二项目", Brief: "第二个项目的创作需求。"})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.UpdateProject("personal-demo", other.ID, "project-fixture-key", ProjectUpdate{Title: other.Title, Brief: other.Brief, ExpectedVersion: 1}); e == nil {
		t.Fatal("project domain key reused across project/operation")
	}
	if _, created, e := s.CreateProject("team-demo", "project-fixture-key", projectInput()); e != nil || !created {
		t.Fatal("same key in different space rejected", e)
	}
	if _, created, e := s.Create("personal-demo", "project-fixture-key", Input{Prompt: "独立图像域", Kind: "general"}); e != nil || !created {
		t.Fatal("project key collided with image domain", e)
	}
	if _, e = s.GetProject("team-demo", p.ID); e != ErrNotFound {
		t.Fatal("cross-space project read", e)
	}
	if _, e = s.ListScriptGenerations("team-demo", p.ID); e != ErrNotFound {
		t.Fatal("cross-space generation list", e)
	}
}
