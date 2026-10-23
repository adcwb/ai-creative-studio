package studio

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/adcwb/ai-creative-studio/backend/internal/jobqueue"
	"time"
)

func RunQueuedCreativeWorker(ctx context.Context, q jobqueue.Queue, s *FileStore) error {
	return runQueuedCreative(ctx, q, s, "creative")
}
func RunQueuedCreativeRecoveryWorker(ctx context.Context, q jobqueue.Queue, s *FileStore) error {
	return runQueuedCreative(ctx, q, s, "creative_resume")
}
func runQueuedCreative(ctx context.Context, q jobqueue.Queue, s *FileStore, kind string) error {
	if s == nil || s.creative == nil || s.creative.pg == nil {
		return errors.New("creative worker requires postgres")
	}
	c := s.creative
	return jobqueue.Run(ctx, q, kind, func(ctx context.Context, q jobqueue.Queue, j jobqueue.Job) error {
		payload, err := projectJobPayload(j)
		if err != nil {
			return q.Finish(ctx, j, "failed", json.RawMessage(`{"code":"invalid_job"}`))
		}
		c.mu.Lock()
		job, exists := c.data.Jobs[j.ID]
		if !exists || job.SpaceID != j.TenantID || job.ProjectID != payload.ProjectID {
			c.mu.Unlock()
			return errors.New("queued creative job not found")
		}
		if job.Status != "queued" && job.Status != "running" {
			c.mu.Unlock()
			return jobqueue.ErrLeaseLost
		}
		job.Status = "running"
		job.UpdatedAt = time.Now().UTC()
		job.lease = &j
		c.data.Jobs[j.ID] = job
		c.pg.beforeCommit = func(ctx context.Context, tx *sql.Tx) error { return jobqueue.CheckLeaseTx(ctx, tx, j) }
		err = c.saveLocked()
		c.pg.beforeCommit = nil
		c.mu.Unlock()
		if err != nil {
			return err
		}
		if err = q.Dispatch(ctx, j); err != nil {
			return err
		}
		runCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
		defer cancel()
		if kind == "creative_resume" {
			if (job.Kind != "video" && !(job.Kind == "image" && job.Provider == "jimeng-images")) || job.UpstreamID == "" || job.RuntimeSelection == nil {
				err = ErrExecutionGovernance
			} else {
				request := ExecutionRequest{ID: "creative:" + job.ID, Tenant: job.SpaceID, Provider: job.Provider, Model: job.Model, Capability: job.RuntimeSelection.Capability, Selection: job.RuntimeSelection, UpstreamID: job.UpstreamID}
				err = s.execution.ExecuteRecovery(runCtx, s.runtime, request, func(ctx context.Context, providers RuntimeProviders) error {
					return s.executeCreativeJobWith(ctx, &job, &providers)
				})
			}
		} else {
			err = s.executeCreativeJob(runCtx, &job)
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			job.Status = "failed"
			job.ErrorCode, job.Error = creativeFailure(err)
		} else {
			job.Status = "succeeded"
		}
		return c.updateJob(job)
	})
}
