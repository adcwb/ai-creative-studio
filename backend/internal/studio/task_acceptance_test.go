package studio

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestRuntimeAcceptanceBindsAuthenticatedActorAndVersion(t *testing.T) {
	s := testStore(t)
	auth, err := OpenAuthStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	user, token, session, err := auth.login("runtime-acceptance@example.test", "synthetic-password-for-test", true)
	if err != nil {
		t.Fatal(err)
	}
	tenant := auth.spaces(user.ID)[0]["id"]
	auth.models, err = OpenModelConfigStore(t.TempDir(), bytes.Repeat([]byte{91}, 32))
	if err != nil {
		t.Fatal(err)
	}
	model := syntheticModel()
	model.Provider = "openai-images"
	model.Capability = "image"
	report, err := auth.models.update(tenant, 0, model, ModelCredentialInput{Action: "set", Secret: "synthetic-never-transmitted"})
	if err != nil {
		t.Fatal(err)
	}
	id := report.Entries[0].ID
	h := SecureHandler(s, "http://localhost:5173", auth, AuthOptions{})
	post := func(key, body string, want int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest("POST", "/api/v1/tasks?space_id="+tenant, strings.NewReader(body))
		r.Header.Set("Origin", "http://localhost:5173")
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Idempotency-Key", key)
		r.Header.Set("X-CSRF-Token", session.CSRF)
		r.AddCookie(&http.Cookie{Name: "studio_session", Value: token})
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("got %d want %d: %s", w.Code, want, w.Body.String())
		}
		return w
	}
	body := fmt.Sprintf(`{"prompt":"synthetic image request","kind":"general","model_config_id":%q,"model_config_version":1,"budget_currency":"USD","budget_maximum_minor":30}`, id)
	post("runtime-not-ready", body, 503)
	governor, _ := executionFixture(t)
	ConfigureExecutionGovernance(s, governor)
	post("runtime-no-cost-consent", strings.Replace(body, `,"budget_currency":"USD","budget_maximum_minor":30`, "", 1), 400)
	post("runtime-spoof-actor", strings.TrimSuffix(body, "}")+`,"actor_id":"other-user"}`, 400)
	post("runtime-spoof-scope", strings.TrimSuffix(body, "}")+`,"runtime_selection":{"tenant_id":"other"}}`, 400)
	post("runtime-stale-version", strings.Replace(body, `"model_config_version":1`, `"model_config_version":2`, 1), 409)
	first := post("runtime-accepted", body, 202)
	var task Task
	if err = json.Unmarshal(first.Body.Bytes(), &task); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(first.Body.Bytes(), []byte("runtime_selection")) || bytes.Contains(first.Body.Bytes(), []byte("actor_id")) {
		t.Fatal("internal binding exposed")
	}
	task, err = s.Get(tenant, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if task.RuntimeSelection == nil || task.RuntimeSelection.ActorID != user.ID || task.RuntimeSelection.TenantID != tenant || task.RuntimeSelection.ModelID != id || task.RuntimeSelection.Version != 1 || task.Provider != "openai-images" {
		t.Fatalf("incorrect server binding: %#v", task)
	}
	replay := post("runtime-accepted", body, 200)
	var again Task
	_ = json.Unmarshal(replay.Body.Bytes(), &again)
	if again.ID != task.ID {
		t.Fatal("same idempotency key created a second task")
	}
	raw, err := os.ReadFile(s.path)
	if err != nil || !bytes.Contains(raw, []byte(`"actor_id"`)) || bytes.Contains(raw, []byte("synthetic-never-transmitted")) {
		t.Fatal("durable binding missing or secret leaked", err)
	}
	// An unrelated tenant's configuration is never selected, even with a known ID.
	other, err := auth.models.update("other-tenant", 0, model, ModelCredentialInput{Action: "set", Secret: "synthetic-other"})
	if err != nil {
		t.Fatal(err)
	}
	post("runtime-other-tenant", strings.Replace(body, id, other.Entries[0].ID, 1), 409)
	// Revoking a config invalidates both old and new submissions before queueing.
	model.ID = id
	_, err = auth.models.update(tenant, 1, model, ModelCredentialInput{Action: "revoke"})
	if err != nil {
		t.Fatal(err)
	}
	post("runtime-after-revoke", body, 409)
	if len(s.List(tenant)) != 1 {
		t.Fatal("rejected submission created a task")
	}
}

func TestModelRequestRejectsClientOwnedMetadataAcrossInputs(t *testing.T) {
	for _, input := range []any{&ScriptGenerationInput{}, &CreativeJobInput{}, &AlignmentInput{}} {
		r := httptest.NewRequest("POST", "/", strings.NewReader(`{"runtime_selection":{"actor_id":"attacker"}}`))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		if decodeProject(w, r, input) || w.Code != 400 {
			t.Fatalf("accepted execution metadata in %T", input)
		}
	}
}

