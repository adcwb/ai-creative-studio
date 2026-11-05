package application

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/adcwb/ai-creative-studio/backend/internal/modules/story"
	"github.com/adcwb/ai-creative-studio/backend/internal/persistence/storyrepo"
	"github.com/adcwb/ai-creative-studio/backend/internal/platform/jobqueue"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func TestResponsesNativeCounterUsesExactInputAndReservesOutputWithoutChatFallback(t *testing.T) {
	var mode atomic.Int32
	var countCalls, generations atomic.Int32
	var mu sync.Mutex
	var counted map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Method != "POST" || r.Header.Get("Authorization") != "Bearer synthetic" {
			t.Error("unguarded protocol")
		}
		var body map[string]any
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			t.Fatal("bad request")
		}
		switch r.URL.Path {
		case "/v1/responses/input_tokens":
			countCalls.Add(1)
			counted = body
			switch mode.Load() {
			case 1:
				json.NewEncoder(w).Encode(map[string]any{"object": "response.input_tokens", "input_tokens": 501})
				return
			case 2:
				w.WriteHeader(404)
				w.Write([]byte(`{"error":{"message":"synthetic upstream private prose"}}`))
				return
			case 3:
				w.Write([]byte(`{"object":"response.input_tokens","input_tokens":null}`))
				return
			case 4:
				w.Write([]byte(`{"object":"response.input_tokens","input_tokens":-1}`))
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"object": "response.input_tokens", "input_tokens": 500})
		case "/v1/responses":
			generations.Add(1)
			if body["max_output_tokens"] != float64(100) || body["store"] != false || body["stream"] != false || body["background"] != false || body["truncation"] != "disabled" || body["tool_choice"] != "none" {
				t.Error("wrong generation controls", body)
			}
			for _, key := range []string{"model", "input", "instructions", "text", "tools", "tool_choice"} {
				if !reflect.DeepEqual(body[key], counted[key]) {
					t.Error("counted input differs", key)
				}
			}
			status := "completed"
			if mode.Load() == 5 {
				status = "incomplete"
			}
			kind := "output_text"
			if mode.Load() == 6 {
				kind = "refusal"
			}
			json.NewEncoder(w).Encode(map[string]any{"object": "response", "status": status, "error": nil, "output": []any{map[string]any{"type": "reasoning", "summary": []any{}}, map[string]any{"type": "message", "role": "assistant", "status": "completed", "content": []any{map[string]any{"type": kind, "text": `{"answer":"原稿保持完整"}`}}}}})
		default:
			t.Error("unexpected request or silent chat fallback", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer upstream.Close()
	p := newCreativeProvider("synthetic", upstream.URL+"/v1", upstream.Client())
	p.textModel = "opaque/upstream-model"
	p.textProtocol = "responses"
	p.nativeInputCounting = true
	p.contextWindowTokens = 600
	p.maxOutputTokens = 100
	var out struct {
		Answer string `json:"answer"`
	}
	if e := p.jsonBounded(context.Background(), map[string]string{"script": strings.Repeat("人物独立判断", 100)}, "Preserve the whole script and return JSON.", &out, 64<<10, 64<<10); e != nil || out.Answer != "原稿保持完整" {
		t.Fatal(e, out)
	}
	mu.Lock()
	instruction := counted["instructions"].(string)
	mu.Unlock()
	if !strings.Contains(instruction, "Preserve the whole script") {
		t.Fatal("outer instructions omitted from native count")
	}
	for _, test := range []struct {
		mode int32
		code string
	}{{1, "provider_input_capacity"}, {2, "provider_input_count_unavailable"}, {3, "provider_input_count_unavailable"}, {4, "provider_input_count_unavailable"}, {5, "invalid_provider_response"}, {6, "invalid_provider_response"}} {
		mode.Store(test.mode)
		before := generations.Load()
		e := p.json(context.Background(), map[string]string{"script": "完整原稿"}, "Return JSON.", &out)
		var pe *ProviderError
		if !errors.As(e, &pe) || pe.Code != test.code {
			t.Fatal(test.mode, e)
		}
		if test.mode <= 4 && generations.Load() != before {
			t.Fatal("generation sent despite failed capacity preflight")
		}
	}
	if countCalls.Load() != 7 || generations.Load() != 3 {
		t.Fatal(countCalls.Load(), generations.Load())
	}
}
func TestResponsesOptionalCountingAndNativeContextErrorsStayExplicit(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/responses" {
			t.Error("unrequested counter or fallback", r.URL.Path)
		}
		w.WriteHeader(400)
		w.Write([]byte(`{"error":{"code":"context_length_exceeded","message":"sensitive provider error text"}}`))
	}))
	defer server.Close()
	p := newCreativeProvider("synthetic", server.URL, server.Client())
	p.textModel = "configured-model"
	p.textProtocol = "responses"
	var result map[string]any
	e := p.json(context.Background(), map[string]string{"x": "y"}, "Return JSON", &result)
	var pe *ProviderError
	if !errors.As(e, &pe) || pe.Code != "provider_input_capacity" || strings.Contains(e.Error(), "sensitive") || calls.Load() != 1 {
		t.Fatal("optional configuration blocked or error leaked", e, calls.Load())
	}
}

