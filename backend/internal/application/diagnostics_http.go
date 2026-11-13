package application

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/adcwb/ai-creative-studio/backend/internal/platform/diagnostics"
	"github.com/adcwb/ai-creative-studio/backend/internal/platform/tracing"
)

type diagnosticKey struct{}
type requestDiagnostic struct {
	mu       sync.Mutex
	id       string
	upstream *diagnostics.Upstream
	route    string
	span     *tracing.Span
}
type diagnosticResponse struct {
	http.ResponseWriter
	ctx    context.Context
	status int
	code   string
}

func (w *diagnosticResponse) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *diagnosticResponse) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
		w.ResponseWriter.WriteHeader(code)
	}
}
func (w *diagnosticResponse) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(200)
	}
	return w.ResponseWriter.Write(data)
}
func (w *diagnosticResponse) Flush() {
	if w.status == 0 {
		w.WriteHeader(200)
	}
	_ = http.NewResponseController(w.ResponseWriter).Flush()
}
func diagnosticWriter(w http.ResponseWriter) *diagnosticResponse {
	for range 32 {
		if d, ok := w.(*diagnosticResponse); ok {
			return d
		}
		u, ok := w.(interface{ Unwrap() http.ResponseWriter })
		if !ok {
			return nil
		}
		w = u.Unwrap()
	}
	return nil
}

// WithDiagnostics is idempotent across public/demo/auth/audit wrappers. Headers
// supplied by a caller never become the server request identity.
func WithDiagnostics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := r.Context().Value(diagnosticKey{}).(*requestDiagnostic); ok {
			next.ServeHTTP(w, r)
			return
		}
		id := diagnostics.NewID()
		if id == "" {
			http.Error(w, "request identity unavailable", 503)
			return
		}
		ctx := r.Context()
		if tracing.Parent(ctx) == "" {
			parent := ""
			if values := r.Header.Values("traceparent"); len(values) == 1 && tracing.ValidParent(values[0]) {
				parent = values[0]
			}

			ctx = tracing.WithParent(ctx, parent)
		}
		ctx, span := tracing.Start(ctx, "http.server", tracing.Attributes{Method: r.Method})
		defer span.End("error")
		ctx = context.WithValue(ctx, diagnosticKey{}, &requestDiagnostic{id: id, span: span, route: gatewayManagementRoute(r.URL.Path)})
		r = r.WithContext(ctx)
		w.Header().Set("X-Request-ID", id)
		w.Header().Set("X-Correlation-ID", diagnostics.Correlation(ctx))
		w.Header().Set("Access-Control-Expose-Headers", "X-Request-ID, X-Correlation-ID")
		out := &diagnosticResponse{ResponseWriter: w, ctx: ctx}
		start := time.Now()
		defer func() {
			panicked := recover()
			if panicked != nil {
				diagnostics.Event(ctx, "http_panic", "internal_panic", id, nil)
				if out.status != 0 {
					panic(http.ErrAbortHandler)
				}
				for _, key := range []string{"Content-Length", "Content-Range", "Content-Disposition", "Location", "Set-Cookie"} {
					out.Header().Del(key)
				}
				problem(out, 500, "internal_panic", "服务内部执行被中断，请保留关联编号并核对操作结果。")
			}
			status := out.status
			if status == 0 {
				status = 200
			}
			span.HTTPStatus(status)
			if status >= 500 {
				span.End("error")
			} else if status >= 400 {
				span.End("denied")
			} else {
				span.End("ok")
			}
			if status >= 400 {
				code := out.code
				if code == "" {
					code = "internal_error"
					if status == 404 {
						code = "not_found"
					}
					if status == 405 {
						code = "method_not_allowed"
					}
				}
				d := diagnostics.For(code, status)
				level := slog.LevelWarn
				if status >= 500 {
					level = slog.LevelError
				}
				method := r.Method
				switch method {
				case "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS":
				default:
					method = "OTHER"
				}
				state, _ := ctx.Value(diagnosticKey{}).(*requestDiagnostic)
				state.mu.Lock()
				route := state.route
				var upstream *diagnostics.Upstream
				if state.upstream != nil {
					copy := *state.upstream
					upstream = &copy
				}
				state.mu.Unlock()
				if route == "" {
					route = "unmatched"
				}
				fields := []any{"event", "http_error", "request_id", id, "correlation_id", diagnostics.Correlation(ctx), "http_method", method, "route", route, "http_status", status, "error_code", diagnostics.SafeCode(code), "category", d.Category, "cause_known", d.CauseKnown, "duration_ms", time.Since(start).Milliseconds()}
				if upstream != nil {
					fields = append(fields, "upstream_http_status", upstream.HTTPStatus, "upstream_error_code", diagnostics.SafeUpstreamCode(upstream.Code), "upstream_request_id", diagnostics.SafeID(upstream.RequestID))
				}
				slog.Log(ctx, level, "HTTP operation failed", fields...)
			}
		}()
		next.ServeHTTP(out, r)
	})
}
func problemDiagnostic(w http.ResponseWriter, status int, code string) diagnostics.Detail {
	d := diagnostics.For(code, status)
	if out := diagnosticWriter(w); out != nil {
		out.code = diagnostics.SafeCode(code)
		if state, ok := out.ctx.Value(diagnosticKey{}).(*requestDiagnostic); ok {
			state.mu.Lock()
			d.RequestID = state.id
			if state.upstream != nil {
				u := *state.upstream
				d.Upstream = &u
			}
			state.mu.Unlock()
		}
		d.CorrelationID = diagnostics.Correlation(out.ctx)
	} else {
		d.RequestID = w.Header().Get("X-Request-ID")
		if d.RequestID == "" {
			d.RequestID = diagnostics.NewID()
			w.Header().Set("X-Request-ID", d.RequestID)
		}
	}
	return d
}

