package jobqueue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adcwb/ai-creative-studio/backend/internal/platform/diagnostics"
)

func TestDiagnosticWorkerPanicRetainsDispatchFenceAndReleasesSlot(t *testing.T) {
	q := testQueue(t)
	q.Slots = make(chan struct{}, 1)
	kind := fmt.Sprintf("diagnostic-%d", time.Now().UnixNano())
	j := Job{TenantID: kind, ID: "synthetic-job", Kind: kind, IdempotencyKey: "one", Payload: json.RawMessage(`{}`), TraceParent: "00-1234567890abcdef1234567890abcdef-1234567890abcdef-01"}
	t.Cleanup(func() { q.DB.Exec(`DELETE FROM execution_jobs WHERE tenant_id=$1`, kind) })
	enqueue(t, q, j)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var calls atomic.Int32
	err := Run(ctx, q, kind, func(ctx context.Context, q Queue, claimed Job) error {
		calls.Add(1)
		if err := q.Dispatch(ctx, claimed); err != nil {
			return err
		}
		panic("sk-private-token private-prompt")
	})
	if !errors.Is(err, diagnostics.ErrPanic) || calls.Load() != 1 || len(q.Slots) != 0 {
		t.Fatal("panic retried or leaked a worker slot", err, calls.Load())
	}
	stored, err := q.Get(context.Background(), kind, j.ID)
	if err != nil || stored.State != "dispatch_started" {
		t.Fatal("dispatched outcome was falsely completed", stored.State, err)
	}
}
