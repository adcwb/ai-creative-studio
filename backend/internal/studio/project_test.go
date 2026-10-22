package studio

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func scriptFixture(t *testing.T) Script {
	t.Helper()
	var result Script
	if e := json.Unmarshal([]byte(projectJSON(t, projectScriptFixture())), &result); e != nil {
		t.Fatal(e)
	}
	return result
}
func projectInput() ProjectInput {
	return ProjectInput{Title: "车站来信", Brief: "修钟师与快递员合作，把迟到的信送达。"}
}
func createProject(t *testing.T, s *FileStore) Project {
	t.Helper()
	p, created, e := s.CreateProject("personal-demo", "project-fixture-key", projectInput())
	if e != nil || !created {
		t.Fatal("create project", e)
	}
	return p
}
func makeScript(t *testing.T, s *FileStore, p Project) Project {
	t.Helper()
	g, created, e := s.CreateScriptGeneration("personal-demo", p.ID, "generation-fixture-key", ScriptGenerationInput{ExpectedVersion: p.Version})
	if e != nil || !created {
		t.Fatal(e)
	}
	if e = s.FinishScriptGeneration(g, scriptFixture(t), nil); e != nil {
		t.Fatal(e)
	}
	p, e = s.GetProject("personal-demo", p.ID)
	if e != nil {
		t.Fatal(e)
	}
	return p
}

type recordingScriptProvider struct {
	input  ProjectInput
	calls  int
	result Script
}

