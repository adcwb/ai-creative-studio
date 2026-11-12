package application

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adcwb/ai-creative-studio/backend/internal/platform/diagnostics"
	"github.com/adcwb/ai-creative-studio/backend/internal/platform/tracing"
)

func diagnosticLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var b bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&b, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })
	return &b
}
func noPrivateDiagnostics(t *testing.T, values ...string) {
	t.Helper()
	for _, v := range values {
		for _, secret := range []string{"sk-private-token", "private-prompt", "user@example.invalid", "private-cookie", "private-url"} {
			if strings.Contains(v, secret) {
				t.Fatalf("diagnostics exposed %s", secret)
			}
		}
	}
}

func TestDiagnosticHTTPPreservesLegacyFieldsAndServerIdentity(t *testing.T) {
	logs := diagnosticLogs(t)
	h := WithDiagnostics(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { problem(w, 409, "version_conflict", "版本已变化") }))
	ids := map[string]bool{}
	for range 2 {
		r := httptest.NewRequest("POST", "/private-url?prompt=private-prompt", strings.NewReader("user@example.invalid"))
		r.Header.Set("X-Request-ID", "sk-private-token")
		r.Header.Set("Cookie", "private-cookie")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)

		var body map[string]map[string]any
		if json.Unmarshal(w.Body.Bytes(), &body) != nil {
			t.Fatal("invalid error JSON")
		}
		e := body["error"]
		id := w.Header().Get("X-Request-ID")
		if w.Code != 409 || e["code"] != "version_conflict" || e["message"] != "版本已变化" || e["request_id"] != id || len(id) != 32 || ids[id] || e["category"] != "conflict" || e["suggestion"] == "" || e["correlation_id"] != w.Header().Get("X-Correlation-ID") {
			t.Fatal("legacy or diagnostic contract changed", w.Body.String())
		}
		ids[id] = true
		noPrivateDiagnostics(t, w.Body.String(), logs.String())
	}
}
func TestDiagnosticHTTPPanicIsSafeAndAuditFinishes(t *testing.T) {
	logs := diagnosticLogs(t)
	dir := t.TempDir()
	ops, err := OpenOperations(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer ops.Close()
	h := ops.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		SetAuditIdentity(r.Context(), "private-actor", "private-tenant", "owner")
		if BeginAuditMutation(r.Context()) != nil {
			t.Fatal("audit failed")
		}
		panic("sk-private-token private-prompt user@example.invalid")
	}))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "/private-url", nil))
	if w.Code != 500 || !strings.Contains(w.Body.String(), `"code":"internal_panic"`) {
		t.Fatal(w.Code, w.Body.String())
	}
	noPrivateDiagnostics(t, w.Body.String(), logs.String())
	if strings.Contains(logs.String(), "private-actor") || strings.Contains(logs.String(), "private-tenant") {
		t.Fatal("console included audit identities")
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "audit.jsonl"))
	if !strings.Contains(string(raw), `"status":500`) || strings.Count(string(raw), "\n") != 2 {
		t.Fatal("panic audit not finalized", string(raw))
	}
}
func TestDiagnosticPanicAfterCommitAbortsWithoutAppendingJSON(t *testing.T) {
	logs := diagnosticLogs(t)
	w := httptest.NewRecorder()
	h := WithDiagnostics(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "safe-partial")
		panic("sk-private-token")
	}))
	var caught any
	func() { defer func() { caught = recover() }(); h.ServeHTTP(w, httptest.NewRequest("GET", "/", nil)) }()
	if caught != http.ErrAbortHandler || w.Body.String() != "safe-partial" {
		t.Fatal("committed stream was rewritten")
	}
	noPrivateDiagnostics(t, logs.String())
}
func TestDiagnosticUpstreamMetadataIsSafeAndCorrelated(t *testing.T) {
	logs := diagnosticLogs(t)
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		io.Copy(io.Discard, r.Body)
		w.WriteHeader(400)
		fmt.Fprint(w, `{"code":"InvalidParameter","message":"sk-private-token private-prompt user@example.invalid","request_id":"f63f4568-9026-4b82-b076-05f92f92f711"}`)
	}))
	defer srv.Close()
	h := WithDiagnostics(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := bailianAPI{api: newCreativeProvider("synthetic", srv.URL, srv.Client())}
		_, e := p.request(r.Context(), http.MethodPost, bailianMultimodal, map[string]string{"model": "wan2.7-image"}, 1024)
		if e == nil {
			t.Fatal("expected rejection")
		}
		problem(w, 502, "provider_rejected", "上游拒绝请求")
	}))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "/", nil))
	var v struct {
		Error struct {
			Upstream   diagnostics.Upstream `json:"upstream"`
			CauseKnown bool                 `json:"cause_known"`
		}
	}
	if json.Unmarshal(w.Body.Bytes(), &v) != nil || v.Error.Upstream.HTTPStatus != 400 || v.Error.Upstream.Code != "InvalidParameter" || v.Error.Upstream.RequestID != "f63f4568-9026-4b82-b076-05f92f92f711" || v.Error.CauseKnown || calls != 1 {
		t.Fatal("lost safe upstream metadata", w.Body.String(), calls)
	}
	noPrivateDiagnostics(t, w.Body.String(), logs.String())
}
func TestDiagnosticObserverDoesNotReadOrRetryForLogging(t *testing.T) {
	diagnosticLogs(t)
	r := &countingDiagnosticBody{Reader: strings.NewReader(strings.Repeat("private-prompt", 10000))}
	b := &diagnosticBody{ReadCloser: r, ctx: context.Background(), status: 400}
	buf := make([]byte, 128)
	if _, e := b.Read(buf); e != nil {
		t.Fatal(e)
	}
	before := r.read
	if b.Close() != nil || r.read != before {
		t.Fatal("logging consumed response data")
	}
}

