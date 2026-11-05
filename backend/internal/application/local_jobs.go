package application

import "sync"

// localJobs bounds execution, not admission. The owning store commits each job
// before submitting it here; queued work never starts its execution timeout
// while waiting for a slot. PostgreSQL uses the durable, fenced worker queue.
type localJobs struct {
	mu      sync.Mutex
	pending []func()
	wake    chan struct{}
	stop    chan struct{}
	wg      sync.WaitGroup
	settled sync.WaitGroup
	once    sync.Once
}

func newLocalJobs(workers int) *localJobs {
	q := &localJobs{wake: make(chan struct{}, 1), stop: make(chan struct{})}
	for range workers {
		q.wg.Add(1)
		go func() {
			defer q.wg.Done()
			for {
				select {
				case <-q.stop:
					return
				default:
				}
				q.mu.Lock()
				var run func()
				if len(q.pending) > 0 {
					run = q.pending[0]
					q.pending[0] = nil
					q.pending = q.pending[1:]
				}
				more := len(q.pending) > 0
				q.mu.Unlock()
				if more {
					select {
					case q.wake <- struct{}{}:
					default:
					}
				}
				if run != nil {
					run()
					q.settled.Done()
					continue
				}
				select {
				case <-q.stop:
					return
				case <-q.wake:
				}
			}
		}()
	}
	return q
}

func (q *localJobs) submit(run func()) {
	q.mu.Lock()
	q.settled.Add(1)
	q.pending = append(q.pending, run)
	q.mu.Unlock()
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

func (q *localJobs) close() {
	if q == nil {
		return
	}
	q.once.Do(func() { close(q.stop) })
	q.wg.Wait()
	q.mu.Lock()
	q.settled.Add(-len(q.pending))
	q.pending = nil
	q.mu.Unlock()
}

func (q *localJobs) wait() {
	if q != nil {
		q.settled.Wait()
	}
}
