package jobqueue

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	_ "github.com/jackc/pgx/v5/stdlib"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func testQueue(t *testing.T) Queue {
	t.Helper()
	dsn := os.Getenv("JOBQUEUE_TEST_DSN")
	if dsn == "" {
		t.Skip("JOBQUEUE_TEST_DSN required: real isolated PostgreSQL")
	}
	db, e := sql.Open("pgx", dsn)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	q := Queue{DB: db, Lease: time.Second}
	if e = q.Init(context.Background()); e != nil {
		t.Fatal(e)
	}
	return q
}
func enqueue(t *testing.T, q Queue, j Job) {
	t.Helper()
	tx, e := q.DB.BeginTx(context.Background(), nil)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	if e = EnqueueTx(context.Background(), tx, j); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
}
func TestQueueTransactionsFencingAndIsolation(t *testing.T) {
	q := testQueue(t)
	ctx := context.Background()
	kind := fmt.Sprintf("test-%d", time.Now().UnixNano())
	t.Cleanup(func() { q.DB.Exec(`DELETE FROM execution_jobs WHERE kind=$1`, kind) })
	j := Job{TenantID: kind + "a", ID: "same", Kind: kind, IdempotencyKey: "k", Payload: json.RawMessage(`{"task_id":"same"}`)}
	tx, e := q.DB.BeginTx(ctx, nil)
	if e != nil {
		t.Fatal(e)
	}
	if e = EnqueueTx(ctx, tx, j); e != nil {
		t.Fatal(e)
	}
	tx.Rollback()
	if _, e = q.Get(ctx, j.TenantID, "same"); !errors.Is(e, sql.ErrNoRows) {
		t.Fatalf("rollback: %v", e)
	}
	enqueue(t, q, j)
	enqueue(t, q, j)
	other := j
	other.TenantID = kind + "b"
	enqueue(t, q, other)
	tx, _ = q.DB.BeginTx(ctx, nil)
	changed := j
	changed.Payload = json.RawMessage(`{"changed":true}`)
	if e = EnqueueTx(ctx, tx, changed); !errors.Is(e, ErrConflict) {
		t.Fatalf("conflict %v", e)
	}
	tx.Rollback()
	var wg sync.WaitGroup
	claims := make(chan Job, 2)
	errs := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			job, ok, e := q.Claim(ctx, kind)
			if e != nil {
				errs <- e
			} else if !ok {
				errs <- errors.New("missing claim")
			} else {
				claims <- job
			}
		}()
	}
	wg.Wait()
	close(claims)
	close(errs)
	for e := range errs {
		t.Fatal(e)
	}
	seen := map[string]bool{}
	var first Job
	for job := range claims {
		if seen[job.TenantID] {
			t.Fatal("double claim")
		}
		seen[job.TenantID] = true
		first = job
	}
	if len(seen) != 2 {
		t.Fatal(seen)
	}
	forged := first
	forged.TenantID = "another"
	if e = q.Dispatch(ctx, forged); !errors.Is(e, ErrLeaseLost) {
		t.Fatalf("tenant fence %v", e)
	}
	_, e = q.DB.Exec(`UPDATE execution_jobs SET lease_until=clock_timestamp()-interval '1 second' WHERE tenant_id=$1 AND id=$2`, first.TenantID, first.ID)
	if e != nil {
		t.Fatal(e)
	}
	newer, ok, e := q.Claim(ctx, kind)
	if e != nil || !ok || newer.Token == first.Token {
		t.Fatalf("reclaim %v %v", ok, e)
	}
	if e = q.Finish(ctx, first, "succeeded", nil); !errors.Is(e, ErrLeaseLost) {
		t.Fatalf("stale finish %v", e)
	}
	if e = q.Dispatch(ctx, newer); e != nil {
		t.Fatal(e)
	}
	_, e = q.DB.Exec(`UPDATE execution_jobs SET lease_until=clock_timestamp()-interval '1 second' WHERE tenant_id=$1 AND id=$2`, newer.TenantID, newer.ID)
	if e != nil {
		t.Fatal(e)
	}
	_, _, e = q.Claim(ctx, kind)
	if e != nil {
		t.Fatal(e)
	}
	got, e := q.Get(ctx, newer.TenantID, newer.ID)
	if e != nil || got.State != "unknown" {
		t.Fatalf("dispatch must not retry: %+v %v", got, e)
	}
	if e = q.Finish(ctx, newer, "succeeded", nil); !errors.Is(e, ErrLeaseLost) {
		t.Fatalf("expired dispatched finish %v", e)
	}
}
func TestQueueCancelAndAtomicFinish(t *testing.T) {
	q := testQueue(t)
	ctx := context.Background()
	kind := fmt.Sprintf("finish-%d", time.Now().UnixNano())
	t.Cleanup(func() { q.DB.Exec(`DELETE FROM execution_jobs WHERE kind=$1`, kind) })
	j := Job{TenantID: "tenant", ID: kind, Kind: kind, IdempotencyKey: kind, Payload: json.RawMessage(`{}`)}
	enqueue(t, q, j)
	leased, ok, e := q.Claim(ctx, kind)
	if e != nil || !ok {
		t.Fatal(e)
	}
	tx, _ := q.DB.BeginTx(ctx, nil)
	if e = FinishTx(ctx, tx, leased, "succeeded", nil); e != nil {
		t.Fatal(e)
	}
	tx.Rollback()
	got, _ := q.Get(ctx, j.TenantID, j.ID)
	if got.State != "leased" {
		t.Fatal("non-atomic finish")
	}
	if e = q.Dispatch(ctx, leased); e != nil {
		t.Fatal(e)
	}
	if e = q.Cancel(ctx, "wrong", j.ID); !errors.Is(e, ErrLeaseLost) {
		t.Fatal("cross tenant cancel")
	}
	if e = q.Cancel(ctx, j.TenantID, j.ID); e != nil {
		t.Fatal(e)
	}
	got, _ = q.Get(ctx, j.TenantID, j.ID)
	if got.State != "unknown" {
		t.Fatal("cancel erased uncertainty")
	}
	if e = q.Renew(ctx, leased); !errors.Is(e, ErrLeaseLost) {
		t.Fatal("revoked lease renew")
	}
}

