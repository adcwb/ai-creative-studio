package application

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestExactRequestQuoteAcrossImageAndCreativeModalities(t *testing.T) {
	for _, kind := range []string{"image", "speech", "video", "music"} {
		t.Run(kind, func(t *testing.T) {
			store := testStore(t)
			auth, err := OpenAuthStore(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			user, token, session, err := auth.login(kind+"-quote@example.test", "synthetic-quote-password", true)
			if err != nil {
				t.Fatal(err)
			}
			tenant := auth.spaces(user.ID)[0]["id"]
			auth.models, err = OpenModelConfigStore(t.TempDir(), bytes.Repeat([]byte{31}, 32))
			if err != nil {
				t.Fatal(err)
			}
			config := ModelConfigInput{Provider: "openai-images", Model: "synthetic-image", Capability: "image", Region: "global", Enabled: true, Lifecycle: "active"}
			if kind == "speech" {
				config.Provider = "minimax-speech"
				config.Model = "synthetic-tts"
				config.Capability = "speech"
			}
			if kind == "music" {
				config.Provider = "minimax-music"
				config.Model = "music-3.0"
				config.Capability = "music"
			}
			if kind == "video" {
				config.Provider = "ark-video"
				config.Model = "synthetic-video"
				config.Capability = "video"
				config.Region = "cn-beijing"
			}
			report, err := auth.models.update(tenant, 0, config, ModelCredentialInput{Action: "set", Secret: "synthetic-never-transmitted"})
			if err != nil {
				t.Fatal(err)
			}
			entry := report.Entries[0]
			selection := RuntimeSelection{TenantID: tenant, ActorID: user.ID, ModelID: entry.ID, Version: 1, Capability: config.Capability}
			preview := RuntimeQuoteInput{Operation: "image", ModelConfigID: entry.ID, ModelConfigVersion: 1}
			payload := map[string]any{"prompt": "Synthetic quoted request", "kind": "general", "ratio": "1:1", "style": "natural"}
			target := "/api/v1/tasks?space_id=" + tenant
			if kind != "image" {
				project, _, e := store.CreateProject(tenant, "quote-project-key", ProjectInput{Title: "Synthetic quote story", Brief: "Synthetic local preview only."})
				if e != nil {
					t.Fatal(e)
				}
				preview.Operation = "creative"
				preview.ProjectID = project.ID
				payload = map[string]any{"prompt": "Synthetic quoted request", "kind": kind, "expected_version": project.Version}
				target = "/api/v1/projects/" + project.ID + "/creative-jobs?space_id=" + tenant
				if kind == "speech" {
					selection.Voice = "synthetic-voice"
					preview.Voice = selection.Voice
				}
			}
			preview.Input, _ = json.Marshal(payload)
			expected, err := store.buildQuoteExecutionRequest(tenant, preview, selection, entry)
			if err != nil {
				t.Fatal(err)
			}
			policy := &ExecutionPolicy{Prices: []ExecutionPriceRule{{Provider: entry.Provider, Model: entry.Model, Capability: entry.Capability, RequestDigest: ExecutionRequestDigest(expected), Currency: "USD", MaximumMinor: 15, MaximumInputBytes: 8192, Source: "synthetic fixed request fixture", ExpiresAt: time.Now().Add(time.Minute)}}}
			if kind == "music" {
				policy.MusicEntitlements = []MusicEntitlement{{TenantID: tenant, ModelConfigID: entry.ID, ModelConfigVersion: 1, Model: entry.Model, Contract: "minimax-existing-paid-account-v1", Source: "synthetic eligibility fixture", ExpiresAt: time.Now().Add(time.Hour)}}
			}
			governor, _ := executionFixture(t)
			governor.Quoter = policy
			ConfigureExecutionGovernance(store, governor)
			handler := SecureHandler(store, "http://localhost:5173", auth, AuthOptions{})
			post := func(path string, body any, want int) *httptest.ResponseRecorder {
				t.Helper()
				raw, _ := json.Marshal(body)
				r := httptest.NewRequest("POST", path, bytes.NewReader(raw))
				r.Header.Set("Content-Type", "application/json")
				r.Header.Set("Origin", "http://localhost:5173")
				r.Header.Set("X-CSRF-Token", session.CSRF)
				r.Header.Set("Idempotency-Key", "synthetic-quote-accept")
				r.AddCookie(&http.Cookie{Name: "studio_session", Value: token})
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, r)
				if w.Code != want {
					t.Fatalf("%s got %d want %d: %s", path, w.Code, want, w.Body.String())
				}
				return w
			}
			if kind == "music" {
				approved := policy.MusicEntitlements
				policy.MusicEntitlements = nil
				post("/api/v1/runtime-quote?space_id="+tenant, preview, 409)
				policy.MusicEntitlements = approved
			}
			response := post("/api/v1/runtime-quote?space_id="+tenant, preview, 200)
			var quote RuntimeRequestQuote
			if json.Unmarshal(response.Body.Bytes(), &quote) != nil || quote.MaximumMinor != 15 || quote.RequestDigest != ExecutionRequestDigest(expected) {
				t.Fatal("quote differs from worker request")
			}
			entries, e := governor.Ledger.Entries(tenant, "USD")
			if e != nil || len(entries) != 0 {
				t.Fatal("preview changed ledger")
			}
			payload["model_config_id"] = entry.ID
			payload["model_config_version"] = 1
			payload["budget_currency"] = "USD"
			payload["budget_maximum_minor"] = 15
			payload["quote_request_sha256"] = quote.RequestDigest
			if preview.Voice != "" {
				payload["voice"] = preview.Voice
			}
			payload["prompt"] = "Changed after preview"
			post(target, payload, 409)
			payload["prompt"] = "Synthetic quoted request"
			post(target, payload, 202)
			// A changed request cannot get a quote from an exact-input policy.
			changed := map[string]any{}
			_ = json.Unmarshal(preview.Input, &changed)
			changed["prompt"] = "Changed after preview"
			preview.Input, _ = json.Marshal(changed)
			post("/api/v1/runtime-quote?space_id="+tenant, preview, 409)
		})
	}
}

