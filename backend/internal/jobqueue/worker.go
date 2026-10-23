package jobqueue

import (
	"context"
	"errors"
	"time"
)

// Handler must persist domain results and FinishTx in the same transaction.
// It calls Dispatch immediately before side effects. Any ambiguous failure after
// Dispatch must preserve unknown cost; Run never retries a dispatched job.
type Handler func(context.Context, Queue, Job) error

func Run(ctx context.Context, q Queue, kind string, handler Handler) error {
	if handler == nil {
		return errors.New("job handler required")
	}
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
		}
		j, ok, err := q.Claim(ctx, kind)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		runCtx, cancel := context.WithCancel(ctx)
		done := make(chan error, 1)
		go func() {
			timer := time.NewTicker(q.lease() / 3)
			defer timer.Stop()
			for {
				select {
				case <-runCtx.Done():
					done <- nil
					return
				case <-timer.C:
					if err := q.Renew(runCtx, j); err != nil {
						cancel()
						done <- err
						return
					}
				}
			}
		}()
		err = handler(runCtx, q, j)
		cancel()
		renewErr := <-done
		// A completed handler clears the token; renewal racing its commit can observe
		// ErrLeaseLost. The fenced domain transaction is authoritative in that case.
		if err != nil {
			return err
		}
		if renewErr != nil && !errors.Is(renewErr, ErrLeaseLost) && ctx.Err() == nil {
			return renewErr
		}
	}
}
