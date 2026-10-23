package studio

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Operations is a local append-only request audit and aggregate telemetry sink.
// It never records request bodies, cookies, query strings, model prompts or URLs.
// It is not a tamper-proof external audit system or a distributed tracing backend.
type Operations struct {
	mu       sync.Mutex
	file     *os.File
	failed   bool
	counts   map[int]uint64
	duration time.Duration
	started  time.Time
}
type auditIdentityKey struct{}
type auditIdentity struct{ Actor, Tenant, Role string }

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
	file, err := os.OpenFile(filepath.Join(dir, "audit.jsonl"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return nil, err
	}
	return &Operations{file: file, counts: map[int]uint64{}, started: time.Now()}, nil
}
func (o *Operations) append(record auditRecord) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.failed {
		return errors.New("audit unavailable")
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
	}
	return err
}
func (o *Operations) Close() error { o.mu.Lock(); defer o.mu.Unlock(); return o.file.Close() }

type observedResponse struct {
	http.ResponseWriter
	status int
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
	return w.ResponseWriter.Write(data)
}
func (w *observedResponse) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (o *Operations) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		identity := &auditIdentity{}
		r = r.WithContext(context.WithValue(r.Context(), auditIdentityKey{}, identity))
		var id [16]byte
		if _, err := rand.Read(id[:]); err != nil {
			http.Error(w, "request identity unavailable", 503)
			return
		}
		requestID := hex.EncodeToString(id[:])
		w.Header().Set("X-Request-ID", requestID)
		if err := o.append(auditRecord{At: start.UTC(), RequestID: requestID, Method: r.Method, Phase: "started"}); err != nil && (authWrite(r) || r.URL.Path == "/api/v1/auth/sso/callback") {
			http.Error(w, "audit unavailable", 503)
			return
		}
		response := &observedResponse{ResponseWriter: w}
		next.ServeHTTP(response, r)
		status := response.status
		if status == 0 {
			status = 200
		}
		elapsed := time.Since(start)
		_ = o.append(auditRecord{At: time.Now().UTC(), RequestID: requestID, Method: r.Method, Phase: "finished", Actor: identity.Actor, Tenant: identity.Tenant, Role: identity.Role, Status: status, DurationMS: elapsed.Milliseconds()})
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
		o.mu.Lock()
		defer o.mu.Unlock()
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
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
