package diagnostics

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

func TestDiagnosticMetadataNeverEchoesUnrecognizedValues(t *testing.T) {
	secret := "Bearer sk-sp-private-token user@example.invalid private prompt"
	if SafeID(secret) == secret || !strings.HasPrefix(SafeID(secret), "sha256:") || SafeID(SafeID(secret)) != SafeID(secret) {
		t.Fatal("unsafe or unstable identifier")
	}
	if SafeUpstreamCode(secret) == secret || SafeCode(secret) != "internal_error" {
		t.Fatal("unsafe code")
	}
	id := "f63f4568-9026-4b82-b076-05f92f92f711"
	if SafeID(id) != id || SafeUpstreamCode("InvalidParameter") != "InvalidParameter" {
		t.Fatal("documented metadata lost")
	}
	var out bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&out, nil)))
	defer slog.SetDefault(old)
	Event(context.Background(), secret, secret, secret, &Upstream{HTTPStatus: 99999, Code: secret, RequestID: secret})
	if strings.Contains(out.String(), secret) || strings.Contains(out.String(), "user@example.invalid") {
		t.Fatal("raw metadata leaked")
	}
	var row map[string]any
	if json.Unmarshal(out.Bytes(), &row) != nil || row["upstream_http_status"] != float64(0) {
		t.Fatal("invalid structured log")
	}
}
func TestDiagnosticPanicDropsValuesAndRetainsInterruption(t *testing.T) {
	var out bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&out, nil)))
	defer slog.SetDefault(old)
	err := Invoke(context.Background(), "worker_panic", func() error { panic("sk-secret private-prompt user@example.invalid") })
	if !errors.Is(err, ErrPanic) || ErrorCode(err) != "execution_interrupted" || strings.Contains(out.String(), "sk-secret") || strings.Contains(out.String(), "private-prompt") || strings.Contains(out.String(), "goroutine") {
		t.Fatal("unsafe panic result/log")
	}
	if For("provider_rejected", 502).CauseKnown {
		t.Fatal("claimed an unknown supplier root cause")
	}
}

func TestDiagnosticHintsDistinguishLocalConfigurationFromSupplierRejection(t *testing.T) {
	for _, code := range []string{"provider_not_ready", "provider_unavailable", "gateway_target_denied", "unsupported_capability"} {
		if d := For(code, 0); d.Category != "configuration" || d.CauseKnown {
			t.Fatal("local readiness was misreported as supplier rejection", code, d)
		}
	}
	if d := For("provider_rejected", 502); d.Category != "upstream" || d.CauseKnown {
		t.Fatal("supplier rejection claimed a specific root cause", d)
	}
}
