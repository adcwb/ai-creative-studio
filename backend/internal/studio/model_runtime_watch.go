package studio

import (
	"context"
	"time"
)

// RuntimeNoDispatchError proves that this invocation did not enter an outbound
// transport. It is safe to release its reservation. Any attempted transport,
// including a connection error, is conservatively not covered by this evidence.
type RuntimeNoDispatchError struct{ cause error }

func (e *RuntimeNoDispatchError) Error() string { return "model request was not dispatched" }
func (e *RuntimeNoDispatchError) Unwrap() error { return e.cause }
func RuntimeNoDispatch(err error) bool {
	// An inner callback's local failure cannot erase an outer dispatch attempt.
	_, ok := err.(*RuntimeNoDispatchError)
	return ok
}

type runtimeAttemptedError struct{ cause error }

func (e *runtimeAttemptedError) Error() string { return "model request dispatch was attempted" }
func (e *runtimeAttemptedError) Unwrap() error { return e.cause }

// Membership/configuration changes made by another API process are observed
// through the shared authoritative store. One-second polling plus a two-second
// refresh watchdog bounds local cancellation to three seconds; already accepted
// supplier work cannot be recalled and remains charge-unknown. A slow store
// refresh cancels the request rather than continuing with a cached permission.
func (r *ModelRuntime) watchAuthorization(ctx context.Context, selection RuntimeSelection, cancel context.CancelFunc) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		checked := make(chan error, 1)
		go func() { checked <- r.check(ctx, selection) }()
		watchdog := time.NewTimer(2 * time.Second)
		select {
		case <-ctx.Done():
			watchdog.Stop()
			return
		case <-watchdog.C:
			cancel()
			return
		case err := <-checked:
			watchdog.Stop()
			if err != nil {
				cancel()
				return
			}
		}
	}
}
