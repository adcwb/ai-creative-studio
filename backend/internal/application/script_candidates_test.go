package application

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adcwb/ai-creative-studio/backend/internal/platform/jobqueue"
)

func candidateContract(t *testing.T, s *FileStore) {
	t.Helper()
	p := makeScript(t, s, createProject(t, s))
	original := cloneProject(p)
	in := ScriptGenerationInput{ExpectedVersion: p.Version, CandidateOnly: true}
	g, created, e := s.CreateScriptGeneration(p.SpaceID, p.ID, "candidate-create", in)
	if e != nil || !created || g.SourceScript == nil {
		t.Fatal("create", e)
	}
	g.SourceScript.Title = "mutated returned source"
	output := scriptFixture(t)
	output.Title = "独立的第二稿"
	if e = s.FinishScriptGeneration(g, output, nil); e != nil {
		t.Fatal(e)
	}
	p, e = s.GetProject(p.SpaceID, p.ID)
	if e != nil || p.Version != original.Version || p.ScriptVersion != original.ScriptVersion || !reflect.DeepEqual(p.Script, original.Script) {
		t.Fatal("candidate replaced official draft", e)
	}
	full, e := s.GetScriptGeneration(p.SpaceID, p.ID, g.ID)
	if e != nil || full.Candidate == nil || full.SourceScript.Title != original.Script.Title || full.Candidate.Title != output.Title {
		t.Fatal("candidate/source not saved", e, full)
	}
	full.Candidate.Scenes[0].Action = "mutated returned result"
	items, _ := s.ListScriptGenerations(p.SpaceID, p.ID)
	if items[0].Candidate != nil || items[0].SourceScript != nil || p.LatestGeneration.Candidate != nil {
		t.Fatal("full scripts leaked into polling")
	}
	// A later manual draft is never overwritten merely because a candidate exists.
	manual := cloneScript(*p.Script)
	manual.Title = "作者后来写的正式稿"
	p, e = s.SaveProjectScript(p.SpaceID, p.ID, "candidate-newer-draft", ScriptSaveInput{ExpectedVersion: p.Version, Script: manual})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.AdoptScriptCandidate(p.SpaceID, p.ID, g.ID, "candidate-no-review", ScriptAdoptionInput{ExpectedVersion: p.Version}); !errors.Is(e, ErrVersionConflict) {
		t.Fatal("source change not reviewed", e)
	}
	if _, e = s.AdoptScriptCandidate(p.SpaceID, p.ID, g.ID, "candidate-stale-version", ScriptAdoptionInput{ExpectedVersion: original.Version, ReviewedChanges: true}); !errors.Is(e, ErrVersionConflict) {
		t.Fatal("CAS bypass", e)
	}
	if _, e = s.GetScriptGeneration("other-space", p.ID, g.ID); !errors.Is(e, ErrNotFound) {
		t.Fatal("cross space read", e)
	}
	other, _, _ := s.CreateProject(p.SpaceID, "candidate-other-project", projectInput())
	if _, e = s.AdoptScriptCandidate(p.SpaceID, other.ID, g.ID, "candidate-cross-project", ScriptAdoptionInput{ExpectedVersion: other.Version, ReviewedChanges: true}); !errors.Is(e, ErrNotFound) {
		t.Fatal("cross project adoption", e)
	}
	full, _ = s.GetScriptGeneration(p.SpaceID, p.ID, g.ID)
	edited := cloneScript(*full.Candidate)
	edited.Logline = "编剧明确编辑后选择的版本。"
	adopt := ScriptAdoptionInput{ExpectedVersion: p.Version, ReviewedChanges: true, Script: &edited, Reason: "保留迟疑，调整结尾的节奏。"}
	p, e = s.AdoptScriptCandidate(p.SpaceID, p.ID, g.ID, "candidate-adopt", adopt)
	if e != nil || p.Script.Title != output.Title || p.Script.Logline != edited.Logline || p.ScriptVersion != original.ScriptVersion+2 {
		t.Fatal("adoption", e, p)
	}
	full, _ = s.GetScriptGeneration(p.SpaceID, p.ID, g.ID)
	if full.Adoption == nil || !full.Adoption.Edited || full.Adoption.Reason != adopt.Reason || full.Candidate.Logline == edited.Logline || full.SourceScript.Title != original.Script.Title || full.Candidate.Scenes[0].Action == "mutated returned result" {
		t.Fatal("immutable receipt and source", full)
	}
	if _, e = s.AdoptScriptCandidate(p.SpaceID, p.ID, g.ID, "candidate-adopt-again", adopt); !errors.Is(e, ErrConflict) {
		t.Fatal("second decision rewrote draft", e)
	}
	manual = cloneScript(*p.Script)
	manual.Title = "采纳之后又修改的正式稿"
	p, e = s.SaveProjectScript(p.SpaceID, p.ID, "candidate-latest-draft", ScriptSaveInput{ExpectedVersion: p.Version, Script: manual})
	if e != nil {
		t.Fatal(e)
	}
	replayed, e := s.AdoptScriptCandidate(p.SpaceID, p.ID, g.ID, "candidate-adopt", adopt)
	if e != nil || replayed.Version != p.Version || replayed.Script.Title != manual.Title {
		t.Fatal("original decision reapplied over later draft", e)
	}
	replay, created, e := s.CreateScriptGeneration(p.SpaceID, p.ID, "candidate-create", in)
	if e != nil || created || replay.ID != g.ID || replay.Candidate == nil {
		t.Fatal("generation original key", e)
	}
	changed := in
	changed.CandidateOnly = false
	if _, _, e = s.CreateScriptGeneration(p.SpaceID, p.ID, "candidate-create", changed); !errors.Is(e, ErrIdempotencyConflict) {
		t.Fatal("candidate flag missing from request identity", e)
	}
	history, e := s.ProjectHistory(p.SpaceID, p.ID)
	if e != nil || len(history) < 3 || history[1].Script.Title != "作者后来写的正式稿" {
		t.Fatal("old official draft not recoverable", e, history)
	}
}

