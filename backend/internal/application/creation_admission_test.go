package application

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestSelectedScriptModelQueuesBeyondOldGlobalHourlyQuota(t *testing.T) {
	s := testStore(t)
	for i := range 21 {
		p, _, err := s.CreateProject("personal-demo", fmt.Sprintf("script-queue-project-%d", i), ProjectInput{Title: "独立剧本草稿", Brief: "仅验证持久接单，不启动或调用模型"})
		if err != nil {
			t.Fatal(err)
		}
		input := ScriptGenerationInput{ExpectedVersion: p.Version, ModelRequest: ModelRequest{
			selection:    &RuntimeSelection{TenantID: p.SpaceID, ActorID: "synthetic-actor", ModelID: "synthetic-model", Version: 1, Capability: "text"},
			selectedCaps: Capabilities{Provider: "gateway-text", Model: "synthetic-model", Mode: "real", Ready: true},
		}}
		g, created, err := s.CreateScriptGeneration(p.SpaceID, p.ID, fmt.Sprintf("script-queue-%d", i), input)
		if err != nil || !created || g.Status != "queued" {
			t.Fatal("historical/global count blocked configured script model", i, g, err)
		}
		replay, created, err := s.CreateScriptGeneration(p.SpaceID, p.ID, fmt.Sprintf("script-queue-%d", i), input)
		if err != nil || created || replay.ID != g.ID {
			t.Fatal("queued replay changed identity", replay, err)
		}
	}
}

func TestModelCallHistoryDoesNotBecomeAnExecutionQuota(t *testing.T) {
	dir := t.TempDir()
	s, err := openModelUsageStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	for i := range 10000 {
		id := fmt.Sprintf("history-%d", i)
		s.data.Calls[id] = ModelCall{ID: id, Tenant: "tenant", GatewayID: "gateway", State: "failed", StartedAt: time.Now().UTC()}
	}
	next := ModelCall{ID: "next-call", Tenant: "tenant", GatewayID: "gateway", State: "running", StartedAt: time.Now().UTC()}
	if err = s.save(next); err != nil {
		t.Fatal("record count blocked a new dispatch observation", err)
	}
	reopened, err := openModelUsageStore(dir)
	if err != nil || len(reopened.data.Calls) != 10001 {
		t.Fatal("history was discarded to free an artificial quota", err)
	}
	if reopened.data.Calls[next.ID].State != "running" {
		t.Fatal("latest dispatch observation missing")
	}
}

func TestSceneReferenceStillRequiresAnExistingSourceScene(t *testing.T) {
	s := testStore(t)
	p := boardProject(t, s)
	shots := boardFixture(t)
	shots[0].SceneIndex = len(p.Script.Scenes) + 1
	_, err := s.SaveProjectStoryboard(p.SpaceID, p.ID, "invalid-source-scene", StoryboardSaveInput{ExpectedVersion: p.Version, Shots: shots})
	if !errors.Is(err, ErrInvalidProjectInput) {
		t.Fatal("removing numeric cardinality limits removed relational validation", err)
	}
}
