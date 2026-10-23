package studio

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// ValidateRuntimeFromEnv prevents unsupported production settings from silently
// running against local files. Configured-but-unavailable real providers remain
// inspectable. Ready real execution is closed until every worker is accounted.
func ValidateRuntimeFromEnv(capabilities ...Capabilities) error {
	storage, queue := os.Getenv("STORAGE_BACKEND"), os.Getenv("QUEUE_BACKEND")
	if storage == "" {
		storage = "file"
	}
	if queue == "" {
		queue = "inprocess"
	}
	if (storage != "file" || queue != "inprocess") && (storage != "postgres" || queue != "postgres") {
		return errors.New("unsupported storage/queue pair; refusing silent fallback")
	}
	if media := os.Getenv("MEDIA_BACKEND"); media != "" && media != "local" {
		return errors.New("MEDIA_BACKEND is not integrated")
	}
	for _, caps := range capabilities {
		if caps.Mode == "real" && caps.Ready && !caps.localExecution {
			return errors.New("live execution is disabled until per-request budget and moderation enforcement is integrated; use isolated protocol tests")
		}
	}
	return nil
}

// CurrencyMinorUnits is an explicit supported currency registry. Unknown codes
// fail closed; extending it requires review of the ISO currency's minor units.
func CurrencyMinorUnits(currency string) (int, error) {
	if currency == "" {
		currency = "USD"
	}
	switch currency {
	case "USD", "EUR", "GBP", "CNY", "CAD", "AUD", "CHF", "HKD", "SGD":
		return 2, nil
	case "JPY", "KRW":
		return 0, nil
	case "KWD", "BHD":
		return 3, nil
	default:
		return 0, errors.New("unsupported currency")
	}
}
func normalizeCurrency(currency string) (string, error) {
	if currency == "" {
		currency = "USD"
	}
	_, err := CurrencyMinorUnits(currency)
	return currency, err
}

