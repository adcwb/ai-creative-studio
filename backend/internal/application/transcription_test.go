package application

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTextTranscriptionWireBoundsAndNoInventedTiming(t *testing.T) {
	calls := 0
	body := `{"text":"Synthetic words.","languages":[{"code":"en"}]}`
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if err := r.ParseMultipartForm(25 << 20); err != nil {
			t.Fatal(err)
		}
		defer r.MultipartForm.RemoveAll()
		if r.FormValue("model") != "gpt-transcribe" || r.FormValue("response_format") != "json" || len(r.MultipartForm.Value["timestamp_granularities[]"]) != 0 {
			t.Error("incorrect text-only wire contract")
		}
		_, _ = w.Write([]byte(body))
	}))
	defer s.Close()
	p := newOpenAIAlignmentProvider("synthetic", "gpt-transcribe", s.URL, s.Client())
	result, err := p.Transcribe(context.Background(), syntheticWAV(1), "audio/wav", 30)
	if err != nil || result.Transcript != "Synthetic words." || len(result.Cues) != 0 || result.TimingBasis != "transcript-only" {
		t.Fatal(result, err)
	}
	if _, err = p.Transcribe(context.Background(), syntheticWAV(1), "audio/wav", 30.01); err == nil || calls != 1 {
		t.Fatal("duration limit not checked before request")
	}
	for _, bad := range []string{`{}`, `{"text":" "}`, `{"text":123}`, `{"text":` + string(mustJSON(strings.Repeat("x", 2001))) + `}`} {
		body = bad
		if _, err = p.Transcribe(context.Background(), syntheticWAV(1), "audio/wav", 1); err == nil {
			t.Fatal("invalid transcript accepted")
		}
	}
	if validAlignmentModeModel("asr", "gpt-transcribe") || validAlignmentModeModel("transcription", "whisper-1") {
		t.Fatal("model substitution")
	}
}
func mustJSON(value any) []byte { b, _ := json.Marshal(value); return b }

