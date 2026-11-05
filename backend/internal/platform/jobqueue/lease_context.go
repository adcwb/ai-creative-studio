package jobqueue

import (
	"context"
	"database/sql"
	"errors"
)

type leaseContextKey struct{}
type leaseContext struct {
	queue Queue
	job   Job
}

// WithLeaseFence carries a trusted worker's current lease through provider
// preparation. It is not a substitute for Dispatch or a fenced result commit.
func WithLeaseFence(ctx context.Context, q Queue, j Job) context.Context {
	return context.WithValue(ctx, leaseContextKey{}, leaseContext{q, j})
}

// CheckContextLease performs a read-only last check after preparation/network
// counting. No row lock is held across the following provider HTTP request.
func CheckContextLease(ctx context.Context) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	lease, ok := ctx.Value(leaseContextKey{}).(leaseContext)
	if !ok {
		return nil
	}
	if lease.queue.DB == nil {
		return ErrLeaseLost
	}
	var id string
	e := lease.queue.DB.QueryRowContext(ctx, `SELECT id FROM execution_jobs WHERE tenant_id=$1 AND id=$2 AND lease_token=$3 AND state IN ('leased','dispatch_started') AND lease_until>clock_timestamp()`, lease.job.TenantID, lease.job.ID, lease.job.Token).Scan(&id)
	if errors.Is(e, sql.ErrNoRows) {
		return ErrLeaseLost
	}
	return e
}