// Amounts are integer currency minor units, never floating point or implicitly
// converted. A successful upstream response alone is not billing evidence.
type CostEntry struct {
	ID        string    `json:"id"`
	Tenant    string    `json:"tenant"`
	Provider  string    `json:"provider"`
	Model     string    `json:"model"`
	Currency  string    `json:"currency"`
	Reserved  int64     `json:"reserved_minor"`
	Actual    int64     `json:"actual_minor"`
	State     string    `json:"state"`
	Evidence  string    `json:"evidence_ref,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}
type BudgetChange struct {
	Actor    string    `json:"actor"`
	Tenant   string    `json:"tenant"`
	Currency string    `json:"currency"`
	Before   int64     `json:"before_minor"`
	After    int64     `json:"after_minor"`
	At       time.Time `json:"at"`
}
type costSnapshot struct {
	Changes []BudgetChange       `json:"changes,omitempty"`
	Version int                  `json:"version"`
	Limits  map[string]int64     `json:"limits"`
	Entries map[string]CostEntry `json:"entries"`
}
type BudgetLedger struct {
	pg   *postgresScope
	mu   stateMutex
	path string
	data costSnapshot
}

func budgetKey(tenant, currency string) (string, error) {
	if tenant == "" || strings.ContainsAny(tenant, "\x00\r\n") {
		return "", errors.New("invalid tenant")
	}
	return tenant + "\x00" + currency, nil
}

// OpenBudgetLedger belongs under the application's exclusive DATA_DIR lock.
// It is a storage primitive, not a claim of provider billing integration.
func OpenBudgetLedger(dir string) (*BudgetLedger, error) {
	ledger := &BudgetLedger{path: filepath.Join(dir, "budget.json"), data: costSnapshot{Version: 1, Limits: map[string]int64{}, Entries: map[string]CostEntry{}}}
	raw, err := os.ReadFile(ledger.path)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if err == nil {
		ledger.data = costSnapshot{}
		if json.Unmarshal(raw, &ledger.data) != nil || ledger.data.Version != 1 || ledger.data.Limits == nil || ledger.data.Entries == nil {
			return nil, errors.New("invalid budget snapshot; refusing reset")
		}
		for key, limit := range ledger.data.Limits {
			parts := strings.Split(key, "\x00")
			if len(parts) != 2 || parts[0] == "" || limit < 0 {
				return nil, errors.New("invalid budget limit")
			}
			if _, err = normalizeCurrency(parts[1]); err != nil || parts[1] == "" {
				return nil, errors.New("invalid budget currency")
			}
		}

		if len(ledger.data.Changes) > 10000 {
			return nil, errors.New("budget audit capacity exceeded")
		}
		for _, change := range ledger.data.Changes {
			_, currencyErr := normalizeCurrency(change.Currency)
			if change.Actor == "" || change.Tenant == "" || change.Currency == "" || currencyErr != nil || change.Before < 0 || change.After < 0 || change.At.IsZero() {
				return nil, errors.New("invalid budget audit entry")
			}
		}
		for id, e := range ledger.data.Entries {
			if e.ID != id || e.Tenant == "" || e.Reserved <= 0 || e.Actual < 0 || (e.State != "reserved" && e.State != "unknown" && e.State != "settled" && e.State != "released") {
				return nil, errors.New("invalid budget entry")
			}
			if _, err = normalizeCurrency(e.Currency); err != nil || e.Currency == "" {
				return nil, errors.New("invalid entry currency")
			}
			if e.State == "reserved" {
				e.State = "unknown"
				e.UpdatedAt = time.Now().UTC()
				ledger.data.Entries[id] = e
			}
		}
		if err = ledger.persist(ledger.data); err != nil {
			return nil, err
		}
	}
	return ledger, nil
}
func (l *BudgetLedger) clone() costSnapshot {
	n := l.data
	n.Changes = append([]BudgetChange(nil), l.data.Changes...)
	n.Limits = map[string]int64{}
	n.Entries = map[string]CostEntry{}
	for k, v := range l.data.Limits {
		n.Limits[k] = v
	}
	for k, v := range l.data.Entries {
		n.Entries[k] = v
	}
	return n
}
func (l *BudgetLedger) persist(data costSnapshot) error {
	if l.pg != nil {
		if err := l.pg.save(data); err != nil {
			return err
		}
		l.data = data
		return nil
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(l.path), ".budget-")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err = f.Write(raw); err == nil {
		err = f.Sync()
	}
	err = errors.Join(err, f.Close())
	if err != nil {
		return err
	}
	if err = os.Rename(name, l.path); err != nil {
		return err
	}
	l.data = data
	d, err := os.Open(filepath.Dir(l.path))
	if err != nil {
		return err
	}
	return errors.Join(d.Sync(), d.Close())
}
func exposure(data costSnapshot, tenant, currency string) (int64, error) {
	var total int64
	for _, e := range data.Entries {
		if e.Tenant != tenant || e.Currency != currency {
			continue
		}
		amount := e.Reserved
		if e.State == "settled" {
			amount = e.Actual
		}
		if e.State == "released" {
			amount = 0
		}
		if amount > int64(^uint64(0)>>1)-total {
			return 0, errors.New("budget overflow")
		}
		total += amount
	}
	return total, nil
}
func (l *BudgetLedger) SetLimit(tenant, currency string, limit int64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	currency, err := normalizeCurrency(currency)
	if err != nil {
		return err
	}
	key, err := budgetKey(tenant, currency)
	if err != nil {
		return err
	}
	used, err := exposure(l.data, tenant, currency)
	if err != nil {
		return err
	}
	if limit < used || limit < 0 {
		return errors.New("limit below existing exposure")
	}
	n := l.clone()
	n.Limits[key] = limit
	return l.persist(n)
}
func (l *BudgetLedger) Reserve(id, tenant, provider, model, currency string, amount int64) (CostEntry, error) {
	return l.reserve(id, tenant, provider, model, currency, amount, false)
}

// ClaimDispatch atomically reserves and records intent before network I/O. Any
// existing ID refuses a second dispatch, even if another governor owns it.
func (l *BudgetLedger) ClaimDispatch(id, tenant, provider, model, currency string, amount int64) (CostEntry, error) {
	return l.reserve(id, tenant, provider, model, currency, amount, true)
}
func (l *BudgetLedger) reserve(id, tenant, provider, model, currency string, amount int64, dispatch bool) (CostEntry, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	currency, err := normalizeCurrency(currency)
	if err != nil {
		return CostEntry{}, err
	}
	key, err := budgetKey(tenant, currency)
	if err != nil {
		return CostEntry{}, err
	}
	if id == "" || provider == "" || model == "" || amount <= 0 {
		return CostEntry{}, errors.New("reservation requires identity and positive bound")
	}
	if e, ok := l.data.Entries[id]; ok {
		if dispatch {
			if e.State != "reserved" || e.Tenant != tenant || e.Provider != provider || e.Model != model || e.Currency != currency || e.Reserved < amount {
				return CostEntry{}, errors.New("dispatch already claimed or reservation mismatch")
			}
			e.State = "unknown"
			e.Evidence = "dispatch_started"
			e.UpdatedAt = time.Now().UTC()
			n := l.clone()
			n.Entries[id] = e
			return e, l.persist(n)
		}
		if e.Tenant != tenant || e.Provider != provider || e.Model != model || e.Currency != currency || e.Reserved != amount {
			return CostEntry{}, errors.New("reservation conflict")
		}
		return e, nil
	}
	used, err := exposure(l.data, tenant, currency)
	if err != nil {
		return CostEntry{}, err
	}
	limit, ok := l.data.Limits[key]
	if !ok || used > limit || amount > limit-used {
		return CostEntry{}, errors.New("budget exhausted or missing")
	}
	e := CostEntry{ID: id, Tenant: tenant, Provider: provider, Model: model, Currency: currency, Reserved: amount, State: "reserved", UpdatedAt: time.Now().UTC()}
	if dispatch {
		e.State = "unknown"
		e.Evidence = "dispatch_started"
	}
	n := l.clone()
	n.Entries[id] = e
	return e, l.persist(n)
}

// Resolve requires a billing evidence reference. Unknown exposure is retained.
// Actual overrun is recorded honestly and blocks new reservations in that currency.
func (l *BudgetLedger) Resolve(id, tenant, currency, state string, actual int64, evidence string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	currency, err := normalizeCurrency(currency)
	if err != nil {
		return err
	}
	e, ok := l.data.Entries[id]
	if !ok || e.Tenant != tenant || e.Currency != currency {
		return errors.New("reservation not found in tenant/currency")
	}
	if state != "unknown" && state != "settled" && state != "released" {
		return errors.New("invalid settlement state")
	}
	if actual < 0 || (state != "settled" && actual != 0) {
		return errors.New("invalid actual amount")
	}
	if (state == "settled" || state == "released") && strings.TrimSpace(evidence) == "" {
		return errors.New("billing evidence required")
	}
	if e.State == "settled" || e.State == "released" {
		if state == e.State && actual == e.Actual {
			return nil
		}
		return errors.New("terminal settlement conflict")
	}
	if len(evidence) > 256 {
		return errors.New("evidence reference too long")
	}
	e.State = state
	e.Actual = actual
	e.Evidence = evidence
	e.UpdatedAt = time.Now().UTC()
	n := l.clone()
	n.Entries[id] = e
	return l.persist(n)
}
func (l *BudgetLedger) Entries(tenant, currency string) ([]CostEntry, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	currency, err := normalizeCurrency(currency)
	if err != nil {
		return nil, err
	}
	out := []CostEntry{}
	for _, e := range l.data.Entries {
		if e.Tenant == tenant && e.Currency == currency {
			out = append(out, e)
		}
	}
	return out, nil
}

// ExchangeQuote only reports a provider-supplied quotation; it never mutates a
// budget or silently converts ledger entries. No provider means no conversion.
type ExchangeQuote struct {
	From   string    `json:"from"`
	To     string    `json:"to"`
	Rate   string    `json:"rate"`
	Source string    `json:"source"`
	AsOf   time.Time `json:"as_of"`
	Stale  bool      `json:"stale"`
}

type ExchangeProvider interface {
	Quote(context.Context, string, string) (ExchangeQuote, error)
}

var exchangeRatePattern = regexp.MustCompile(`^[0-9]+(\.[0-9]+)?$`)

func GetExchangeQuote(ctx context.Context, provider ExchangeProvider, from, to string, now time.Time, maxAge time.Duration) (ExchangeQuote, error) {
	from, err := normalizeCurrency(from)
	if err != nil {
		return ExchangeQuote{}, err
	}
	to, err = normalizeCurrency(to)
	if err != nil {
		return ExchangeQuote{}, err
	}
	if provider == nil {
		return ExchangeQuote{}, errors.New("exchange provider unavailable; amounts are not converted")
	}
	quote, err := provider.Quote(ctx, from, to)
	if err != nil {
		return ExchangeQuote{}, errors.New("exchange quote unavailable; amounts are not converted")
	}
	if len(quote.Rate) > 64 || !exchangeRatePattern.MatchString(quote.Rate) || len(quote.Source) > 120 || strings.ContainsAny(quote.Source, "\x00\r\n") {
		return ExchangeQuote{}, errors.New("invalid exchange quote")
	}
	rate, ok := new(big.Rat).SetString(quote.Rate)
	if !ok || rate.Sign() <= 0 || quote.From != from || quote.To != to || quote.Source == "" || quote.AsOf.IsZero() || quote.AsOf.After(now) || maxAge <= 0 {
		return ExchangeQuote{}, errors.New("invalid exchange quote")
	}
	quote.Stale = quote.Stale || now.Sub(quote.AsOf) > maxAge
	return quote, nil
}

// ReleaseUndispatched only changes reserved entries under the same ledger lock.
// An attempted/in-flight/ambiguous dispatch can never be released by this path.
func (l *BudgetLedger) ReleaseUndispatched(id, tenant, currency string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	e, ok := l.data.Entries[id]
	if !ok || e.Tenant != tenant || e.Currency != currency || e.State != "reserved" {
		return nil
	}
	e.State = "released"
	e.Evidence = "local:never_dispatched"
	e.UpdatedAt = time.Now().UTC()
	n := l.clone()
	n.Entries[id] = e
	return l.persist(n)
}
