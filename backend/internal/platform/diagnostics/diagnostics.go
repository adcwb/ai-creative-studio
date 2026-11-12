// Package diagnostics accepts bounded, typed metadata, never raw errors, URLs,
// credentials, prompts, headers, panic values or personal identities.
package diagnostics

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/adcwb/ai-creative-studio/backend/internal/platform/tracing"
)

var ErrPanic = errors.New("execution interrupted by an internal panic")

type Upstream struct {
	HTTPStatus int    `json:"http_status,omitempty"`
	Code       string `json:"error_code,omitempty"`
	RequestID  string `json:"request_id,omitempty"`
}
type Detail struct {
	RequestID     string    `json:"request_id,omitempty"`
	CorrelationID string    `json:"correlation_id,omitempty"`
	Category      string    `json:"category"`
	Suggestion    string    `json:"suggestion"`
	CauseKnown    bool      `json:"cause_known"`
	Upstream      *Upstream `json:"upstream,omitempty"`
}

func Configure(service string) {
	if service != "api" && service != "worker" {
		service = "backend"
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, nil)).With("service", service))
}

// StartupFailure identifies a fixed initialization component without formatting
// errors that may contain database credentials, private paths or addresses.
func StartupFailure(component string) {
	switch component {
	case "deployment_config", "tracing", "storage", "blob_storage", "execution", "providers", "pipeline", "authentication", "audit", "metrics", "worker_config", "worker_runtime", "serve":
	default:
		component = "unknown"
	}
	slog.Error("backend initialization or service failed", "event", "startup_error", "error_code", "startup_failed", "component", component, "cause_known", false)
}

func NewID() string {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return ""
	}
	return hex.EncodeToString(id[:])
}
func Correlation(ctx context.Context) string { return FromParent(tracing.Parent(ctx)) }
func FromParent(parent string) string {
	if tracing.ValidParent(parent) {
		return parent[3:35]
	}
	return ""
}
func For(code string, status int) Detail {
	code = SafeCode(code)
	d := Detail{Category: "internal", Suggestion: "请保留关联编号联系管理员；操作结果不确定时先核对记录，不要重复提交。"}
	switch {
	case code == "provider_timeout" || code == "execution_interrupted" || code == "internal_panic":
		d.Category, d.Suggestion = "interrupted", "执行结果可能不确定。请核对已有任务和上游记录，确认后再决定是否显式重试。"
	case code == "provider_not_ready" || code == "provider_unavailable" || code == "gateway_target_denied" || code == "gateway_model_required" || code == "model_selection_required" || code == "unsupported_capability":
		d.Category, d.Suggestion = "configuration", "请核对已启用的网关、模型能力、功能绑定及部署允许的目标地址；本地配置就绪不代表已经验证上游服务。"
	case strings.HasPrefix(code, "provider_") || code == "invalid_provider_response" || code == "gateway_catalog_unavailable":
		d.Category, d.Suggestion = "upstream", "请核对网关厂商、地址、模型和输入参数，并用上游请求编号查询供应商记录；此分类不能确定上游内部原因。"
	case strings.Contains(code, "storage") || strings.Contains(code, "audit") || strings.Contains(code, "persistence"):
		d.Category, d.Suggestion = "storage", "请由管理员检查存储及审计可用性；写入结果可能不确定，先刷新核对已有记录。"
	case code == "startup_failed" || code == "model_key_unavailable" || strings.Contains(code, "config") || code == "provider_not_ready":
		d.Category, d.Suggestion = "configuration", "请核对部署配置、数据库与目录权限、模型能力和功能绑定；不要在诊断信息中提供密钥或令牌。"
	case status == 401 || status == 403 || strings.Contains(code, "denied") || strings.Contains(code, "auth"):
		d.Category, d.Suggestion = "access", "请核对当前登录会话、工作空间和操作权限，必要时联系管理员。"
	case status == 409 || strings.Contains(code, "conflict"):
		d.Category, d.Suggestion = "conflict", "请重新读取当前版本并核对已有结果，保留本地修改后再操作。"
	case status >= 400 && status < 500:
		d.Category, d.Suggestion = "input", "请核对请求格式、必填字段和当前操作支持的输入。"
	}
	// A classification/hint is not proof of a supplier's internal root cause.
	switch code {
	case "invalid_json", "invalid_input", "invalid_key", "version_conflict", "idempotency_conflict":
		d.CauseKnown = true
	}
	return d
}

var uuid = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
var requestID = regexp.MustCompile(`^req_[0-9a-fA-F]{16,96}$`)
var opaqueHex = regexp.MustCompile(`^[0-9a-f]{16,64}$`)
var fingerprint = regexp.MustCompile(`^sha256:[0-9a-f]{24}$`)
var unknownCode = regexp.MustCompile(`^unrecognized:[0-9a-f]{24}$`)

func Fingerprint(value string) string {
	h := sha256.Sum256([]byte(value))
	return "sha256:" + hex.EncodeToString(h[:12])
}