func TestTextTranscriptionPersistenceAndNoSubtitleProvenance(t *testing.T) {
	m, p := alignmentFixture(t)
	defer m.close()
	a := m.alignment
	task := AlignmentTask{ID: mediaID(), ProjectID: "p", SpaceID: "s", ClipID: p.Timeline.Clips[0].ID, SourceVersion: p.Version, Mode: "transcription", Status: "succeeded", Transcript: "Synthetic text.", Cues: []SubtitleCue{}, Warnings: []string{}, TimingBasis: "transcript-only"}
	a.mu.Lock()
	d := a.cloneLocked()
	d.Tasks[task.ID] = task
	err := a.commitLocked(d)
	a.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := openAlignmentStore(m.dir)
	if err != nil {
		t.Fatal(err)
	}
	reopened.close()
	if len(reopened.list("p", "another-tenant")) != 0 || reopened.list("p", "s")[0].Transcript != task.Transcript {
		t.Fatal("isolation or persistence")
	}
	timeline := p.Timeline
	timeline.Clips[0].SubtitleAlignmentID = task.ID
	timeline.Clips[0].SubtitleSource = "manual"
	if _, err = m.SaveTimeline("p", "s", "transcript-provenance", p.Version, timeline); err == nil {
		t.Fatal("transcript accepted as timestamp provenance")
	}
	for _, invalid := range []AlignmentTask{func() AlignmentTask { v := task; v.Transcript = ""; return v }(), func() AlignmentTask {
		v := task
		v.Cues = []SubtitleCue{{Start: 0, End: 1, Text: "invented"}}
		return v
	}()} {
		broken := alignmentDisk{Version: 1, Tasks: map[string]AlignmentTask{task.ID: invalid}, Receipts: map[string]alignmentReceipt{}}
		dir := t.TempDir()
		if err = os.WriteFile(filepath.Join(dir, "alignments.json"), mustJSON(broken), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err = openAlignmentStore(dir); err == nil {
			t.Fatal("invalid persisted transcript accepted")
		}
	}
}

// Real managed runtime, synthetic transport only: review sees the actual text,
// dispatch remains unknown without billing evidence, revoked/cross-tenant reuse is denied.
func TestTextTranscriptionGovernedRuntime(t *testing.T) {
	runtime, _, input := runtimeFixture(t)
	input.Provider = "openai-asr"
	input.Model = "gpt-transcribe"
	input.Capability = "asr"
	report, err := runtime.auth.models.update("tenant-a", 1, input, ModelCredentialInput{Action: "set", Secret: "synthetic-transcription-key"})
	if err != nil {
		t.Fatal(err)
	}
	selection, _, err := runtime.Resolve(context.Background(), "tenant-a", "actor-a", report.Entries[0].ID, "asr")
	if err != nil {
		t.Fatal(err)
	}
	ledger, err := OpenBudgetLedger(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = ledger.SetLimit("tenant-a", "USD", 100); err != nil {
		t.Fatal(err)
	}
	moderator := &transcriptReview{}
	g := &ExecutionGovernor{Ledger: ledger, Quoter: testExecutionQuote{"USD", 30}, Moderator: moderator}
	runtime.authorize = g.AuthorizeRuntime
	selection.Currency = "USD"
	selection.MaximumMinor = 30
	request := alignmentExecutionRequest(AlignmentTask{ID: "synthetic", SpaceID: "tenant-a", Provider: "openai-asr", Model: "gpt-transcribe", Mode: "transcription", AudioDuration: 1, Duration: 1, RuntimeSelection: &selection}, syntheticWAV(1), "audio/wav")
	calls := 0
	err = g.ExecuteRuntime(context.Background(), runtime, request, func(ctx context.Context, p RuntimeProviders) error {
		client := p.Alignment.(*OpenAIAlignmentProvider).client
		transport := client.Transport.(runtimeTransport)
		transport.base = runtimeFixtureTransport(func(*http.Request) (*http.Response, error) {
			calls++
			return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"text":"Reviewed transcript."}`))}, nil
		})
		client.Transport = transport
		result, e := p.Alignment.Transcribe(ctx, syntheticWAV(1), "audio/wav", 1)
		if e != nil {
			return e
		}
		raw, _ := json.Marshal(result)
		return g.ReviewResult(ctx, ExecutionRequest{ID: request.ID, Tenant: "tenant-a", Selection: &selection, Provider: request.Provider, Model: request.Model, Capability: "text", Text: string(raw)})
	})
	if err != nil || calls != 1 || !moderator.seen {
		t.Fatal(err, calls, moderator.seen)
	}
	entries, err := ledger.Entries("tenant-a", "USD")
	if err != nil || len(entries) != 1 || entries[0].State != "unknown" {
		t.Fatal(entries, err)
	}
	other := selection
	other.TenantID = "tenant-b"
	if runtime.Execute(context.Background(), other, func(context.Context, RuntimeProviders) error { t.Fatal("cross tenant dispatch"); return nil }) == nil {
		t.Fatal("cross tenant accepted")
	}
	if _, err = runtime.auth.models.update("tenant-a", 2, input, ModelCredentialInput{Action: "revoke"}); err != nil {
		t.Fatal(err)
	}
	if runtime.Execute(context.Background(), selection, func(context.Context, RuntimeProviders) error { t.Fatal("revoked dispatch"); return nil }) == nil {
		t.Fatal("revoked accepted")
	}
}

type transcriptReview struct{ seen bool }

func (m *transcriptReview) ReviewExecution(_ context.Context, r ExecutionRequest) (ModerationDecision, error) {
	if r.Capability == "text" {
		m.seen = strings.Contains(r.Text, "Reviewed transcript.")
	}
	return ModerationDecision{Provider: "fixture", Model: "synthetic", Status: "allow", CheckedAt: time.Now()}, nil
}

func TestTextTranscriptionQuoteBindsModeAndAudioLimit(t *testing.T) {
	store := testStore(t)
	project, _, err := store.CreateProject("tenant-a", "transcript-project", ProjectInput{Title: "Synthetic transcription", Brief: "Synthetic local audio only"})
	if err != nil {
		t.Fatal(err)
	}
	state, err := store.media.Demo(project.ID, "tenant-a", "transcript-demo", 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	state.Timeline.Clips[0].AudioAssetID = state.Assets[0].ID
	state, err = store.media.SaveTimeline(project.ID, "tenant-a", "transcript-timeline", state.Version, state.Timeline)
	if err != nil {
		t.Fatal(err)
	}
	input := AlignmentInput{Mode: "transcription", ClipID: state.Timeline.Clips[0].ID, AudioAssetID: state.Assets[0].ID, ExpectedVersion: state.Version}
	preview := RuntimeQuoteInput{Operation: "alignment", ProjectID: project.ID, Input: mustJSON(input)}
	selection := RuntimeSelection{TenantID: "tenant-a", Capability: "asr"}
	entry := ModelConfigEntry{Provider: "openai-asr", Model: "gpt-transcribe", Capability: "asr"}
	request, err := store.buildQuoteExecutionRequest("tenant-a", preview, selection, entry)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(request.Parameters), "transcription") {
		t.Fatal("mode absent from quote")
	}
	input.Mode = "asr"
	preview.Input = mustJSON(input)
	if _, err = store.buildQuoteExecutionRequest("tenant-a", preview, selection, entry); err == nil {
		t.Fatal("plain model accepted as timing model")
	}
	input.Mode = "transcription"
	preview.Input = mustJSON(input)
	if _, err = store.buildQuoteExecutionRequest("other-tenant", preview, selection, entry); err == nil {
		t.Fatal("cross tenant audio quote")
	}
	store.media.mu.Lock()
	key := mediaKey("tenant-a", project.ID)
	p := store.media.data.Projects[key]
	for i := range p.Assets {
		if p.Assets[i].ID == input.AudioAssetID {
			p.Assets[i].Duration = 30.01
		}
	}
	store.media.data.Projects[key] = p
	store.media.mu.Unlock()
	if _, err = store.buildQuoteExecutionRequest("tenant-a", preview, selection, entry); err == nil {
		t.Fatal("overlong input quoted")
	}
}

func TestTextTranscriptionHTTPQuoteAndAcceptance(t *testing.T) {
	store := testStore(t)
	auth, err := OpenAuthStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	user, token, session, err := auth.login("transcription-http@example.test", "synthetic-password", true)
	if err != nil {
		t.Fatal(err)
	}
	tenant := auth.spaces(user.ID)[0]["id"]
	auth.models, err = OpenModelConfigStore(t.TempDir(), bytes.Repeat([]byte{29}, 32))
	if err != nil {
		t.Fatal(err)
	}
	report, err := auth.models.update(tenant, 0, ModelConfigInput{Provider: "openai-asr", Model: "gpt-transcribe", Capability: "asr", Region: "global", Enabled: true, Lifecycle: "active"}, ModelCredentialInput{Action: "set", Secret: "synthetic-never-sent"})
	if err != nil {
		t.Fatal(err)
	}
	project, _, err := store.CreateProject(tenant, "transcription-http-project", ProjectInput{Title: "Synthetic HTTP transcription", Brief: "Synthetic no network test"})
	if err != nil {
		t.Fatal(err)
	}
	media, err := store.media.Demo(project.ID, tenant, "transcription-http-demo", 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	media.Timeline.Clips[0].AudioAssetID = media.Assets[0].ID
	media, err = store.media.SaveTimeline(project.ID, tenant, "transcription-http-timeline", media.Version, media.Timeline)
	if err != nil {
		t.Fatal(err)
	}
	entry := report.Entries[0]
	selection := RuntimeSelection{TenantID: tenant, ActorID: user.ID, ModelID: entry.ID, Version: 1, Capability: "asr"}
	input := AlignmentInput{Mode: "transcription", ClipID: media.Timeline.Clips[0].ID, AudioAssetID: media.Assets[0].ID, ExpectedVersion: media.Version}
	preview := RuntimeQuoteInput{Operation: "alignment", ProjectID: project.ID, ModelConfigID: entry.ID, ModelConfigVersion: 1, Input: mustJSON(input)}
	expected, err := store.buildQuoteExecutionRequest(tenant, preview, selection, entry)
	if err != nil {
		t.Fatal(err)
	}
	governor, _ := executionFixture(t)
	governor.Quoter = &ExecutionPolicy{Prices: []ExecutionPriceRule{{Provider: entry.Provider, Model: entry.Model, Capability: "asr", RequestDigest: ExecutionRequestDigest(expected), Currency: "USD", MaximumMinor: 15, MaximumInputBytes: 1 << 20, Source: "synthetic exact transcription", ExpiresAt: time.Now().Add(time.Minute)}}}
	governor.Moderator = testExecutionModerator{false} // acceptance only; never sends to supplier
	if err = governor.Ledger.SetLimit(tenant, "USD", 100); err != nil {
		t.Fatal(err)
	}
	ConfigureExecutionGovernance(store, governor)
	handler := SecureHandler(store, "http://localhost:5173", auth, AuthOptions{})
	post := func(path string, body any, want int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest("POST", path, bytes.NewReader(mustJSON(body)))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", "http://localhost:5173")
		r.Header.Set("X-CSRF-Token", session.CSRF)
		r.Header.Set("Idempotency-Key", "synthetic-transcript-http")
		r.AddCookie(&http.Cookie{Name: "studio_session", Value: token})
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("got %d want %d: %s", w.Code, want, w.Body.String())
		}
		return w
	}
	response := post("/api/v1/runtime-quote?space_id="+tenant, preview, 200)
	var quote RuntimeRequestQuote
	if err = json.Unmarshal(response.Body.Bytes(), &quote); err != nil {
		t.Fatal(err)
	}
	input.ModelConfigID = entry.ID
	input.ModelConfigVersion = 1
	input.BudgetCurrency = "USD"
	input.BudgetMaximumMinor = 15
	input.QuoteRequestDigest = quote.RequestDigest
	response = post("/api/v1/projects/"+project.ID+"/media/alignments?space_id="+tenant, input, 202)
	var task AlignmentTask
	if err = json.Unmarshal(response.Body.Bytes(), &task); err != nil || task.Mode != "transcription" || len(task.Cues) != 0 {
		t.Fatal(task, err)
	}
}
