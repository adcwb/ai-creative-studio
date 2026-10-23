package jobqueue

import (
	"context"
	"errors"
	"github.com/adcwb/ai-creative-studio/backend/internal/tracing"
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
		if ctx.Err() != nil {
			return nil
		}
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
		acquired := false
		if q.Slots != nil {
			select {
			case q.Slots <- struct{}{}:
				acquired = true
			case <-runCtx.Done():
			}
		}
		if q.Slots == nil || acquired {
			traceCtx, span := tracing.Start(tracing.WithParent(runCtx, j.TraceParent), "worker.execute", tracing.Attributes{Kind: j.Kind})
			err = handler(traceCtx, q, j)
			outcome := "ok"
			if err != nil {
				outcome = "unknown"
			}
			span.End(outcome)
		} else {
			err = runCtx.Err()
		}
		if acquired {
			<-q.Slots
		}
		cancel()
		renewErr := <-done
		if ctx.Err() != nil {
			return nil
		}
		// A completed handler clears the token; renewal racing its commit can observe
		// ErrLeaseLost. The fenced domain transaction is authoritative in that case.
		if errors.Is(err, ErrLeaseLost) || (errors.Is(err, context.Canceled) && errors.Is(renewErr, ErrLeaseLost) && ctx.Err() == nil) {
			continue
		}
		if errors.Is(err, context.Canceled) && ctx.Err() == nil {
			current, readErr := q.Get(ctx, j.TenantID, j.ID)
			if readErr == nil && (current.State == "unknown" || current.State == "canceled") {
				continue
			}
		}
		if err != nil {
			return err
		}
		if renewErr != nil && !errors.Is(renewErr, ErrLeaseLost) && ctx.Err() == nil {
			return renewErr
		}
	}
}