type countingDiagnosticBody struct {
	io.Reader
	read int
}

func (b *countingDiagnosticBody) Read(p []byte) (int, error) {
	n, e := b.Reader.Read(p)
	b.read += n
	return n, e
}
func (*countingDiagnosticBody) Close() error { return nil }

type diagnosticPanicProvider struct{}

func (diagnosticPanicProvider) Generate(context.Context, Input) (GenerationResult, error) {
	panic("sk-private-token private-prompt user@example.invalid")
}
func TestDiagnosticWorkflowAndTaskResponsesDoNotExposePanic(t *testing.T) {
	logs := diagnosticLogs(t)
	parent := "00-1234567890abcdef1234567890abcdef-1234567890abcdef-00"
	p, e := NewPipeline(context.Background(), diagnosticPanicProvider{})
	if e != nil {
		t.Fatal(e)
	}
	_, e = p.Invoke(tracing.WithParent(context.Background(), parent), Input{Kind: "general", Prompt: "synthetic"})
	if !errors.Is(e, diagnostics.ErrPanic) {
		t.Fatal("panic escaped the guard", e)
	}
	code, message := safeFailure(e, "gateway-images")
	if code != "execution_interrupted" {
		t.Fatal(code)
	}
	raw, e := json.Marshal(publicExecutionResponse(Task{ID: "synthetic", TraceParent: parent, Status: "failed", ErrorCode: code, Error: message}))
	if e != nil || !bytes.Contains(raw, []byte(`"correlation_id":"1234567890abcdef1234567890abcdef"`)) || !bytes.Contains(raw, []byte(`"diagnostics"`)) {
		t.Fatal("missing persisted-task correlation", string(raw), e)
	}
	noPrivateDiagnostics(t, string(raw), logs.String())
}
func TestDiagnosticLocalQueueSurvivesPanicWithoutLosingSlots(t *testing.T) {
	logs := diagnosticLogs(t)
	q := newLocalJobs(1)
	defer q.close()
	ran := false
	q.submit(func() { panic("sk-private-token private-prompt") })
	q.submit(func() { ran = true })
	q.wait()
	if !ran {
		t.Fatal("worker slot lost after panic")
	}
	noPrivateDiagnostics(t, logs.String())
}

type diagnosticAlignmentPanic struct{}

func (diagnosticAlignmentPanic) Transcribe(context.Context, []byte, string, float64) (AlignmentResult, error) {
	panic("sk-private-token private-prompt user@example.invalid")
}
func (diagnosticAlignmentPanic) TranscribeFile(context.Context, string, string, float64) (AlignmentResult, error) {
	panic("sk-private-token private-prompt user@example.invalid")
}
func TestDiagnosticFileAlignmentPanicIsInterruptedWithoutRetry(t *testing.T) {
	logs := diagnosticLogs(t)
	_, err := runAlignmentFile(context.Background(), diagnosticAlignmentPanic{}, AlignmentTask{AudioDuration: 2}, "unused", "audio/wav")
	if !errors.Is(err, diagnostics.ErrPanic) {
		t.Fatal("ASR panic escaped provider boundary", err)
	}
	noPrivateDiagnostics(t, logs.String())
}
func TestDiagnosticModelCallUsesLastResponseWithoutLeakingOldValues(t *testing.T) {
	o := gatewayObservation{}
	o.response(400, "f63f4568-9026-4b82-b076-05f92f92f711", []byte(`{"code":"InvalidParameter"}`))
	if o.call.UpstreamErrorCode != "InvalidParameter" || o.call.RequestID == "" {
		t.Fatal("lost first response metadata")
	}
	o.response(503, "", []byte(`{"message":"sk-private-token"}`))
	if o.call.HTTPStatus != 503 || o.call.RequestID != "" || o.call.UpstreamErrorCode != "" {
		t.Fatal("mixed metadata from different responses")
	}
}
func TestDiagnosticOversizedMetadataIsDiscardedWithoutLimitingResponse(t *testing.T) {
	logs := diagnosticLogs(t)
	raw := `{"code":"InvalidParameter","message":"` + strings.Repeat("private-prompt", 10000) + `"}`
	b := &diagnosticBody{ReadCloser: io.NopCloser(strings.NewReader(raw)), ctx: context.Background(), status: 400}
	data, err := io.ReadAll(b)
	if err != nil || string(data) != raw || !b.truncated || b.buf.Len() != 0 {
		t.Fatal("diagnostic buffer changed the business response")
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	noPrivateDiagnostics(t, logs.String())
}

func TestDiagnosticValidationPreservesLocalHintsWithoutExposingWrapper(t *testing.T) {
	for _, tc := range []struct {
		err     error
		handler func(http.ResponseWriter, error)
		hint    string
	}{
		{fmt.Errorf("sk-private-token: %w", mediaInvalid("预演不能作为正式成片发布")), mediaError, "预演"},
		{fmt.Errorf("private-prompt: %w", projectInputError("请选择有效项目标题")), storeError, "标题"},
	} {
		w := httptest.NewRecorder()
		tc.handler(w, tc.err)
		if w.Code != 400 || !strings.Contains(w.Body.String(), tc.hint) {
			t.Fatal("controlled validation hint lost")
		}
		noPrivateDiagnostics(t, w.Body.String())
	}
}
