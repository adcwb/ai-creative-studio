package application

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/adcwb/ai-creative-studio/backend/internal/persistence/storyrepo"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/story"
)

func writingFixture(t *testing.T, s *FileStore, endpoint string) (story.ContextSnapshot, ModelRequest, story.Version) {
	t.Helper()
	ctx := context.Background()
	snapshot, model := checkFixture(t, s, endpoint)
	if _, e := s.runtime.auth.models.saveFeatureBinding(checkActor.Space, checkActor.User, 0, FeatureBinding{FeatureKey: "script_generation", ModelConfigID: model.ModelConfigID, Enabled: true}); e != nil {
		t.Fatal(e)
	}
	model.selection = cloneRuntimeSelection(model.selection)
	model.selection.FeatureKey = "script_generation"
	dir, e := s.series.Context(ctx, checkActor.Space, snapshot.EpisodeID)
	if e != nil {
		t.Fatal(e)
	}
	in := story.Input{Title: "本集明确节拍", Kind: "outline", Status: "active", Body: story.Body{Outline: &story.Outline{SeasonID: dir.Season.ID, EpisodeID: dir.Episode.ID, Goal: "保持观众与人物不同的知识边界", Opening: "门前的迟疑", Conflict: "误认来客", Turn: "选择暂不揭示", Hook: "门后传来声响"}}}
	id := story.OutlineID(dir.Series.ID, dir.Season.ID, dir.Episode.ID)
	v, e := s.SaveStory(ctx, checkActor, dir.Series.ID, id, "writing-beat-initial", in)
	if e != nil {
		t.Fatal(e)
	}
	_, e = s.SaveStory(ctx, checkActor, dir.Series.ID, story.OutlineID(dir.Series.ID, dir.Season.ID, ""), "writing-season-initial", story.Input{Title: "本季主线", Kind: "outline", Status: "active", Body: story.Body{Outline: &story.Outline{SeasonID: dir.Season.ID, Goal: "从误认走向独立判断"}}})
	if e != nil {
		t.Fatal(e)
	}
	snapshot, e = s.CreateStoryContext(ctx, checkActor, dir.Episode.ID, "writing-context-initial", story.Selection{})
	if e != nil {
		t.Fatal(e)
	}
	return snapshot, model, v
}
func TestWritingContextActualGatewaySourcesAndExplicitCurrentAdoption(t *testing.T) {
	for _, storage := range []string{"file", "postgres"} {
		t.Run(storage, func(t *testing.T) {
			var calls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				var b struct{ Messages []struct{ Content string } }
				if json.NewDecoder(r.Body).Decode(&b) != nil || len(b.Messages) != 2 {
					t.Fatal("bad text envelope")
				}
				raw := b.Messages[1].Content
				if len(raw) < 64<<10 || !strings.Contains(raw, "本集明确节拍") || !strings.Contains(raw, "本季主线") || !strings.Contains(raw, "选择暂不揭示") || strings.Contains(raw, "FUTURE_PRIVATE_MATERIAL") {
					t.Error("context truncated or wrong narrative scope")
				}
				var payload scriptWritingPayload
				if json.Unmarshal([]byte(raw), &payload) != nil || payload.Script == nil || payload.EpisodeID == "" {
					t.Error("missing original script/episode source")
				}
				out, _ := json.Marshal(scriptFixture(t))
				_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]string{"content": string(out)}}}})
			}))
			defer upstream.Close()
			u, _ := url.Parse(upstream.URL)
			t.Setenv("MODEL_GATEWAY_ALLOWED_HOSTS", u.Host)
			s := testStore(t)
			if storage == "postgres" {
				s = postgresTestStore(t, postgresTestDB(t))
			}
			snapshot, model, beat := writingFixture(t, s, upstream.URL+"/v1")
			before, _ := s.GetProject(checkActor.Space, snapshot.EpisodeID)
			in := ScriptGenerationInput{ExpectedVersion: before.Version, CandidateOnly: true, ContextID: snapshot.ID, ModelRequest: model}
			g, _, e := s.CreateScriptGeneration(checkActor.Space, before.ID, "writing-generate-1", in)
			if e != nil {
				t.Fatal(e)
			}
			out, e := s.managedScript(context.Background(), g, nil)
			if e != nil {
				t.Fatal(e)
			}
			if e = s.FinishScriptGeneration(g, out, nil); e != nil {
				t.Fatal(e)
			}
			after, _ := s.GetProject(checkActor.Space, before.ID)
			if calls.Load() != 1 || after.Version != before.Version || !reflect.DeepEqual(after.Script, before.Script) {
				t.Fatal("candidate altered official content")
			}
			// Source changed while another accepted attempt waits: no new HTTP call.
			queued, _, e := s.CreateScriptGeneration(checkActor.Space, before.ID, "writing-generate-stale", in)
			if e != nil {
				t.Fatal(e)
			}
			change := story.Input{ExpectedVersion: beat.Number, Title: beat.Title, Kind: beat.Kind, Status: "active", Body: beat.Body}
			change.Body.Outline.Hook = "作者改变了本集结尾"
			beat, e = s.SaveStory(context.Background(), checkActor, beat.SeriesID, beat.ID, "writing-beat-change", change)
			if e != nil {
				t.Fatal(e)
			}
			_, e = s.managedScript(context.Background(), queued, nil)
			var pe *ProviderError
			if !errors.As(e, &pe) || pe.Code != "writing_context_changed" || calls.Load() != 1 {
				t.Fatal("stale source reached upstream", e, calls.Load())
			}
			if e = s.FinishScriptGeneration(queued, Script{}, e); e != nil {
				t.Fatal(e)
			}
			decision := ScriptAdoptionInput{ExpectedVersion: before.Version, ReviewContextID: snapshot.ID, ReviewedChanges: true}
			if _, e = s.AdoptScriptCandidate(before.SpaceID, before.ID, g.ID, "writing-adopt-stale", decision); !errors.Is(e, ErrVersionConflict) {
				t.Fatal("stale review adopted", e)
			}
			current, e := s.CreateStoryContext(context.Background(), checkActor, before.ID, "writing-current-review", snapshot.Context.Selection)
			if e != nil {
				t.Fatal(e)
			}
			decision.ReviewContextID = current.ID
			decision.ReviewedChanges = false
			if _, e = s.AdoptScriptCandidate(before.SpaceID, before.ID, g.ID, "writing-adopt-unack", decision); !errors.Is(e, ErrVersionConflict) {
				t.Fatal("unacknowledged source changes adopted", e)
			}
			decision.ReviewedChanges = true
			adopted, e := s.AdoptScriptCandidate(before.SpaceID, before.ID, g.ID, "writing-adopt-current", decision)
			if e != nil {
				t.Fatal(e)
			}
			if adopted.Version != before.Version+1 || calls.Load() != 1 {
				t.Fatal("adoption was not a local version commit")
			}
			// Exact receipt still resolves after its review context ceases to be current.
			replay, e := s.AdoptScriptCandidate(before.SpaceID, before.ID, g.ID, "writing-adopt-current", decision)
			if e != nil || replay.Version != adopted.Version {
				t.Fatal("original decision lost", e)
			}
			detail, _ := s.GetScriptGeneration(before.SpaceID, before.ID, g.ID)
			if detail.ContextSHA256 != snapshot.SHA256 || detail.Adoption.ReviewContextID != current.ID {
				t.Fatal("source/review not pinned")
			}
			if _, _, e = s.CreateScriptGeneration("other-space", before.ID, "writing-cross-scope", in); !errors.Is(e, ErrNotFound) {
				t.Fatal("cross space context", e)
			}
		})
	}
}

