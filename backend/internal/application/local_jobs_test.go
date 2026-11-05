package application

import (
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

func TestLocalJobsQueueAdmissionDoesNotIncreaseExecutionConcurrency(t *testing.T) {
	q := newLocalJobs(2)
	gate := make(chan struct{})
	started := make(chan struct{}, 12)
	var running, peak, finished atomic.Int32
	for range 12 {
		q.submit(func() {
			n := running.Add(1)
			for old := peak.Load(); n > old; old = peak.Load() {
				if peak.CompareAndSwap(old, n) {
					break
				}
			}
			started <- struct{}{}
			<-gate
			running.Add(-1)
			finished.Add(1)
		})
	}
	for range 2 {
		select {
		case <-started:
		case <-time.After(3 * time.Second):
			close(gate)
			q.close()
			t.Fatal("queued work did not use available worker slots")
		}
	}
	if peak.Load() != 2 {
		t.Errorf("execution concurrency %d", peak.Load())
	}
	close(gate)
	deadline := time.Now().Add(3 * time.Second)
	for finished.Load() != 12 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	q.close()
	if finished.Load() != 12 || peak.Load() > 2 {
		t.Fatal("queued work lost or worker limit bypassed", finished.Load(), peak.Load())
	}
}

func TestFileExportQueueDoesNotRejectThirdExportAndKeepsCancellation(t *testing.T) {
	m, err := openMediaStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	gate := make(chan struct{})
	started := make(chan struct{}, 2)
	m.jobs = newLocalJobs(2)
	for range 2 {
		m.jobs.submit(func() { started <- struct{}{}; <-gate })
	}
	for range 2 {
		<-started
	}
	defer m.close()
	defer close(gate)
	p, err := m.Demo("project", "space", "queued-demo", 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := range 5 {
		key := fmt.Sprintf("queued-export-%d", i)
		e, err := m.StartExport("project", "space", key, p.Version)
		if err != nil || e.Status != "queued" {
			t.Fatal("admission quota instead of persistent waiting", e, err)
		}
		again, err := m.StartExport("project", "space", key, p.Version)
		if err != nil || again.ID != e.ID {
			t.Fatal("queued replay", again, err)
		}
		canceled, err := m.CancelExport("project", "space", fmt.Sprintf("cancel-%d", i), e.ID)
		if err != nil || canceled.Status != "cancelled" {
			t.Fatal("queued cancel", canceled, err)
		}
	}
	if len(m.Get("project", "space").Exports) != 5 {
		t.Fatal("accepted queued work was not persisted")
	}
}
