// Package jobqueue implements a PostgreSQL durable queue. The database is the
// queue, so enqueue and domain mutations can share a transaction without outbox
// dual writes. Leases fence late workers; they do not promise provider exactly-once.
package jobqueue

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

var ErrLeaseLost = errors.New("job lease lost")
var ErrConflict = errors.New("job idempotency conflict")

const Schema = `CREATE TABLE IF NOT EXISTS execution_jobs (
 tenant_id text NOT NULL CHECK (tenant_id <> ''),
 id text NOT NULL CHECK (id <> ''),
 kind text NOT NULL CHECK (kind <> ''),
 idempotency_key text NOT NULL CHECK (idempotency_key <> ''),
 payload jsonb NOT NULL,
 state text NOT NULL DEFAULT 'queued' CHECK (state IN ('queued','leased','dispatch_started','succeeded','failed','unknown','canceled')),
 lease_token text NOT NULL DEFAULT '', lease_until timestamptz,
 attempt bigint NOT NULL DEFAULT 0 CHECK (attempt >= 0),
 result jsonb, created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY (tenant_id,id), UNIQUE (tenant_id,kind,idempotency_key)
);
CREATE INDEX IF NOT EXISTS execution_jobs_claim ON execution_jobs(kind,created_at) WHERE state IN ('queued','leased');`

type Job struct {
	TenantID       string          `json:"tenant_id"`
	ID             string          `json:"id"`
	Kind           string          `json:"kind"`
	IdempotencyKey string          `json:"idempotency_key"`
	Payload        json.RawMessage `json:"payload"`
	State          string          `json:"state"`
	Token          string          `json:"-"`
	Attempt        int64           `json:"attempt"`
}
type Queue struct {
	DB          *sql.DB
	Lease       time.Duration
	BeforeClaim func(context.Context) error
}

func (q Queue) Init(ctx context.Context) error { _, err := q.DB.ExecContext(ctx, Schema); return err }

// EnqueueTx must be called in the same transaction as resource ownership,
// budget reservation, and domain record insertion. Payloads must contain IDs,
// never secrets. A replay cannot replace the original payload.
func EnqueueTx(ctx context.Context, tx *sql.Tx, j Job) error {
	if j.TenantID == "" || j.ID == "" || j.Kind == "" || j.IdempotencyKey == "" || !json.Valid(j.Payload) || len(j.Payload) > 65536 {
		return errors.New("invalid job")
	}
	var id string
	err := tx.QueryRowContext(ctx, `INSERT INTO execution_jobs(tenant_id,id,kind,idempotency_key,payload) VALUES($1,$2,$3,$4,$5) ON CONFLICT(tenant_id,kind,idempotency_key) DO UPDATE SET idempotency_key=execution_jobs.idempotency_key WHERE execution_jobs.id=EXCLUDED.id AND execution_jobs.payload=EXCLUDED.payload RETURNING id`, j.TenantID, j.ID, j.Kind, j.IdempotencyKey, []byte(j.Payload)).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrConflict
	}
	return err
}
func (q Queue) lease() time.Duration {
	if q.Lease < time.Second {
		return 30 * time.Second
	}
	return q.Lease
}

// Claim recovers only work that has not crossed the durable dispatch boundary.
// Expired dispatched work is unknown and never submitted a second time.
func (q Queue) Claim(ctx context.Context, kind string) (Job, bool, error) {
	if q.BeforeClaim != nil {
		if err := q.BeforeClaim(ctx); err != nil {
			return Job{}, false, err
		}
	}
	if kind == "" {
		return Job{}, false, errors.New("job kind required")
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return Job{}, false, err
	}
	token := hex.EncodeToString(b)
	tx, err := q.DB.BeginTx(ctx, nil)
	if err != nil {
		return Job{}, false, err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `UPDATE execution_jobs SET state='unknown',lease_token='',updated_at=clock_timestamp() WHERE kind=$1 AND state='dispatch_started' AND lease_until<=clock_timestamp()`, kind)
	if err != nil {
		return Job{}, false, err
	}
	var j Job
	err = tx.QueryRowContext(ctx, `WITH candidate AS (SELECT tenant_id,id FROM execution_jobs WHERE kind=$1 AND (state='queued' OR (state='leased' AND lease_until<=clock_timestamp())) ORDER BY created_at,tenant_id,id FOR UPDATE SKIP LOCKED LIMIT 1) UPDATE execution_jobs j SET state='leased',lease_token=$2,lease_until=clock_timestamp()+($3 * interval '1 millisecond'),attempt=attempt+1,updated_at=clock_timestamp() FROM candidate c WHERE j.tenant_id=c.tenant_id AND j.id=c.id RETURNING j.tenant_id,j.id,j.kind,j.idempotency_key,j.payload,j.state,j.lease_token,j.attempt`, kind, token, q.lease().Milliseconds()).Scan(&j.TenantID, &j.ID, &j.Kind, &j.IdempotencyKey, &j.Payload, &j.State, &j.Token, &j.Attempt)
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, false, tx.Commit()
	}
	if err != nil {
		return Job{}, false, err
	}
	err = tx.Commit()
	return j, err == nil, err
}
func affected(r sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, err := r.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrLeaseLost
	}
	return nil
}
func (q Queue) Renew(ctx context.Context, j Job) error {
	return affected(q.DB.ExecContext(ctx, `UPDATE execution_jobs SET lease_until=clock_timestamp()+($4 * interval '1 millisecond') WHERE tenant_id=$1 AND id=$2 AND lease_token=$3 AND state IN ('leased','dispatch_started') AND lease_until>clock_timestamp()`, j.TenantID, j.ID, j.Token, q.lease().Milliseconds()))
}
func (q Queue) Dispatch(ctx context.Context, j Job) error {
	return affected(q.DB.ExecContext(ctx, `UPDATE execution_jobs SET state='dispatch_started',updated_at=clock_timestamp() WHERE tenant_id=$1 AND id=$2 AND lease_token=$3 AND state='leased' AND lease_until>clock_timestamp()`, j.TenantID, j.ID, j.Token))
}