func TestWritingNativeCountUsesFullContextAndRechecksSources(t *testing.T) {
	for _, change := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "changed-during-count"}[change], func(t *testing.T) {
			var counts, generated atomic.Int32
			afterCount := make(chan func(), 1)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request map[string]any
				if json.NewDecoder(r.Body).Decode(&request) != nil {
					t.Error("bad request")
					w.WriteHeader(400)
					return
				}
				raw, _ := json.Marshal(request)
				if !strings.Contains(string(raw), "选择暂不揭示") || len(raw) < 64<<10 {
					t.Error("native count/generation omitted full writing material")
				}
				if r.URL.Path == "/v1/responses/input_tokens" {
					counts.Add(1)
					(<-afterCount)()
					_ = json.NewEncoder(w).Encode(map[string]any{"object": "response.input_tokens", "input_tokens": 700})
					return
				}
				if r.URL.Path != "/v1/responses" {
					t.Error("wrong native protocol", r.URL.Path)
					w.WriteHeader(400)
					return
				}
				generated.Add(1)
				script, _ := json.Marshal(scriptFixture(t))
				_ = json.NewEncoder(w).Encode(map[string]any{"object": "response", "status": "completed", "error": nil, "output": []any{map[string]any{"type": "message", "role": "assistant", "status": "completed", "content": []any{map[string]string{"type": "output_text", "text": string(script)}}}}})
			}))
			defer upstream.Close()
			u, _ := url.Parse(upstream.URL)
			t.Setenv("MODEL_GATEWAY_ALLOWED_HOSTS", u.Host)
			s := testStore(t)
			snapshot, model, beat := writingFixture(t, s, upstream.URL+"/v1")
			store := s.runtime.auth.models
			store.mu.Lock()
			m := store.data.Tenants[checkActor.Space].Models[model.ModelConfigID]
			store.mu.Unlock()
			m.TextProtocol = "responses"
			m.NativeInputCounting = true
			m.ContextWindowTokens = 4096
			report, e := store.saveGatewayModel(checkActor.Space, m.Version, m)
			if e != nil {
				t.Fatal(e)
			}
			m = report.Models[0]
			selection, entry, e := s.runtime.Resolve(context.Background(), checkActor.Space, checkActor.User, m.ID, "text")
			if e != nil {
				t.Fatal(e)
			}
			selection.FeatureKey = "script_generation"
			model = ModelRequest{ModelConfigID: m.ID, ModelConfigVersion: m.Version, selection: &selection, selectedCaps: Capabilities{Provider: entry.Provider, Model: entry.Model, Mode: "real", Ready: true}}
			afterCount <- func() {
				if change {
					in := story.Input{ExpectedVersion: beat.Number, Title: beat.Title, Kind: beat.Kind, Status: "active", Body: beat.Body}
					in.Body.Outline.Turn = "计数时刚更新的人物选择"
					if _, e := s.SaveStory(context.Background(), checkActor, beat.SeriesID, beat.ID, "writing-native-change", in); e != nil {
						t.Error(e)
					}
				}
			}
			g, _, e := s.CreateScriptGeneration(checkActor.Space, snapshot.EpisodeID, "writing-native-generate", ScriptGenerationInput{ExpectedVersion: snapshot.ProjectVersion, CandidateOnly: true, ContextID: snapshot.ID, ModelRequest: model})
			if e != nil {
				t.Fatal(e)
			}
			_, e = s.managedScript(context.Background(), g, nil)
			if change {
				var pe *ProviderError
				if !errors.As(e, &pe) || pe.Code != "writing_context_changed" || generated.Load() != 0 {
					t.Fatal("changed source still generated", e, generated.Load())
				}
			} else if e != nil || generated.Load() != 1 {
				t.Fatal("native writing failed", e, generated.Load())
			}
			if counts.Load() != 1 {
				t.Fatal("native count repeated", counts.Load())
			}
		})
	}
}