// The helper runs in a different process with no inherited provider credentials.
func TestQueueProcessHelper(t *testing.T) {
	if os.Getenv("JOBQUEUE_HELPER") != "1" {
		return
	}
	q := testQueue(t)
	ctx := context.Background()
	j, ok, err := q.Claim(ctx, os.Getenv("JOBQUEUE_KIND"))
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		return
	}
	if os.Getenv("JOBQUEUE_DISPATCH") == "1" {
		if err = q.Dispatch(ctx, j); err != nil {
			t.Fatal(err)
		}
	}
	fmt.Println("CLAIMED")
	// Exit without Finish or lease renewal, equivalent to worker loss.
}
func TestQueueIndependentProcessesAndCrash(t *testing.T) {
	q := testQueue(t)
	ctx := context.Background()
	kind := fmt.Sprintf("process-%d", time.Now().UnixNano())
	t.Cleanup(func() { q.DB.Exec(`DELETE FROM execution_jobs WHERE kind=$1`, kind) })
	j := Job{TenantID: kind, ID: kind, Kind: kind, IdempotencyKey: kind, Payload: json.RawMessage(`{}`)}
	enqueue(t, q, j)
	var wg sync.WaitGroup
	out := make(chan string, 2)
	errCh := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cmd := exec.Command(os.Args[0], "-test.run=^TestQueueProcessHelper$")
			cmd.Env = []string{"JOBQUEUE_HELPER=1", "JOBQUEUE_KIND=" + kind, "JOBQUEUE_DISPATCH=1", "JOBQUEUE_TEST_DSN=" + os.Getenv("JOBQUEUE_TEST_DSN")}
			b, e := cmd.CombinedOutput()
			if e != nil {
				errCh <- fmt.Errorf("helper: %w %s", e, b)
			}
			out <- string(b)
		}()
	}
	wg.Wait()
	close(out)
	close(errCh)
	for e := range errCh {
		t.Fatal(e)
	}
	count := 0
	for s := range out {
		count += strings.Count(s, "CLAIMED")
	}
	if count != 1 {
		t.Fatalf("cross-process duplicate claim: %d", count)
	}
	if _, e := q.DB.Exec(`UPDATE execution_jobs SET lease_until=clock_timestamp()-interval '1 second' WHERE tenant_id=$1 AND id=$2`, j.TenantID, j.ID); e != nil {
		t.Fatal(e)
	}
	_, ok, e := q.Claim(ctx, kind)
	if e != nil || ok {
		t.Fatalf("crashed dispatched job retried: %v %v", ok, e)
	}
	got, e := q.Get(ctx, j.TenantID, j.ID)
	if e != nil || got.State != "unknown" {
		t.Fatalf("expected unknown %+v %v", got, e)
	}
}

