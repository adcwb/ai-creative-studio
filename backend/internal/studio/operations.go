package studio

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/adcwb/ai-creative-studio/backend/internal/tracing"
)

// Operations is a local append-only request audit and aggregate telemetry sink.
// It never records request bodies, cookies, query strings, model prompts or URLs.
// It is not a tamper-proof external audit system or a distributed tracing backend.
type Operations struct {
	mu            sync.Mutex
	file          *os.File
	db            *sql.DB
	closed        bool
	failed        bool
	auditFailures uint64
	dbFailures    uint64
	counts        map[int]uint64
	duration      time.Duration
	started       time.Time
}
type auditIdentityKey struct{}
type auditIdentity struct {
	Actor, Tenant, Role string
	operations          *Operations
	requestID, method   string
	start               time.Time
	began               bool
}

// BeginAuditMutation is called after authorization, before committing a mutation.
// Anonymous failed attempts and all reads are aggregated only in metrics.
func BeginAuditMutation(ctx context.Context) error {
	identity, ok := ctx.Value(auditIdentityKey{}).(*auditIdentity)
	if !ok || identity.operations == nil || identity.began {
		return nil
	}
	if err := identity.operations.append(auditRecord{At: identity.start.UTC(), RequestID: identity.requestID, Method: identity.method, Phase: "started", Actor: identity.Actor, Tenant: identity.Tenant, Role: identity.Role}); err != nil {
		return err
	}
	identity.began = true
	return nil
}
func (o *Operations) available() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.failed || o.closed {
		return errors.New("audit unavailable")
	}
	if o.db != nil {
		return nil
	}
	info, err := o.file.Stat()
	if err == nil && info.Size() > 64<<20 {
		err = errors.New("audit capacity reached")
	}
	if err != nil {
		o.failed = true
		o.auditFailures++
	}
	return err
}

func SetAuditIdentity(ctx context.Context, actor, tenant, role string) {
	if identity, ok := ctx.Value(auditIdentityKey{}).(*auditIdentity); ok {
		identity.Actor = actor
		identity.Tenant = tenant
		identity.Role = role
	}
}

type auditRecord struct {
	Actor      string    `json:"actor_id,omitempty"`
	Tenant     string    `json:"tenant_id,omitempty"`
	Role       string    `json:"role,omitempty"`
	At         time.Time `json:"at"`
	RequestID  string    `json:"request_id"`
	Method     string    `json:"method"`
	Phase      string    `json:"phase"`
	Status     int       `json:"status,omitempty"`
	DurationMS int64     `json:"duration_ms,omitempty"`
}

func OpenOperations(dir string) (*Operations, error) {
	file, err := os.OpenFile(filepath.Join(dir, "audit.jsonl"), os.O_CREATE|os.O_WRONLY|os.O_APPEND|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	return &Operations{file: file, counts: map[int]uint64{}, started: time.Now()}, nil
}
func (o *Operations) append(record auditRecord) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.failed || o.closed {
		return errors.New("audit unavailable")
	}
	if o.db != nil {
		if err := o.appendPostgres(record); err != nil {
			o.failed = true
			o.auditFailures++
			return errors.New("audit unavailable")
		}
		return nil
	}
	info, err := o.file.Stat()
	if err == nil && info.Size() > 64<<20 {
		err = errors.New("audit capacity reached; archive audit during maintenance")
	}
	if err == nil {
		var data []byte
		data, err = json.Marshal(record)
		if err == nil {
			data = append(data, '\n')
			_, err = o.file.Write(data)
		}
		if err == nil {
			err = o.file.Sync()
		}
	}
	if err != nil {
		o.failed = true
		o.auditFailures++
	}
	return err
}
func (o *Operations) Close() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed {
		return nil
	}
	o.closed = true
	if o.file != nil {
		return o.file.Close()
	}
	return nil
}

type observedResponse struct {
	http.ResponseWriter
	status      int
	writeFailed bool
}

