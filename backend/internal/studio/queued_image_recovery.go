package studio

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/adcwb/ai-creative-studio/backend/internal/jobqueue"
	"time"
)

func (s *FileStore) SetImageUpstreamQueued(ctx context.Context, j jobqueue.Job, t Task, id string) error {
	if !upstreamImageIDPattern.MatchString(id) {
		return providerFailure("invalid_provider_response")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.data.Tasks[t.ID]
	if !ok || current.SpaceID != j.TenantID || current.ID != j.ID || s.pg == nil {
		return ErrNotFound
	}
	if (current.Status != "queued" && current.Status != "running") || current.Provider != t.Provider || current.Model != t.Model || (current.UpstreamID != "" && current.UpstreamID != id) {
		return ErrConflict
	}
	current.UpstreamID = id
	current.UpdatedAt = time.Now().UTC()
	d := s.clone()
	d.Tasks[t.ID] = current
	s.pg.beforeCommit = func(ctx context.Context, tx *sql.Tx) error { return jobqueue.CheckLeaseTx(ctx, tx, j) }
	defer func() { s.pg.beforeCommit = nil }()
	return s.commit(d)
}
func RunQueuedImageRecoveryWorker(ctx context.Context, q jobqueue.Queue, s *FileStore) error {
	if s == nil || s.pg == nil {
		return errors.New("image recovery requires postgres")
	}
	return jobqueue.Run(ctx, q, "image_resume", func(ctx context.Context, q jobqueue.Queue, j jobqueue.Job) error {
		var payload struct {
			TaskID string `json:"task_id"`
		}
		if json.Unmarshal(j.Payload, &payload) != nil || payload.TaskID != j.ID {
			return q.Finish(ctx, j, "failed", nil)
		}
		t, err := s.Get(j.TenantID, j.ID)
		if err != nil {
			return err
		}
		if t.UpstreamID == "" || t.RuntimeSelection == nil || t.Provider != "jimeng-images" {
			return s.FinishQueued(ctx, j, GenerationResult{}, ErrExecutionGovernance)
		}
		if err := s.markQueuedRunning(ctx, j); err != nil {
			return err
		}
		if err = q.Dispatch(ctx, j); err != nil {
			return err
		}
		var result GenerationResult
		request := imageExecutionRequest(t)
		request.UpstreamID = t.UpstreamID
		runErr := s.execution.ExecuteRecovery(ctx, s.runtime, request, func(ctx context.Context, providers RuntimeProviders) error {
			async, ok := providers.Image.(AsyncImageProvider)
			if !ok {
				return ErrExecutionGovernance
			}
			var e error
			result, e = awaitAsyncImage(ctx, async, t.UpstreamID, 2*time.Second)
			if e == nil {
				e = s.execution.ReviewResult(ctx, ExecutionRequest{ID: "image:" + t.ID, Tenant: t.SpaceID, Provider: t.Provider, Model: t.Model, Capability: "image", Selection: t.RuntimeSelection, HasMedia: true, Media: []ExecutionMedia{{Data: result.Data, MIME: result.MIMEType}}})
			}
			return e
		})
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return s.FinishQueued(ctx, j, result, runErr)
	})
}
