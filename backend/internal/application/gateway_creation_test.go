package application

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestManagedGenerationFailuresKeepDraftsAndNeverReportMockErrors(t *testing.T) {
	for _, kind := range []string{"script", "storyboard"} {
		for _, failure := range []struct {
			name, code string
			err        error
		}{
			{"governance", "execution_governance_denied", ErrExecutionGovernance},
			{"revoked-model", "model_runtime_denied", errRuntimeDenied},
			{"upstream-rejected", "provider_rejected", providerFailure("provider_rejected")},
			{"timeout", "provider_error", context.DeadlineExceeded},
			{"invalid-output", "invalid_provider_output", nil},
		} {
			t.Run(kind+"/"+failure.name, func(t *testing.T) {
				s := testStore(t)
				before := boardProject(t, s)
				input := ScriptGenerationInput{ExpectedVersion: before.Version}
				// Exercise completion with a persisted managed selection. Acceptance
				// and dispatch are covered separately; no upstream is called here.
				selection := &RuntimeSelection{GatewayID: "synthetic-gateway", ModelID: "synthetic-model"}
				if kind == "script" {
					g, _, err := s.CreateScriptGeneration(before.SpaceID, before.ID, "managed-script-failure", input)
					if err != nil {
						t.Fatal(err)
					}
					s.mu.Lock()
					d := s.clone()
					current := d.ScriptGenerations[g.ID]
					current.RuntimeSelection = selection
					current.Provider = "gateway-text"
					d.ScriptGenerations[g.ID] = current
					err = s.commit(d)
					s.mu.Unlock()
					if err != nil {
						t.Fatal(err)
					}
					if err = s.FinishScriptGeneration(g, Script{}, failure.err); err != nil {
						t.Fatal(err)
					}
				} else {
					g, _, err := s.CreateStoryboardGeneration(before.SpaceID, before.ID, "managed-board-failure", input)
					if err != nil {
						t.Fatal(err)
					}
					s.mu.Lock()
					d := s.clone()
					current := d.StoryboardGenerations[g.ID]
					current.RuntimeSelection = selection
					current.Provider = "gateway-text"
					d.StoryboardGenerations[g.ID] = current
					err = s.commit(d)
					s.mu.Unlock()
					if err != nil {
						t.Fatal(err)
					}
					if err = s.FinishStoryboardGeneration(g, nil, failure.err); err != nil {
						t.Fatal(err)
					}
				}
				after, err := s.GetProject(before.SpaceID, before.ID)
				if err != nil || after.Version != before.Version || !reflect.DeepEqual(after.Script, before.Script) || !reflect.DeepEqual(after.Storyboard, before.Storyboard) {
					t.Fatal("failed gateway generation replaced a saved draft", err)
				}
				status, code, message := after.LatestGeneration.Status, after.LatestGeneration.ErrorCode, after.LatestGeneration.Error
				if kind == "storyboard" {
					status, code, message = after.LatestStoryboardGeneration.Status, after.LatestStoryboardGeneration.ErrorCode, after.LatestStoryboardGeneration.Error
				}
				if status != "failed" || code != failure.code || strings.Contains(message, "模拟") || strings.Contains(message, "mock") {
					t.Fatalf("misleading gateway failure: %s %s %s", status, code, message)
				}
			})
		}
	}
}

