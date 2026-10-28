package application

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/adcwb/ai-creative-studio/backend/internal/platform/jobqueue"
)

// QueuedImageRepository commits the domain result and queue fencing check in one
// database transaction. It must never delegate FinishQueued to an unfenced write.
type QueuedImageRepository interface {
	Get(string, string) (Task, error)
	FinishQueued(context.Context, jobqueue.Job, GenerationResult, error) error
}

// RunQueuedImageWorker executes durable image jobs outside the API process.
// Managed remote selections cross the same fresh authorization, reserve,
// moderation and dispatch guard as file workers; ungoverned remote jobs fail closed.
func RunQueuedImageWorker(ctx context.Context, q jobqueue.Queue, repo QueuedImageRepository, p *Pipeline) error {
	if repo == nil || p == nil {
		return errors.New("queued image worker dependencies required")
	}
	return jobqueue.Run(ctx, q, "image", func(ctx context.Context, q jobqueue.Queue, j jobqueue.Job) error {
		var payload struct {
			TaskID string `json:"task_id"`
		}
		if json.Unmarshal(j.Payload, &payload) != nil || payload.TaskID == "" || payload.TaskID != j.ID {
			return q.Finish(ctx, j, "failed", json.RawMessage(`{"code":"invalid_job"}`))
		}
		t, err := repo.Get(j.TenantID, payload.TaskID)
		if err != nil {
			return err
		}
		if t.SpaceID != j.TenantID || t.ID != j.ID {
			return errors.New("queued image tenant mismatch")
		}
		if store, ok := repo.(*FileStore); ok {
			if err := store.markQueuedRunning(ctx, j); err != nil {
				return err
			}
		}
		if err = q.Dispatch(ctx, j); err != nil {
			return err
		}
		var result GenerationResult
		var runErr error
		if t.RuntimeSelection != nil {
			store, ok := repo.(*FileStore)
			if !ok {
				runErr = ErrExecutionGovernance
			} else {
				runErr = store.executeManaged(ctx, imageExecutionRequest(t), func(ctx context.Context, providers RuntimeProviders) error {
					if providers.Image == nil {
						return ErrExecutionGovernance
					}
					var e error
					if async, ok := providers.Image.(AsyncImageProvider); ok {
						if t.UpstreamID == "" {
							ack, submitErr := async.SubmitImage(ctx, t.Input)
							e = submitErr
							if e == nil {
								e = store.SetImageUpstreamQueued(ctx, j, t, ack.ID)
								if e == nil {
									t.UpstreamID = ack.ID
								}
							}
						}
						if e == nil {
							result, e = awaitAsyncImage(ctx, async, t.UpstreamID, 2*time.Second)
						}
					} else {
						result, e = providers.Image.Generate(ctx, t.Input)
					}
					if e == nil {
						e = store.execution.ReviewResult(ctx, ExecutionRequest{ID: "image:" + t.ID, Selection: t.RuntimeSelection, Tenant: t.SpaceID, Provider: t.Provider, Model: t.Model, Capability: "image", HasMedia: true, Media: []ExecutionMedia{{Data: result.Data, MIME: result.MIMEType}}})
					}
					return e
				})
			}
		} else if !p.capabilities.Ready || p.capabilities.Mode != "mock" || t.Provider != p.capabilities.Provider || t.Model != p.capabilities.Model {
			runErr = providerFailure("provider_unavailable")
		} else {
			result, runErr = p.Invoke(ctx, t.Input)
		}

		if ctx.Err() != nil {
			return ctx.Err()
		}
		return repo.FinishQueued(ctx, j, result, runErr)
	})
}
