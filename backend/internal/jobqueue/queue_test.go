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