func TestScriptCandidatesImmutableCompareExplicitAdoption(t *testing.T) {
	candidateContract(t, testStore(t))
}
func TestPostgresScriptCandidatesImmutableCompareExplicitAdoption(t *testing.T) {
	candidateContract(t, postgresTestStore(t, postgresTestDB(t)))
}

func TestScriptCandidateAdoptionHasOneWinnerAndRejectsInvalidOutput(t *testing.T) {
	s := testStore(t)
	p := makeScript(t, s, createProject(t, s))
	g, _, e := s.CreateScriptGeneration(p.SpaceID, p.ID, "candidate-race-create", ScriptGenerationInput{ExpectedVersion: p.Version, CandidateOnly: true})
	if e != nil {
		t.Fatal(e)
	}
	if e = s.FinishScriptGeneration(g, scriptFixture(t), nil); e != nil {
		t.Fatal(e)
	}
	var accepted atomic.Int32
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, e := s.AdoptScriptCandidate(p.SpaceID, p.ID, g.ID, "candidate-race-"+string(rune('a'+i)), ScriptAdoptionInput{ExpectedVersion: p.Version})
			if e == nil {
				accepted.Add(1)
			} else if !errors.Is(e, ErrConflict) && !errors.Is(e, ErrVersionConflict) {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	if accepted.Load() != 1 {
		t.Fatal("multiple adoption winners", accepted.Load())
	}
	p, _ = s.GetProject(p.SpaceID, p.ID)
	g, _, e = s.CreateScriptGeneration(p.SpaceID, p.ID, "candidate-invalid-output", ScriptGenerationInput{ExpectedVersion: p.Version, CandidateOnly: true})
	if e != nil {
		t.Fatal(e)
	}
	bad := scriptFixture(t)
	bad.Characters[1].Name = bad.Characters[0].Name
	if e = s.FinishScriptGeneration(g, bad, nil); e != nil {
		t.Fatal(e)
	}
	failed, _ := s.GetScriptGeneration(p.SpaceID, p.ID, g.ID)
	if failed.Status != "failed" || failed.Candidate != nil {
		t.Fatal("invalid output saved as candidate")
	}
	if _, e = s.AdoptScriptCandidate(p.SpaceID, p.ID, g.ID, "candidate-invalid-adopt", ScriptAdoptionInput{ExpectedVersion: p.Version}); !errors.Is(e, ErrConflict) {
		t.Fatal("invalid adopted", e)
	}
}

func TestScriptCandidateHTTPRolesOwnershipAndOriginalReceipt(t *testing.T) {
	s := testStore(t)
	auth, e := OpenAuthStore(filepath.Join(t.TempDir(), "auth"))
	if e != nil {
		t.Fatal(e)
	}
	a, token, session, e := auth.login("writer-owner@example.invalid", "Synthetic-writer-password!", true)
	if e != nil {
		t.Fatal(e)
	}
	b, otherToken, otherSession, e := auth.login("writer-viewer@example.invalid", "Synthetic-writer-password!", true)
	if e != nil {
		t.Fatal(e)
	}
	space := testTeamSpace(t, auth, a.ID)
	otherSpace := testTeamSpace(t, auth, b.ID)
	p, _, e := s.CreateProject(space, "candidate-http-project", projectInput())
	if e != nil {
		t.Fatal(e)
	}
	in := ScriptGenerationInput{ExpectedVersion: p.Version, CandidateOnly: true}
	g, _, e := s.CreateScriptGeneration(space, p.ID, "candidate-http-generation", in)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.FinishScriptGeneration(g, scriptFixture(t), nil); e != nil {
		t.Fatal(e)
	}
	h := SecureHandler(s, "http://localhost:5173", auth, AuthOptions{AllowRegistration: true})
	path := "/api/v1/projects/" + p.ID + "/generations/" + g.ID
	authRequest(t, h, "GET", path+"?space_id="+space, "", "", "", 401)
	authRequest(t, h, "GET", path+"?space_id="+space, "", otherToken, "", 403)
	authRequest(t, h, "GET", path+"?space_id="+otherSpace, "", otherToken, "", 404)
	members := "/api/v1/organizations/" + space + "/members"
	authRequest(t, h, "PUT", members, testMemberInput(t, auth, "writer-viewer@example.invalid", "viewer"), token, session.CSRF, 200)
	w := authRequest(t, h, "GET", path+"?space_id="+space, "", otherToken, "", 200)
	var detail ScriptGeneration
	_ = json.Unmarshal(w.Body.Bytes(), &detail)
	if detail.Candidate == nil || strings.Contains(w.Body.String(), "runtime_selection") {
		t.Fatal("detail contract or runtime privacy", w.Body.String())
	}
	authRequest(t, h, "POST", path+"/adoptions?space_id="+space, `{"expected_version":1}`, otherToken, otherSession.CSRF, 403)
	authRequest(t, h, "POST", path+"/adoptions?space_id="+space, `{"expected_version":1}`, token, "", 403)
	authRequest(t, h, "POST", path+"/adoptions?space_id="+space, `{"expected_version":1,"script":null}`, token, session.CSRF, 400)
	authRequest(t, h, "POST", path+"/adoptions?space_id="+space, `{"expected_version":1,"actor":"spoof"}`, token, session.CSRF, 400)
	authRequest(t, h, "POST", path+"/adoptions?space_id="+space, `{"expected_version":1}`, token, session.CSRF, 200)
	adopted, _ := s.GetScriptGeneration(space, p.ID, g.ID)
	if adopted.Adoption == nil || adopted.Adoption.Actor != a.ID {
		t.Fatal("decision did not pin authenticated creator")
	}
	// A managed model is not resolved again when checking an accepted candidate.
	request := httptest.NewRequest(http.MethodPost, "/api/v1/projects/"+p.ID+"/gateway-generations?space_id="+space, strings.NewReader(`{"expected_version":1,"candidate_only":true}`))
	request.Header.Set("Origin", "http://localhost:5173")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-CSRF-Token", session.CSRF)
	request.Header.Set("Idempotency-Key", "candidate-http-generation")
	request.AddCookie(&http.Cookie{Name: "studio_session", Value: token})
	response := httptest.NewRecorder()
	h.ServeHTTP(response, request)
	if response.Code != 200 || !strings.Contains(response.Body.String(), g.ID) {
		t.Fatal("accepted receipt incorrectly required a new model", response.Code, response.Body.String())
	}
	authRequest(t, h, "PUT", members, testMemberInput(t, auth, "writer-viewer@example.invalid", "remove"), token, session.CSRF, 200)
	authRequest(t, h, "GET", path+"?space_id="+space, "", otherToken, "", 403)
}

func TestScriptCandidateFileRestartKeepsSourceCandidateAndDecision(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "store")
	s, e := OpenStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	p := makeScript(t, s, createProject(t, s))
	in := ScriptGenerationInput{ExpectedVersion: p.Version, CandidateOnly: true}
	g, _, e := s.CreateScriptGeneration(p.SpaceID, p.ID, "candidate-restart-create", in)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.FinishScriptGeneration(g, scriptFixture(t), nil); e != nil {
		t.Fatal(e)
	}
	s.Close()
	s, e = OpenStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	full, e := s.GetScriptGeneration(p.SpaceID, p.ID, g.ID)
	if e != nil || full.SourceScript == nil || full.Candidate == nil {
		t.Fatal("restart lost candidate", e)
	}
	decision := ScriptAdoptionInput{ExpectedVersion: p.Version, Reason: "明确采用这版"}
	adopted, e := s.AdoptScriptCandidate(p.SpaceID, p.ID, g.ID, "candidate-restart-adopt", decision)
	if e != nil {
		t.Fatal(e)
	}
	s.Close()
	s, e = OpenStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	replay, e := s.AdoptScriptCandidate(p.SpaceID, p.ID, g.ID, "candidate-restart-adopt", decision)
	if e != nil || replay.Version != adopted.Version {
		t.Fatal("restart re-applied adoption", e)
	}
	full, e = s.GetScriptGeneration(p.SpaceID, p.ID, g.ID)
	if e != nil || full.Adoption == nil || full.Adoption.Reason != decision.Reason {
		t.Fatal("restart lost immutable receipt", e)
	}
}

