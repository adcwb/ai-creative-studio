package studio

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/adcwb/ai-creative-studio/backend/internal/tracing"
	"sync"
	"time"
)

// ExecutionRequest is assembled from the durable task, never from a submitted
// billing amount. Text is reviewed in memory and is not written to the ledger.
type ExecutionRequest struct {
	UpstreamID                                    string
	ID, Tenant, Provider, Model, Capability, Text string
	HasMedia                                      bool
	Parameters                                    json.RawMessage
	Stage                                         string
	Media                                         []ExecutionMedia
	Selection                                     *RuntimeSelection
}
type ExecutionMedia struct {
	Data []byte
	MIME string
}

func ExecutionRequestDigest(r ExecutionRequest) string {
	hashes := []string{}
	for _, m := range r.Media {
		sum := sha256.Sum256(m.Data)
		hashes = append(hashes, m.MIME+":"+hex.EncodeToString(sum[:]))
	}
	voice := ""
	if r.Selection != nil {
		voice = r.Selection.Voice
	}
	raw, _ := json.Marshal(struct {
		Contract, Provider, Model, Capability, Text, Voice string
		HasMedia                                           bool
		Parameters                                         json.RawMessage
		MediaHashes                                        []string
	}{"studio-request-v1", r.Provider, r.Model, r.Capability, r.Text, voice, r.HasMedia, r.Parameters, hashes})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func imageExecutionRequest(t Task) ExecutionRequest {
	parameters, _ := json.Marshal(struct{ Prompt, Kind, Ratio, Style string }{t.Prompt, t.Kind, t.Ratio, t.Style})
	return ExecutionRequest{ID: "image:" + t.ID, Tenant: t.SpaceID, Provider: t.Provider, Model: t.Model, Capability: "image", Text: t.Prompt, Parameters: parameters, Selection: t.RuntimeSelection}
}

type ExecutionQuote struct {
	Currency     string
	MaximumMinor int64
	Evidence     string
	ExpiresAt    time.Time
}
type ExecutionQuoter interface {
	QuoteExecution(context.Context, ExecutionRequest) (ExecutionQuote, error)
}

// ExecutionModerator must explicitly review the requested modality. A text-only
// allow response cannot authorize images/audio/video by pretending to review it.
type ExecutionModerator interface {
	ReviewExecution(context.Context, ExecutionRequest) (ModerationDecision, error)
}
type BillingEvidence struct {
	State       string // settled or released; empty/unknown keeps the whole reservation
	ActualMinor int64
	Reference   string
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

// Execute persists unknown BEFORE dispatch. A crash, timeout, successful output
// without billing evidence, or ambiguous HTTP failure keeps the maximum bound.
// Replaying the same task never invokes dispatch twice. The ledger is not a
// substitute for atomic DB task acceptance; a stranded reservation is safe.
func (g *ExecutionGovernor) Execute(ctx context.Context, r ExecutionRequest, dispatch func(context.Context) (BillingEvidence, error)) (finalErr error) {
	ctx, span := tracing.Start(ctx, "governance.execute", tracing.Attributes{Kind: r.Capability, Provider: r.Provider})
	outcome := "denied"
	defer func() { span.End(outcome) }()
	claimed := false
	defer func() {
		if !claimed && g != nil && g.Ledger != nil && r.Selection != nil {
			if e := g.Ledger.ReleaseUndispatched(r.ID, r.Tenant, r.Selection.Currency); e != nil {
				finalErr = e
			}
		}
	}()

	if g == nil || g.Ledger == nil || g.Quoter == nil || g.Moderator == nil || dispatch == nil || r.ID == "" || r.Tenant == "" || r.Provider == "" || r.Model == "" || r.Selection == nil || r.Selection.TenantID != r.Tenant || r.Selection.Capability != r.Capability || r.Selection.ActorID == "" || r.Selection.ModelID == "" || r.Selection.Version <= 0 {
		return ErrExecutionGovernance
	}
	if r.Selection.RequestDigest != "" && r.Selection.RequestDigest != ExecutionRequestDigest(r) {
		return ErrExecutionGovernance
	}
	quote, err := g.Quoter.QuoteExecution(ctx, r)
	if err != nil || quote.Currency == "" || quote.MaximumMinor <= 0 || quote.Evidence == "" || len(quote.Evidence) > 256 || !quote.ExpiresAt.After(time.Now()) {
		return ErrExecutionGovernance
	}
	if r.Selection.Currency != quote.Currency || r.Selection.MaximumMinor < quote.MaximumMinor {
		return ErrExecutionGovernance
	}
	if _, err = CurrencyMinorUnits(quote.Currency); err != nil {
		return ErrExecutionGovernance
	}
	decision, err := g.Moderator.ReviewExecution(ctx, r)
	if err != nil || !freshModerationAllow(decision, time.Now()) {
		return ErrExecutionGovernance
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if !quote.ExpiresAt.After(time.Now()) {
		return ErrExecutionGovernance
	}
	_, err = g.Ledger.ClaimDispatch(r.ID, r.Tenant, r.Provider, r.Model, quote.Currency, quote.MaximumMinor)
	if err != nil {
		return err
	}
	claimed = true
	outcome = "unknown"
	permit := &executionPermit{governor: g, selection: *r.Selection, id: r.ID, tenant: r.Tenant, currency: quote.Currency, active: true, expires: quote.ExpiresAt}
	runCtx := context.WithValue(ctx, executionPermitKey{}, permit)
	defer func() { permit.mu.Lock(); permit.active = false; permit.mu.Unlock() }()
	if ctx.Err() != nil {
		// No dispatch callback was invoked; this is local proof of no remote submit.
		if e := g.Ledger.Resolve(r.ID, r.Tenant, quote.Currency, "released", 0, "cancelled_before_dispatch"); e != nil {
			return e
		}
		return ctx.Err()
	}
	evidence, runErr := dispatch(runCtx)
	if g.Billing != nil && !(evidence.State == "released" && evidence.Reference == "local:no_dispatch") {
		if checked, e := g.Billing.ReadExecutionBilling(context.WithoutCancel(ctx), r); e == nil {
			evidence = checked
		}
	}
	if evidence.State == "settled" || evidence.State == "released" {
		if evidence.State == "released" && evidence.ActualMinor != 0 {
			return ErrExecutionGovernance
		}
		if err = g.Ledger.Resolve(r.ID, r.Tenant, quote.Currency, evidence.State, evidence.ActualMinor, evidence.Reference); err != nil {
			return err
		}
	} else if evidence.State != "" && evidence.State != "unknown" {
		return ErrExecutionGovernance
	}
	if evidence.State == "settled" || evidence.State == "released" {
		outcome = "ok"
		if runErr != nil {
			outcome = "error"
		}
	}
	return runErr
}

func (g *ExecutionGovernor) Ready() bool {
	if g == nil || g.Ledger == nil || g.Quoter == nil || g.Moderator == nil {
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
	decision, e := g.Moderator.ReviewExecution(ctx, r)
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

// ExecuteRecovery grants only GET requests and never claims another reservation.
// Queries must be explicitly documented free by the operator's fresh policy.
func (g *ExecutionGovernor) ExecuteRecovery(ctx context.Context, runtime *ModelRuntime, r ExecutionRequest, run func(context.Context, RuntimeProviders) error) error {
	ctx, span := tracing.Start(ctx, "governance.execute", tracing.Attributes{Kind: r.Capability, Provider: r.Provider})
	defer span.End("unknown")
	if !g.Ready() || runtime == nil || r.Selection == nil || r.Capability != r.Selection.Capability || r.Tenant != r.Selection.TenantID || r.UpstreamID == "" {
		return ErrExecutionGovernance
	}
	current, entry, e := runtime.Resolve(ctx, r.Tenant, r.Selection.ActorID, r.Selection.ModelID, r.Selection.Capability)
	if e != nil || current.Version != r.Selection.Version || entry.Provider != r.Provider || entry.Model != r.Model {
		return ErrExecutionGovernance
	}
	authorizer, ok := g.Quoter.(ExecutionRecoveryAuthorizer)
	if !ok {
		return ErrExecutionGovernance
	}
	expiry, e := authorizer.RecoveryExpiry(ctx, r)
	if e != nil || !expiry.After(time.Now()) {
		return ErrExecutionGovernance
	}
	entries, e := g.Ledger.Entries(r.Tenant, r.Selection.Currency)
	if e != nil {
		return e
	}
	found := false
	for _, entry := range entries {
		if entry.ID == r.ID && entry.Provider == r.Provider && entry.Model == r.Model && entry.State == "unknown" {
			found = true
		}
	}
	if !found {
		return ErrExecutionGovernance
	}
	permit := &executionPermit{governor: g, selection: *r.Selection, id: r.ID, tenant: r.Tenant, currency: r.Selection.Currency, active: true, expires: expiry, upstream: r.UpstreamID}
	defer func() { permit.mu.Lock(); permit.active = false; permit.mu.Unlock() }()
	runCtx := context.WithValue(context.WithValue(ctx, executionPermitKey{}, permit), executionReadOnlyKey{}, true)
	e = runtime.Execute(runCtx, *r.Selection, run)
	// No success-to-free inference: query results do not provide billing evidence.
	return e
}

// RecoveryReady performs only local/database validation; no secret decryption,
// moderation command, provider query or new reservation occurs at acceptance.
func (g *ExecutionGovernor) RecoveryReady(ctx context.Context, runtime *ModelRuntime, r ExecutionRequest) error {
	if !g.Ready() || runtime == nil || r.Selection == nil || r.Tenant != r.Selection.TenantID || r.Capability != r.Selection.Capability || r.UpstreamID == "" {
		return ErrExecutionGovernance
	}
	current, entry, e := runtime.Resolve(ctx, r.Tenant, r.Selection.ActorID, r.Selection.ModelID, r.Selection.Capability)
	if e != nil || current.Version != r.Selection.Version || entry.Provider != r.Provider || entry.Model != r.Model {
		return ErrExecutionGovernance
	}
	policy, ok := g.Quoter.(ExecutionRecoveryAuthorizer)
	if !ok {
		return ErrExecutionGovernance
	}
	expiry, e := policy.RecoveryExpiry(ctx, r)
	if e != nil || !expiry.After(time.Now()) {
		return ErrExecutionGovernance
	}
	entries, e := g.Ledger.Entries(r.Tenant, r.Selection.Currency)
	if e != nil {
		return e
	}
	for _, entry := range entries {
		if entry.ID == r.ID && entry.Provider == r.Provider && entry.Model == r.Model && entry.State == "unknown" {
			return nil
		}
	}
	return ErrExecutionGovernance
}
