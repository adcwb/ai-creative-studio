package studio

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/adcwb/ai-creative-studio/backend/internal/jobqueue"
	"time"
)

func RunQueuedAlignmentWorker(ctx context.Context, q jobqueue.Queue, s *FileStore) error {
	if s == nil || s.media == nil || s.media.alignment.pg == nil {
		return errors.New("alignment worker requires postgres")
	}
	a := s.media.alignment
	return jobqueue.Run(ctx, q, "alignment", func(ctx context.Context, q jobqueue.Queue, j jobqueue.Job) error {
		payload, err := projectJobPayload(j)
		if err != nil {
			return q.Finish(ctx, j, "failed", json.RawMessage(`{"code":"invalid_job"}`))
		}
		a.mu.Lock()
		task, exists := a.data.Tasks[j.ID]
		a.mu.Unlock()
		if !exists || task.SpaceID != j.TenantID || task.ProjectID != payload.ProjectID {
			return errors.New("queued alignment not found")
		}
		if err = q.Dispatch(ctx, j); err != nil {
			return err
		}
		runCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
		defer cancel()
		return a.runTask(runCtx, s.media, j.ID, &j)
	})
}