func (p *recordingScriptProvider) Generate(_ context.Context, in ProjectInput) (Script, error) {
	p.calls++
	p.input = in
	return p.result, nil
}
func TestScriptEinoNormalizesInputsAndValidatesStructuredResults(t *testing.T) {
	provider := &recordingScriptProvider{result: scriptFixture(t)}
	pipeline, e := NewScriptPipeline(context.Background(), provider)
	if e != nil {
		t.Fatal(e)
	}
	result, e := pipeline.Invoke(context.Background(), ProjectInput{Title: "  车站来信 ", Brief: "  修钟师与快递员合作，把迟到的信送达。  "})
	if e != nil || provider.calls != 1 || provider.input != projectInput() || result.Title != "车站来信" {
		t.Fatal("Eino did not execute normalized provider", e, provider)
	}
	if _, e = pipeline.Invoke(context.Background(), ProjectInput{Title: "", Brief: "valid brief"}); e == nil || provider.calls != 1 {
		t.Fatal("invalid input reached provider")
	}
	provider.result.Characters[1].Name = provider.result.Characters[0].Name
	if _, e = pipeline.Invoke(context.Background(), projectInput()); e == nil {
		t.Fatal("duplicate character names accepted from provider")
	}
	mock, e := NewScriptPipeline(context.Background(), MockScriptProvider{})
	if e != nil {
		t.Fatal(e)
	}
	output, e := mock.Invoke(context.Background(), projectInput())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = NormalizeScript(output); e != nil {
		t.Fatal("mock structured output invalid", e)
	}
	if _, e = mock.Invoke(context.Background(), ProjectInput{Title: "失败样本", Brief: "[mock-fail] 合成失败路径"}); e == nil {
		t.Fatal("mock failure silently succeeded")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e = mock.Invoke(ctx, projectInput()); e == nil {
		t.Fatal("cancelled invocation succeeded")
	}
}
func TestScriptContractRejectsInvalidStructureAndUnicodeBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*Script)
	}{
		{"missing-character", func(s *Script) { s.Characters = s.Characters[:1] }}, {"extra-character", func(s *Script) { s.Characters = append(s.Characters, s.Characters[0]) }}, {"duplicate-trimmed-name", func(s *Script) { s.Characters[1].Name = " " + s.Characters[0].Name + " " }}, {"unknown-speaker", func(s *Script) { s.Scenes[0].Dialogue[0].Character = "不存在" }}, {"missing-scene", func(s *Script) { s.Scenes = s.Scenes[:2] }}, {"extra-scene", func(s *Script) { s.Scenes = append(s.Scenes, s.Scenes[0]) }}, {"empty-dialogue", func(s *Script) { s.Scenes[0].Dialogue = nil }}, {"too-many-dialogues", func(s *Script) { s.Scenes[0].Dialogue = make([]ScriptDialogue, 7) }}, {"title-too-long", func(s *Script) { s.Title = strings.Repeat("😀", 81) }}, {"logline-too-short", func(s *Script) { s.Logline = "😀😀" }}, {"description-too-long", func(s *Script) { s.Characters[0].Description = strings.Repeat("😀", 201) }}, {"heading-too-long", func(s *Script) { s.Scenes[0].Heading = strings.Repeat("😀", 101) }}, {"action-too-long", func(s *Script) { s.Scenes[0].Action = strings.Repeat("😀", 1001) }}, {"dialogue-too-long", func(s *Script) { s.Scenes[0].Dialogue[0].Text = strings.Repeat("😀", 301) }}} {
		t.Run(tc.name, func(t *testing.T) {
			s := scriptFixture(t)
			tc.mutate(&s)
			if _, e := NormalizeScript(s); e == nil {
				t.Fatal("invalid script accepted")
			}
		})
	}
	s := scriptFixture(t)
	s.Title = "  " + strings.Repeat("😀", 80) + "  "
	s.Logline = strings.Repeat("😀", 300)
	s.Characters[0].Description = strings.Repeat("😀", 200)
	s.Scenes[0].Heading = strings.Repeat("😀", 100)
	s.Scenes[0].Action = strings.Repeat("😀", 1000)
	s.Scenes[0].Dialogue[0].Text = strings.Repeat("😀", 300)
	normalized, e := NormalizeScript(s)
	if e != nil || strings.HasPrefix(normalized.Title, " ") {
		t.Fatal("valid Unicode boundary rejected", e)
	}
	in, e := NormalizeProjectInput(ProjectInput{Title: strings.Repeat("😀", 80), Brief: strings.Repeat("😀", 2000)})
	if e != nil || len([]rune(in.Brief)) != 2000 {
		t.Fatal("project Unicode limit", e)
	}
}
func TestProjectReceiptsReplayBeforeVersionAndActiveChecks(t *testing.T) {
	s := testStore(t)
	p := createProject(t, s)
	updated, e := s.UpdateProject("personal-demo", p.ID, "update-project-key", ProjectUpdate{Title: "新的名称", Brief: p.Brief, ExpectedVersion: 1})
	if e != nil || updated.Version != 2 {
		t.Fatal(e, updated)
	}
	oldCreate, created, e := s.CreateProject("personal-demo", "project-fixture-key", projectInput())
	if e != nil || created || oldCreate.Version != 2 || oldCreate.Title != updated.Title {
		t.Fatal("create receipt not current projection")
	}
	g, created, e := s.CreateScriptGeneration("personal-demo", p.ID, "generate-project-key", ScriptGenerationInput{ExpectedVersion: 2})
	if e != nil || !created || g.BaseVersion != 2 || g.SourceTitle != updated.Title {
		t.Fatal("generation binding", g, e)
	}
	projectErrorCode(t, projectHTTP(t, s, "PUT", "/projects/"+p.ID+"?space_id=personal-demo", "active-update-key", `{"title":"覆盖","brief":"活跃期间覆盖尝试","expected_version":2}`, 409), "generation_active")
	projectErrorCode(t, projectHTTP(t, s, "POST", "/projects/"+p.ID+"/generations?space_id=personal-demo", "active-generate-key", `{"expected_version":2}`, 409), "generation_active")
	replay, e := s.UpdateProject("personal-demo", p.ID, "update-project-key", ProjectUpdate{Title: updated.Title, Brief: p.Brief, ExpectedVersion: 1})
	if e != nil || replay.Version != 2 {
		t.Fatal("accepted receipt blocked by active/version", e)
	}
	if e = s.FinishScriptGeneration(g, scriptFixture(t), nil); e != nil {
		t.Fatal(e)
	}
	p, _ = s.GetProject("personal-demo", p.ID)
	if p.Version != 3 || p.ScriptBrief != g.SourceBrief || p.Script == nil {
		t.Fatal("generation did not publish once", p)
	}
	if e = s.FinishScriptGeneration(g, scriptFixture(t), nil); e == nil {
		t.Fatal("terminal generation published twice")
	}
	savedInput := ScriptSaveInput{Script: scriptFixture(t), ExpectedVersion: 3}
	savedInput.Script.Logline = "明确修改后的剧本梗概。"
	saved, e := s.SaveProjectScript("personal-demo", p.ID, "save-project-key", savedInput)
	if e != nil || saved.Version != 4 {
		t.Fatal(e, saved)
	}
	newest, e := s.UpdateProject("personal-demo", p.ID, "newest-project-key", ProjectUpdate{Title: p.Title, Brief: "改过的创作需求不会改变原剧本来源。", ExpectedVersion: 4})
	if e != nil || newest.Version != 5 {
		t.Fatal(e)
	}
	replay, e = s.SaveProjectScript("personal-demo", p.ID, "save-project-key", savedInput)
	if e != nil || replay.Version != 5 || replay.Brief != newest.Brief || replay.ScriptBrief != g.SourceBrief {
		t.Fatal("save response-loss replay overwrote new content", e)
	}
	again, created, e := s.CreateScriptGeneration("personal-demo", p.ID, "generate-project-key", ScriptGenerationInput{ExpectedVersion: 2})
	if e != nil || created || again.ID != g.ID || again.Status != "succeeded" {
		t.Fatal("generation receipt lost terminal state")
	}
	projectErrorCode(t, projectHTTP(t, s, "PUT", "/projects/"+p.ID+"?space_id=personal-demo", "stale-version-key", `{"title":"陈旧名称","brief":"不应覆盖新内容","expected_version":4}`, 409), "version_conflict")
	projectErrorCode(t, projectHTTP(t, s, "PUT", "/projects/"+p.ID+"?space_id=personal-demo", "save-project-key", `{"title":"键跨操作","brief":"禁止跨操作复用","expected_version":5}`, 409), "idempotency_conflict")
}
func TestProjectConcurrentWritesAndGenerationAreAtomic(t *testing.T) {
	s := testStore(t)
	p := createProject(t, s)
	var success atomic.Int32
	var wg sync.WaitGroup
	for i := range 12 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, e := s.UpdateProject("personal-demo", p.ID, "concurrent-update-"+string(rune('a'+i)), ProjectUpdate{Title: "并发编辑", Brief: p.Brief, ExpectedVersion: 1})
			if e == nil {
				success.Add(1)
			}
		}(i)
	}
	wg.Wait()
	if success.Load() != 1 {
		t.Fatal("version check not atomic", success.Load())
	}
	p, _ = s.GetProject("personal-demo", p.ID)
	if p.Version != 2 {
		t.Fatal(p.Version)
	}
	ids := make(chan string, 12)
	createdCount := atomic.Int32{}
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			g, created, e := s.CreateScriptGeneration("personal-demo", p.ID, "concurrent-generation-key", ScriptGenerationInput{ExpectedVersion: 2})
			if e != nil {
				t.Error(e)
				return
			}
			if created {
				createdCount.Add(1)
			}
			ids <- g.ID
		}()
	}
	wg.Wait()
	close(ids)
	var first string
	for id := range ids {
		if first == "" {
			first = id
		}
		if first != id {
			t.Fatal("duplicate generations")
		}
	}
	if createdCount.Load() != 1 {
		t.Fatal("generation accepted more than once")
	}
}
func TestProjectDeepCopiesAndCommitFailurePreserveSavedScript(t *testing.T) {
	dir := t.TempDir()
	s, e := OpenStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	p := makeScript(t, s, createProject(t, s))
	p.Script.Characters[0].Name = "内存篡改"
	p.Script.Scenes[0].Dialogue[0].Text = "内存篡改"
	listed := s.ListProjects("personal-demo")
	listed[0].Script.Scenes[1].Action = "列表篡改"
	fresh, _ := s.GetProject("personal-demo", p.ID)
	if fresh.Script.Characters[0].Name == "内存篡改" || fresh.Script.Scenes[0].Dialogue[0].Text == "内存篡改" || fresh.Script.Scenes[1].Action == "列表篡改" {
		t.Fatal("read leaked mutable store slices")
	}
	before, e := os.ReadFile(filepath.Join(dir, "state.json"))
	if e != nil {
		t.Fatal(e)
	}
	oldPath := s.path
	s.path = filepath.Join(dir, "missing", "state.json")
	edited := scriptFixture(t)
	edited.Scenes[0].Dialogue[0].Text = "保存失败不能污染旧稿"
	_, e = s.SaveProjectScript("personal-demo", p.ID, "failed-commit-save", ScriptSaveInput{Script: edited, ExpectedVersion: fresh.Version})
	if e == nil {
		t.Fatal("commit failure hidden")
	}
	after, _ := os.ReadFile(oldPath)
	unchanged, _ := s.GetProject("personal-demo", p.ID)
	if !bytes.Equal(before, after) || unchanged.Version != fresh.Version || !reflect.DeepEqual(unchanged.Script, fresh.Script) {
		t.Fatal("failed commit published nested content")
	}
	s.path = oldPath
	saved, e := s.SaveProjectScript("personal-demo", p.ID, "failed-commit-save", ScriptSaveInput{Script: edited, ExpectedVersion: fresh.Version})
	if e != nil || saved.Version != fresh.Version+1 {
		t.Fatal("failed commit consumed key", e)
	}
	edited.Scenes[0].Dialogue[0].Text = "caller mutation"
	latest, _ := s.GetProject("personal-demo", p.ID)
	if latest.Script.Scenes[0].Dialogue[0].Text == "caller mutation" {
		t.Fatal("saved input aliases store")
	}
}
func TestScriptWorkerFailureRetainsPreviousScriptAndVersion(t *testing.T) {
	s := testStore(t)
	p := makeScript(t, s, createProject(t, s))
	original := p.Script
	source := p.ScriptBrief
	p, e := s.UpdateProject("personal-demo", p.ID, "fail-brief-key", ProjectUpdate{Title: p.Title, Brief: "[mock-fail] 合成生成失败，旧稿保留", ExpectedVersion: p.Version})
	if e != nil {
		t.Fatal(e)
	}
	g, _, e := s.CreateScriptGeneration("personal-demo", p.ID, "failure-generation-key", ScriptGenerationInput{ExpectedVersion: p.Version})
	if e != nil {
		t.Fatal(e)
	}
	pipeline, e := NewScriptPipeline(context.Background(), MockScriptProvider{})
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- RunScriptWorker(ctx, s, pipeline) }()
	deadline := time.Now().Add(3 * time.Second)
	var got Project
	for time.Now().Before(deadline) {
		got, _ = s.GetProject("personal-demo", p.ID)
		if got.LatestGeneration != nil && got.LatestGeneration.Status == "failed" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	if e = <-done; e != nil && !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	if got.Version != p.Version || !reflect.DeepEqual(got.Script, original) || got.ScriptBrief != source || got.LatestGeneration.ID != g.ID || got.LatestGeneration.ErrorCode != "mock_script_failed" {
		t.Fatal("failure changed previous script/version or lacked error", got)
	}
}