// guardResult catches the panic before a workflow library can format its value
// or stack into a returned error. It never retries the provider.
func guardResult[T any](ctx context.Context, run func() (T, error)) (result T, err error) {
	err = diagnostics.Invoke(ctx, "worker_panic", func() error { var e error; result, e = run(); return e })
	return
}
func logTaskFailure(parent, kind, id, code string) {
	if code == "" {
		return
	}
	ctx := tracing.WithParent(context.Background(), parent)
	diagnostics.Event(ctx, "task_error", code, id, nil, kind)
}
func recoverFileWorker(ctx context.Context, err *error) {
	if recover() != nil {
		*err = diagnostics.ErrPanic
		diagnostics.Event(ctx, "worker_panic", "execution_interrupted", "", nil)
	} else if *err != nil {
		diagnostics.Event(ctx, "worker_error", diagnostics.ErrorCode(*err), "", nil)
	}
}
func taskDiagnostic(parent, code string) *diagnostics.Detail {
	if code == "" {
		return nil
	}
	d := diagnostics.For(code, 0)
	d.CorrelationID = diagnostics.FromParent(parent)
	return &d
}
func diagnosticFailureCode(err error) string {
	if errors.Is(err, diagnostics.ErrPanic) {
		return "execution_interrupted"
	}
	code := diagnostics.ErrorCode(err)
	if code == "internal_error" {
		// This helper is used only after an actual upstream transport operation.
		// Do not label an unclassified transport failure as a proven local defect.
		return "provider_error"
	}
	return code
}

func diagnoseErrorResponse(w http.ResponseWriter, status int, value any) any {
	v, ok := value.(map[string]any)
	if !ok {
		return value
	}
	fields := map[string]any{}
	switch e := v["error"].(type) {
	case map[string]string:
		for k, v := range e {
			fields[k] = v
		}
	case map[string]any:
		for k, v := range e {
			fields[k] = v
		}
	default:
		return value
	}
	code, _ := fields["code"].(string)
	code = diagnostics.SafeCode(code)
	// Existing handler-defined codes remain a compatibility contract. The
	// separately logged code is restricted to the registered vocabulary.
	d := problemDiagnostic(w, status, code)
	fields["request_id"], fields["correlation_id"] = d.RequestID, d.CorrelationID
	fields["category"], fields["suggestion"], fields["cause_known"] = d.Category, d.Suggestion, d.CauseKnown
	if d.Upstream != nil {
		fields["upstream"] = d.Upstream
	}
	copy := map[string]any{}
	for k, v := range v {
		copy[k] = v
	}
	copy["error"] = fields
	return copy
}

// A typed writer helper retains streaming behavior (no response buffering).
var _ io.Writer = (*diagnosticResponse)(nil)
