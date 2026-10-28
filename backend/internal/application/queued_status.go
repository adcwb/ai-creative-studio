package application

import (
	"context"
	"database/sql"
	"time"

	"github.com/adcwb/ai-creative-studio/backend/internal/platform/jobqueue"
)

// A lease reclaimed before dispatch can safely retain running UI state; the
// queue token, not the cached domain status, determines execution ownership.
func (s *FileStore) markQueuedRunning(ctx context.Context, j jobqueue.Job) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pg == nil {
		return ErrConflict
	}
	d := s.clone()
	now := time.Now().UTC()
	switch j.Kind {
	case "image", "image_resume":
		t, ok := d.Tasks[j.ID]
		if !ok || t.SpaceID != j.TenantID || (t.Status != "queued" && t.Status != "running") {
			return ErrConflict
		}
		t.Status = "running"
		t.Progress = 35
		t.UpdatedAt = now
		d.Tasks[j.ID] = t
	case "script":
		t, ok := d.ScriptGenerations[j.ID]
		if !ok || t.SpaceID != j.TenantID || (t.Status != "queued" && t.Status != "running") {
			return ErrConflict
		}
		t.Status = "running"
		t.Progress = 35
		t.UpdatedAt = now
		d.ScriptGenerations[j.ID] = t
	case "storyboard":
		t, ok := d.StoryboardGenerations[j.ID]
		if !ok || t.SpaceID != j.TenantID || (t.Status != "queued" && t.Status != "running") {
			return ErrConflict
		}
		t.Status = "running"
		t.Progress = 35
		t.UpdatedAt = now
		d.StoryboardGenerations[j.ID] = t
	default:
		return ErrConflict
	}
	s.pg.beforeCommit = func(ctx context.Context, tx *sql.Tx) error { return jobqueue.CheckLeaseTx(ctx, tx, j) }
	defer func() { s.pg.beforeCommit = nil }()
	return s.commit(d)
}