func TestImageQuoteDigestIgnoresConsentAndInternalIdentity(t *testing.T) {
	in, _ := Normalize(Input{Prompt: "Synthetic request", Kind: "general"})
	task := Task{Provider: "openai-images", Model: "synthetic", Input: in, RuntimeSelection: &RuntimeSelection{Voice: ""}}
	before := ExecutionRequestDigest(imageExecutionRequest(task))
	task.Input.ModelRequest = ModelRequest{ModelConfigID: "config", ModelConfigVersion: 5, BudgetCurrency: "JPY", BudgetMaximumMinor: 100, QuoteRequestDigest: strings.Repeat("a", 64)}
	task.RuntimeSelection.RequestDigest = before
	task.RuntimeSelection.ActorID = "another-actor"
	task.RuntimeSelection.MaximumMinor = 100
	if got := ExecutionRequestDigest(imageExecutionRequest(task)); got != before {
		t.Fatal("quote digest has circular consent dependency")
	}
	task.Ratio = "16:9"
	if ExecutionRequestDigest(imageExecutionRequest(task)) == before {
		t.Fatal("different wire parameters reused quote")
	}
	// Confirmation cannot be reused with a different voice.
	r := imageExecutionRequest(task)
	first := ExecutionRequestDigest(r)
	r.Selection.Voice = "different"
	if ExecutionRequestDigest(r) == first {
		t.Fatal("voice not bound")
	}
}

func TestAlignmentRequestDigestBindsAudioAndTiming(t *testing.T) {
	task := AlignmentTask{Mode: "asr", Provider: "openai-asr", Model: "synthetic", AudioDuration: 4, Duration: 3, TrimStart: 1, RuntimeSelection: &RuntimeSelection{}}
	before := ExecutionRequestDigest(alignmentExecutionRequest(task, []byte("synthetic wav A"), "audio/wav"))
	if before == ExecutionRequestDigest(alignmentExecutionRequest(task, []byte("synthetic wav B"), "audio/wav")) {
		t.Fatal("audio contents not bound")
	}
	task.TrimStart = 0
	if before == ExecutionRequestDigest(alignmentExecutionRequest(task, []byte("synthetic wav A"), "audio/wav")) {
		t.Fatal("trim timing not bound")
	}
}
