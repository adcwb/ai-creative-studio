package studio

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

const PostgresAuditSchema = `CREATE TABLE IF NOT EXISTS studio_audit_events (
		request_id text NOT NULL CHECK(length(request_id)=32),
		phase text NOT NULL CHECK(phase IN ('started','finished')),
		at timestamptz NOT NULL,
		actor_id text NOT NULL,
		tenant_id text NOT NULL,
		role text NOT NULL,
		method text NOT NULL,
		status integer NOT NULL CHECK(status BETWEEN 0 AND 599),
		duration_ms bigint NOT NULL CHECK(duration_ms >= 0),
		PRIMARY KEY(request_id, phase)
	)`

// OpenPostgresOperations shares durable audit events across API processes. The
// caller owns db. This is not an external immutable audit archive: database
// administrators can alter records. Started events commit before mutations;
// they do not imply that the associated business transaction committed.
func OpenPostgresOperations(db *sql.DB) (*Operations, error) {
	if db == nil {
		return nil, errors.New("audit database required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var present bool
	err := db.QueryRowContext(ctx, "SELECT to_regclass('studio_audit_events') IS NOT NULL").Scan(&present)
	if err == nil && !present {
		err = errors.New("audit schema missing")
	}
	if err != nil {
		return nil, errors.New("audit database initialization failed")
	}
	return &Operations{db: db, counts: map[int]uint64{}, started: time.Now()}, nil
}

func (o *Operations) appendPostgres(record auditRecord) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// Explicitly require WAL durability even if an operator changed the session
	// default. A completed audit write must survive a process crash.
	tx, err := o.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "SET LOCAL synchronous_commit = on"); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO studio_audit_events
		(request_id, phase, at, actor_id, tenant_id, role, method, status, duration_ms)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`, record.RequestID, record.Phase,
		record.At, record.Actor, record.Tenant, record.Role, record.Method, record.Status, record.DurationMS)
	if err != nil {
		return err
	}
	return tx.Commit()
}
