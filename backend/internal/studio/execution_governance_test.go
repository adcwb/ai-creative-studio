package studio

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type testExecutionQuote struct {
	currency string
	amount   int64
}

func (q testExecutionQuote) QuoteExecution(context.Context, ExecutionRequest) (ExecutionQuote, error) {
	return ExecutionQuote{Currency: q.currency, MaximumMinor: q.amount, Evidence: "fixture:bounded-request", ExpiresAt: time.Now().Add(time.Minute)}, nil
}

type testExecutionModerator struct{ allow bool }

func (m testExecutionModerator) ReviewExecution(context.Context, ExecutionRequest) (ModerationDecision, error) {
	status := "review"
	if m.allow {
		status = "allow"
	}
	return ModerationDecision{Provider: "fixture", Model: "synthetic", Status: status, CheckedAt: time.Now()}, nil
}
func executionFixture(t *testing.T) (*ExecutionGovernor, ExecutionRequest) {
	t.Helper()
	l, e := OpenBudgetLedger(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	if e = l.SetLimit("tenant", "USD", 100); e != nil {
		t.Fatal(e)
	}
	return &ExecutionGovernor{Ledger: l, Quoter: testExecutionQuote{"USD", 30}, Moderator: testExecutionModerator{true}}, ExecutionRequest{ID: "task", Tenant: "tenant", Provider: "fixture", Model: "synthetic", Capability: "image", Selection: &RuntimeSelection{TenantID: "tenant", ActorID: "actor", ModelID: "config", Version: 1, Capability: "image", Currency: "USD", MaximumMinor: 30}}
}
func TestExecutionGovernanceDispatchIsDurableAndExactlyClaimed(t *testing.T) {
	g, r := executionFixture(t)
	var calls atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = g.Execute(context.Background(), r, func(ctx context.Context) (BillingEvidence, error) {
				calls.Add(1)
				if e := g.AuthorizeRuntime(ctx, *r.Selection); e != nil {
					t.Error(e)
				}
				entries, _ := g.Ledger.Entries("tenant", "USD")
				if len(entries) != 1 || entries[0].State != "unknown" {
					t.Error("dispatch preceded durable unknown")
				}
				return BillingEvidence{}, nil
			})
		}()
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("calls %d", calls.Load())
	}
	reopened, e := OpenBudgetLedger(g.Ledger.path[:len(g.Ledger.path)-len("budget.json")])
	if e != nil {
		t.Fatal(e)
	}
	g.Ledger = reopened
	if e = g.Execute(context.Background(), r, func(context.Context) (BillingEvidence, error) {
		t.Fatal("replayed after restart")
		return BillingEvidence{}, nil
	}); e == nil {
		t.Fatal("replay allowed")
	}
}
func TestExecutionGovernanceEvidenceAndUnknown(t *testing.T) {
	for _, state := range []string{"settled", "released", "unknown"} {
		t.Run(state, func(t *testing.T) {
			g, r := executionFixture(t)
			actual := int64(0)
			if state == "settled" {
				actual = 12
			}
			e := g.Execute(context.Background(), r, func(context.Context) (BillingEvidence, error) {
				return BillingEvidence{State: state, ActualMinor: actual, Reference: "fixture:receipt"}, errors.New("synthetic transport error")
			})
			if e == nil {
				t.Fatal("lost error")
			}
			entries, _ := g.Ledger.Entries("tenant", "USD")
			if entries[0].State != state || entries[0].Actual != actual {
				t.Fatal(entries)
			}
		})
	}
}
func TestExecutionGovernancePermitCannotEscapeOrCrossTenant(t *testing.T) {
	g, r := executionFixture(t)
	var saved context.Context
	e := g.Execute(context.Background(), r, func(ctx context.Context) (BillingEvidence, error) {
		saved = ctx
		other := *r.Selection
		other.TenantID = "other"
		if g.AuthorizeRuntime(ctx, other) == nil {
			t.Fatal("cross tenant")
		}
		other = *r.Selection
		other.Version++
		if g.AuthorizeRuntime(ctx, other) == nil {
			t.Fatal("cross version")
		}
		return BillingEvidence{}, nil
	})
	if e != nil {
		t.Fatal(e)
	}
	if g.AuthorizeRuntime(saved, *r.Selection) == nil || g.AuthorizeRuntime(context.Background(), *r.Selection) == nil {
		t.Fatal("escaped permit")
	}
}
func TestExecutionGovernanceRefusesUnreviewedOrUnfunded(t *testing.T) {
	for _, mode := range []string{"moderation", "quote", "currency", "budget", "tenant"} {
		t.Run(mode, func(t *testing.T) {
			g, r := executionFixture(t)
			switch mode {
			case "moderation":
				g.Moderator = testExecutionModerator{}
			case "quote":
				g.Quoter = nil
			case "currency":
				g.Quoter = testExecutionQuote{"JPY", 30}
			case "budget":
				g.Quoter = testExecutionQuote{"USD", 101}
			case "tenant":
				r.Tenant = "other"
			}
			if g.Execute(context.Background(), r, func(context.Context) (BillingEvidence, error) {
				t.Fatal("dispatch forbidden")
				return BillingEvidence{}, nil
			}) == nil {
				t.Fatal("allowed")
			}
			entries, _ := g.Ledger.Entries("tenant", "USD")
			if len(entries) != 0 {
				t.Fatal(entries)
			}
		})
	}
}