func TestPostgresWritingPlansContextAndCandidateOfflineRoundTrip(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	file, e := OpenStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("storage migration must not call providers")
		w.WriteHeader(500)
	}))
	defer endpoint.Close()
	u, _ := url.Parse(endpoint.URL)
	t.Setenv("MODEL_GATEWAY_ALLOWED_HOSTS", u.Host)
	snapshot, model, beat := writingFixture(t, file, endpoint.URL+"/v1")
	in := ScriptGenerationInput{ExpectedVersion: snapshot.ProjectVersion, CandidateOnly: true, ContextID: snapshot.ID, ModelRequest: model}
	g, _, e := file.CreateScriptGeneration(checkActor.Space, snapshot.EpisodeID, "writing-offline-generate", in)
	if e != nil {
		t.Fatal(e)
	}
	if e = file.FinishScriptGeneration(g, scriptFixture(t), nil); e != nil {
		t.Fatal(e)
	}
	decision := ScriptAdoptionInput{ExpectedVersion: snapshot.ProjectVersion, ReviewContextID: snapshot.ID, actor: checkActor.User, Reason: "保留离线迁移前的明确决定"}
	adopted, e := file.AdoptScriptCandidate(checkActor.Space, snapshot.EpisodeID, g.ID, "writing-offline-adopt", decision)
	if e != nil {
		t.Fatal(e)
	}
	original, e := file.GetScriptGeneration(checkActor.Space, snapshot.EpisodeID, g.ID)
	if e != nil {
		t.Fatal(e)
	}
	if e = file.Close(); e != nil {
		t.Fatal(e)
	}
	before, e := os.ReadFile(filepath.Join(dir, "state.json"))
	if e != nil {
		t.Fatal(e)
	}
	db := postgresTestDB(t)
	if _, e = ImportPostgresData(ctx, dir, db); e != nil {
		t.Fatal(e)
	}
	after, e := os.ReadFile(filepath.Join(dir, "state.json"))
	if e != nil || !bytes.Equal(before, after) {
		t.Fatal("import rewrote original source", e)
	}
	pg := postgresTestStore(t, db)
	got, e := pg.GetScriptGeneration(checkActor.Space, snapshot.EpisodeID, g.ID)
	if e != nil || !reflect.DeepEqual(got, original) {
		t.Fatal("candidate/source/review did not survive import", e)
	}
	pinned, e := pg.writingSnapshot(ctx, checkActor.Space, snapshot.EpisodeID, snapshot.ID)
	if e != nil || !reflect.DeepEqual(pinned, snapshot) {
		t.Fatal("canonical input snapshot did not survive import", e)
	}
	current, e := pg.story.Get(ctx, storyrepo.Scope{Space: checkActor.Space, Series: snapshot.SeriesID}, beat.ID, "")
	if e != nil || !reflect.DeepEqual(current, beat) {
		t.Fatal("plan did not survive import", e)
	}
	target := filepath.Join(t.TempDir(), "restored")
	if _, e = ExportPostgresData(ctx, target, db); e != nil {
		t.Fatal(e)
	}
	restored, e := OpenStore(target)
	if e != nil {
		t.Fatal(e)
	}
	defer restored.Close()
	got, e = restored.GetScriptGeneration(checkActor.Space, snapshot.EpisodeID, g.ID)
	if e != nil || !reflect.DeepEqual(got, original) {
		t.Fatal("candidate/source/review did not survive export", e)
	}
	replay, e := restored.AdoptScriptCandidate(checkActor.Space, snapshot.EpisodeID, g.ID, "writing-offline-adopt", decision)
	if e != nil || replay.Version != adopted.Version {
		t.Fatal("restored decision re-applied", e)
	}
}
