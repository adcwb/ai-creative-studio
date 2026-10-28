package tracing

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

// Attributes is intentionally typed: arbitrary strings/maps, prompt, URL,
// headers, credentials, actor/tenant identifiers and raw errors are unsupported.
type Attributes struct {
	Method   string `json:"http_method,omitempty"`
	Status   int    `json:"http_status,omitempty"`
	Kind     string `json:"task_kind,omitempty"`
	Provider string `json:"provider,omitempty"`
}
type Record struct {
	TraceID    string     `json:"trace_id"`
	SpanID     string     `json:"span_id"`
	ParentID   string     `json:"parent_span_id,omitempty"`
	Operation  string     `json:"operation"`
	Service    string     `json:"service"`
	StartedAt  time.Time  `json:"started_at"`
	DurationNS int64      `json:"duration_ns"`
	Outcome    string     `json:"outcome"`
	Attributes Attributes `json:"attributes"`
}

// Sink must honor context deadlines. Implementations receive only sanitized
// records. The default is no sink; no network exporter is configured implicitly.
type Sink interface {
	Export(context.Context, []Record) error
	Flush(context.Context) error
	Close() error
}
type Stats struct{ Exported, Dropped, Failures uint64 }
type Tracer struct {
	mu                          sync.RWMutex
	queue                       chan Record
	sink                        Sink
	service                     string
	closed                      bool
	done                        chan struct{}
	ctx                         context.Context
	cancel                      context.CancelFunc
	failed                      atomic.Bool
	exported, dropped, failures atomic.Uint64
}

func New(sink Sink, service string, capacity int) (*Tracer, error) {
	if sink == nil || (service != "api" && service != "worker") || capacity < 1 || capacity > 4096 {
		return nil, errors.New("invalid tracing configuration")
	}
	ctx, cancel := context.WithCancel(context.Background())
	t := &Tracer{queue: make(chan Record, capacity), sink: sink, service: service, done: make(chan struct{}), ctx: ctx, cancel: cancel}
	go t.run()
	return t, nil
}
func (t *Tracer) Stats() Stats { return Stats{t.exported.Load(), t.dropped.Load(), t.failures.Load()} }
func (t *Tracer) fail()        { t.failures.Add(1); t.failed.Store(true) }
func (t *Tracer) run() {
	defer close(t.done)
	defer t.cancel()
	defer func() {
		if t.sink.Close() != nil {
			t.fail()
		}
	}()
	for record := range t.queue {
		batch := []Record{record}
		for len(batch) < 32 {
			select {
			case r, ok := <-t.queue:
				if !ok {
					goto export
				}
				batch = append(batch, r)
			default:
				goto export
			}
		}
	export:
		if t.failed.Load() || t.ctx.Err() != nil {
			t.dropped.Add(uint64(len(batch)))
			continue
		}
		ctx, cancel := context.WithTimeout(t.ctx, 2*time.Second)
		err := t.sink.Export(ctx, batch)
		cancel()
		if err != nil {
			t.fail()
			t.dropped.Add(uint64(len(batch)))
		} else {
			t.exported.Add(uint64(len(batch)))
		}
	}
	ctx, cancel := context.WithTimeout(t.ctx, 2*time.Second)
	defer cancel()
	if t.sink.Flush(ctx) != nil {
		t.fail()
	}
}
func (t *Tracer) Close(ctx context.Context) error {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	if !t.closed {
		t.closed = true
		close(t.queue)
	}
	t.mu.Unlock()
	select {
	case <-ctx.Done():
		t.cancel()
		return ctx.Err()
	case <-t.done:
		if t.failed.Load() {
			return errors.New("trace sink unavailable")
		}
		return nil
	}
}
func (t *Tracer) submit(record Record) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if t.closed || t.failed.Load() {
		t.dropped.Add(1)
		return
	}
	select {
	case t.queue <- record:
	default:
		t.dropped.Add(1)
	}
}

type Span struct {
	mu      sync.Mutex
	once    sync.Once
	t       *Tracer
	record  Record
	started time.Time
	sampled bool
}

func Start(ctx context.Context, operation string, attrs Attributes) (context.Context, *Span) {
	parent := Parent(ctx)
	traceID, parentID, flags := randomID(16), "", "01"
	if parent != "" {
		traceID, parentID, flags = parent[3:35], parent[36:52], parent[53:]
	}
	id := randomID(8)
	started := time.Now()
	t := Current(ctx)
	service := ""
	if t != nil {
		service = t.service
	}
	span := &Span{t: t, started: started, sampled: flags == "01", record: Record{TraceID: traceID, SpanID: id, ParentID: parentID, Operation: safeOperation(operation), Service: service, StartedAt: started.UTC(), Attributes: safeAttributes(attrs)}}
	if traceID == "" || id == "" {
		span.sampled = false
		return WithParent(ctx, ""), span
	}
	return WithParent(ctx, "00-"+traceID+"-"+id+"-"+flags), span
}
func (s *Span) End(outcome string) {
	if s == nil {
		return
	}
	s.once.Do(func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.t == nil || !s.sampled {
			return
		}
		s.record.DurationNS = time.Since(s.started).Nanoseconds()
		s.record.Outcome = safeOutcome(outcome)
		s.t.submit(s.record)
	})
}
func (s *Span) HTTPStatus(status int) {
	if s != nil && status >= 100 && status <= 599 {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.record.Attributes.Status = status
	}
}
func safeOperation(value string) string {
	switch value {
	case "http.server", "task.accept", "worker.execute", "governance.execute", "provider.request":
		return value
	}
	return "internal"
}
func safeOutcome(value string) string {
	switch value {
	case "ok", "error", "unknown", "canceled", "denied":
		return value
	}
	return "unknown"
}
func safeAttributes(a Attributes) Attributes {
	switch a.Method {
	case "GET", "POST", "PUT", "DELETE", "PATCH", "OPTIONS", "HEAD":
	default:
		a.Method = ""
	}
	if a.Status < 100 || a.Status > 599 {
		a.Status = 0
	}
	switch a.Kind {
	case "image_resume", "creative_resume", "analysis", "consistency", "image", "script", "storyboard", "creative", "alignment", "export", "text", "vision", "speech", "video", "asr", "music":
	default:
		a.Kind = ""
	}
	switch a.Provider {
	case "openai-text", "openai-images", "openai-speech", "openai-asr", "anthropic-text", "gemini-text", "gemini-images", "gemini-interactions-images", "gemini-video", "gemini-speech", "ark-text", "ark-images", "ark-video", "minimax-text", "minimax-images", "minimax-video", "minimax-speech", "minimax-music", "jimeng-images", "bytedance-speech", "mock-svg", "mock-script", "mock-storyboard", "mock-text":
	default:
		a.Provider = ""
	}
	return a
}