func TestExecutionGovernanceRuntimeAdapterSettlementAndOutputReview(t *testing.T) {
	runtime, sel, _ := runtimeFixture(t)
	ledger, e := OpenBudgetLedger(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	if e = ledger.SetLimit("tenant-a", "USD", 100); e != nil {
		t.Fatal(e)
	}
	g := &ExecutionGovernor{Ledger: ledger, Quoter: testExecutionQuote{"USD", 30}, Moderator: testExecutionModerator{true}}
	runtime.authorize = g.AuthorizeRuntime
	sel.Currency = "USD"
	sel.MaximumMinor = 30
	request := ExecutionRequest{ID: "analysis:fixture", Tenant: "tenant-a", Provider: "openai-text", Model: "synthetic-model", Capability: "text", Text: "Synthetic fixture", Selection: &sel}
	var sends int
	e = g.Execute(context.Background(), request, func(ctx context.Context) (BillingEvidence, error) {
		e := runtime.Execute(ctx, sel, func(ctx context.Context, p RuntimeProviders) error {
			runtimeReplaceTransport(p, func(req *http.Request) (*http.Response, error) {
				sends++
				content := `{"summary":"Synthetic summary","questions":[],"suggestions":[]}`
				data, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]string{"content": content}}}})
				return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(data)), Header: http.Header{}}, nil
			})
			result, e := p.Creative.Analyze(ctx, ProjectInput{Title: "Test title", Brief: request.Text})
			if e != nil {
				return e
			}
			return g.ReviewResult(ctx, ExecutionRequest{Tenant: request.Tenant, Provider: request.Provider, Model: request.Model, Capability: "text", Text: result.Summary})
		})
		return BillingEvidence{State: "settled", ActualMinor: 9, Reference: "synthetic-authoritative-receipt"}, e
	})
	if e != nil || sends != 1 {
		t.Fatal(e, sends)
	}
	entries, _ := ledger.Entries("tenant-a", "USD")
	if len(entries) != 1 || entries[0].State != "settled" || entries[0].Actual != 9 {
		t.Fatal(entries)
	}
}
func TestExecutionGovernanceAcceptedReservationAndPreflightRelease(t *testing.T) {
	g, r := executionFixture(t)
	if _, e := g.Ledger.Reserve(r.ID, r.Tenant, r.Provider, r.Model, "USD", 50); e != nil {
		t.Fatal(e)
	}
	if e := g.Execute(context.Background(), r, func(context.Context) (BillingEvidence, error) { return BillingEvidence{}, nil }); e != nil {
		t.Fatal(e)
	}
	entries, _ := g.Ledger.Entries(r.Tenant, "USD")
	if entries[0].State != "unknown" || entries[0].Reserved != 50 {
		t.Fatal(entries)
	}
	if e := g.Ledger.ReleaseUndispatched(r.ID, r.Tenant, "USD"); e != nil {
		t.Fatal(e)
	}
	entries, _ = g.Ledger.Entries(r.Tenant, "USD")
	if entries[0].State != "unknown" {
		t.Fatal("released attempted dispatch")
	}
	r.ID = "preflight"
	if _, e := g.Ledger.Reserve(r.ID, r.Tenant, r.Provider, r.Model, "USD", 30); e != nil {
		t.Fatal(e)
	}
	g.Moderator = testExecutionModerator{}
	if e := g.Execute(context.Background(), r, func(context.Context) (BillingEvidence, error) {
		t.Fatal("must not dispatch")
		return BillingEvidence{}, nil
	}); e == nil {
		t.Fatal("moderation bypass")
	}
	entries, _ = g.Ledger.Entries(r.Tenant, "USD")
	for _, entry := range entries {
		if entry.ID == r.ID && entry.State != "released" {
			t.Fatal(entry)
		}
	}
}