func TestScriptCandidateFailedAdoptionCommitDoesNotConsumeDecision(t *testing.T) {
	s := testStore(t)
	p := makeScript(t, s, createProject(t, s))
	g, _, e := s.CreateScriptGeneration(p.SpaceID, p.ID, "candidate-failure-create", ScriptGenerationInput{ExpectedVersion: p.Version, CandidateOnly: true})
	if e != nil {
		t.Fatal(e)
	}
	if e = s.FinishScriptGeneration(g, scriptFixture(t), nil); e != nil {
		t.Fatal(e)
	}
	in := ScriptAdoptionInput{ExpectedVersion: p.Version, Reason: "只有落盘才算采纳"}
	originalPath := s.path
	s.path = filepath.Join(t.TempDir(), "missing", "state.json")
	if _, e = s.AdoptScriptCandidate(p.SpaceID, p.ID, g.ID, "candidate-failed-write", in); e == nil {
		t.Fatal("write failure hidden")
	}
	s.path = originalPath
	current, e := s.GetProject(p.SpaceID, p.ID)
	if e != nil || current.Version != p.Version || !reflect.DeepEqual(current.Script, p.Script) {
		t.Fatal("failed decision changed draft", e)
	}
	detail, e := s.GetScriptGeneration(p.SpaceID, p.ID, g.ID)
	if e != nil || detail.Adoption != nil {
		t.Fatal("failed decision published receipt", e)
	}
	if _, e = s.AdoptScriptCandidate(p.SpaceID, p.ID, g.ID, "candidate-failed-write", in); e != nil {
		t.Fatal("failed decision consumed original key", e)
	}
}