func TestQueueExplicitRecoveryAndFencedSideEffects(t *testing.T) {
	q := testQueue(t)
	ctx := context.Background()
	id := fmt.Sprintf("recovery-%d", time.Now().UnixNano())
	j := Job{TenantID: id, ID: id, Kind: "creative", IdempotencyKey: id, Payload: json.RawMessage(`{"task_id":"synthetic"}`)}
	t.Cleanup(func() { q.DB.Exec(`DELETE FROM execution_jobs WHERE tenant_id=$1`, id) })
	enqueue(t, q, j)
	lease, ok, e := q.Claim(ctx, "creative")
	if e != nil || !ok {
		t.Fatal(e)
	}
	if lease.ID != id {
		t.Fatal("test database contains unrelated creative work")
	}
	if e = q.Dispatch(ctx, lease); e != nil {
		t.Fatal(e)
	}
	if e = q.Finish(ctx, lease, "unknown", nil); e != nil {
		t.Fatal(e)
	}
	tx, _ := q.DB.BeginTx(ctx, nil)
	if e = CheckLeaseTx(ctx, tx, lease); !errors.Is(e, ErrLeaseLost) {
		t.Fatal("old token admitted domain side effect")
	}
	tx.Rollback()
	recovery := j
	recovery.Kind = "creative_resume"
	recovery.IdempotencyKey = "explicit-recovery"
	tx, _ = q.DB.BeginTx(ctx, nil)
	if e = ResumeTx(ctx, tx, recovery); e != nil {
		t.Fatal(e)
	}
	tx.Rollback()
	got, _ := q.Get(ctx, id, id)
	if got.State != "unknown" {
		t.Fatal("resume leaked before commit")
	}
	tx, _ = q.DB.BeginTx(ctx, nil)
	if e = ResumeTx(ctx, tx, recovery); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	resumed, ok, e := q.Claim(ctx, "creative_resume")
	if e != nil || !ok || resumed.ID != id || resumed.Attempt != 2 {
		t.Fatalf("explicit query claim %+v %v", resumed, e)
	}
	tx, _ = q.DB.BeginTx(ctx, nil)
	if e = CheckLeaseTx(ctx, tx, resumed); e != nil {
		t.Fatal(e)
	}
	if e = FinishTx(ctx, tx, resumed, "succeeded", nil); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	tx, _ = q.DB.BeginTx(ctx, nil)
	if e = ResumeTx(ctx, tx, recovery); !errors.Is(e, ErrLeaseLost) {
		t.Fatal("completed recovery was requeued")
	}
	tx.Rollback()
}