func TestExecutionDigestBindsResources(t *testing.T) {
	task := Task{ID: "image", SpaceID: "tenant", Provider: "openai-images", Model: "synthetic", Input: Input{Prompt: "synthetic image", Ratio: "1:1"}}
	first := ExecutionRequestDigest(imageExecutionRequest(task))
	task.Ratio = "16:9"
	if first == ExecutionRequestDigest(imageExecutionRequest(task)) {
		t.Fatal("image ratio not bound")
	}
	r := imageExecutionRequest(task)
	r.Selection = &RuntimeSelection{Voice: "voice-a"}
	first = ExecutionRequestDigest(r)
	r.Selection.Voice = "voice-b"
	if first == ExecutionRequestDigest(r) {
		t.Fatal("voice not bound")
	}
	r.Media = []ExecutionMedia{{Data: []byte("one"), MIME: "image/png"}}
	first = ExecutionRequestDigest(r)
	r.Media[0].Data = []byte("two")
	if first == ExecutionRequestDigest(r) {
		t.Fatal("media not bound")
	}
}

func TestExecutionRejectsCapabilityMismatchBeforeDispatch(t *testing.T) {
	g, r := executionFixture(t)
	r.Selection.Capability = "video"
	if e := g.Execute(context.Background(), r, func(context.Context) (BillingEvidence, error) {
		t.Fatal("mismatched capability dispatched")
		return BillingEvidence{}, nil
	}); e == nil {
		t.Fatal("mismatch accepted")
	}
	entries, _ := g.Ledger.Entries("tenant", "USD")
	if len(entries) != 0 {
		t.Fatal(entries)
	}
}

type recoveryTestQuote struct{ testExecutionQuote }

func (recoveryTestQuote) RecoveryExpiry(context.Context, ExecutionRequest) (time.Time, error) {
	return time.Now().Add(time.Minute), nil
}
func TestExecutionRecoveryDoesNotReleasePriorUnknown(t *testing.T) {
	runtime, sel, _ := runtimeFixture(t)
	sel.Currency = "USD"
	sel.MaximumMinor = 30
	ledger, e := OpenBudgetLedger(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	if e = ledger.SetLimit("tenant-a", "USD", 100); e != nil {
		t.Fatal(e)
	}
	g := &ExecutionGovernor{Ledger: ledger, Quoter: recoveryTestQuote{testExecutionQuote{"USD", 30}}, Moderator: testExecutionModerator{true}}
	runtime.authorize = g.AuthorizeRuntime
	r := ExecutionRequest{UpstreamID: "synthetic-upstream", ID: "old", Tenant: "tenant-a", Provider: "openai-text", Model: "synthetic-model", Capability: "text", Selection: &sel}
	if _, e = ledger.ClaimDispatch(r.ID, r.Tenant, r.Provider, r.Model, "USD", 30); e != nil {
		t.Fatal(e)
	}
	if e = g.RecoveryReady(context.Background(), runtime, r); e != nil {
		t.Fatal(e)
	}
	if e = g.ExecuteRecovery(context.Background(), runtime, r, func(context.Context, RuntimeProviders) error { return errors.New("before read-only request") }); e == nil {
		t.Fatal("lost error")
	}
	entries, _ := ledger.Entries(r.Tenant, "USD")
	if entries[0].State != "unknown" || entries[0].Reserved != 30 {
		t.Fatal("prior ambiguity incorrectly released", entries)
	}
	g.Quoter = testExecutionQuote{"USD", 30}
	if g.RecoveryReady(context.Background(), runtime, r) == nil {
		t.Fatal("assumed free query")
	}
}

type datedExecutionModerator struct{ at time.Time }

func (m datedExecutionModerator) ReviewExecution(context.Context, ExecutionRequest) (ModerationDecision, error) {
	return ModerationDecision{Provider: "synthetic", Model: "synthetic", Status: "allow", CheckedAt: m.at}, nil
}
func TestExecutionModerationFreshnessAtInputAndPublication(t *testing.T) {
	for _, at := range []time.Time{{}, time.Now().Add(-6 * time.Minute), time.Now().Add(time.Minute)} {
		g, r := executionFixture(t)
		g.Moderator = datedExecutionModerator{at}
		called := false
		if err := g.Execute(context.Background(), r, func(context.Context) (BillingEvidence, error) { called = true; return BillingEvidence{}, nil }); err == nil || called {
			t.Fatal("stale/future input review reached dispatch")
		}
		if g.ReviewResult(context.Background(), r) == nil {
			t.Fatal("stale/future output review published")
		}
	}
	now := time.Now()
	for _, at := range []time.Time{now, now.Add(-4 * time.Minute), now.Add(29 * time.Second)} {
		if !freshModerationAllow(ModerationDecision{Provider: "synthetic", Model: "synthetic", Status: "allow", CheckedAt: at}, now) {
			t.Fatal("valid bounded clock skew rejected")
		}
	}
}
