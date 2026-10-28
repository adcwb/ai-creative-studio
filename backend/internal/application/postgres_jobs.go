package application

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/adcwb/ai-creative-studio/backend/internal/platform/jobqueue"
)

func (s *FileStore) enqueueNewPostgres(d snapshot) error {
	if s.pg.err != nil {
		return s.pg.err
	}
	if s.pg.tx == nil {
		return errors.New("task mutation requires transaction")
	}
	enqueue := func(tenant, id, kind, key, project, provider, model string, selection *RuntimeSelection, traceParent string) error {
		if selection != nil {
			if err := ReservePostgresTx(s.pg.ctx, s.pg.tx, kind+":"+id, tenant, provider, model, selection.Currency, selection.MaximumMinor); err != nil {
				return err
			}
		}
		b, _ := json.Marshal(map[string]string{"task_id": id, "project_id": project})
		return jobqueue.EnqueueTx(s.pg.ctx, s.pg.tx, jobqueue.Job{TenantID: tenant, ID: id, Kind: kind, IdempotencyKey: key, Payload: b, TraceParent: traceParent})
	}
	for id, t := range d.Tasks {
		if _, ok := s.data.Tasks[id]; !ok && t.Status == "queued" {
			if err := enqueue(t.SpaceID, t.ID, "image", t.Key, "", t.Provider, t.Model, t.RuntimeSelection, t.TraceParent); err != nil {
				return err
			}
		}
	}
	for id, t := range d.ScriptGenerations {
		if _, ok := s.data.ScriptGenerations[id]; !ok && t.Status == "queued" {
			if err := enqueue(t.SpaceID, t.ID, "script", t.ID, t.ProjectID, t.Provider, t.Model, t.RuntimeSelection, t.TraceParent); err != nil {
				return err
			}
		}
	}
	for id, t := range d.StoryboardGenerations {
		if _, ok := s.data.StoryboardGenerations[id]; !ok && t.Status == "queued" {
			if err := enqueue(t.SpaceID, t.ID, "storyboard", t.ID, t.ProjectID, t.Provider, t.Model, t.RuntimeSelection, t.TraceParent); err != nil {
				return err
			}
		}
	}
	return nil
}
func (s *FileStore) queueFence(j jobqueue.Job, runErr error) error {
	if s.pg == nil || s.pg.tx == nil || s.pg.err != nil {
		return errors.New("postgres transaction unavailable")
	}
	if err := jobqueue.CheckLeaseTx(s.pg.ctx, s.pg.tx, j); err != nil {
		return err
	}
	state := "succeeded"
	if runErr != nil {
		state = "failed"
	}
	s.pg.beforeCommit = func(ctx context.Context, tx *sql.Tx) error {
		table := map[string]string{"image": "studio_image_tasks", "image_resume": "studio_image_tasks", "script": "studio_script_generations", "storyboard": "studio_storyboard_generations"}[j.Kind]
		if table == "" {
			return ErrConflict
		}
		var domainState string
		if err := tx.QueryRowContext(ctx, `SELECT state FROM `+table+` WHERE tenant_id=$1 AND entity_id=$2`, j.TenantID, j.ID).Scan(&domainState); err != nil {
			return err
		}
		if domainState == "failed" {
			state = "failed"
		}
		return jobqueue.FinishTx(ctx, tx, j, state, json.RawMessage(`{}`))
	}
	return nil
}
func (s *FileStore) FinishQueued(ctx context.Context, j jobqueue.Job, result GenerationResult, runErr error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.queueFence(j, runErr); err != nil {
		return err
	}
	defer func() { s.pg.beforeCommit = nil }()
	if j.Kind != "image" && j.Kind != "image_resume" {
		return ErrConflict
	}
	t, ok := s.data.Tasks[j.ID]
	if !ok || t.SpaceID != j.TenantID {
		return ErrNotFound
	}
	return s.finishLocked(t, result, runErr)
}
func (s *FileStore) FinishScriptQueued(ctx context.Context, j jobqueue.Job, script Script, runErr error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.queueFence(j, runErr); err != nil {
		return err
	}
	defer func() { s.pg.beforeCommit = nil }()
	if j.Kind != "script" {
		return ErrConflict
	}
	t, ok := s.data.ScriptGenerations[j.ID]
	if !ok || t.SpaceID != j.TenantID {
		return ErrNotFound
	}
	return s.finishScriptLocked(t, script, runErr)
}
func (s *FileStore) FinishStoryboardQueued(ctx context.Context, j jobqueue.Job, shots []StoryboardShot, runErr error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.queueFence(j, runErr); err != nil {
		return err
	}
	defer func() { s.pg.beforeCommit = nil }()
	if j.Kind != "storyboard" {
		return ErrConflict
	}
	t, ok := s.data.StoryboardGenerations[j.ID]
	if !ok || t.SpaceID != j.TenantID {
		return ErrNotFound
	}
	return s.finishStoryboardLocked(t, shots, runErr)
}
