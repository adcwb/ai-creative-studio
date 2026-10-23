package studio

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/adcwb/ai-creative-studio/backend/internal/jobqueue"
	"time"
)

func readPostgresScopeTx(ctx context.Context, tx *sql.Tx, name string, template any) ([]byte, map[string]map[string]json.RawMessage, error) {
	raw, err := json.Marshal(template)
	if err != nil {
		return nil, nil, err
	}
	env, _, err := splitPostgres(raw, pgCollections[name])
	if err != nil {
		return nil, nil, err
	}
	raw, _ = json.Marshal(env)
	if _, err = tx.ExecContext(ctx, `INSERT INTO studio_scopes(name,envelope) VALUES($1,$2) ON CONFLICT DO NOTHING`, name, raw); err != nil {
		return nil, nil, err
	}
	if err = tx.QueryRowContext(ctx, `SELECT envelope FROM studio_scopes WHERE name=$1 FOR UPDATE`, name).Scan(&raw); err != nil {
		return nil, nil, err
	}
	if err = json.Unmarshal(raw, &env); err != nil {
		return nil, nil, err
	}
	records := map[string]map[string]json.RawMessage{}
	for _, c := range pgCollections[name] {
		rows, e := tx.QueryContext(ctx, `SELECT record_key,payload FROM `+c.table)
		if e != nil {
			return nil, nil, e
		}
		values := map[string]json.RawMessage{}
		for rows.Next() {
			var k string
			var b []byte
			if e = rows.Scan(&k, &b); e != nil {
				break
			}
			key, x := base64.RawURLEncoding.DecodeString(k)
			if x != nil {
				e = x
				break
			}
			values[string(key)] = b
		}
		e = errors.Join(e, rows.Err(), rows.Close())
		if e != nil {
			return nil, nil, e
		}
		records[c.field] = values
		if c.kind == "array" {
			arr := make([]json.RawMessage, len(values))
			for i := range arr {
				b, ok := values[fmt.Sprintf("%012d", i)]
				if !ok {
					return nil, nil, errors.New("invalid ordered record sequence")
				}
				arr[i] = b
			}
			env[c.field], _ = json.Marshal(arr)
		} else {
			env[c.field], _ = json.Marshal(values)
		}
	}
	raw, err = json.Marshal(env)
	return raw, records, err
}

// ReservePostgresTx uses the SAME transaction as domain acceptance and enqueue.
// Reserve the user's entire explicit bound; execution may use a smaller verified
// quotation but cannot silently raise or convert this reservation.
func ReservePostgresTx(ctx context.Context, tx *sql.Tx, id, tenant, provider, model, currency string, amount int64) error {
	currency, err := normalizeCurrency(currency)
	if err != nil {
		return err
	}
	key, err := budgetKey(tenant, currency)
	if err != nil {
		return err
	}
	if id == "" || provider == "" || model == "" || amount <= 0 {
		return ErrExecutionGovernance
	}
	d := costSnapshot{Version: 1, Limits: map[string]int64{}, Entries: map[string]CostEntry{}}
	raw, before, err := readPostgresScopeTx(ctx, tx, "budget", d)
	if err != nil {
		return err
	}
	if err = json.Unmarshal(raw, &d); err != nil {
		return err
	}
	if e, ok := d.Entries[id]; ok {
		if e.Tenant == tenant && e.Provider == provider && e.Model == model && e.Currency == currency && e.Reserved == amount && e.State == "reserved" {
			return nil
		}
		return ErrConflict
	}
	used, err := exposure(d, tenant, currency)
	if err != nil {
		return err
	}
	limit, ok := d.Limits[key]
	if !ok || used > limit || amount > limit-used {
		return errors.New("budget exhausted or missing")
	}
	d.Entries[id] = CostEntry{ID: id, Tenant: tenant, Provider: provider, Model: model, Currency: currency, Reserved: amount, State: "reserved", UpdatedAt: time.Now().UTC()}
	p := &postgresScope{name: "budget", ctx: ctx, tx: tx, before: before}
	return p.write(d)
}

