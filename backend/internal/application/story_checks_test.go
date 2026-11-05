package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/projects"
	"github.com/adcwb/ai-creative-studio/backend/internal/modules/series"
	"github.com/adcwb/ai-creative-studio/backend/internal/modules/story"
	"github.com/adcwb/ai-creative-studio/backend/internal/persistence/storyrepo"
	"github.com/adcwb/ai-creative-studio/backend/internal/platform/jobqueue"
)

var checkActor = story.Actor{Space: "tenant-a", User: "actor-a", Writable: true}

func checkServer(t *testing.T, mode *atomic.Int32, calls *atomic.Int32) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != "POST" || r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer synthetic-gateway-key" {
			t.Error("unguarded or wrong protocol request")
		}
		var request struct {
			Model    string                           `json:"model"`
			Max      int                              `json:"max_completion_tokens"`
			Format   any                              `json:"response_format"`
			Messages []struct{ Role, Content string } `json:"messages"`
		}
		if json.NewDecoder(r.Body).Decode(&request) != nil || request.Model != "vendor/shared-model" || request.Max != 128 || request.Format != nil || len(request.Messages) != 2 {
			t.Error("wrong configured text request", request.Model, request.Max, request.Format)
		}
		if strings.Contains(request.Messages[1].Content, "FUTURE_PRIVATE_MATERIAL") || len(request.Messages[1].Content) < 64<<10 {
			t.Error("omitted prose leaked or text body unexpectedly truncated")
		}
		if mode.Load() == 3 {
			<-r.Context().Done()
			return
		}
		if mode.Load() == 4 {
			w.WriteHeader(429)
			w.Write([]byte(`{"error":"synthetic rejected with secret upstream text"}`))
			return
		}
		var input struct {
			Episode  string              `json:"episode_id"`
			Script   Script              `json:"script"`
			Material []story.ContextItem `json:"material"`
		}
		if json.Unmarshal([]byte(request.Messages[1].Content), &input) != nil || len(input.Material) != 1 {
			t.Error("incorrect scoped context input")
			return
		}
		evidence := story.CheckEvidence{RecordID: input.Material[0].ID, VersionID: input.Material[0].VersionID, Quote: input.Material[0].Body.Bible.Theme}
		if mode.Load() == 2 {
			evidence.Quote = "invented quote that never existed"
		}
		out := story.CheckResult{Summary: "本集的一处认知边界值得作者复核。", Findings: []story.Finding{{Title: "核对人物动机", Kind: "ambiguity", Severity: "note", Confidence: "low", Explanation: "此处可能是有意的悬念，应由作者决定。", Evidence: []story.CheckEvidence{evidence}}}}
		if mode.Load() == 5 {
			body := input.Material[0].Body
			copy := *body.Bible
			copy.Theme = "模型建议的新主题，仍需作者编辑确认。"
			body.Bible = &copy
			out.Findings[0].TargetRecordID = input.Material[0].ID
			out.Findings[0].Proposal = &story.Input{Title: input.Material[0].Title, Kind: "bible", Status: "active", Required: true, Body: body}
		}
		if mode.Load() == 6 {
			second, _ := story.Clone(out.Findings[0])
			second.Title = "另一处有意误导"
			third, _ := story.Clone(out.Findings[0])
			third.Title = "可忽略的疑问"
			out.Findings = append(out.Findings, second, third)
		}
		raw, _ := json.Marshal(out)
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]string{"content": string(raw)}}}, "usage": map[string]int{"prompt_tokens": 30, "completion_tokens": 20}})
	}))
}
func checkFixture(t *testing.T, s *FileStore, endpoint string) (story.ContextSnapshot, ModelRequest) {
	t.Helper()
	ctx := context.Background()
	runtime, _, m := gatewayFixture(t, endpoint)
	m.JSONOutput = false
	m.MaxOutputTokens = 128
	m.Currency = ""
	m.MaximumMinor = 0
	m.PriceSource = ""
	report, e := runtime.auth.models.saveGatewayModel(checkActor.Space, m.Version, m)
	if e != nil {
		t.Fatal(e)
	}
	m = report.Models[0]
	if _, e = runtime.auth.models.saveFeatureBinding(checkActor.Space, "actor-a", 0, FeatureBinding{FeatureKey: "story_continuity", ModelConfigID: m.ID, Enabled: true, AllowOverride: true}); e != nil {
		t.Fatal(e)
	}
	ledger, e := OpenBudgetLedger(t.TempDir())
	if s.pg != nil {
		ledger, e = OpenPostgresBudgetLedger(s.pg.db)
	}
	if e != nil {
		t.Fatal(e)
	}
	ConfigureExecutionGovernance(s, &ExecutionGovernor{Ledger: ledger, Moderator: testExecutionModerator{true}})
	ConfigureManagedRuntime(s, runtime.auth)
	a := series.Actor{Space: checkActor.Space, User: checkActor.User, Writable: true}
	root, e := s.series.Mutate(ctx, a, "check-root-key", series.Command{Operation: "create", Kind: "series", ID: series.NewID(a, "create", "check-root-key"), Input: series.Input{Title: "连续性验收", Summary: "人物认知与叙事连续性的合成验收", Profile: series.DefaultProfile()}}, nil)
	if e != nil {
		t.Fatal(e)
	}
	season, e := s.series.Mutate(ctx, a, "check-season-key", series.Command{Operation: "create", Kind: "season", ID: series.NewID(a, "create", "check-season-key"), Parent: root.ID}, nil)
	if e != nil {
		t.Fatal(e)
	}
	ep, p, e := s.CreateSeriesEpisode(ctx, a, "check-episode-key", season.ID, series.Input{Title: "认知边界"})
	if e != nil {
		t.Fatal(e)
	}
	_, future, e := s.CreateSeriesEpisode(ctx, a, "check-future-key", season.ID, series.Input{Title: "将来发生"})
	if e != nil {
		t.Fatal(e)
	}
	script := scriptFixture(t)
	for i := range script.Scenes {
		script.Scenes[i].EpisodeNumber = 1
	}
	stable, _, e := projects.CompileStructure(script, a.Space+":"+ep.ID, 1)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.SaveProjectScript(a.Space, p.ID, "check-script-key", ScriptSaveInput{ExpectedVersion: p.Version, Script: stable}); e != nil {
		t.Fatal(e)
	}
	bible := storyBible()
	bible.Body.Bible.WorldRules = strings.Repeat("确定的规则。", 9000)
	if _, e = s.SaveStory(ctx, checkActor, root.ID, storyrepo.BibleID(root.ID), "check-bible-key", bible); e != nil {
		t.Fatal(e)
	}
	ev := story.Input{Title: "FUTURE_PRIVATE_MATERIAL", Kind: "event", Status: "active", Required: true, Body: story.Body{Event: &story.Event{EpisodeID: future.ID, Description: "FUTURE_PRIVATE_MATERIAL"}}}
	if _, e = s.SaveStory(ctx, checkActor, root.ID, story.NewID(checkActor, root.ID, "future-event-key"), "future-event-key", ev); e != nil {
		t.Fatal(e)
	}
	snapshot, e := s.CreateStoryContext(ctx, checkActor, ep.ID, "check-context-key", story.Selection{})
	if e != nil || len(snapshot.Context.Problems) != 0 {
		t.Fatal(e, snapshot.Context.Problems)
	}
	selection, entry, e := s.runtime.Resolve(ctx, a.Space, a.User, m.ID, "text")
	if e != nil {
		t.Fatal(e)
	}
	selection.FeatureKey = "story_continuity"
	return snapshot, ModelRequest{ModelConfigID: m.ID, ModelConfigVersion: m.Version, selection: &selection, selectedCaps: Capabilities{Provider: entry.Provider, Model: entry.Model, Ready: true, Mode: "real"}}
}
func createCheck(t *testing.T, s *FileStore, snapshot story.ContextSnapshot, model ModelRequest, key string) story.Check {
	t.Helper()
	c, e := s.acceptStoryCheck(context.Background(), checkActor, snapshot.EpisodeID, key, story.CheckInput{ContextID: snapshot.ID, ModelID: model.ModelConfigID, ModelVersion: model.ModelConfigVersion}, model)
	if e != nil {
		t.Fatal(e)
	}
	return c
}
func TestStoryCheckRealGatewayRequestAndStrictEvidenceWithoutCostsOrAutomaticFacts(t *testing.T) {
	for _, modeValue := range []int32{0, 2, 4} {
		t.Run(fmt.Sprint(modeValue), func(t *testing.T) {
			var calls, mode atomic.Int32
			mode.Store(modeValue)
			upstream := checkServer(t, &mode, &calls)
			defer upstream.Close()
			u, _ := url.Parse(upstream.URL)
			t.Setenv("MODEL_GATEWAY_ALLOWED_HOSTS", u.Host)
			s := testStore(t)
			snapshot, model := checkFixture(t, s, upstream.URL+"/v1")
			c := createCheck(t, s, snapshot, model, "check-real-key")
			r, ok, e := s.story.NextFileCheck(context.Background())
			if e != nil || !ok {
				t.Fatal(e)
			}
			result, state, code := s.executeStoryCheck(context.Background(), r, func(context.Context) error { return nil })
			if modeValue == 0 {
				if state != "succeeded" || len(result.Findings) != 1 || !story.ID.MatchString(result.Findings[0].ID) {
					t.Fatal(state, code, result)
				}
			} else {
				if state != "failed" {
					t.Fatal(state, code)
				}
				if modeValue == 2 && code != "invalid_story_check_output" {
					t.Fatal("forged evidence accepted", code)
				}
			}
			var output *story.CheckResult
			if state == "succeeded" {
				output = &result
			}
			q := storyrepo.Scope{Space: c.Space, Series: c.SeriesID}
			if _, e = s.story.TransitionCheck(context.Background(), q, c.EpisodeID, c.ID, "running", state, code, output, nil); e != nil {
				t.Fatal(e)
			}
			replay := createCheck(t, s, snapshot, model, "check-real-key")
			if replay.ID != c.ID || replay.State != state || calls.Load() != 1 {
				t.Fatal("replayed provider call", calls.Load(), replay)
			}
			bible, e := s.story.Get(context.Background(), q, storyrepo.BibleID(c.SeriesID), "")
			if e != nil || bible.Number != 1 {
				t.Fatal("AI check wrote formal facts", e)
			}
			entries, _ := s.execution.Ledger.Entries(c.Space, "USD")
			if len(entries) != 0 {
				t.Fatal("check created a financial reservation")
			}
		})
	}
}
func TestStoryCheckChangedSourcesAndRevokedRoleNeverDispatch(t *testing.T) {
	for _, revoke := range []bool{false, true} {
		t.Run(fmt.Sprint(revoke), func(t *testing.T) {
			var calls, mode atomic.Int32
			upstream := checkServer(t, &mode, &calls)
			defer upstream.Close()
			u, _ := url.Parse(upstream.URL)
			t.Setenv("MODEL_GATEWAY_ALLOWED_HOSTS", u.Host)
			s := testStore(t)
			snapshot, model := checkFixture(t, s, upstream.URL+"/v1")
			createCheck(t, s, snapshot, model, "check-stale-key")
			if revoke {
				s.runtime.auth.mu.Lock()
				org := s.runtime.auth.data.Organizations[checkActor.Space]
				org.Members[checkActor.User] = "viewer"
				s.runtime.auth.data.Organizations[checkActor.Space] = org
				s.runtime.auth.mu.Unlock()
			} else {
				in := storyBible()
				in.ExpectedVersion = 1
				in.Title = "作者先改了正式规则"
				if _, e := s.SaveStory(context.Background(), checkActor, snapshot.SeriesID, storyrepo.BibleID(snapshot.SeriesID), "check-concurrent-edit", in); e != nil {
					t.Fatal(e)
				}
			}
			r, ok, e := s.story.NextFileCheck(context.Background())
			if e != nil || !ok {
				t.Fatal(e)
			}
			_, state, _ := s.executeStoryCheck(context.Background(), r, func(context.Context) error { return nil })
			if state != "failed" || calls.Load() != 0 {
				t.Fatal("stale input or revoked actor dispatched", state, calls.Load())
			}
		})
	}
}
func TestPostgresStoryCheckQueueAndExpiredDispatchBecomeUnknown(t *testing.T) {
	var calls, mode atomic.Int32
	upstream := checkServer(t, &mode, &calls)
	defer upstream.Close()
	u, _ := url.Parse(upstream.URL)
	t.Setenv("MODEL_GATEWAY_ALLOWED_HOSTS", u.Host)
	db := postgresTestDB(t)
	api := postgresTestStore(t, db)
	snapshot, model := checkFixture(t, api, upstream.URL+"/v1")
	c := createCheck(t, api, snapshot, model, "check-queued-success")
	worker := postgresTestStore(t, db)
	worker.execution = api.execution
	ConfigureManagedRuntime(worker, api.runtime.auth)
	queue := jobqueue.Queue{DB: db}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- RunQueuedStoryCheckWorker(ctx, queue, worker) }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		got, e := api.story.GetCheck(context.Background(), storyrepo.Scope{Space: c.Space, Series: c.SeriesID}, c.EpisodeID, c.ID)
		if e != nil {
			t.Fatal(e)
		}
		if got.Check.State == "succeeded" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("queued check did not finish", got.Check)
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
	if calls.Load() != 1 {
		t.Fatal("wrong dispatch count", calls.Load())
	}
	c = createCheck(t, api, snapshot, model, "check-unknown-dispatch")
	j, ok, e := queue.Claim(context.Background(), "story_check")
	if e != nil || !ok {
		t.Fatal(e)
	}
	scope := storyrepo.Scope{Space: c.Space, Series: c.SeriesID}
	if _, e = api.story.TransitionCheck(context.Background(), scope, c.EpisodeID, c.ID, "queued", "running", "", nil, &j); e != nil {
		t.Fatal(e)
	}
	if e = queue.Dispatch(context.Background(), j); e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(`UPDATE execution_jobs SET lease_until=clock_timestamp()-interval '1 second' WHERE tenant_id=$1 AND id=$2`, c.Space, c.ID); e != nil {
		t.Fatal(e)
	}
	if _, ok, e = queue.Claim(context.Background(), "story_check"); e != nil || ok {
		t.Fatal("sticky dispatch reclaimed", e)
	}
	if e = ReconcilePostgresJobs(context.Background(), db); e != nil {
		t.Fatal(e)
	}
	unknown, e := api.story.GetCheck(context.Background(), scope, c.EpisodeID, c.ID)
	if e != nil || unknown.Check.State != "unknown" {
		t.Fatal("domain did not preserve unknown", e, unknown.Check.State)
	}
	if _, e = api.story.TransitionCheck(context.Background(), scope, c.EpisodeID, c.ID, "running", "failed", "stale-worker", nil, &j); !errors.Is(e, jobqueue.ErrLeaseLost) {
		t.Fatal("expired worker persisted result", e)
	}
	if calls.Load() != 1 {
		t.Fatal("unknown text POST resubmitted")
	}
}
