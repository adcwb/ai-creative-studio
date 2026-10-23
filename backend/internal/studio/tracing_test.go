package studio

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/adcwb/ai-creative-studio/backend/internal/jobqueue"
	"github.com/adcwb/ai-creative-studio/backend/internal/tracing"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type traceCollector struct {
	mu      sync.Mutex
	records []tracing.Record
}

func (c *traceCollector) Export(_ context.Context, r []tracing.Record) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.records = append(c.records, r...)
	return nil
}
func (c *traceCollector) Flush(context.Context) error { return nil }
func (c *traceCollector) Close() error                { return nil }
func TestPostgresTraceHTTPQueueWorker(t *testing.T) {
	db := postgresTestDB(t)
	q := jobqueue.Queue{DB: db}
	collector := &traceCollector{}
	tr, err := tracing.New(collector, "api", 64)
	if err != nil {
		t.Fatal(err)
	}
	ops, err := OpenPostgresOperations(db)
	if err != nil {
		t.Fatal(err)
	}
	defer ops.Close()
	const parent = "00-1234567890abcdef1234567890abcdef-1234567890abcdef-01"
	handler := ops.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		in := ModelRequest{}
		if !resolveModelRequest(w, r, nil, &in, "image") {
			t.Error("mock accept failed")
			return
		}
		if _, ok := r.Context().Value(authScopeKey{}).(authScope); ok {
			t.Error("trace granted authorization")
		}
		tx, e := db.BeginTx(r.Context(), nil)
		if e != nil {
			t.Error(e)
			return
		}
		defer tx.Rollback()
		e = jobqueue.EnqueueTx(r.Context(), tx, jobqueue.Job{TenantID: "synthetic-tenant", ID: "trace-task", Kind: "image", IdempotencyKey: "trace-key", Payload: json.RawMessage(`{}`), TraceParent: in.traceParent})
		if e == nil {
			e = tx.Commit()
		}
		if e != nil {
			t.Error(e)
		}
		w.WriteHeader(202)
	}))
	req := httptest.NewRequest("POST", "/tasks?prompt=DO_NOT_EXPORT", nil)
	req.Header.Set("Traceparent", parent)
	req.Header.Set("Authorization", "DO_NOT_EXPORT")
	req = req.WithContext(tracing.WithTracer(req.Context(), tr))
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != 202 {
		t.Fatalf("HTTP %d", res.Code)
	}
	stored, e := q.Get(context.Background(), "synthetic-tenant", "trace-task")
	if e != nil || !tracing.ValidParent(stored.TraceParent) {
		t.Fatalf("persisted %v %+v", e, stored)
	}
	if _, e = q.Get(context.Background(), "other-tenant", stored.ID); e == nil {
		t.Fatal("cross tenant queue read")
	}
	workerCtx, cancel := context.WithTimeout(tracing.WithTracer(context.Background(), tr), 3*time.Second)
	defer cancel()
	err = jobqueue.Run(workerCtx, q, "image", func(ctx context.Context, q jobqueue.Queue, j jobqueue.Job) error {
		if tracing.Parent(ctx)[3:35] != parent[3:35] {
			t.Error("trace lost after durable queue")
		}
		ctx, span := tracing.Start(ctx, "governance.execute", tracing.Attributes{Kind: "image"})
		defer span.End("unknown")
		transport := runtimeTransport{lifetime: ctx, guard: func(context.Context) error { return nil }, base: runtimeFixtureTransport(func(r *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("fixture"))}, nil
		})}
		request, _ := http.NewRequestWithContext(ctx, "POST", "https://fixture.invalid/path?secret=DO_NOT_EXPORT", nil)
		response, e := transport.RoundTrip(request)
		if e != nil {
			return e
		}
		response.Body.Close()
		e = q.Finish(ctx, j, "unknown", nil)
		cancel()
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	metrics := httptest.NewRecorder()
	ops.MetricsHandler().ServeHTTP(metrics, httptest.NewRequest("GET", "/metrics", nil))
	if !strings.Contains(metrics.Body.String(), `studio_queue_jobs{state="unknown"} 1`) {
		t.Fatal(metrics.Body.String())
	}
	if err = tr.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, r := range collector.records {
		if r.TraceID != parent[3:35] {
			t.Fatal("trace id changed")
		}
		seen[r.Operation] = true
	}
	for _, op := range []string{"http.server", "worker.execute", "governance.execute", "provider.request"} {
		if !seen[op] {
			t.Fatalf("missing %s", op)
		}
	}
	raw, _ := json.Marshal(collector.records)
	for _, bad := range []string{"DO_NOT_EXPORT", "synthetic-tenant", "fixture.invalid", "trace-task"} {
		if strings.Contains(string(raw), bad) {
			t.Fatal("private data exported")
		}
	}
}