func TestGatewayCreationRoutesRejectOmittedChoiceBeforeAnyMockTask(t *testing.T) {
	s := testStore(t)
	h := Handler(s, "http://localhost:5173")
	for _, in := range []struct{ path, body string }{
		{"/gateway-tasks", `{"prompt":"Synthetic no fallback","kind":"general","ratio":"1:1","style":"natural"}`},
		{"/gateway-tasks/missing/retry", `{"prompt":"Synthetic no fallback","kind":"general"}`},
		{"/projects/missing/gateway-generations", `{"expected_version":1}`},
		{"/projects/missing/gateway-storyboard-generations", `{"expected_version":1}`},
		{"/projects/missing/gateway-analysis", `{"expected_version":1}`},
		{"/projects/missing/gateway-creative-jobs", `{"kind":"image","prompt":"Synthetic no fallback","expected_version":1}`},
	} {
		r := httptest.NewRequest("POST", "/api/v1"+in.path+"?space_id=personal-demo", strings.NewReader(in.body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Idempotency-Key", "synthetic-strict-creation")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 409 || !strings.Contains(w.Body.String(), `"gateway_model_required"`) {
			t.Fatalf("%s: %d %s", in.path, w.Code, w.Body.String())
		}
	}
	if len(s.List("personal-demo")) != 0 || len(s.ListProjects("personal-demo")) != 0 {
		t.Fatal("rejected request created mock data")
	}
}

func TestGatewayCreationRejectsLegacyModelAndCapabilityMismatch(t *testing.T) {
	s := testStore(t)
	auth, err := OpenAuthStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	user, token, session, err := auth.login("strict-gateway@example.test", "synthetic-passphrase", true)
	if err != nil {
		t.Fatal(err)
	}
	tenant := auth.spaces(user.ID)[0]["id"]
	auth.models, err = OpenModelConfigStore(t.TempDir(), bytes.Repeat([]byte{19}, 32))
	if err != nil {
		t.Fatal(err)
	}
	legacyInput := syntheticModel()
	legacyInput.Provider = "openai-images"
	legacyInput.Capability = "image"
	legacy, err := auth.models.update(tenant, 0, legacyInput, ModelCredentialInput{Action: "set", Secret: "synthetic-key"})
	if err != nil {
		t.Fatal(err)
	}
	gateways, err := auth.models.saveGateway(tenant, 0, AIGateway{Name: "Local fixture", BaseURL: "https://synthetic.example/v1", Enabled: true, TimeoutSeconds: 30}, ModelCredentialInput{Action: "set", Secret: "synthetic-key"})
	if err != nil {
		t.Fatal(err)
	}
	models, err := auth.models.saveGatewayModel(tenant, 0, GatewayModel{GatewayID: gateways.Gateways[0].ID, Name: "Text only", Model: "synthetic-text", Capabilities: []string{"text"}, Enabled: true, MaxOutputTokens: 128, JSONOutput: true, Currency: "USD", MaximumMinor: 10, PriceSource: "Synthetic fixture"})
	if err != nil {
		t.Fatal(err)
	}
	h := SecureHandler(s, "http://localhost:5173", auth, AuthOptions{})
	post := func(path string, body map[string]any, csrf string, want string) {
		t.Helper()
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest("POST", "/api/v1"+path+"?space_id="+tenant, bytes.NewReader(raw))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", "http://localhost:5173")
		r.Header.Set("X-CSRF-Token", csrf)
		r.Header.Set("Idempotency-Key", "synthetic-strict-model")
		r.AddCookie(&http.Cookie{Name: "studio_session", Value: token})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if !strings.Contains(w.Body.String(), `"`+want+`"`) || w.Code < 400 {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
	}
	body := map[string]any{"prompt": "Synthetic no fallback", "kind": "general", "model_config_id": legacy.Entries[0].ID, "model_config_version": models.Version}
	post("/gateway-tasks", body, session.CSRF, "gateway_model_required")
	post("/gateway-runtime-quote", map[string]any{"operation": "image", "model_config_id": legacy.Entries[0].ID, "model_config_version": models.Version, "input": map[string]any{"expected_version": 1}}, session.CSRF, "gateway_model_required")
	body["model_config_id"] = models.Models[0].ID
	body["model_config_version"] = models.Models[0].Version
	post("/gateway-tasks", body, session.CSRF, "model_config_conflict")
	post("/gateway-tasks", body, "", "csrf_denied")
	textRequest := map[string]any{"expected_version": 1, "model_config_id": models.Models[0].ID, "model_config_version": models.Models[0].Version}
	post("/projects/missing/gateway-generations", textRequest, session.CSRF, "feature_model_denied")
	if _, err := auth.models.saveFeatureBinding(tenant, user.ID, 0, FeatureBinding{FeatureKey: "script_generation", ModelConfigID: models.Models[0].ID, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	post("/projects/missing/gateway-generations", textRequest, session.CSRF, "provider_not_ready")
	if len(s.List(tenant)) != 0 {
		t.Fatal("rejected request created a fallback image")
	}
}
