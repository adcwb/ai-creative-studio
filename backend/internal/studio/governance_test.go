package studio

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestBudgetReservationUnknownRecoveryAndSettlement(t *testing.T) {
	dir := t.TempDir()
	ledger, err := OpenBudgetLedger(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ledger.Reserve("a", "tenant", "provider", "model", "", 50); err == nil {
		t.Fatal("missing budget accepted")
	}
	if err = ledger.SetLimit("tenant", "", 100); err != nil {
		t.Fatal(err)
	}
	if _, err = ledger.Reserve("a", "tenant", "provider", "model", "", 60); err != nil {
		t.Fatal(err)
	}
	if _, err = ledger.Reserve("a", "tenant", "provider", "model", "USD", 60); err != nil {
		t.Fatal("idempotency", err)
	}
	if _, err = ledger.Reserve("a", "other", "provider", "model", "USD", 60); err == nil {
		t.Fatal("cross tenant accepted")
	}
	if _, err = ledger.Reserve("b", "tenant", "provider", "model", "USD", 41); err == nil {
		t.Fatal("overspend accepted")
	}
	ledger, err = OpenBudgetLedger(dir)
	if err != nil {
		t.Fatal(err)
	}
	entries, _ := ledger.Entries("tenant", "")
	if entries[0].State != "unknown" {
		t.Fatal("restart released exposure")
	}
	if err = ledger.Resolve("a", "tenant", "USD", "released", 0, ""); err == nil {
		t.Fatal("no evidence accepted")
	}
	if err = ledger.Resolve("a", "other", "USD", "released", 0, "invoice"); err == nil {
		t.Fatal("cross tenant settlement")
	}
	if err = ledger.Resolve("a", "tenant", "USD", "settled", 20, "invoice/ref"); err != nil {
		t.Fatal(err)
	}
	if _, err = ledger.Reserve("b", "tenant", "provider", "model", "USD", 80); err != nil {
		t.Fatal(err)
	}
	if err = ledger.Resolve("a", "tenant", "USD", "released", 0, "invoice"); err == nil {
		t.Fatal("terminal state changed")
	}
	other, _ := ledger.Entries("other", "")
	if len(other) != 0 {
		t.Fatal("cross tenant read")
	}
}
func TestBudgetCurrencyIsolationAndUnits(t *testing.T) {
	ledger, err := OpenBudgetLedger(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for currency, units := range map[string]int{"USD": 2, "JPY": 0, "KWD": 3} {
		got, err := CurrencyMinorUnits(currency)
		if err != nil || got != units {
			t.Fatal(currency, got, err)
		}
		if err = ledger.SetLimit("org", currency, 10); err != nil {
			t.Fatal(err)
		}
		if _, err = ledger.Reserve(currency, "org", "p", "m", currency, 10); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = ledger.Reserve("USD", "org", "p", "m", "JPY", 10); err == nil {
		t.Fatal("cross currency replay accepted")
	}
	if err = ledger.Resolve("USD", "org", "JPY", "settled", 5, "bill"); err == nil {
		t.Fatal("cross currency settlement accepted")
	}
	for _, currency := range []string{"usd", "XYZ", "US", "USD "} {
		if err = ledger.SetLimit("org", currency, 10); err == nil {
			t.Fatal("invalid currency accepted", currency)
		}
	}
	entries, _ := ledger.Entries("org", "JPY")
	if len(entries) != 1 || entries[0].Currency != "JPY" {
		t.Fatal(entries)
	}
}
func TestBudgetConcurrencyAndOverrun(t *testing.T) {
	ledger, err := OpenBudgetLedger(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = ledger.SetLimit("org", "", 1); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	accepted := 0
	for _, id := range []string{"a", "b", "c", "d"} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			if _, err := ledger.Reserve(id, "org", "p", "m", "", 1); err == nil {
				mu.Lock()
				accepted++
				mu.Unlock()
			}
		}(id)
	}
	wg.Wait()
	if accepted != 1 {
		t.Fatalf("accepted %d reservations", accepted)
	}
	entries, _ := ledger.Entries("org", "")
	if err = ledger.Resolve(entries[0].ID, "org", "", "settled", 2, "supplier overrun"); err != nil {
		t.Fatal("actual overrun must remain recorded", err)
	}
	if _, err = ledger.Reserve("extra", "org", "p", "m", "", 1); err == nil {
		t.Fatal("overrun did not block")
	}
}
func TestBudgetCorruptionAndWriteFailure(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "budget.json"), []byte(`{"version":1}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenBudgetLedger(dir); err == nil {
		t.Fatal("corrupt ledger reset")
	}
	ledger, err := OpenBudgetLedger(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ledger.path = filepath.Join(dir, "missing", "budget.json")
	if err = ledger.SetLimit("org", "", 10); err == nil {
		t.Fatal("failed persistence accepted")
	}
	if len(ledger.data.Limits) != 0 {
		t.Fatal("failed transaction mutated budget")
	}
}
func TestRuntimeRefusesSilentFallbackAndLive(t *testing.T) {
	for _, key := range []string{"STORAGE_BACKEND", "QUEUE_BACKEND", "MEDIA_BACKEND"} {
		t.Setenv(key, "")
	}
	if err := ValidateRuntimeFromEnv(Capabilities{Mode: "real", Ready: false}); err != nil {
		t.Fatal(err)
	}
	for key, value := range map[string]string{"STORAGE_BACKEND": "postgres", "QUEUE_BACKEND": "redis", "MEDIA_BACKEND": "s3"} {
		t.Setenv(key, value)
		if err := ValidateRuntimeFromEnv(); err == nil {
			t.Fatalf("accepted %s", key)
		}
		t.Setenv(key, "")
	}
	if err := ValidateRuntimeFromEnv(Capabilities{Mode: "real", Ready: true, localExecution: true}); err != nil {
		t.Fatal("local plugin blocked", err)
	}
	if err := ValidateRuntimeFromEnv(Capabilities{Mode: "real", Ready: true}); err == nil {
		t.Fatal("ungoverned live accepted")
	}
}

type quoteStub struct{ q ExchangeQuote }

func (s quoteStub) Quote(context.Context, string, string) (ExchangeQuote, error) { return s.q, nil }
func TestExchangeQuoteNoImplicitConversion(t *testing.T) {
	now := time.Now().UTC()
	ctx := context.Background()
	if _, err := GetExchangeQuote(ctx, nil, "USD", "JPY", now, time.Hour); err == nil {
		t.Fatal("missing provider")
	}
	q := ExchangeQuote{From: "USD", To: "JPY", Rate: "150.125", Source: "synthetic fixture", AsOf: now.Add(-2 * time.Hour)}
	got, err := GetExchangeQuote(ctx, quoteStub{q}, "USD", "JPY", now, time.Hour)
	if err != nil || !got.Stale {
		t.Fatal(got, err)
	}
	q.Rate = "-1"
	if _, err = GetExchangeQuote(ctx, quoteStub{q}, "USD", "JPY", now, time.Hour); err == nil {
		t.Fatal("negative rate")
	}
	q.Rate = "1"
	q.To = "EUR"
	if _, err = GetExchangeQuote(ctx, quoteStub{q}, "USD", "JPY", now, time.Hour); err == nil {
		t.Fatal("currency mismatch")
	}
}
