package studio

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestGovernanceAuthenticatedCurrencyBudget(t *testing.T) {
	s := testStore(t)
	auth, err := OpenAuthStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a, ta, sa, err := auth.login("budget-owner@example.test", "synthetic-owner-password", true)
	if err != nil {
		t.Fatal(err)
	}
	b, tb, sb, err := auth.login("budget-reader@example.test", "synthetic-reader-password", true)
	if err != nil {
		t.Fatal(err)
	}
	tenant, other := auth.spaces(a.ID)[0]["id"], auth.spaces(b.ID)[0]["id"]
	h := SecureHandler(s, "http://localhost:5173", auth, AuthOptions{})
	reportPath := "/api/v1/governance?space_id=" + tenant
	writePath := "/api/v1/governance/budget?space_id=" + tenant
	body := `{"currency":"USD","limit_minor":1000,"expected_limit_minor":0}`
	authRequest(t, h, "GET", reportPath, "", "", "", 401)
	authRequest(t, h, "GET", reportPath, "", tb, "", 403)
	authRequest(t, h, "PUT", writePath, body, ta, "", 403)
	authRequest(t, h, "PUT", writePath, body, tb, sb.CSRF, 403)
	w := authRequest(t, h, "PUT", writePath, body, ta, sa.CSRF, 200)
	var report BudgetReport
	if err = json.Unmarshal(w.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Currency != "USD" || report.MinorUnits != 2 || report.Limit != 1000 || report.Available != 1000 || report.LiveExecutionEnabled || len(report.Changes) != 1 || report.Changes[0].Actor != a.ID {
		t.Fatalf("bad report %+v", report)
	}
	authRequest(t, h, "PUT", writePath, body, ta, sa.CSRF, 409)
	authRequest(t, h, "PUT", writePath, `{"currency":"USD","limit_minor":1000,"expected_limit_minor":1000}`, ta, sa.CSRF, 200)
	if _, err = s.budget.Reserve("request-usd", tenant, "synthetic", "synthetic", "USD", 700); err != nil {
		t.Fatal(err)
	}
	if err = s.budget.Resolve("request-usd", tenant, "USD", "unknown", 0, ""); err != nil {
		t.Fatal(err)
	}
	authRequest(t, h, "PUT", writePath, `{"currency":"USD","limit_minor":699,"expected_limit_minor":1000}`, ta, sa.CSRF, 400)
	authRequest(t, h, "PUT", writePath, `{"currency":"JPY","limit_minor":2000,"expected_limit_minor":0}`, ta, sa.CSRF, 200)
	if _, err = s.budget.Reserve("request-jpy", tenant, "synthetic", "synthetic", "JPY", 100); err != nil {
		t.Fatal(err)
	}
	w = authRequest(t, h, "GET", reportPath, "", ta, "", 200)
	_ = json.Unmarshal(w.Body.Bytes(), &report)
	if report.Exposure != 700 || report.Available != 300 || len(report.Entries) != 1 || report.Entries[0].Currency != "USD" || report.Entries[0].State != "unknown" {
		t.Fatalf("currency leak %+v", report)
	}
	w = authRequest(t, h, "GET", reportPath+"&currency=JPY", "", ta, "", 200)
	_ = json.Unmarshal(w.Body.Bytes(), &report)
	if report.MinorUnits != 0 || report.Exposure != 100 || report.Available != 1900 || len(report.Entries) != 1 {
		t.Fatalf("JPY mixed %+v", report)
	}
	w = authRequest(t, h, "GET", "/api/v1/governance?space_id="+other, "", tb, "", 200)
	_ = json.Unmarshal(w.Body.Bytes(), &report)
	if report.Limit != 0 || report.Exposure != 0 || len(report.Entries) != 0 || len(report.Changes) != 0 {
		t.Fatal("cross-tenant cost or audit leak")
	}
	memberPath := "/api/v1/organizations/" + tenant + "/members"
	for _, role := range []string{"viewer", "editor", "admin"} {
		authRequest(t, h, "PUT", memberPath, `{"email":"budget-reader@example.test","role":"`+role+`"}`, ta, sa.CSRF, 200)
		authRequest(t, h, "GET", reportPath, "", tb, "", 200)
		authRequest(t, h, "PUT", writePath, `{"currency":"USD","limit_minor":2000,"expected_limit_minor":1000}`, tb, sb.CSRF, 403)
	}
	authRequest(t, h, "POST", "/api/v1/governance/resolve?space_id="+tenant, `{}`, ta, sa.CSRF, 404)
	for _, invalid := range []string{`{}`, `{"currency":"XYZ","limit_minor":1,"expected_limit_minor":0}`, `{"currency":"USD","limit_minor":null,"expected_limit_minor":1000}`, `{"currency":"USD","limit_minor":9007199254740992,"expected_limit_minor":1000}`, `{"currency":"USD","limit_minor":1.5,"expected_limit_minor":1000}`, `{"currency":"USD","limit_minor":1000,"expected_limit_minor":1000,"tenant":"foreign"}`} {
		authRequest(t, h, "PUT", writePath, invalid, ta, sa.CSRF, 400)
	}
	authRequest(t, h, "GET", reportPath+"&currency=XYZ", "", ta, "", 400)
	authRequest(t, h, "GET", reportPath+"&offset=-1", "", ta, "", 400)
	authRequest(t, h, "GET", "/api/v1/governance/exchange?space_id="+tenant+"&from=USD&to=CNY", "", ta, "", 503)
	w = authRequest(t, h, "GET", "/api/v1/governance/exchange?space_id="+tenant+"&from=USD&to=USD", "", ta, "", 200)
	var quote ExchangeQuote
	_ = json.Unmarshal(w.Body.Bytes(), &quote)
	if quote.Rate != "1" || quote.Source != "identity" {
		t.Fatal("invalid identity quote")
	}
	req := httptest.NewRequest("GET", "/api/v1/governance?space_id=personal-demo", nil)
	out := httptest.NewRecorder()
	Handler(s, "http://localhost:5173").ServeHTTP(out, req)
	if out.Code != 403 {
		t.Fatal("demo budget leak")
	}
}

func TestGovernanceBudgetAuditRecoveryAndPagination(t *testing.T) {
	dir := t.TempDir()
	l, err := OpenBudgetLedger(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err = l.configure("tenant-a", "owner-a", "KWD", 0, 1000); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 102; i++ {
		if _, err = l.Reserve(strconv.Itoa(i), "tenant-a", "synthetic", "synthetic", "KWD", 1); err != nil {
			t.Fatal(err)
		}
	}
	first, err := l.report("tenant-a", "KWD", 0)
	if err != nil {
		t.Fatal(err)
	}
	if first.TotalEntries != 102 || len(first.Entries) != 100 || first.NextOffset == nil || *first.NextOffset != 100 || first.MinorUnits != 3 {
		t.Fatal("bad pagination", first)
	}
	second, err := l.report("tenant-a", "KWD", *first.NextOffset)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Entries) != 2 || second.NextOffset != nil {
		t.Fatal("wrong final page")
	}
	l, err = OpenBudgetLedger(dir)
	if err != nil {
		t.Fatal(err)
	}
	report, err := l.report("tenant-a", "KWD", 0)
	if err != nil {
		t.Fatal(err)
	}
	if report.Limit != 1000 || report.Exposure != 102 || len(report.Changes) != 1 || report.Changes[0].Actor != "owner-a" || report.Entries[0].State != "unknown" {
		t.Fatal("budget restart lost evidence", report)
	}
	if err = l.configure("tenant-a", "owner-a", "KWD", 1000, 101); err == nil {
		t.Fatal("unknown liability released by lowering budget")
	}
}

func TestExchangeQuoteRejectsNonDecimalOrUnboundedMetadata(t *testing.T) {
	now := time.Now().UTC()
	for _, rate := range []string{"1/2", "1e4", strings.Repeat("1", 65), ".5", "NaN", "0"} {
		_, err := GetExchangeQuote(context.Background(), quoteStub{ExchangeQuote{From: "USD", To: "CNY", Rate: rate, Source: "fixture", AsOf: now}}, "USD", "CNY", now, time.Hour)
		if err == nil {
			t.Fatal("accepted invalid rate", rate)
		}
	}
	for _, source := range []string{strings.Repeat("s", 121), "line\nbreak"} {
		_, err := GetExchangeQuote(context.Background(), quoteStub{ExchangeQuote{From: "USD", To: "CNY", Rate: "7.25", Source: source, AsOf: now}}, "USD", "CNY", now, time.Hour)
		if err == nil {
			t.Fatal("accepted invalid source")
		}
	}
}
