package application

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSelectedImageModelHasNoHourlyCostQuota(t *testing.T) {
	s := testStore(t)
	in := Input{Prompt: "Synthetic configured image", Kind: "general", ModelRequest: ModelRequest{
		selection:    &RuntimeSelection{TenantID: "personal-demo", ActorID: "synthetic", ModelID: "synthetic-config", Version: 1, Capability: "image"},
		selectedCaps: Capabilities{Provider: "gateway-images", Model: "synthetic-image", Mode: "real", Ready: true},
	}}
	for i := 0; i < 21; i++ {
		task, _, err := s.Create("personal-demo", fmt.Sprintf("cost-free-image-%d", i), in)
		if err != nil {
			t.Fatal("historical attempt count blocked configured model", i, err)
		}
		// Complete each local fixture without calling a provider, retaining all
		// failed attempts. Queue-concurrency protection must still apply.
		if err := s.Finish(task, GenerationResult{}, errors.New("synthetic local failure")); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 4; i++ {
		if _, _, err := s.Create("personal-demo", fmt.Sprintf("concurrent-image-%d", i), in); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := s.Create("personal-demo", "concurrent-image-overflow", in); !errors.Is(err, ErrProviderNotReady) {
		t.Fatal("removing cost quota removed concurrency protection", err)
	}
}

func TestUsageCountsDoNotSumHistoricalCostCeilings(t *testing.T) {
	s, err := openModelUsageStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for i, currency := range []string{"USD", "CNY", ""} {
		if err := s.save(ModelCall{ID: fmt.Sprintf("call-%d", i), Tenant: "tenant", GatewayID: "gateway", ModelConfigID: "model", Model: "synthetic", Capability: "text", State: "succeeded", HTTPCalls: 1, StartedAt: now, FinishedAt: &now, Currency: currency, MaximumMinor: 9007199254740991}); err != nil {
			t.Fatal(err)
		}
	}
	r, err := s.report("tenant", httptest.NewRequest("GET", "/model-usage", nil))
	if err != nil || len(r.Summary) != 1 || r.Summary[0].Calls != 3 || r.Summary[0].MaximumMinor != 0 || r.Summary[0].Currency != "" || len(r.Items) != 3 {
		t.Fatal("historical currency/ceiling restricted nonfinancial statistics", r, err)
	}
	for _, call := range r.Items {
		if call.MaximumMinor != 9007199254740991 {
			t.Fatal("historical per-call metadata was rewritten")
		}
	}
	other, err := s.report("another-tenant", httptest.NewRequest("GET", "/model-usage", nil))
	if err != nil || other.Total != 0 {
		t.Fatal("usage report crossed tenant boundary", other, err)
	}
}

func TestExecutionNeedsNoQuotesLimitsOrCostConsent(t *testing.T) {
	g, r := executionFixture(t)
	g.Quoter = nil
	if err := g.Ledger.SetLimit(r.Tenant, "USD", 0); err != nil {
		t.Fatal(err)
	}
	r.Selection.Currency, r.Selection.MaximumMinor, r.Selection.RequestDigest = "", 0, "obsolete-client-quote"
	for _, id := range []string{"image:first", "image:second", "script:third", "storyboard:fourth"} {
		r.ID = id
		if err := g.Execute(context.Background(), r, func(ctx context.Context) (BillingEvidence, error) {
			return BillingEvidence{}, g.AuthorizeRuntime(ctx, *r.Selection)
		}); err != nil {
			t.Fatal("cost configuration blocked execution", err)
		}
	}
	entries, err := g.Ledger.Entries(r.Tenant, "USD")
	if err != nil || len(entries) != 0 {
		t.Fatal("new calls created financial reservations", entries, err)
	}
}

func TestExecutionPreservesExhaustedHistoricalLedgerWithoutUsingIt(t *testing.T) {
	g, r := executionFixture(t)
	if _, err := g.Ledger.ClaimDispatch("historical", r.Tenant, r.Provider, r.Model, "USD", 100); err != nil {
		t.Fatal(err)
	}
	before, _ := g.Ledger.Entries(r.Tenant, "USD")
	g.Quoter = nil
	r.Selection.Currency, r.Selection.MaximumMinor = "", 0
	if err := g.Execute(context.Background(), r, func(context.Context) (BillingEvidence, error) { return BillingEvidence{}, nil }); err != nil {
		t.Fatal("exhausted historical budget blocked a new call", err)
	}
	after, _ := g.Ledger.Entries(r.Tenant, "USD")
	if !reflect.DeepEqual(before, after) {
		t.Fatal("historical financial evidence changed", before, after)
	}
	r.ID = "historical"
	if err := g.Execute(context.Background(), r, func(context.Context) (BillingEvidence, error) {
		t.Fatal("replayed an old unknown dispatch")
		return BillingEvidence{}, nil
	}); err == nil {
		t.Fatal("old dispatched task was replayable")
	}
}

func TestPostgresCostFreeDispatchClaimAcrossInstances(t *testing.T) {
	db := postgresTestDB(t)
	a, b := postgresTestStore(t, db), postgresTestStore(t, db)
	_, r := executionFixture(t)
	var calls atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		ledger := a.budget
		if i%2 == 1 {
			ledger = b.budget
		}
		g := &ExecutionGovernor{Ledger: ledger, Moderator: testExecutionModerator{true}}
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = g.Execute(context.Background(), r, func(context.Context) (BillingEvidence, error) {
				calls.Add(1)
				return BillingEvidence{}, nil
			})
		}()
	}
	wg.Wait()
	if calls.Load() != 1 || !b.budget.hasExecution(r) {
		t.Fatal("duplicate or missing persistent execution claim", calls.Load())
	}
	entries, err := b.budget.Entries(r.Tenant, "USD")
	if err != nil || len(entries) != 0 {
		t.Fatal("cross-instance execution required budget records", entries, err)
	}
	r.Tenant = "another-tenant"
	if b.budget.hasExecution(r) {
		t.Fatal("another tenant gained access to the dispatch")
	}
}