func TestPostgresScriptCandidateActualGatewayQueueAndSeparateAdoption(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var body struct {
			Model    string
			Messages []struct{ Role, Content string }
		}
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer synthetic-gateway-key" || json.NewDecoder(r.Body).Decode(&body) != nil || body.Model != "vendor/shared-model" || len(body.Messages) != 2 || !strings.Contains(body.Messages[1].Content, "候选不能替换正式稿") {
			t.Error("wrong actual request", r.URL.Path, body)
		}
		raw, _ := json.Marshal(scriptFixture(t))
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]string{"content": string(raw)}}}})
	}))
	defer upstream.Close()
	u, _ := url.Parse(upstream.URL)
	t.Setenv("MODEL_GATEWAY_ALLOWED_HOSTS", u.Host)
	runtime, _, m := gatewayFixture(t, upstream.URL+"/v1")
	m.Currency = ""
	m.MaximumMinor = 0
	m.PriceSource = ""
	report, e := runtime.auth.models.saveGatewayModel("tenant-a", m.Version, m)
	if e != nil {
		t.Fatal(e)
	}
	m = report.Models[0]
	if _, e = runtime.auth.models.saveFeatureBinding("tenant-a", "actor-a", 0, FeatureBinding{FeatureKey: "script_generation", ModelConfigID: m.ID, Enabled: true, AllowOverride: true}); e != nil {
		t.Fatal(e)
	}
	db := postgresTestDB(t)
	api := postgresTestStore(t, db)
	worker := postgresTestStore(t, db)
	ledger, e := OpenPostgresBudgetLedger(db)
	if e != nil {
		t.Fatal(e)
	}
	governor := &ExecutionGovernor{Ledger: ledger, Moderator: testExecutionModerator{true}}
	for _, s := range []*FileStore{api, worker} {
		ConfigureExecutionGovernance(s, governor)
		ConfigureManagedRuntime(s, runtime.auth)
	}
	p, _, e := api.CreateProject("tenant-a", "candidate-worker-project", ProjectInput{Title: "异步独立候选", Brief: "候选不能替换正式稿，只能由作者明确采纳。"})
	if e != nil {
		t.Fatal(e)
	}
	p, e = api.SaveProjectScript(p.SpaceID, p.ID, "candidate-worker-original", ScriptSaveInput{ExpectedVersion: p.Version, Script: scriptFixture(t)})
	if e != nil {
		t.Fatal(e)
	}
	original := cloneProject(p)
	selection, entry, e := api.runtime.Resolve(context.Background(), "tenant-a", "actor-a", m.ID, "text")
	if e != nil {
		t.Fatal(e)
	}
	selection.FeatureKey = "script_generation"
	g, _, e := api.CreateScriptGeneration(p.SpaceID, p.ID, "candidate-worker-create", ScriptGenerationInput{ExpectedVersion: p.Version, CandidateOnly: true, ModelRequest: ModelRequest{ModelConfigID: m.ID, ModelConfigVersion: m.Version, selection: &selection, selectedCaps: Capabilities{Provider: entry.Provider, Model: entry.Model, Mode: "real", Ready: true}}})
	if e != nil {
		t.Fatal(e)
	}
	pipeline, e := NewScriptPipeline(context.Background(), MockScriptProvider{})
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- RunQueuedScriptWorker(ctx, jobqueue.Queue{DB: db}, worker, pipeline) }()
	defer func() {
		cancel()
		if e := <-done; e != nil {
			t.Error(e)
		}
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		p, e = api.GetProject(p.SpaceID, p.ID)
		if e != nil {
			t.Fatal(e)
		}
		if p.LatestGeneration.Status == "succeeded" {
			break
		}
		if p.LatestGeneration.Status == "failed" || time.Now().After(deadline) {
			t.Fatal("worker did not create candidate", p.LatestGeneration)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if calls.Load() != 1 || p.Version != original.Version || !reflect.DeepEqual(p.Script, original.Script) {
		t.Fatal("worker replaced draft or repeated dispatch", calls.Load())
	}
	detail, e := api.GetScriptGeneration(p.SpaceID, p.ID, g.ID)
	if e != nil || detail.Candidate == nil || detail.SourceScript == nil {
		t.Fatal("worker did not persist sources and candidate", e)
	}
	if _, e = api.AdoptScriptCandidate(p.SpaceID, p.ID, g.ID, "candidate-worker-adopt", ScriptAdoptionInput{ExpectedVersion: p.Version, actor: "actor-a"}); e != nil {
		t.Fatal(e)
	}
	if calls.Load() != 1 {
		t.Fatal("adoption called model")
	}
}
