package studio

import (
	"errors"
	"net/http"
	"sort"
	"strconv"
	"time"
)

// BudgetReport keeps currencies separate. No exchange quote changes ledger exposure.
type BudgetReport struct {
	Currency             string         `json:"currency"`
	MinorUnits           int            `json:"minor_units"`
	Limit                int64          `json:"limit_minor"`
	Exposure             int64          `json:"exposure_minor"`
	Available            int64          `json:"available_minor"`
	Entries              []CostEntry    `json:"entries"`
	TotalEntries         int            `json:"total_entries"`
	NextOffset           *int           `json:"next_offset"`
	Changes              []BudgetChange `json:"budget_changes"`
	LiveExecutionEnabled bool           `json:"live_execution_enabled"`
	FXAvailable          bool           `json:"fx_available"`
}

var errBudgetConflict = errors.New("budget limit changed")
var errBudgetInput = errors.New("invalid budget limit")

func (l *BudgetLedger) report(tenant, currency string, offset int) (BudgetReport, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	var err error
	currency, err = normalizeCurrency(currency)
	if err != nil {
		return BudgetReport{}, err
	}
	key, err := budgetKey(tenant, currency)
	if err != nil {
		return BudgetReport{}, err
	}
	units, _ := CurrencyMinorUnits(currency)
	used, err := exposure(l.data, tenant, currency)
	if err != nil {
		return BudgetReport{}, err
	}
	out := BudgetReport{Currency: currency, MinorUnits: units, Limit: l.data.Limits[key], Exposure: used, Entries: []CostEntry{}, Changes: []BudgetChange{}}
	if used < out.Limit {
		out.Available = out.Limit - used
	}
	for _, e := range l.data.Entries {
		if e.Tenant == tenant && e.Currency == currency {
			out.Entries = append(out.Entries, e)
		}
	}
	sort.Slice(out.Entries, func(i, j int) bool {
		if out.Entries[i].UpdatedAt.Equal(out.Entries[j].UpdatedAt) {
			return out.Entries[i].ID < out.Entries[j].ID
		}
		return out.Entries[i].UpdatedAt.After(out.Entries[j].UpdatedAt)
	})
	out.TotalEntries = len(out.Entries)
	if offset > len(out.Entries) {
		offset = len(out.Entries)
	}
	end := offset + 100
	if end < len(out.Entries) {
		out.NextOffset = &end
	} else {
		end = len(out.Entries)
	}
	out.Entries = out.Entries[offset:end]
	// Show the latest 100 configuration changes for this tenant/currency only.
	for i := len(l.data.Changes) - 1; i >= 0 && len(out.Changes) < 100; i-- {
		c := l.data.Changes[i]
		if c.Tenant == tenant && c.Currency == currency {
			out.Changes = append(out.Changes, c)
		}
	}
	return out, nil
}
func (l *BudgetLedger) configure(tenant, actor, currency string, expected, limit int64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	currency, err := normalizeCurrency(currency)
	if err != nil {
		return errBudgetInput
	}
	key, err := budgetKey(tenant, currency)
	if err != nil {
		return errBudgetInput
	}
	// Largest exact JavaScript integer keeps the browser review faithful to stored minor units.
	if actor == "" || expected < 0 || limit < 0 || expected > 9007199254740991 || limit > 9007199254740991 {
		return errBudgetInput
	}
	if l.data.Limits[key] != expected {
		return errBudgetConflict
	}
	used, err := exposure(l.data, tenant, currency)
	if err != nil {
		return err
	}
	if limit < used {
		return errBudgetInput
	}
	if limit == expected {
		return nil
	}
	if len(l.data.Changes) >= 10000 {
		return errors.New("budget audit capacity reached")
	}
	n := l.clone()
	n.Limits[key] = limit
	n.Changes = append(n.Changes, BudgetChange{Actor: actor, Tenant: tenant, Currency: currency, Before: expected, After: limit, At: time.Now().UTC()})
	return l.persist(n)
}
func governanceScope(w http.ResponseWriter, r *http.Request, owner bool) (authScope, bool) {
	scope, ok := r.Context().Value(authScopeKey{}).(authScope)
	if !ok || scope.UserID == "" || scope.SpaceID == "" || scope.SpaceID != r.URL.Query().Get("space_id") || scope.Role == "" {
		problem(w, 403, "authentication_required", "Budget governance requires an authenticated workspace.")
		return authScope{}, false
	}
	if owner && scope.Role != "owner" {
		problem(w, 403, "owner_required", "Only the workspace owner can change budgets.")
		return authScope{}, false
	}
	return scope, true
}
func registerGovernanceRoutes(mux *http.ServeMux, s *FileStore) {
	mux.HandleFunc("GET /api/v1/governance", func(w http.ResponseWriter, r *http.Request) {
		scope, ok := governanceScope(w, r, false)
		if !ok {
			return
		}
		offset := 0
		if raw := r.URL.Query().Get("offset"); raw != "" {
			v, err := strconv.Atoi(raw)
			if err != nil || v < 0 {
				problem(w, 400, "invalid_input", "Invalid ledger offset.")
				return
			}
			offset = v
		}
		report, err := s.budget.report(scope.SpaceID, r.URL.Query().Get("currency"), offset)
		if err != nil {
			problem(w, 400, "invalid_input", "Unsupported currency or invalid ledger.")
			return
		}
		report.FXAvailable = s.exchange != nil
		respond(w, 200, report)
	})
	mux.HandleFunc("PUT /api/v1/governance/budget", func(w http.ResponseWriter, r *http.Request) {
		scope, ok := governanceScope(w, r, true)
		if !ok {
			return
		}
		var in struct {
			Currency string `json:"currency"`
			Limit    *int64 `json:"limit_minor"`
			Expected *int64 `json:"expected_limit_minor"`
		}
		if !decode(w, r, &in) {
			return
		}
		if in.Limit == nil || in.Expected == nil {
			problem(w, 400, "invalid_input", "Both limit_minor and expected_limit_minor are required.")
			return
		}
		if err := s.budget.configure(scope.SpaceID, scope.UserID, in.Currency, *in.Expected, *in.Limit); err != nil {
			switch {
			case errors.Is(err, errBudgetConflict):
				problem(w, 409, "budget_conflict", "Budget changed. Refresh before saving.")
			case errors.Is(err, errBudgetInput):
				problem(w, 400, "invalid_input", "Unsupported currency, unsafe amount or limit below existing exposure.")
			default:
				problem(w, 503, "budget_storage_error", "Budget could not be saved. Refresh before retrying.")
			}
			return
		}
		report, err := s.budget.report(scope.SpaceID, in.Currency, 0)
		if err != nil {
			problem(w, 503, "budget_storage_error", "Budget saved; report unavailable. Refresh before retrying.")
			return
		}
		report.FXAvailable = s.exchange != nil
		respond(w, 200, report)
	})
	mux.HandleFunc("GET /api/v1/governance/exchange", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := governanceScope(w, r, false); !ok {
			return
		}
		from, e1 := normalizeCurrency(r.URL.Query().Get("from"))
		to, e2 := normalizeCurrency(r.URL.Query().Get("to"))
		if e1 != nil || e2 != nil {
			problem(w, 400, "invalid_input", "Unsupported currency.")
			return
		}
		if from == to {
			respond(w, 200, ExchangeQuote{From: from, To: to, Rate: "1", Source: "identity", AsOf: time.Now().UTC()})
			return
		}
		quote, err := GetExchangeQuote(r.Context(), s.exchange, from, to, time.Now().UTC(), 24*time.Hour)
		if err != nil {
			problem(w, 503, "exchange_unavailable", "Exchange quote unavailable. Amounts remain in their original currencies.")
			return
		}
		respond(w, 200, quote)
	})
}
