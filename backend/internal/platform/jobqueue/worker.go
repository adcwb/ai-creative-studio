package jobqueue

import (
	"context"
	"errors"
	"github.com/adcwb/ai-creative-studio/backend/internal/platform/diagnostics"
	"log/slog"
	"time"

	"github.com/adcwb/ai-creative-studio/backend/internal/platform/tracing"
)

// Handler must persist domain results and FinishTx in the same transaction.
// It calls Dispatch immediately before side effects. Any ambiguous failure after
// Dispatch must preserve unknown cost; Run never retries a dispatched job.
type Handler func(context.Context, Queue, Job) error

func Run(ctx context.Context, q Queue, kind string, handler Handler) (resultErr error) {
	activeCtx := ctx
	stage := "initialize"
	defer func() {
		if recover() != nil {
			resultErr = diagnostics.ErrPanic
			diagnostics.Event(activeCtx, "worker_panic", "execution_interrupted", "", nil)
		} else if resultErr != nil {
			diagnostics.Event(activeCtx, "worker_error", diagnostics.ErrorCode(resultErr), "", nil)
			safeKind := "unknown"
			switch kind {
			case "image", "script", "storyboard", "export", "alignment", "creative", "creative_recovery", "image_recovery", "story_check":
				safeKind = kind
			}
			attrs := []any{"event", "worker_failure", "task_kind", safeKind, "worker_stage", stage, "error_code", diagnostics.ErrorCode(resultErr)}
			attrs = append(attrs, diagnostics.SystemMetadata(resultErr)...)
			slog.ErrorContext(activeCtx, "durable worker stopped", attrs...)
		}
	}()
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
		activeCtx = ctx
		stage = "claim"
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
			defer func() {
				if recover() != nil {
					cancel()
					diagnostics.Event(tracing.WithParent(runCtx, j.TraceParent), "worker_panic", "execution_interrupted", "", nil)
					done <- diagnostics.ErrPanic
				}
			}()
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
			traceCtx, span := tracing.Start(tracing.WithParent(WithLeaseFence(runCtx, q, j), j.TraceParent), "worker.execute", tracing.Attributes{Kind: j.Kind})
			activeCtx = traceCtx
			stage = "execute"
			err = diagnostics.Invoke(traceCtx, "worker_panic", func() error { return handler(traceCtx, q, j) })
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
			stage = "renew"
			return renewErr
		}
	}
}