// CancelQueuedPostgresTx atomically cancels queue ownership and resolves only a
// reservation proven not to have crossed dispatch. Unknown exposure survives.
func CancelQueuedPostgresTx(ctx context.Context, tx *sql.Tx, tenant, id, kind string) error {
	d := costSnapshot{Version: 1, Limits: map[string]int64{}, Entries: map[string]CostEntry{}}
	raw, before, err := readPostgresScopeTx(ctx, tx, "budget", d)
	if err != nil {
		return err
	}
	if err = json.Unmarshal(raw, &d); err != nil {
		return err
	}
	if err = jobqueue.CancelTx(ctx, tx, tenant, id); err != nil {
		return err
	}
	var state, actualKind string
	if err = tx.QueryRowContext(ctx, `SELECT state,kind FROM execution_jobs WHERE tenant_id=$1 AND id=$2`, tenant, id).Scan(&state, &actualKind); err != nil {
		return err
	}
	if actualKind != kind && actualKind != kind+"_resume" {
		return ErrConflict
	}
	key := kind + ":" + id
	e, ok := d.Entries[key]
	if !ok {
		return nil
	}
	if e.Tenant != tenant {
		return ErrConflict
	}
	if e.State != "reserved" {
		return nil
	}
	if state == "canceled" {
		e.State = "released"
		e.Evidence = "cancelled_before_dispatch"
	} else {
		e.State = "unknown"
		e.Evidence = ""
	}
	e.UpdatedAt = time.Now().UTC()
	d.Entries[key] = e
	p := &postgresScope{name: "budget", ctx: ctx, tx: tx, before: before}
	return p.write(d)
}

func OpenPostgresBudgetLedger(db *sql.DB) (*BudgetLedger, error) {
	l := &BudgetLedger{data: costSnapshot{Version: 1, Limits: map[string]int64{}, Entries: map[string]CostEntry{}}}
	var err error
	l.pg, err = attachPostgres(db, "budget", &l.data, &l.mu)
	return l, err
}

// WithPostgresBudgetMaintenance is an explicitly offline privileged operation.
// The session lock excludes all normal domain transactions. Only this private
// ledger bypasses the shared barrier, avoiding a lock upgrade deadlock.
func WithPostgresBudgetMaintenance(ctx context.Context, db *sql.DB, fn func(*BudgetLedger) error, tenants ...string) error {
	if fn == nil {
		return errors.New("maintenance callback required")
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return errors.New("maintenance connection unavailable")
	}
	defer conn.Close()
	var acquired bool
	if err = conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock(728219117)`).Scan(&acquired); err != nil || !acquired {
		return errors.New("stop all API and workers before maintenance")
	}
	defer conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock(728219117)`)
	l := &BudgetLedger{data: costSnapshot{Version: 1, Limits: map[string]int64{}, Entries: map[string]CostEntry{}}}
	raw, _ := json.Marshal(l.data)
	p := &postgresScope{db: db, name: "budget", target: &l.data, empty: raw, skipMigrationBarrier: true}
	l.pg = p
	l.mu.scope = p
	l.mu.Lock()
	err = p.err
	l.mu.Unlock()
	if err != nil {
		return err
	}

	tenant := ""
	if len(tenants) > 0 {
		tenant = tenants[0]
	}
	var actor string
	if err = conn.QueryRowContext(ctx, `SELECT current_user`).Scan(&actor); err != nil {
		return errors.New("maintenance actor unavailable")
	}
	request := authRandom(16)
	started := time.Now()
	writeAudit := func(phase string, status int) error {
		_, e := conn.ExecContext(ctx, `INSERT INTO studio_audit_events(request_id,phase,at,actor_id,tenant_id,role,method,status,duration_ms) VALUES($1,$2,$3,$4,$5,'database-operator','BUDGET_MAINTENANCE',$6,$7)`, request, phase, time.Now().UTC(), actor, tenant, status, time.Since(started).Milliseconds())
		if e != nil {
			return errors.New("maintenance audit unavailable")
		}
		return nil
	}
	if err = writeAudit("started", 0); err != nil {
		return err
	}
	operationErr := fn(l)
	status := 200
	if operationErr != nil {
		status = 500
	}
	return errors.Join(operationErr, writeAudit("finished", status))
}