func TestWorkerContinuesAfterCrossProcessCancellation(t *testing.T) {
	q := testQueue(t)
	kind := fmt.Sprintf("cancel-loop-%d", time.Now().UnixNano())
	t.Cleanup(func() { q.DB.Exec(`DELETE FROM execution_jobs WHERE kind=$1`, kind) })
	for i := range 2 {
		id := fmt.Sprintf("%s-%d", kind, i)
		enqueue(t, q, Job{TenantID: kind, ID: id, Kind: kind, IdempotencyKey: id, Payload: json.RawMessage(`{}`)})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	calls := 0
	err := Run(ctx, q, kind, func(ctx context.Context, q Queue, j Job) error {
		calls++
		if e := q.Dispatch(ctx, j); e != nil {
			return e
		}
		if calls == 1 {
			if e := q.Cancel(ctx, j.TenantID, j.ID); e != nil {
				return e
			}
			return context.Canceled
		}
		if e := q.Finish(ctx, j, "succeeded", nil); e != nil {
			return e
		}
		cancel()
		return nil
	})
	if err != nil || calls != 2 {
		t.Fatalf("cancellation stopped worker: calls=%d err=%v", calls, err)
	}
}

func TestDistributedTenantAdmissionAcrossKinds(t *testing.T) {
	q := testQueue(t)
	q.Lease = time.Minute
	ctx := context.Background()
	tenant := fmt.Sprintf("admission-%d", time.Now().UnixNano())
	t.Cleanup(func() { q.DB.Exec(`DELETE FROM execution_jobs WHERE tenant_id=$1`, tenant) })
	kinds := make([]string, 8)
	for i := range kinds {
		kinds[i] = fmt.Sprintf("%s-%d", tenant, i)
		enqueue(t, q, Job{TenantID: tenant, ID: kinds[i], Kind: kinds[i], IdempotencyKey: kinds[i], Payload: json.RawMessage(`{}`)})
	}
	var wg sync.WaitGroup
	claims := make(chan bool, 8)
	errs := make(chan error, 8)
	for _, kind := range kinds {
		wg.Add(1)
		go func(kind string) { defer wg.Done(); _, ok, e := q.Claim(ctx, kind); claims <- ok; errs <- e }(kind)
	}
	wg.Wait()
	close(claims)
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	count := 0
	for ok := range claims {
		if ok {
			count++
		}
	}
	if count != 4 {
		t.Fatalf("distributed tenant cap: %d", count)
	}
}
func TestWorkerSharedSlotsBoundConcurrentHandlers(t *testing.T) {
	q := testQueue(t)
	q.Slots = make(chan struct{}, 1)
	tenant := fmt.Sprintf("slots-%d", time.Now().UnixNano())
	t.Cleanup(func() { q.DB.Exec(`DELETE FROM execution_jobs WHERE tenant_id=$1`, tenant) })
	kinds := make([]string, 3)
	for i := range kinds {
		kinds[i] = fmt.Sprintf("%s-%d", tenant, i)
		enqueue(t, q, Job{TenantID: tenant, ID: kinds[i], Kind: kinds[i], IdempotencyKey: kinds[i], Payload: json.RawMessage(`{}`)})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var active, maxSeen, finished atomic.Int64
	done := make(chan error, 3)
	for _, kind := range kinds {
		go func(kind string) {
			done <- Run(ctx, q, kind, func(ctx context.Context, q Queue, j Job) error {
				n := active.Add(1)
				defer active.Add(-1)
				for old := maxSeen.Load(); n > old && !maxSeen.CompareAndSwap(old, n); old = maxSeen.Load() {
				}
				if e := q.Dispatch(ctx, j); e != nil {
					return e
				}
				time.Sleep(40 * time.Millisecond)
				if e := q.Finish(ctx, j, "succeeded", nil); e != nil {
					return e
				}
				if finished.Add(1) == 3 {
					cancel()
				}
				return nil
			})
		}(kind)
	}
	for range 3 {
		if e := <-done; e != nil {
			t.Fatal(e)
		}
	}
	if finished.Load() != 3 || maxSeen.Load() != 1 {
		t.Fatalf("handlers finished=%d max=%d", finished.Load(), maxSeen.Load())
	}
}
