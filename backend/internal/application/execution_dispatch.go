package application

import "time"

// Dispatch receipts prevent duplicate upstream submissions. They contain no
// prices, balances or reservations and never consult historical budget limits.
// They share the durable ledger scope so existing backup/restore preserves both
// pre-upgrade financial history and the new nonfinancial execution receipts.
type executionDispatch struct {
	ID       string    `json:"id"`
	Tenant   string    `json:"tenant"`
	Provider string    `json:"provider"`
	Model    string    `json:"model"`
	At       time.Time `json:"at"`
}

func (l *BudgetLedger) claimExecution(r ExecutionRequest) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, exists := l.data.Dispatches[r.ID]; exists {
		return ErrConflict
	}
	// An old attempted call must never be replayed during an upgrade. A queued
	// pre-upgrade reservation may proceed once without any budget or price check.
	old, exists := l.data.Entries[r.ID]
	if exists && (old.Tenant != r.Tenant || old.Provider != r.Provider || old.Model != r.Model || old.State != "reserved") {
		return ErrConflict
	}
	n := l.clone()
	if exists {
		old.State, old.Evidence, old.UpdatedAt = "unknown", "dispatch_started", time.Now().UTC()
		n.Entries[r.ID] = old
	}
	n.Dispatches[r.ID] = executionDispatch{r.ID, r.Tenant, r.Provider, r.Model, time.Now().UTC()}
	return l.persist(n)
}

func (l *BudgetLedger) hasExecution(r ExecutionRequest) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.pg != nil && l.pg.err != nil {
		return false
	}
	if d, ok := l.data.Dispatches[r.ID]; ok {
		return d.Tenant == r.Tenant && d.Provider == r.Provider && d.Model == r.Model
	}
	d, ok := l.data.Entries[r.ID]
	return ok && d.Tenant == r.Tenant && d.Provider == r.Provider && d.Model == r.Model && d.State == "unknown"
}
