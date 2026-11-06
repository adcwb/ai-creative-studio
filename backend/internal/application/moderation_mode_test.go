package application

import (
	"context"
	"errors"
	"testing"
)

func TestDisabledModerationDoesNotPretendAllowOrRemoveDispatchGuards(t *testing.T) {
	g, request := executionFixture(t)
	m := &sourceBoundaryModerator{}
	g.Moderator = m
	g.ModerationDisabled = true
	if !g.Ready() || g.ModerationMode() != "disabled" {
		t.Fatal("explicitly disabled review blocked model execution")
	}
	calls := 0
	dispatch := func(context.Context) (BillingEvidence, error) { calls++; return BillingEvidence{}, nil }
	invalid := request
	invalid.Tenant = "other-space"
	if err := g.Execute(context.Background(), invalid, dispatch); err == nil || calls != 0 {
		t.Fatal("disabled review bypassed tenant validation")
	}
	denied := context.WithValue(context.Background(), libraryDispatchKey{}, func(context.Context) error { return providerFailure("library_use_denied") })
	if err := g.Execute(denied, request, dispatch); err == nil || calls != 0 {
		t.Fatal("disabled review bypassed source authorization")
	}
	if err := g.Execute(context.Background(), request, dispatch); err != nil || calls != 1 || m.calls != 0 {
		t.Fatal("disabled review ran an adapter or blocked dispatch", err, calls, m.calls)
	}
	if err := g.ReviewResult(context.Background(), request); err != nil || m.calls != 0 {
		t.Fatal("disabled output review fabricated an allow", err)
	}
	if err := g.Execute(context.Background(), request, dispatch); err == nil || calls != 1 {
		t.Fatal("disabled review allowed duplicate generation", err, calls)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if !errors.Is(g.ReviewResult(ctx, request), context.Canceled) {
		t.Fatal("disabled review ignored cancellation")
	}
	entries, err := g.Ledger.Entries(request.Tenant, "USD")
	if err != nil || len(entries) != 0 {
		t.Fatal("disabled review restored financial reservations", err)
	}
}

func TestModerationDeploymentDefaultsDisabledAndExplicitRequiredFailsClosed(t *testing.T) {
	for _, key := range []string{"EXECUTION_POLICY_FILE", "GATEWAY_MODERATION_COMMAND", "GATEWAY_MODERATION_SOURCE", "GATEWAY_MODERATION_EXPIRES_AT", "CONTENT_MODERATION_MODE"} {
		t.Setenv(key, "")
	}
	s := testStore(t)
	if err := ConfigureExecutionFromEnv(s); err != nil || !s.execution.Ready() || s.execution.ModerationMode() != "disabled" || s.execution.Moderator != nil {
		t.Fatal("unconfigured review blocked the deployment", err)
	}
	t.Setenv("CONTENT_MODERATION_MODE", "required")
	if err := ConfigureExecutionFromEnv(s); err == nil {
		t.Fatal("required mode accepted a missing review adapter")
	}
	t.Setenv("CONTENT_MODERATION_MODE", "always_allow")
	if err := ConfigureExecutionFromEnv(s); err == nil {
		t.Fatal("unknown review mode accepted")
	}
}