func (w *observedResponse) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
		w.ResponseWriter.WriteHeader(code)
	}
}
func (w *observedResponse) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(200)
	}
	n, err := w.ResponseWriter.Write(data)
	if err != nil {
		w.writeFailed = true
	}
	return n, err
}
func (w *observedResponse) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (o *Operations) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parent := ""
		if values := r.Header.Values("traceparent"); len(values) == 1 {
			parent = values[0]
		}
		traceCtx, span := tracing.Start(tracing.WithParent(r.Context(), parent), "http.server", tracing.Attributes{Method: r.Method})
		r = r.WithContext(traceCtx)
		defer span.End("error")
		start := time.Now()
		identity := &auditIdentity{operations: o, start: start, method: r.Method}
		r = r.WithContext(context.WithValue(r.Context(), auditIdentityKey{}, identity))
		var id [16]byte
		if _, err := rand.Read(id[:]); err != nil {
			span.HTTPStatus(503)
			http.Error(w, "request identity unavailable", 503)
			return
		}
		requestID := hex.EncodeToString(id[:])
		identity.requestID = requestID
		mutation := authWrite(r) || r.URL.Path == "/api/v1/auth/sso/callback"
		w.Header().Set("X-Request-ID", requestID)
		if err := o.available(); err != nil && mutation {
			span.HTTPStatus(503)
			http.Error(w, "audit unavailable", 503)
			return
		}
		response := &observedResponse{ResponseWriter: w}
		next.ServeHTTP(response, r)
		status := response.status
		if status == 0 {
			status = 200
		}
		span.HTTPStatus(status)
		if response.writeFailed || status >= 500 {
			span.End("error")
		} else if status >= 400 {
			span.End("denied")
		} else {
			span.End("ok")
		}
		elapsed := time.Since(start)
		if mutation && (identity.began || identity.Actor != "" && status < 400) {
			_ = o.append(auditRecord{At: time.Now().UTC(), RequestID: requestID, Method: r.Method, Phase: "finished", Actor: identity.Actor, Tenant: identity.Tenant, Role: identity.Role, Status: status, DurationMS: elapsed.Milliseconds()})
		}
		o.mu.Lock()
		o.counts[status/100]++
		o.duration += elapsed
		o.mu.Unlock()
	})
}

// MetricsHandler must be served on a separate operator-only listener. Do not
// mount it behind the public frontend proxy or assume loopback proxy peers authenticate users.
func (o *Operations) MetricsHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/metrics" {
			http.NotFound(w, r)
			return
		}

		counts := map[string]int64{}
		configured, available := 0, 0
		if o.db != nil {
			configured = 1
			ctx, cancel := context.WithTimeout(r.Context(), time.Second)
			rows, err := o.db.QueryContext(ctx, "SELECT state,count(*) FROM execution_jobs GROUP BY state")
			if err == nil {
				for rows.Next() {
					var state string
					var count int64
					if err = rows.Scan(&state, &count); err != nil {
						break
					}
					counts[state] = count
				}
				if err == nil {
					err = rows.Err()
				}
				rows.Close()
			}
			cancel()
			if err == nil {
				available = 1
			} else {
				o.mu.Lock()
				o.dbFailures++
				o.mu.Unlock()
			}
		}
		o.mu.Lock()
		defer o.mu.Unlock()
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		fmt.Fprintf(w, "# TYPE studio_database_configured gauge\nstudio_database_configured %d\n# TYPE studio_database_available gauge\nstudio_database_available %d\n# TYPE studio_database_failures_total counter\nstudio_database_failures_total %d\n", configured, available, o.dbFailures)
		fmt.Fprintf(w, "# TYPE studio_audit_failures_total counter\nstudio_audit_failures_total %d\n", o.auditFailures)
		fmt.Fprintln(w, "# TYPE studio_queue_jobs gauge")
		for _, state := range []string{"queued", "leased", "dispatch_started", "succeeded", "failed", "unknown", "canceled"} {
			fmt.Fprintf(w, "studio_queue_jobs{state=%q} %d\n", state, counts[state])
		}
		stats := tracing.DefaultStats()
		fmt.Fprintf(w, "# TYPE studio_trace_exported_total counter\nstudio_trace_exported_total %d\n# TYPE studio_trace_dropped_total counter\nstudio_trace_dropped_total %d\n# TYPE studio_trace_failures_total counter\nstudio_trace_failures_total %d\n", stats.Exported, stats.Dropped, stats.Failures)
		fmt.Fprintln(w, "# TYPE studio_http_requests_total counter")
		for _, class := range []int{1, 2, 3, 4, 5} {
			fmt.Fprintf(w, "studio_http_requests_total{class=\"%dxx\"} %d\n", class, o.counts[class])
		}
		fmt.Fprintf(w, "# TYPE studio_http_duration_seconds_sum counter\nstudio_http_duration_seconds_sum %.6f\n", o.duration.Seconds())
		healthy := 1
		if o.failed {
			healthy = 0
		}
		fmt.Fprintf(w, "# TYPE studio_audit_available gauge\nstudio_audit_available %d\n", healthy)
		fmt.Fprintf(w, "# TYPE studio_uptime_seconds gauge\nstudio_uptime_seconds %.3f\n", time.Since(o.started).Seconds())
	})
}