// Unknown vendor identifier formats are fingerprinted, never echoed. These are
// diagnostic references only and cannot authorize a data lookup.
func SafeID(value string) string {
	if value == "" {
		return ""
	}
	if fingerprint.MatchString(value) {
		return value
	}
	if len(value) <= 200 && (uuid.MatchString(value) || requestID.MatchString(value) || opaqueHex.MatchString(value)) {
		return value
	}
	return Fingerprint(value)
}
func SafeUpstreamCode(value string) string {
	if unknownCode.MatchString(value) {
		return value
	}
	switch value {
	case "InvalidParameter", "InvalidParameter.UnsupportedModel", "InvalidParameter.UnsupportedParameter", "InvalidApiKey", "InvalidApiKey.NotFound", "AuthenticationFailed", "AccessDenied", "AccessDenied.Unpurchased", "ModelNotFound", "NotFound", "Throttling", "Throttling.RateQuota", "Throttling.AllocationQuota", "Arrearage", "DataInspectionFailed", "InputDataInspectionFailed", "OutputDataInspectionFailed", "InternalError", "ServiceUnavailable", "invalid_api_key", "insufficient_quota", "rate_limit_exceeded", "model_not_found", "invalid_request_error", "authentication_error", "permission_error", "rate_limit_error", "overloaded_error", "INVALID_ARGUMENT", "PERMISSION_DENIED", "UNAUTHENTICATED", "NOT_FOUND", "RESOURCE_EXHAUSTED", "UNAVAILABLE", "INTERNAL":
		return value
	}
	if value == "" {
		return ""
	}
	return "unrecognized:" + strings.TrimPrefix(Fingerprint(value), "sha256:")
}
func ErrorCode(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, ErrPanic) {
		return "execution_interrupted"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "provider_timeout"
	}
	if errors.Is(err, context.Canceled) {
		return "execution_interrupted"
	}
	var coded interface{ DiagnosticCode() string }
	if errors.As(err, &coded) {
		return SafeCode(coded.DiagnosticCode())
	}
	return "internal_error"
}
func Event(ctx context.Context, event, code, ref string, upstream *Upstream, kinds ...string) {
	// Event/phase identifiers also come from a fixed vocabulary.
	switch event {
	case "http_error", "http_panic", "task_error", "worker_error", "worker_panic", "upstream_error", "gateway_error", "startup_error", "http_transport_error":
	default:
		event = "worker_error"
	}
	code = SafeCode(code)
	d := For(code, 0)
	args := []any{"event", event, "error_code", code, "category", d.Category, "correlation_id", Correlation(ctx)}
	args = append(args, "cause_known", d.CauseKnown)
	if event == "http_panic" || event == "worker_panic" {
		if function, file, line := panicLocation(); function != "" {
			args = append(args, "source_function", function, "source_file", file, "source_line", line)
		}
	}
	if len(kinds) > 0 {
		switch kinds[0] {
		case "image", "script", "storyboard", "creative", "alignment", "export", "story_check":
			args = append(args, "task_kind", kinds[0])
		}
	}
	if ref != "" {
		args = append(args, "reference_id", SafeID(ref))
	}
	if upstream != nil {
		status := upstream.HTTPStatus
		if status < 100 || status > 599 {
			status = 0
		}
		args = append(args, "upstream_http_status", status, "upstream_error_code", SafeUpstreamCode(upstream.Code), "upstream_request_id", SafeID(upstream.RequestID))
	}
	slog.ErrorContext(ctx, "backend operation failed", args...)
}
func panicLocation() (string, string, int) {
	var pcs [32]uintptr
	n := runtime.Callers(2, pcs[:])
	frames := runtime.CallersFrames(pcs[:n])
	const prefix = "github.com/adcwb/ai-creative-studio/backend/"
	for {
		frame, more := frames.Next()
		if strings.HasPrefix(frame.Function, prefix) && !strings.Contains(frame.Function, "/platform/diagnostics.") && !strings.Contains(frame.Function, "WithDiagnostics") && !strings.Contains(frame.Function, "guardResult") {
			return strings.TrimPrefix(frame.Function, prefix), filepath.Base(frame.File), frame.Line
		}
		if !more {
			break
		}
	}
	return "", "", 0
}
func Invoke(ctx context.Context, event string, fn func() error) (err error) {
	defer func() {
		if recover() != nil {
			err = ErrPanic
			Event(ctx, event, "execution_interrupted", "", nil)
		}
	}()
	return fn()
}

// net/http can log arbitrary panic values, paths and peer identities. The API
// handler emits its own bounded diagnostic; unexpected server errors stay generic.
type serverErrorWriter struct{}

func (serverErrorWriter) Write(p []byte) (int, error) {
	Event(context.Background(), "http_transport_error", "http_transport_error", "", nil)
	return len(p), nil
}
func ServerErrorLogger() *log.Logger { return log.New(serverErrorWriter{}, "", 0) }