type failingTraceBody struct{}

func (failingTraceBody) Read([]byte) (int, error) { return 0, errors.New("PRIVATE") }
func (failingTraceBody) Close() error             { return nil }
func TestProviderTracePartialResponseUnknown(t *testing.T) {
	c := &traceCollector{}
	tr, _ := tracing.New(c, "worker", 4)
	ctx, span := tracing.Start(tracing.WithTracer(context.Background(), tr), "provider.request", tracing.Attributes{})
	_ = ctx
	b := runtimeResponseBody{ReadCloser: failingTraceBody{}, stop: func() bool { return true }, cancel: func() {}, span: span, outcome: "ok"}
	_, _ = b.Read(make([]byte, 1))
	b.Close()
	tr.Close(context.Background())
	if len(c.records) != 1 || c.records[0].Outcome != "unknown" {
		t.Fatal(c.records)
	}
}

func TestTraceConfigurationFailsClosed(t *testing.T) {
	t.Setenv("TRACE_MODE", "off")
	t.Setenv("TRACE_JSON_PATH", "")
	closeTrace, err := ConfigureTracingFromEnv("api")
	if err != nil {
		t.Fatal(err)
	}
	if err = closeTrace(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TRACE_MODE", "otlp")
	if _, err = ConfigureTracingFromEnv("api"); err == nil {
		t.Fatal("implicit network exporter")
	}
	t.Setenv("TRACE_MODE", "json")
	t.Setenv("TRACE_JSON_PATH", "relative.json")
	if _, err = ConfigureTracingFromEnv("api"); err == nil {
		t.Fatal("relative path")
	}
	t.Setenv("TRACE_JSON_PATH", t.TempDir()+"/traces.jsonl")
	closeTrace, err = ConfigureTracingFromEnv("api")
	if err != nil {
		t.Fatal(err)
	}
	_, span := tracing.Start(context.Background(), "http.server", tracing.Attributes{})
	span.End("ok")
	if err = closeTrace(); err != nil {
		t.Fatal(err)
	}
	if _, err = ConfigureTracingFromEnv("api"); err == nil {
		t.Fatal("overwrote traces on restart")
	}
}
func TestPostgresTraceMetricsDatabaseFailure(t *testing.T) {
	db := postgresTestDB(t)
	ops, err := OpenPostgresOperations(db)
	if err != nil {
		t.Fatal(err)
	}
	defer ops.Close()
	if _, err = db.Exec(`DROP TABLE execution_jobs`); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRecorder()
	ops.MetricsHandler().ServeHTTP(r, httptest.NewRequest("GET", "/metrics", nil))
	for _, expected := range []string{"studio_database_configured 1", "studio_database_available 0", "studio_database_failures_total 1"} {
		if !strings.Contains(r.Body.String(), expected) {
			t.Fatalf("missing %s", expected)
		}
	}
}

type traceWriteFailure struct{ header http.Header }

func (w *traceWriteFailure) Header() http.Header { return w.header }
func (w *traceWriteFailure) WriteHeader(int)     {}
func (w *traceWriteFailure) Write([]byte) (int, error) {
	return 0, errors.New("PRIVATE transport error")
}
func TestHTTPTraceWriteFailureAndInvalidParent(t *testing.T) {
	c := &traceCollector{}
	tr, _ := tracing.New(c, "api", 8)
	ops, err := OpenOperations(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer ops.Close()
	handler := ops.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("PRIVATE body")) }))
	req := httptest.NewRequest("GET", "/?secret=PRIVATE", nil)
	req.Header.Set("traceparent", "PRIVATE invalid")
	req = req.WithContext(tracing.WithTracer(req.Context(), tr))
	handler.ServeHTTP(&traceWriteFailure{header: make(http.Header)}, req)
	if err = tr.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(c.records) != 1 || c.records[0].Outcome != "error" || c.records[0].Attributes.Status != 200 {
		t.Fatal(c.records)
	}
	raw, _ := json.Marshal(c.records)
	if strings.Contains(string(raw), "PRIVATE") {
		t.Fatal("private trace data")
	}
}
