package studio

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestExecutionPolicyBoundedQuote(t *testing.T) {
	rule := ExecutionPriceRule{Provider: "openai-text", Model: "synthetic", Capability: "text", Currency: "USD", RequestContract: "text-64k-6000-v1", InputMinorPerMillion: "100", OutputMinorPerMillion: "200", MaximumInputBytes: 1000, Source: "synthetic fixture", ExpiresAt: time.Now().Add(time.Minute)}
	p := ExecutionPolicy{Prices: []ExecutionPriceRule{rule}}
	q, e := p.QuoteExecution(context.Background(), ExecutionRequest{Provider: rule.Provider, Model: rule.Model, Capability: "text", Text: "hello"})
	if e != nil || q.MaximumMinor != 8 {
		t.Fatalf("%+v %v", q, e)
	}
	if _, e = p.QuoteExecution(context.Background(), ExecutionRequest{Provider: rule.Provider, Model: rule.Model, Capability: "text", HasMedia: true}); e == nil {
		t.Fatal("unpriced visual tokens accepted")
	}
	rule.InputMinorPerMillion = "1e12"
	if _, e = boundedTextAmount(rule); e == nil {
		t.Fatal("non-decimal rate")
	}
}
func TestExecutionPolicyCommandSettlement(t *testing.T) {
	g, r := executionFixture(t)
	dir := t.TempDir()
	moderator := filepath.Join(dir, "moderate")
	billing := filepath.Join(dir, "bill")
	decision, _ := json.Marshal(ModerationDecision{Provider: "fixture", Model: "fixture", Status: "allow", Categories: []string{}, CheckedAt: time.Now()})
	if e := os.WriteFile(moderator, []byte("#!/bin/sh\ncat >/dev/null\nprintf '%s' '"+string(decision)+"'\n"), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(billing, []byte("#!/bin/sh\ncat >/dev/null\nprintf '%s' '{\"State\":\"settled\",\"ActualMinor\":9,\"Reference\":\"fixture:receipt-1\"}'\n"), 0700); e != nil {
		t.Fatal(e)
	}
	policy := &ExecutionPolicy{ModerationCommand: moderator, BillingCommand: billing, BillingContract: "local-or-free-v1", BillingSource: "synthetic local fixture", BillingExpiresAt: time.Now().Add(time.Hour), ModerationContract: "local-or-free-v1", ModerationSource: "synthetic local fixture", ModerationExpiresAt: time.Now().Add(time.Hour)}
	g.Moderator = policy
	g.Billing = policy
	if e := g.Execute(context.Background(), r, func(context.Context) (BillingEvidence, error) { return BillingEvidence{}, nil }); e != nil {
		t.Fatal(e)
	}
	entries, _ := g.Ledger.Entries(r.Tenant, "USD")
	if entries[0].State != "settled" || entries[0].Actual != 9 {
		t.Fatal(entries)
	}
	if e := os.WriteFile(moderator, []byte("#!/bin/sh\nprintf '%s' '{\"status\":\"allow\",\"extra\":true}'"), 0700); e != nil {
		t.Fatal(e)
	}
	if _, e := policy.ReviewExecution(context.Background(), r); e == nil {
		t.Fatal("unknown output fields accepted")
	}
}

func TestExecutionPolicyUnknownReviewCostNeverRunsCommand(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "called")
	command := filepath.Join(dir, "review")
	if err := os.WriteFile(command, []byte("#!/bin/sh\ntouch '"+marker+"'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, contract := range []string{"", "paid", "local-or-free-v1"} {
		p := &ExecutionPolicy{ModerationCommand: command, ModerationContract: contract, ModerationSource: "synthetic review", ModerationExpiresAt: time.Now().Add(-time.Minute)}
		if contract != "local-or-free-v1" {
			p.ModerationExpiresAt = time.Now().Add(time.Hour)
		}
		g, _ := executionFixture(t)
		g.Moderator = p
		if g.Ready() {
			t.Fatal("unverified/expired review advertised ready")
		}
		if _, err := p.ReviewExecution(context.Background(), ExecutionRequest{}); err == nil {
			t.Fatal("unverified/expired review cost allowed")
		}
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("unknown-price review command executed")
	}
}

func TestExecutionPolicyRequiresNonbillableReviewContractAtStartup(t *testing.T) {
	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	command := filepath.Join(t.TempDir(), "review")
	if err = os.WriteFile(command, []byte("#!/bin/sh\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	p := ExecutionPolicy{Version: 1, ModerationCommand: command, Prices: []ExecutionPriceRule{{Provider: "openai-text", Model: "synthetic", Capability: "text", Currency: "USD", RequestContract: "text-64k-6000-v1", InputMinorPerMillion: "100", OutputMinorPerMillion: "200", MaximumInputBytes: 1000, Source: "synthetic", ExpiresAt: time.Now().Add(time.Hour)}}}
	path := filepath.Join(t.TempDir(), "policy.json")
	t.Setenv("EXECUTION_POLICY_FILE", path)
	save := func() {
		raw, e := json.Marshal(p)
		if e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(path, raw, 0600); e != nil {
			t.Fatal(e)
		}
	}
	save()
	if ConfigureExecutionFromEnv(store) == nil {
		t.Fatal("missing nonbillable review contract accepted")
	}
	p.ModerationContract = "local-or-free-v1"
	p.ModerationSource = "reviewed synthetic local command"
	p.ModerationExpiresAt = time.Now().Add(time.Hour)
	save()
	if err = ConfigureExecutionFromEnv(store); err != nil {
		t.Fatal(err)
	}
	p.BillingCommand = command
	save()
	if ConfigureExecutionFromEnv(store) == nil {
		t.Fatal("unknown billing command cost accepted")
	}
	p.BillingContract = "local-or-free-v1"
	p.BillingSource = "synthetic local billing"
	p.BillingExpiresAt = time.Now().Add(time.Hour)
	save()
	if err = ConfigureExecutionFromEnv(store); err != nil {
		t.Fatal(err)
	}
	p.BillingExpiresAt = time.Now().Add(-time.Second)
	save()
	if ConfigureExecutionFromEnv(store) == nil {
		t.Fatal("expired billing contract accepted")
	}
	p.BillingCommand = ""
	p.ModerationExpiresAt = time.Now().Add(-time.Second)
	save()
	if ConfigureExecutionFromEnv(store) == nil {
		t.Fatal("expired review contract accepted")
	}
}

func TestExecutionPolicyUnknownBillingCostKeepsReservation(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "called")
	command := filepath.Join(dir, "bill")
	if err := os.WriteFile(command, []byte("#!/bin/sh\ntouch '"+marker+"'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, contract := range []string{"", "paid", "local-or-free-v1"} {
		g, r := executionFixture(t)
		p := &ExecutionPolicy{BillingCommand: command, BillingContract: contract, BillingSource: "synthetic evidence", BillingExpiresAt: time.Now().Add(time.Hour)}
		if contract == "local-or-free-v1" {
			p.BillingExpiresAt = time.Now().Add(-time.Second)
		}
		g.Billing = p
		if err := g.Execute(context.Background(), r, func(context.Context) (BillingEvidence, error) { return BillingEvidence{}, nil }); err != nil {
			t.Fatal(err)
		}
		entries, err := g.Ledger.Entries(r.Tenant, "USD")
		if err != nil || len(entries) != 1 || entries[0].State != "unknown" {
			t.Fatal("unknown-price billing query released exposure", entries, err)
		}
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("unknown-price billing command executed")
	}
}
