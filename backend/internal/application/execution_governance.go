package application

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/adcwb/ai-creative-studio/backend/internal/platform/tracing"
)

func imageExecutionRequest(t Task) ExecutionRequest {
	parameters, _ := json.Marshal(struct {
		Prompt, Kind, Ratio, Style string
		ImageSpecification         *reviewedImageSpecification `json:"image_specification,omitempty"`
	}{t.Prompt, t.Kind, t.Ratio, t.Style, imageCandidateSpecification(t.Provider, t.Model)})
	return ExecutionRequest{ID: "image:" + t.ID, Tenant: t.SpaceID, Provider: t.Provider, Model: t.Model, Capability: "image", Text: t.Prompt, Parameters: parameters, Selection: t.RuntimeSelection}
}

type ExecutionGovernor struct {
	Ledger    *BudgetLedger
	Quoter    ExecutionQuoter
	Moderator ExecutionModerator
	Billing   ExecutionBillingReader
}
type executionPermitKey struct{}
type executionPermit struct {
	upstream             string
	governor             *ExecutionGovernor
	selection            RuntimeSelection
	id, tenant, currency string
	expires              time.Time
	active               bool
	mu                   sync.Mutex
}

var ErrExecutionGovernance = errors.New("execution governance unavailable or denied")

// AuthorizeRuntime is validation-only and may be called before every HTTP send.
// Only Execute can mint the unexported permit, which expires at callback return.
func (g *ExecutionGovernor) AuthorizeRuntime(ctx context.Context, s RuntimeSelection) error {
	p, ok := ctx.Value(executionPermitKey{}).(*executionPermit)
	if !ok || p.governor != g || ctx.Err() != nil {
		return ErrExecutionGovernance
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.active || p.selection != s || !p.expires.After(time.Now()) {
		return ErrExecutionGovernance
	}
	return nil
}

// Execute reviews input and persists a nonfinancial dispatch receipt before
// network I/O. Budgets, quotes, billing evidence and client cost fields do not
// authorize execution. Identity, review and duplicate-dispatch guards still do.
func (g *ExecutionGovernor) Execute(ctx context.Context, r ExecutionRequest, dispatch func(context.Context) (BillingEvidence, error)) error {
	ctx, span := tracing.Start(ctx, "governance.execute", tracing.Attributes{Kind: r.Capability, Provider: r.Provider})
	outcome := "denied"
	defer func() { span.End(outcome) }()
	claimed := false
	defer func() {
		// Preserve the meaning of pre-upgrade queued reservations. New tasks do
		// not create financial entries; a bookkeeping error cannot grant a call.
		if !claimed && g != nil && g.Ledger != nil && r.Selection != nil {
			_ = g.Ledger.ReleaseUndispatched(r.ID, r.Tenant, r.Selection.Currency)
		}
	}()
	if !g.Ready() || dispatch == nil || r.ID == "" || r.Tenant == "" || r.Provider == "" || r.Model == "" || r.Selection == nil || r.Selection.TenantID != r.Tenant || r.Selection.Capability != r.Capability || r.Selection.ActorID == "" || r.Selection.ModelID == "" || r.Selection.Version <= 0 {
		return ErrExecutionGovernance
	}
	if err := checkLibraryDispatch(ctx); err != nil {
		return err
	}
	decision, err := g.review(ctx, r)
	if sourceErr := checkLibraryDispatch(ctx); sourceErr != nil {
		return sourceErr
	}
	if err != nil || !freshModerationAllow(decision, time.Now()) {
		return ErrExecutionGovernance
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := g.Ledger.claimExecution(r); err != nil {
		return err
	}
	claimed = true
	permit := &executionPermit{governor: g, selection: *r.Selection, id: r.ID, tenant: r.Tenant, active: true, expires: time.Now().Add(15 * time.Minute)}
	defer func() { permit.mu.Lock(); permit.active = false; permit.mu.Unlock() }()
	runCtx := context.WithValue(ctx, executionPermitKey{}, permit)
	if err := ctx.Err(); err != nil {
		return err
	}
	outcome = "unknown"
	_, err = dispatch(runCtx)
	if err == nil {
		outcome = "ok"
	}
	return err
}

func (g *ExecutionGovernor) Ready() bool {
	if g == nil || g.Ledger == nil || g.Moderator == nil {
		return false
	}
	if readiness, ok := g.Moderator.(interface{ ReviewReady() bool }); ok {
		return readiness.ReviewReady()
	}
	return true
}

// ConfigureExecutionGovernance must run before serving requests or workers.
func ConfigureExecutionGovernance(s *FileStore, g *ExecutionGovernor) {
	s.execution = g
	if s.media != nil && s.media.alignment != nil {
		s.media.alignment.execution = g
	}
}
func (s *FileStore) executeManaged(ctx context.Context, request ExecutionRequest, run func(context.Context, RuntimeProviders) error) error {
	return s.execution.ExecuteRuntime(ctx, s.runtime, request, run)
}
func (g *ExecutionGovernor) ExecuteRuntime(ctx context.Context, runtime *ModelRuntime, request ExecutionRequest, run func(context.Context, RuntimeProviders) error) error {
	if runtime == nil || !g.Ready() || request.Selection == nil {
		if g != nil && g.Ledger != nil && request.Selection != nil {
			if e := g.Ledger.ReleaseUndispatched(request.ID, request.Tenant, request.Selection.Currency); e != nil {
				return e
			}
		}
		return ErrExecutionGovernance
	}
	current, entry, e := runtime.Resolve(ctx, request.Tenant, request.Selection.ActorID, request.Selection.ModelID, request.Selection.Capability)
	if e != nil || current.Version != request.Selection.Version || entry.Provider != request.Provider || entry.Model != request.Model {
		if e := g.Ledger.ReleaseUndispatched(request.ID, request.Tenant, request.Selection.Currency); e != nil {
			return e
		}
		return ErrExecutionGovernance
	}
	return g.Execute(ctx, request, func(ctx context.Context) (BillingEvidence, error) {
		err := runtime.Execute(ctx, *request.Selection, run)
		if err != nil && RuntimeNoDispatch(err) {
			return BillingEvidence{State: "released", Reference: "local:no_dispatch"}, err
		}
		// Existing adapters do not expose authoritative billed amounts. Never infer
		// free usage from an error or settle a reservation from an output alone.
		return BillingEvidence{}, err
	})
}

// A stale cached decision or a far-future timestamp cannot authorize new input
// or publication. Bounded skew allows independently clocked local adapters.
func freshModerationAllow(d ModerationDecision, now time.Time) bool {
	return d.Status == "allow" && d.Provider != "" && d.Model != "" && !d.CheckedAt.IsZero() && !d.CheckedAt.Before(now.Add(-5*time.Minute)) && !d.CheckedAt.After(now.Add(30*time.Second))
}

func (g *ExecutionGovernor) ReviewResult(ctx context.Context, r ExecutionRequest) error {
	if g == nil || g.Moderator == nil {
		return ErrExecutionGovernance
	}
	r.Stage = "output"
	decision, e := g.review(ctx, r)
	if e != nil || !freshModerationAllow(decision, time.Now()) {
		return ErrExecutionGovernance
	}
	return nil
}

type executionReadOnlyKey struct{}

func executionReadOnly(ctx context.Context) bool {
	v, _ := ctx.Value(executionReadOnlyKey{}).(bool)
	return v
}

func executionRecoveryUpstream(ctx context.Context) string {
	if !executionReadOnly(ctx) {
		return ""
	}
	p, ok := ctx.Value(executionPermitKey{}).(*executionPermit)
	if !ok {
		return ""
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.active || !p.expires.After(time.Now()) {
		return ""
	}
	return p.upstream
}

type ExecutionRecoveryAuthorizer interface {
	RecoveryExpiry(context.Context, ExecutionRequest) (time.Time, error)
}

// ExecuteRecovery only grants the adapter's exact GET recovery paths for an
// existing dispatch. It cannot create a second upstream generation request.
func (g *ExecutionGovernor) ExecuteRecovery(ctx context.Context, runtime *ModelRuntime, r ExecutionRequest, run func(context.Context, RuntimeProviders) error) error {
	if err := g.RecoveryReady(ctx, runtime, r); err != nil {
		return err
	}
	permit := &executionPermit{governor: g, selection: *r.Selection, id: r.ID, tenant: r.Tenant, active: true, expires: time.Now().Add(15 * time.Minute), upstream: r.UpstreamID}
	defer func() { permit.mu.Lock(); permit.active = false; permit.mu.Unlock() }()
	runCtx := context.WithValue(context.WithValue(ctx, executionPermitKey{}, permit), executionReadOnlyKey{}, true)
	return runtime.Execute(runCtx, *r.Selection, run)
}

// RecoveryReady validates authorization and existing dispatch identity without
// reading credentials, charging, quoting or submitting a generation request.
func (g *ExecutionGovernor) RecoveryReady(ctx context.Context, runtime *ModelRuntime, r ExecutionRequest) error {
	if !g.Ready() || runtime == nil || r.Selection == nil || r.Tenant != r.Selection.TenantID || r.Capability != r.Selection.Capability || r.UpstreamID == "" {
		return ErrExecutionGovernance
	}
	current, entry, err := runtime.Resolve(ctx, r.Tenant, r.Selection.ActorID, r.Selection.ModelID, r.Selection.Capability)
	if err != nil || current.Version != r.Selection.Version || entry.Provider != r.Provider || entry.Model != r.Model || !g.Ledger.hasExecution(r) {
		return ErrExecutionGovernance
	}
	return nil
}