func TestRuntimeAcceptanceDoesNotConvertDefaultIntoPaidSubmission(t *testing.T) {
	s := testStore(t)
	h := Handler(s, "http://localhost:5173")
	r := httptest.NewRequest("POST", "/api/v1/tasks?space_id=personal-demo", strings.NewReader(`{"prompt":"ordinary mock image","kind":"general"}`))
	r.Header.Set("Idempotency-Key", "runtime-default-test")
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	var task Task
	_ = json.Unmarshal(w.Body.Bytes(), &task)
	if task.Provider != "mock-svg" || task.RuntimeSelection != nil {
		t.Fatal("implicit paid selection")
	}
	r = httptest.NewRequest("POST", "/api/v1/tasks?space_id=personal-demo", strings.NewReader(`{"prompt":"ordinary mock image","kind":"general","model_config_id":"guess","model_config_version":1}`))
	r.Header.Set("Idempotency-Key", "runtime-demo-denied")
	r.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("demo gained model access", w.Code)
	}
}

func TestPublicExecutionResponseRemovesInternalBindingsWithoutMutation(t *testing.T) {
	binding := &RuntimeSelection{TenantID: "private-tenant", ActorID: "private-actor", ModelID: "config", Version: 1, Currency: "USD", MaximumMinor: 30}
	for _, v := range []any{Task{RuntimeSelection: binding}, ScriptGeneration{RuntimeSelection: binding}, StoryboardGeneration{RuntimeSelection: binding}, CreativeJob{RuntimeSelection: binding}, AlignmentTask{RuntimeSelection: binding}, Project{LatestGeneration: &ScriptGeneration{RuntimeSelection: binding}, LatestStoryboardGeneration: &StoryboardGeneration{RuntimeSelection: binding}}} {
		raw, err := json.Marshal(publicExecutionResponse(v))
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(raw, []byte("runtime_selection")) || bytes.Contains(raw, []byte("private-actor")) {
			t.Fatalf("binding exposed for %T", v)
		}
	}
	if binding.ActorID != "private-actor" || binding.MaximumMinor != 30 {
		t.Fatal("response mutated durable binding")
	}
}

func TestRuntimeCatalogIsTenantScopedAndDoesNotExposeCredentialMetadata(t *testing.T) {
	s := testStore(t)
	auth, err := OpenAuthStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	owner, token, session, err := auth.login("catalog-owner@example.test", "synthetic-catalog-passphrase", true)
	if err != nil {
		t.Fatal(err)
	}
	tenant := auth.spaces(owner.ID)[0]["id"]
	auth.models, err = OpenModelConfigStore(t.TempDir(), bytes.Repeat([]byte{17}, 32))
	if err != nil {
		t.Fatal(err)
	}
	m := syntheticModel()
	report, err := auth.models.update(tenant, 0, m, ModelCredentialInput{Action: "set", Secret: "synthetic-catalog-secret"})
	if err != nil {
		t.Fatal(err)
	}
	other, otherToken, _, err := auth.login("catalog-other@example.test", "synthetic-catalog-other-pass", true)
	if err != nil {
		t.Fatal(err)
	}
	h := SecureHandler(s, "http://localhost:5173", auth, AuthOptions{})
	w := authRequest(t, h, "GET", "/api/v1/runtime-models?space_id="+tenant, "", token, "", 200)
	if bytes.Contains(w.Body.Bytes(), []byte("secret")) || bytes.Contains(w.Body.Bytes(), []byte("endpoint")) || !bytes.Contains(w.Body.Bytes(), []byte(report.Entries[0].ID)) {
		t.Fatal("unsafe catalog", w.Body.String())
	}
	authRequest(t, h, "GET", "/api/v1/runtime-models?space_id="+tenant, "", otherToken, "", 403)
	member := fmt.Sprintf(`{"email":%q,"role":"editor"}`, other.Email)
	authRequest(t, h, "PUT", "/api/v1/organizations/"+tenant+"/members", member, token, session.CSRF, 200)
	authRequest(t, h, "GET", "/api/v1/runtime-models?space_id="+tenant, "", otherToken, "", 200)
	authRequest(t, h, "GET", "/api/v1/model-config?space_id="+tenant, "", otherToken, "", 403)
}

func TestRuntimeCatalogOnlyPublishesBoundedFreshQuotes(t *testing.T) {
	g, _ := executionFixture(t)
	rule := ExecutionPriceRule{Provider: "openai-text", Model: "synthetic", Capability: "text", Currency: "CNY", RequestContract: "text-64k-6000-v1", InputMinorPerMillion: "100", OutputMinorPerMillion: "200", MaximumInputBytes: 1000, Source: "synthetic quote source", ExpiresAt: time.Now().Add(time.Minute)}
	policy := &ExecutionPolicy{Prices: []ExecutionPriceRule{rule}}
	g.Quoter = policy
	entry := ModelConfigEntry{Provider: rule.Provider, Model: rule.Model, Capability: rule.Capability}
	q := runtimeQuotePreview(context.Background(), g, entry)
	if q == nil || q.Currency != "CNY" || q.MaximumMinor != 8 || q.MinorUnits != 2 || q.Source != rule.Source {
		t.Fatalf("bad original currency quote: %#v", q)
	}
	policy.Prices[0].ExpiresAt = time.Now().Add(-time.Second)
	if runtimeQuotePreview(context.Background(), g, entry) != nil {
		t.Fatal("expired price advertised")
	}
	policy.Prices[0] = rule
	policy.Prices[0].RequestContract = ""
	policy.Prices[0].RequestDigest = strings.Repeat("0", 64)
	if runtimeQuotePreview(context.Background(), g, entry) != nil {
		t.Fatal("exact-input price advertised as generic quote")
	}
}