// FinishTx lets result persistence and fee settlement share the fenced commit.
func FinishTx(ctx context.Context, tx *sql.Tx, j Job, state string, result json.RawMessage) error {
	if state != "succeeded" && state != "failed" && state != "unknown" {
		return errors.New("invalid terminal state")
	}
	if result == nil {
		result = json.RawMessage(`null`)
	}
	if !json.Valid(result) || len(result) > 65536 {
		return errors.New("invalid job result")
	}
	return affected(tx.ExecContext(ctx, `UPDATE execution_jobs SET state=$4,result=$5,lease_token='',lease_until=NULL,updated_at=clock_timestamp() WHERE tenant_id=$1 AND id=$2 AND lease_token=$3 AND state IN ('leased','dispatch_started') AND lease_until>clock_timestamp()`, j.TenantID, j.ID, j.Token, state, []byte(result)))
}
func (q Queue) Finish(ctx context.Context, j Job, state string, result json.RawMessage) error {
	tx, err := q.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = FinishTx(ctx, tx, j, state, result); err != nil {
		return err
	}
	return tx.Commit()
}

// Cancellation after dispatch records uncertainty, never implies a refund.
func CancelTx(ctx context.Context, tx *sql.Tx, tenant, id string) error {
	if tenant == "" || id == "" {
		return errors.New("tenant and job required")
	}
	return affected(tx.ExecContext(ctx, `UPDATE execution_jobs SET state=CASE WHEN state='dispatch_started' THEN 'unknown' ELSE 'canceled' END,lease_token='',lease_until=NULL,updated_at=clock_timestamp() WHERE tenant_id=$1 AND id=$2 AND state IN ('queued','leased','dispatch_started')`, tenant, id))
}
func (q Queue) Cancel(ctx context.Context, tenant, id string) error {
	tx, err := q.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = CancelTx(ctx, tx, tenant, id); err != nil {
		return err
	}
	return tx.Commit()
}

func (q Queue) Get(ctx context.Context, tenant, id string) (Job, error) {
	var j Job
	if tenant == "" || id == "" {
		return j, fmt.Errorf("tenant and job required")
	}
	err := q.DB.QueryRowContext(ctx, `SELECT tenant_id,id,kind,idempotency_key,payload,state,attempt FROM execution_jobs WHERE tenant_id=$1 AND id=$2`, tenant, id).Scan(&j.TenantID, &j.ID, &j.Kind, &j.IdempotencyKey, &j.Payload, &j.State, &j.Attempt)
	return j, err
}

// CheckLeaseTx fences a domain side effect without ending the job. The row lock
// is retained until the caller commits; revocation and completion serialize with
// that commit, so a stale worker cannot publish an asset or upstream ID.
func CheckLeaseTx(ctx context.Context, tx *sql.Tx, j Job) error {
	var id string
	err := tx.QueryRowContext(ctx, `SELECT id FROM execution_jobs WHERE tenant_id=$1 AND id=$2 AND lease_token=$3 AND state IN ('leased','dispatch_started') AND lease_until>clock_timestamp() FOR UPDATE`, j.TenantID, j.ID, j.Token).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrLeaseLost
	}
	return err
}

// ResumeTx schedules an explicitly requested read-only recovery attempt. The
// caller must validate a durable upstream ID and free-query policy before this
// transaction. It never authorizes another generation submission.
func ResumeTx(ctx context.Context, tx *sql.Tx, j Job) error {
	if j.TenantID == "" || j.ID == "" || (j.Kind != "creative_resume" && j.Kind != "image_resume") || j.IdempotencyKey == "" || !json.Valid(j.Payload) || len(j.Payload) > 65536 {
		return errors.New("invalid recovery job")
	}
	r, err := tx.ExecContext(ctx, `UPDATE execution_jobs SET kind=$3,idempotency_key=$4,payload=$5,state='queued',lease_token='',lease_until=NULL,updated_at=clock_timestamp() WHERE tenant_id=$1 AND id=$2 AND state IN ('unknown','failed','canceled')`, j.TenantID, j.ID, j.Kind, j.IdempotencyKey, []byte(j.Payload))
	return affected(r, err)
}