func TestManagedResponsesCheckRevalidatesBetweenCountAndGeneration(t *testing.T) {
	for _, scenario := range []string{"success", "overflow", "unsupported", "revoked", "source_changed"} {
		t.Run(scenario, func(t *testing.T) {
			var countCalls, generationCalls atomic.Int32
			var store *FileStore
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				if json.NewDecoder(r.Body).Decode(&body) != nil {
					t.Error("invalid request")
					w.WriteHeader(400)
					return
				}
				if r.Header.Get("Authorization") != "Bearer synthetic-gateway-key" {
					t.Error("missing guarded gateway credential")
				}
				switch r.URL.Path {
				case "/v1/responses/input_tokens":
					countCalls.Add(1)
					if len(body["input"].([]any)[0].(map[string]any)["content"].(string)) < 64<<10 || strings.Contains(body["input"].([]any)[0].(map[string]any)["content"].(string), "FUTURE_PRIVATE_MATERIAL") {
						t.Error("native count omitted input or leaked future material")
					}
					if scenario == "unsupported" {
						w.WriteHeader(404)
						return
					}
					if scenario == "source_changed" {
						var input struct {
							EpisodeID string `json:"episode_id"`
						}
						content := body["input"].([]any)[0].(map[string]any)["content"].(string)
						if json.Unmarshal([]byte(content), &input) != nil {
							t.Error("bad scoped input")
						}
						q, e := store.storySnapshotScope(context.Background(), checkActor.Space, input.EpisodeID)
						if e != nil {
							t.Error(e)
							w.WriteHeader(500)
							return
						}
						b, e := store.story.Get(context.Background(), q, storyrepo.BibleID(q.Series), "")
						if e != nil {
							t.Error(e)
							w.WriteHeader(500)
							return
						}
						in := story.Input{ExpectedVersion: b.Number, Title: b.Title + " · 计数期间改稿", Kind: b.Kind, Status: b.Status, Required: b.Required, Body: b.Body}
						if _, e = store.SaveStory(context.Background(), checkActor, q.Series, b.ID, "native-count-source-edit", in); e != nil {
							t.Error(e)
							w.WriteHeader(500)
							return
						}
					}
					if scenario == "revoked" {
						store.runtime.auth.mu.Lock()
						org := store.runtime.auth.data.Organizations[checkActor.Space]
						org.Members[checkActor.User] = "viewer"
						store.runtime.auth.data.Organizations[checkActor.Space] = org
						store.runtime.auth.mu.Unlock()
					}
					count := 500
					if scenario == "overflow" {
						count = 900
					}
					json.NewEncoder(w).Encode(map[string]any{"object": "response.input_tokens", "input_tokens": count})
				case "/v1/responses":
					generationCalls.Add(1)
					if body["max_output_tokens"] != float64(128) {
						t.Error("configured output reserve ignored")
					}
					json.NewEncoder(w).Encode(map[string]any{"object": "response", "status": "completed", "output": []any{map[string]any{"type": "message", "role": "assistant", "status": "completed", "content": []any{map[string]any{"type": "output_text", "text": `{"summary":"本次未发现有依据的问题。","findings":[]}`}}}}})
				default:
					t.Error("unexpected protocol fallback", r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer upstream.Close()
			u, _ := url.Parse(upstream.URL)
			t.Setenv("MODEL_GATEWAY_ALLOWED_HOSTS", u.Host)
			store = testStore(t)
			snapshot, model := checkFixture(t, store, upstream.URL+"/v1")
			configs := store.runtime.auth.models
			configs.mu.Lock()
			entry := configs.data.Tenants[checkActor.Space].Models[model.ModelConfigID]
			configs.mu.Unlock()
			entry.TextProtocol = "responses"
			entry.NativeInputCounting = true
			entry.ContextWindowTokens = 1000
			report, e := configs.saveGatewayModel(checkActor.Space, entry.Version, entry)
			if e != nil {
				t.Fatal(e)
			}
			entry = report.Models[0]
			selection, managed, e := store.runtime.Resolve(context.Background(), checkActor.Space, checkActor.User, entry.ID, "text")
			if e != nil {
				t.Fatal(e)
			}
			selection.FeatureKey = "story_continuity"
			model = ModelRequest{ModelConfigID: entry.ID, ModelConfigVersion: entry.Version, selection: &selection, selectedCaps: Capabilities{Provider: managed.Provider, Model: managed.Model, Ready: true, Mode: "real"}}
			c := createCheck(t, store, snapshot, model, "native-capacity-check")
			record, ok, e := store.story.NextFileCheck(context.Background())
			if e != nil || !ok {
				t.Fatal(e)
			}
			result, state, code := store.executeStoryCheck(context.Background(), record, func(context.Context) error { return nil })
			if countCalls.Load() != 1 {
				t.Fatal("native counter bypassed", countCalls.Load())
			}
			if scenario == "success" {
				if state != "succeeded" || generationCalls.Load() != 1 {
					t.Fatal(state, code, generationCalls.Load())
				}
			} else {
				if state == "succeeded" || generationCalls.Load() != 0 {
					t.Fatal("generation escaped preflight or revocation", state, code, generationCalls.Load())
				}
				if scenario == "overflow" && code != "provider_input_capacity" || scenario == "unsupported" && code != "provider_input_count_unavailable" || scenario == "source_changed" && code != "story_context_changed" {
					t.Fatal(state, code)
				}
			}
			if scenario != "revoked" {
				var output *story.CheckResult
				if state == "succeeded" {
					output = &result
				}
				if _, e = store.story.TransitionCheck(context.Background(), storyrepo.Scope{Space: c.Space, Series: c.SeriesID}, c.EpisodeID, c.ID, "running", state, code, output, nil); e != nil {
					t.Fatal(e)
				}
				before := countCalls.Load()
				replay := createCheck(t, store, snapshot, model, "native-capacity-check")
				if replay.ID != c.ID || replay.State != state || countCalls.Load() != before {
					t.Fatal("original receipt repeated native count or generation")
				}
			}
			entries, _ := store.execution.Ledger.Entries(c.Space, "USD")
			if len(entries) != 0 {
				t.Fatal("native capacity created a financial reservation")
			}
		})
	}
}

func TestGatewayNativeContextConfigurationIsOptionalVersionedAndScoped(t *testing.T) {
	runtime, _, m := gatewayFixture(t, "https://synthetic.example/v1")
	m.MaxOutputTokens = 100
	m.TextProtocol = "responses"
	m.ContextWindowTokens = 600
	m.NativeInputCounting = true
	report, e := runtime.auth.models.saveGatewayModel("tenant-a", m.Version, m)
	if e != nil {
		t.Fatal(e)
	}
	saved := report.Models[0]
	reopened, e := OpenModelConfigStore(filepath.Dir(runtime.auth.models.path), bytes.Repeat([]byte{11}, 32))
	if e != nil {
		t.Fatal(e)
	}
	reopened.mu.Lock()
	copy := reopened.data.Tenants["tenant-a"].Models[saved.ID]
	reopened.mu.Unlock()
	if copy.TextProtocol != "responses" || copy.ContextWindowTokens != 600 || !copy.NativeInputCounting {
		t.Fatal("native capability lost across restart")
	}
	for _, mutate := range []func(*GatewayModel){func(x *GatewayModel) { x.TextProtocol = "arbitrary" }, func(x *GatewayModel) { x.TextProtocol = "chat_completions" }, func(x *GatewayModel) { x.ContextWindowTokens = 100 }, func(x *GatewayModel) { x.Capabilities = []string{"image"} }, func(x *GatewayModel) { x.ContextWindowTokens = -1 }} {
		invalid := saved
		mutate(&invalid)
		if validateGatewayModel(invalid) == nil {
			t.Fatal("invalid native capability accepted", invalid.TextProtocol, invalid.ContextWindowTokens)
		}
	}
	legacy := saved
	legacy.TextProtocol = ""
	legacy.ContextWindowTokens = 0
	legacy.NativeInputCounting = false
	legacy.Currency = ""
	legacy.MaximumMinor = 0
	legacy.PriceSource = ""
	if validateGatewayModel(legacy) != nil {
		t.Fatal("optional configuration became a cost/configuration gate")
	}
}

func TestPostgresNativeCountRechecksSourceAndLease(t *testing.T) {
	for _, scenario := range []string{"success", "source_changed", "lease_lost"} {
		t.Run(scenario, func(t *testing.T) {
			db := postgresTestDB(t)
			api := postgresTestStore(t, db)
			var episode, queuedID string
			var counts, generations atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				if json.NewDecoder(r.Body).Decode(&body) != nil {
					t.Error("native PG upstream received malformed input")
					w.WriteHeader(400)
					return
				}
				if r.URL.Path == "/v1/responses/input_tokens" {
					counts.Add(1)
					if scenario == "success" {
						w.Write([]byte(`{"object":"response.input_tokens","input_tokens":500}`))
						return
					}
					if scenario == "lease_lost" {
						if _, e := db.Exec(`UPDATE execution_jobs SET lease_until=clock_timestamp()-interval '1 second' WHERE tenant_id=$1 AND id=$2`, checkActor.Space, queuedID); e != nil {
							t.Error(e)
							w.WriteHeader(500)
							return
						}
						w.Write([]byte(`{"object":"response.input_tokens","input_tokens":500}`))
						return
					}
					q, e := api.storySnapshotScope(context.Background(), checkActor.Space, episode)
					if e != nil {
						t.Error(e)
						w.WriteHeader(500)
						return
					}
					b, e := api.story.Get(context.Background(), q, storyrepo.BibleID(q.Series), "")
					if e != nil {
						t.Error(e)
						w.WriteHeader(500)
						return
					}
					in := story.Input{ExpectedVersion: b.Number, Title: b.Title + " · 原生计数时修订", Kind: b.Kind, Status: b.Status, Required: b.Required, Body: b.Body}
					if _, e = api.SaveStory(context.Background(), checkActor, q.Series, b.ID, "pg-native-source-edit", in); e != nil {
						t.Error(e)
						w.WriteHeader(500)
						return
					}
					w.Write([]byte(`{"object":"response.input_tokens","input_tokens":500}`))
					return
				}
				generations.Add(1)
				if scenario == "success" {
					w.Write([]byte(`{"object":"response","status":"completed","output":[{"type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"{\"summary\":\"PostgreSQL native review.\",\"findings\":[]}"}]}]}`))
					return
				}
				t.Error("stale PG sources reached generation", r.URL.Path)
				w.WriteHeader(400)
			}))
			defer server.Close()
			u, _ := url.Parse(server.URL)
			t.Setenv("MODEL_GATEWAY_ALLOWED_HOSTS", u.Host)
			snapshot, model := checkFixture(t, api, server.URL+"/v1")
			episode = snapshot.EpisodeID
			configs := api.runtime.auth.models
			configs.mu.Lock()
			m := configs.data.Tenants[checkActor.Space].Models[model.ModelConfigID]
			configs.mu.Unlock()
			m.TextProtocol = "responses"
			m.NativeInputCounting = true
			m.ContextWindowTokens = 1000
			report, e := configs.saveGatewayModel(checkActor.Space, m.Version, m)
			if e != nil {
				t.Fatal(e)
			}
			m = report.Models[0]
			selection, entry, e := api.runtime.Resolve(context.Background(), checkActor.Space, checkActor.User, m.ID, "text")
			if e != nil {
				t.Fatal(e)
			}
			selection.FeatureKey = "story_continuity"
			model = ModelRequest{ModelConfigID: m.ID, ModelConfigVersion: m.Version, selection: &selection, selectedCaps: Capabilities{Provider: entry.Provider, Model: entry.Model, Ready: true, Mode: "real"}}
			c := createCheck(t, api, snapshot, model, "pg-native-check-intent")
			queuedID = c.ID
			q := jobqueue.Queue{DB: db}
			j, ok, e := q.Claim(context.Background(), "story_check")
			if e != nil || !ok {
				t.Fatal(e)
			}
			scope := storyrepo.Scope{Space: c.Space, Series: c.SeriesID}
			if _, e = api.story.TransitionCheck(context.Background(), scope, c.EpisodeID, c.ID, "queued", "running", "", nil, &j); e != nil {
				t.Fatal(e)
			}
			record, e := api.story.GetCheck(context.Background(), scope, c.EpisodeID, c.ID)
			if e != nil {
				t.Fatal(e)
			}
			result, state, code := api.executeStoryCheck(jobqueue.WithLeaseFence(context.Background(), q, j), record, func(ctx context.Context) error { return q.Dispatch(ctx, j) })
			if scenario == "success" {
				if state != "succeeded" || counts.Load() != 1 || generations.Load() != 1 {
					t.Fatal("live lease did not generate", state, code, counts.Load(), generations.Load())
				}
				if _, e = api.story.TransitionCheck(context.Background(), scope, c.EpisodeID, c.ID, "running", state, "", &result, &j); e != nil {
					t.Fatal(e)
				}
				replay := createCheck(t, api, snapshot, model, "pg-native-check-intent")
				if replay.State != "succeeded" || counts.Load() != 1 || generations.Load() != 1 {
					t.Fatal("native success was resubmitted", replay)
				}
				return
			}
			if scenario == "lease_lost" {
				if state == "succeeded" || counts.Load() != 1 || generations.Load() != 0 {
					t.Fatal("expired lease sent generation", state, code, counts.Load(), generations.Load())
				}
				if _, e = api.story.TransitionCheck(context.Background(), scope, c.EpisodeID, c.ID, "running", state, code, nil, &j); !errors.Is(e, jobqueue.ErrLeaseLost) {
					t.Fatal("expired lease committed", e)
				}
				if _, claimed, e := q.Claim(context.Background(), "story_check"); e != nil || claimed {
					t.Fatal("native dispatched intent was automatically reclaimed", e)
				}
				if e = ReconcilePostgresJobs(context.Background(), db); e != nil {
					t.Fatal(e)
				}
				got, e := api.story.GetCheck(context.Background(), scope, c.EpisodeID, c.ID)
				if e != nil || got.Check.State != "unknown" {
					t.Fatal("expired native intent was not unknown", e, got.Check.State)
				}
				if _, claimed, e := q.Claim(context.Background(), "story_check"); e != nil || claimed {
					t.Fatal("native dispatched intent was automatically reclaimed", e)
				}
				replay := createCheck(t, api, snapshot, model, "pg-native-check-intent")
				if replay.State != "unknown" || counts.Load() != 1 || generations.Load() != 0 {
					t.Fatal("unknown native intent repeated upstream", replay)
				}
				return
			}
			if state != "failed" || code != "story_context_changed" || counts.Load() != 1 || generations.Load() != 0 {
				t.Fatal(state, code, counts.Load(), generations.Load())
			}
			if _, e = api.story.TransitionCheck(context.Background(), scope, c.EpisodeID, c.ID, "running", state, code, nil, &j); e != nil {
				t.Fatal(e)
			}
			replay := createCheck(t, api, snapshot, model, "pg-native-check-intent")
			if replay.State != "failed" || replay.ErrorCode != code || counts.Load() != 1 {
				t.Fatal("PG original result was resubmitted", replay)
			}
			var queueState string
			if e = db.QueryRow(`SELECT state FROM execution_jobs WHERE tenant_id=$1 AND id=$2`, c.Space, c.ID).Scan(&queueState); e != nil || queueState != "failed" {
				t.Fatal("domain and queue failure diverged", e, queueState)
			}
		})
	}
}
