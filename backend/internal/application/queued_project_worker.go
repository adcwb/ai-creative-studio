package application

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/adcwb/ai-creative-studio/backend/internal/platform/jobqueue"
)

type QueuedScriptRepository interface {
	ListScriptGenerations(string, string) ([]ScriptGeneration, error)
	FinishScriptQueued(context.Context, jobqueue.Job, Script, error) error
}
type QueuedStoryboardRepository interface {
	ListStoryboardGenerations(string, string) ([]StoryboardGeneration, error)
	FinishStoryboardQueued(context.Context, jobqueue.Job, []StoryboardShot, error) error
}
type queuedProjectPayload struct {
	TaskID    string `json:"task_id"`
	ProjectID string `json:"project_id"`
}

func projectJobPayload(j jobqueue.Job) (queuedProjectPayload, error) {
	var p queuedProjectPayload
	if json.Unmarshal(j.Payload, &p) != nil || p.TaskID != j.ID || p.ProjectID == "" {
		return p, errors.New("invalid project job")
	}
	return p, nil
}
func RunQueuedScriptWorker(ctx context.Context, q jobqueue.Queue, repo QueuedScriptRepository, p *ScriptPipeline) error {
	if repo == nil || p == nil {
		return errors.New("queued script dependencies required")
	}
	return jobqueue.Run(ctx, q, "script", func(ctx context.Context, q jobqueue.Queue, j jobqueue.Job) error {
		payload, e := projectJobPayload(j)
		if e != nil {
			return q.Finish(ctx, j, "failed", json.RawMessage(`{"code":"invalid_job"}`))
		}
		items, e := repo.ListScriptGenerations(j.TenantID, payload.ProjectID)
		if e != nil {
			return e
		}
		var g *ScriptGeneration
		for i := range items {
			if items[i].ID == j.ID && items[i].SpaceID == j.TenantID {
				g = &items[i]
				break
			}
		}
		if g == nil {
			return errors.New("queued script not found")
		}
		if store, ok := repo.(*FileStore); ok {
			if err := store.markQueuedRunning(ctx, j); err != nil {
				return err
			}
		}
		if e = q.Dispatch(ctx, j); e != nil {
			return e
		}
		var result Script
		var runErr error
		if g.RuntimeSelection != nil {
			store, ok := repo.(*FileStore)
			if !ok {
				runErr = ErrExecutionGovernance
			} else {
				runErr = store.executeManaged(ctx, ExecutionRequest{ID: "script:" + g.ID, Tenant: g.SpaceID, Provider: g.Provider, Model: g.Model, Capability: "text", Text: g.SourceTitle + "\n" + g.SourceBrief, Selection: g.RuntimeSelection}, func(ctx context.Context, providers RuntimeProviders) error {
					if providers.Creative == nil {
						return ErrExecutionGovernance
					}
					pipeline, e := NewScriptPipeline(ctx, CreativeScriptProvider(providers.Creative))
					if e != nil {
						return e
					}
					result, e = pipeline.Invoke(ctx, ProjectInput{Title: g.SourceTitle, Brief: g.SourceBrief})
					if e == nil {
						raw, _ := json.Marshal(result)
						e = store.execution.ReviewResult(ctx, ExecutionRequest{ID: g.ID, Selection: g.RuntimeSelection, Tenant: g.SpaceID, Provider: g.Provider, Model: g.Model, Capability: "text", Text: string(raw)})
					}
					return e
				})
			}
		} else if p.provider != "mock-script" || g.Provider != p.provider {
			runErr = providerFailure("provider_unavailable")
		} else {
			result, runErr = p.Invoke(ctx, ProjectInput{Title: g.SourceTitle, Brief: g.SourceBrief})
		}

		if ctx.Err() != nil {
			return ctx.Err()
		}
		return repo.FinishScriptQueued(ctx, j, result, runErr)
	})
}
func RunQueuedStoryboardWorker(ctx context.Context, q jobqueue.Queue, repo QueuedStoryboardRepository, p *StoryboardPipeline) error {
	if repo == nil || p == nil {
		return errors.New("queued storyboard dependencies required")
	}
	return jobqueue.Run(ctx, q, "storyboard", func(ctx context.Context, q jobqueue.Queue, j jobqueue.Job) error {
		payload, e := projectJobPayload(j)
		if e != nil {
			return q.Finish(ctx, j, "failed", json.RawMessage(`{"code":"invalid_job"}`))
		}
		items, e := repo.ListStoryboardGenerations(j.TenantID, payload.ProjectID)
		if e != nil {
			return e
		}
		var g *StoryboardGeneration
		for i := range items {
			if items[i].ID == j.ID && items[i].SpaceID == j.TenantID {
				g = &items[i]
				break
			}
		}
		if g == nil {
			return errors.New("queued storyboard not found")
		}
		if store, ok := repo.(*FileStore); ok {
			store.mu.Lock()
			full, exists := store.data.StoryboardGenerations[j.ID]
			if exists && full.SpaceID == j.TenantID {
				g = &full
			}
			store.mu.Unlock()
		}
		if store, ok := repo.(*FileStore); ok {
			if err := store.markQueuedRunning(ctx, j); err != nil {
				return err
			}
		}
		if e = q.Dispatch(ctx, j); e != nil {
			return e
		}
		var result []StoryboardShot
		var runErr error
		if g.SourceScript == nil {
			runErr = providerFailure("provider_unavailable")
		} else if g.RuntimeSelection != nil {
			store, ok := repo.(*FileStore)
			if !ok {
				runErr = ErrExecutionGovernance
			} else {
				raw, _ := json.Marshal(g.SourceScript)
				runErr = store.executeManaged(ctx, ExecutionRequest{ID: "storyboard:" + g.ID, Tenant: g.SpaceID, Provider: g.Provider, Model: g.Model, Capability: "text", Text: string(raw), Selection: g.RuntimeSelection}, func(ctx context.Context, providers RuntimeProviders) error {
					if providers.Creative == nil {
						return ErrExecutionGovernance
					}
					pipeline, e := NewStoryboardPipeline(ctx, CreativeStoryboardProvider(providers.Creative))
					if e != nil {
						return e
					}
					result, e = pipeline.Invoke(ctx, *g.SourceScript)
					if e == nil {
						raw, _ := json.Marshal(result)
						e = store.execution.ReviewResult(ctx, ExecutionRequest{ID: g.ID, Selection: g.RuntimeSelection, Tenant: g.SpaceID, Provider: g.Provider, Model: g.Model, Capability: "text", Text: string(raw)})
					}
					return e
				})
			}
		} else if p.provider != "mock-storyboard" || g.Provider != p.provider {
			runErr = providerFailure("provider_unavailable")
		} else {
			result, runErr = p.Invoke(ctx, *g.SourceScript)
		}

		if ctx.Err() != nil {
			return ctx.Err()
		}
		return repo.FinishStoryboardQueued(ctx, j, result, runErr)
	})
}
