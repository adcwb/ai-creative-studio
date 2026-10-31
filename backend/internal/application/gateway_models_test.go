package application

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func gatewayFixture(t *testing.T, endpoint string) (*ModelRuntime, AIGateway, GatewayModel) {
	t.Helper()
	s, e := OpenModelConfigStore(t.TempDir(), bytes.Repeat([]byte{11}, 32))
	if e != nil {
		t.Fatal(e)
	}
	report, e := s.saveGateway("tenant-a", 0, AIGateway{Name: "Gateway A", BaseURL: endpoint, Enabled: true, TimeoutSeconds: 30}, ModelCredentialInput{Action: "set", Secret: "synthetic-gateway-key"})
	if e != nil {
		t.Fatal(e)
	}
	gateway := report.Gateways[0]
	report, e = s.saveGatewayModel("tenant-a", 0, GatewayModel{GatewayID: gateway.ID, Name: "Text model", Model: "vendor/shared-model", Capabilities: []string{"text"}, Enabled: true, MaxOutputTokens: 3000, JSONOutput: true, Currency: "USD", MaximumMinor: 10, PriceSource: "Synthetic test ceiling"})
	if e != nil {
		t.Fatal(e)
	}
	model := report.Models[0]
	auth := &AuthStore{models: s, data: authSnapshot{Organizations: map[string]AuthOrganization{"tenant-a": {Members: map[string]string{"actor-a": "editor"}}}}}
	return NewModelRuntime(auth, func(context.Context, RuntimeSelection) error { return nil }), gateway, model
}
func TestGatewayCredentialsPersistenceCASAndFeatureIsolation(t *testing.T) {
	runtime, g, m := gatewayFixture(t, "https://gateway.example.com/v1")
	s := runtime.auth.models
	_, e := s.saveFeatureBinding("tenant-a", "owner", 0, FeatureBinding{FeatureKey: "storyboard_planning", ModelConfigID: m.ID, Enabled: true})
	if e != nil {
		t.Fatal(e)
	}
	if _, ok := runtime.authorizeFeature("tenant-a", "storyboard_planning", m.ID); !ok {
		t.Fatal("correct feature denied")
	}
	if _, ok := runtime.authorizeFeature("tenant-a", "shot_image", m.ID); ok {
		t.Fatal("capability mismatch accepted")
	}
	if _, e = s.saveGateway("tenant-a", 0, g, ModelCredentialInput{Action: "keep"}); !errors.Is(e, errModelConflict) {
		t.Fatal("stale update accepted", e)
	}
	changed := g
	changed.BaseURL = "https://other.example.com/v1"
	if _, e = s.saveGateway("tenant-a", g.Version, changed, ModelCredentialInput{Action: "keep"}); !errors.Is(e, errModelInput) {
		t.Fatal("key moved to another origin", e)
	}
	if _, e = s.saveGatewayModel("tenant-b", m.Version, m); !errors.Is(e, ErrNotFound) {
		t.Fatal("cross-tenant model updated", e)
	}
	if _, e = s.saveFeatureBinding("tenant-b", "actor-b", 0, FeatureBinding{FeatureKey: "script_generation", ModelConfigID: m.ID, Enabled: true}); !errors.Is(e, errModelInput) {
		t.Fatal("cross-tenant feature accepted", e)
	}
	disk, e := os.ReadFile(s.path)
	if e != nil || bytes.Contains(disk, []byte("synthetic-gateway-key")) {
		t.Fatal("plaintext credential persisted")
	}
	restored, e := OpenModelConfigStore(filepath.Dir(s.path), bytes.Repeat([]byte{11}, 32))
	if e != nil {
		t.Fatal(e)
	}
	secret, e := restored.gatewaySecretLocked("tenant-a", restored.data.Tenants["tenant-a"].Gateways[g.ID])
	if e != nil || string(secret) != "synthetic-gateway-key" {
		t.Fatal("credential recovery failed")
	}
	clear(secret)
	rotated, report, e := rotateModelSnapshot(s.data, bytes.Repeat([]byte{11}, 32), bytes.Repeat([]byte{12}, 32), bytes.Repeat([]byte{12}, 32))
	if e != nil || report.Credentials != 1 || rotated.Tenants["tenant-a"].Gateways[g.ID].Ciphertext == s.data.Tenants["tenant-a"].Gateways[g.ID].Ciphertext {
		t.Fatal("gateway key rotation failed", e)
	}
}
func TestGatewayActualHTTPPayloadUsageAndNoRetry(t *testing.T) {
	var calls atomic.Int32
	var reject atomic.Bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer synthetic-gateway-key" {
			t.Error("wrong destination or credentials")
		}
		var body map[string]any
		if json.NewDecoder(r.Body).Decode(&body) != nil || body["model"] != "vendor/shared-model" || body["max_completion_tokens"] != float64(3000) {
			t.Error("model/parameters not applied")
		}
		if reject.Load() {
			w.WriteHeader(429)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("x-request-id", "synthetic-request")
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]string{"content": `{"summary":"Synthetic creative summary","questions":[],"suggestions":[]}`}}}, "usage": map[string]int{"prompt_tokens": 123, "completion_tokens": 45}})
	}))
	defer upstream.Close()
	u, _ := url.Parse(upstream.URL)
	t.Setenv("MODEL_GATEWAY_ALLOWED_HOSTS", u.Host)
	runtime, _, m := gatewayFixture(t, upstream.URL+"/v1")
	selection, _, e := runtime.Resolve(context.Background(), "tenant-a", "actor-a", m.ID, "text")
	if e != nil {
		t.Fatal(e)
	}
	selection.FeatureKey = "script_generation"
	run := func(ctx context.Context, p RuntimeProviders) error {
		_, e := p.Creative.Analyze(ctx, ProjectInput{Title: "Synthetic title", Brief: "Synthetic local-only creative description"})
		return e
	}
	if e = runtime.Execute(context.Background(), selection, run); e != nil || calls.Load() != 1 {
		t.Fatal("gateway call failed", e, calls.Load())
	}
	usage, e := runtime.auth.models.usage.report("tenant-a", httptest.NewRequest("GET", "/model-usage", nil))
	if e != nil || usage.Total != 1 || usage.Items[0].State != "succeeded" || usage.Items[0].InputTokens == nil || *usage.Items[0].InputTokens != 123 || usage.Items[0].OutputTokens == nil || *usage.Items[0].OutputTokens != 45 {
		t.Fatal("usage not captured", usage, e)
	}
	reject.Store(true)
	if e = runtime.Execute(context.Background(), selection, run); e == nil || calls.Load() != 2 {
		t.Fatal("429 retried or treated as success", e, calls.Load())
	}
	other, _ := runtime.auth.models.usage.report("tenant-b", httptest.NewRequest("GET", "/model-usage", nil))
	if other.Total != 0 {
		t.Fatal("cross-tenant usage leaked")
	}
}
func TestGatewayBindingChangesOnlyAffectNewTasksAndRevocation(t *testing.T) {
	runtime, g, m := gatewayFixture(t, "https://gateway.example.com/v1")
	s := runtime.auth.models
	selection, _, e := runtime.Resolve(context.Background(), "tenant-a", "actor-a", m.ID, "text")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.saveFeatureBinding("tenant-a", "owner", 0, FeatureBinding{FeatureKey: "script_generation", ModelConfigID: m.ID, Enabled: true}); e != nil {
		t.Fatal(e)
	}
	if e = runtime.check(context.Background(), selection); e != nil {
		t.Fatal("binding edit invalidated accepted selection", e)
	}
	if _, e = s.saveGateway("tenant-a", 0, AIGateway{Name: "Unrelated", BaseURL: "https://other.example.com/v1", Enabled: true, TimeoutSeconds: 30}, ModelCredentialInput{Action: "keep"}); e != nil {
		t.Fatal(e)
	}
	if e = runtime.check(context.Background(), selection); e != nil {
		t.Fatal("unrelated gateway invalidated selection", e)
	}
	if _, e = s.saveGateway("tenant-a", g.Version, g, ModelCredentialInput{Action: "revoke"}); e != nil {
		t.Fatal(e)
	}
	if e = runtime.check(context.Background(), selection); e == nil {
		t.Fatal("revoked connection allowed")
	}
}
func TestGatewayManagementRejectsEditorAndWrongFeature(t *testing.T) {
	runtime, _, m := gatewayFixture(t, "https://gateway.example.com/v1")
	s := runtime.auth.models
	rec := httptest.NewRecorder()
	s.serveGatewayManagement(rec, httptest.NewRequest("GET", "/api/v1/model-management", nil), authScope{SpaceID: "tenant-a", UserID: "actor-a", Role: "editor"})
	if rec.Code != 403 {
		t.Fatal("editor read admin configuration")
	}
	if _, e := s.saveFeatureBinding("tenant-a", "actor-a", 0, FeatureBinding{FeatureKey: "shot_video", ModelConfigID: m.ID, Enabled: true}); !errors.Is(e, errModelInput) {
		t.Fatal("text model bound to video")
	}
	for _, raw := range []string{"https://user:password@example.com/v1", "https://example.com/v1?key=bad", "https://example.com/a/../v1"} {
		if _, e := normalizeGatewayURL(raw); e == nil {
			t.Fatal("invalid URL accepted")
		}
	}
	normalized, e := normalizeGatewayURL("https://EXAMPLE.com/")
	if e != nil || normalized != "https://example.com/v1" {
		t.Fatal("base URL normalization", normalized, e)
	}
	if validGatewayModelID("../model?key=x") || !validGatewayModelID("vendor/model-alias") {
		t.Fatal("model field validation")
	}
	result, _ := s.managementReport("tenant-a")
	wire, _ := json.Marshal(result)
	if strings.Contains(string(wire), "synthetic-gateway-key") || strings.Contains(string(wire), "ciphertext") {
		t.Fatal("management leaked key")
	}
}

func TestGatewayFeatureLockCannotBeBypassedWithLegacyModel(t *testing.T) {
	runtime, _, model := gatewayFixture(t, "https://gateway.example.com/v1")
	s := runtime.auth.models
	if _, e := s.saveFeatureBinding("tenant-a", "owner", 0, FeatureBinding{FeatureKey: "script_generation", ModelConfigID: model.ID, Enabled: true, AllowOverride: false}); e != nil {
		t.Fatal(e)
	}
	report, e := s.update("tenant-a", s.data.Tenants["tenant-a"].Version, syntheticModel(), ModelCredentialInput{Action: "set", Secret: "synthetic-legacy-key"})
	if e != nil {
		t.Fatal(e)
	}
	if _, allowed := runtime.authorizeFeature("tenant-a", "script_generation", report.Entries[0].ID); allowed {
		t.Fatal("legacy model bypassed feature routing")
	}
	if _, allowed := runtime.authorizeFeature("tenant-a", "storyboard_planning", report.Entries[0].ID); !allowed {
		t.Fatal("unconfigured legacy feature was changed")
	}
}
