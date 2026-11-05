package application

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/story"
	"github.com/adcwb/ai-creative-studio/backend/internal/persistence/storyrepo"
	"github.com/adcwb/ai-creative-studio/backend/internal/platform/jobqueue"
)

func (s *FileStore) acceptStoryCheck(ctx context.Context, a story.Actor, episode, key string, in story.CheckInput, model ModelRequest) (story.Check, error) {
	var out story.Check
	q, e := s.storySnapshotScope(ctx, a.Space, episode)
	if e != nil {
		return out, e
	}
	if replay, found, e := s.story.ReplayCheck(ctx, a, q.Series, episode, key, in); e != nil || found {
		return replay.Check, e
	}
	if model.selection == nil || !story.ValidCheckInput(in) || model.selection.GatewayID == "" || model.selection.FeatureKey != "story_continuity" || model.selection.TenantID != a.Space || model.selection.ActorID != a.User {
		return out, story.ErrDenied
	}
	snapshot, e := s.story.GetContext(ctx, q, episode, in.ContextID)
	if e != nil {
		return out, e
	}
	if snapshot.Script == nil || snapshot.Script.StructureVersion != 2 || len(snapshot.Context.Problems) > 0 {
		return out, story.ErrInput
	}
	fresh, e := s.CheckStoryContext(ctx, a.Space, episode, in.ContextID)
	if e != nil {
		return out, e
	}
	if !fresh.Current {
		return out, story.ErrConflict
	}
	now := time.Now().UTC()
	out = story.Check{ID: storyrepo.CheckID(a, q.Series, episode, key), Space: a.Space, SeriesID: q.Series, EpisodeID: episode, Input: in, State: "queued", Provider: model.selectedCaps.Provider, Model: model.selectedCaps.Model, Actor: a.User, CreatedAt: now, UpdatedAt: now}
	return s.story.CreateCheck(ctx, a, key, out, *model.selection, model.traceParent)
}
func (s *FileStore) executeStoryCheck(ctx context.Context, r storyrepo.CheckRecord, dispatch func(context.Context) error) (story.CheckResult, string, string) {
	var result story.CheckResult
	c := r.Check
	q := storyrepo.Scope{Space: c.Space, Series: c.SeriesID}
	sent := false
	known := false
	snapshot, e := s.story.GetContext(ctx, q, c.EpisodeID, c.Input.ContextID)
	if e != nil {
		return result, "failed", "story_context_unavailable"
	}
	raw, _ := json.Marshal(struct {
		Material []story.ContextItem
		Script   *Script
	}{snapshot.Context.Included, snapshot.Script})
	request := ExecutionRequest{ID: "story_check:" + c.ID, Tenant: c.Space, Provider: c.Provider, Model: c.Model, Capability: "text", Text: string(raw), Selection: &r.Selection}
	if len(snapshot.Context.Problems) > 0 || snapshot.Script == nil {
		return result, "failed", "invalid_story_context"
	}
	e = s.executeManaged(ctx, request, func(ctx context.Context, providers RuntimeProviders) error {
		if providers.StoryCheck == nil {
			return ErrExecutionGovernance
		}
		fresh, e := s.CheckStoryContext(ctx, c.Space, c.EpisodeID, c.Input.ContextID)
		if e != nil {
			return e
		}
		if !fresh.Current {
			return story.ErrConflict
		}
		if e = dispatch(ctx); e != nil {
			return e
		}
		sent = true
		output, e := providers.StoryCheck.CheckStory(ctx, snapshot)
		if e != nil {
			return e
		}
		known = true
		result, e = story.ValidateResult(c.ID, snapshot, output)
		if e != nil {
			return providerFailure("invalid_story_check_output")
		}
		text, _ := json.Marshal(result)
		request.Text = string(text)
		if e = s.execution.ReviewResult(ctx, request); e != nil {
			return e
		}
		return s.runtime.check(ctx, r.Selection)
	})
	if e == nil {
		return result, "succeeded", ""
	}
	if errors.Is(e, story.ErrConflict) {
		return story.CheckResult{}, "failed", "story_context_changed"
	}
	var p *ProviderError
	isProvider := errors.As(e, &p)
	if sent && !known && !RuntimeNoDispatch(e) && (!isProvider || p.Code == "provider_error" || p.Code == "provider_timeout") {
		return story.CheckResult{}, "unknown", "execution_outcome_unknown"
	}
	code, _ := managedGenerationFailure(e)
	if isProvider && p.Code == "invalid_story_check_output" {
		code = p.Code
	}
	return story.CheckResult{}, "failed", code
}
func RunQueuedStoryCheckWorker(ctx context.Context, q jobqueue.Queue, s *FileStore) error {
	if s == nil || s.story == nil {
		return errors.New("story worker dependencies required")
	}
	return jobqueue.Run(ctx, q, "story_check", func(ctx context.Context, q jobqueue.Queue, j jobqueue.Job) error {
		var payload storyrepo.CheckJob
		if strictCreativeJSON(j.Payload, &payload) != nil || payload.ID != j.ID {
			return q.Finish(ctx, j, "failed", json.RawMessage(`{"code":"invalid_job"}`))
		}
		scope := storyrepo.Scope{Space: j.TenantID, Series: payload.Series}
		r, e := s.story.GetCheck(ctx, scope, payload.Episode, j.ID)
		if e != nil {
			return e
		}
		// An expired lease before dispatch is safe to reclaim. A sticky dispatch is
		// never claimed again; the queue reconciler makes its domain result unknown.
		if r.Check.State == "queued" {
			r.Check, e = s.story.TransitionCheck(ctx, scope, payload.Episode, j.ID, "queued", "running", "", nil, &j)
			if e != nil {
				return e
			}
		} else if r.Check.State != "running" {
			return q.Finish(ctx, j, "failed", json.RawMessage(`{"code":"invalid_state"}`))
		}
		result, state, code := s.executeStoryCheck(ctx, r, func(ctx context.Context) error { return q.Dispatch(ctx, j) })
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var output *story.CheckResult
		if state == "succeeded" {
			output = &result
		}
		_, e = s.story.TransitionCheck(ctx, scope, payload.Episode, j.ID, "running", state, code, output, &j)
		return e
	})
}

type storyCheckRunner struct {
	cancel context.CancelFunc
	done   chan struct{}
	wake   chan struct{}
}

func (s *FileStore) startStoryChecks() {
	if s.pg != nil || s.story == nil || s.runtime == nil || s.execution == nil || !s.execution.Ready() {
		return
	}
	s.storyWorkerMu.Lock()
	defer s.storyWorkerMu.Unlock()
	if s.storyWorkerClosed || s.storyRunner != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	runner := &storyCheckRunner{cancel: cancel, done: make(chan struct{}), wake: make(chan struct{}, 1)}
	if e := s.story.RecoverFileChecks(ctx); e != nil {
		cancel()
		slog.Error("story check recovery unavailable")
		return
	}
	s.storyRunner = runner
	go func() {
		defer close(runner.done)
		var wg sync.WaitGroup
		for range 2 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for {
					if ctx.Err() != nil {
						return
					}
					r, ok, e := s.story.NextFileCheck(ctx)
					if e != nil {
						if ctx.Err() == nil {
							slog.Error("story check storage unavailable")
						}
						return
					}
					if ok {
						result, state, code := s.executeStoryCheck(ctx, r, func(context.Context) error { return nil })
						var output *story.CheckResult
						if state == "succeeded" {
							output = &result
						}
						finishCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
						_, e = s.story.TransitionCheck(finishCtx, storyrepo.Scope{Space: r.Check.Space, Series: r.Check.SeriesID}, r.Check.EpisodeID, r.Check.ID, "running", state, code, output, nil)
						cancel()
						if e != nil {
							slog.Error("story check result persistence unavailable")
							return
						}
						continue
					}
					select {
					case <-ctx.Done():
						return
					case <-runner.wake:
					case <-time.After(500 * time.Millisecond):
					}
				}
			}()
		}
		wg.Wait()
	}()
}
func (s *FileStore) wakeStoryChecks() {
	s.startStoryChecks()
	s.storyWorkerMu.Lock()
	defer s.storyWorkerMu.Unlock()
	if s.storyRunner != nil {
		select {
		case s.storyRunner.wake <- struct{}{}:
		default:
		}
	}
}
func (s *FileStore) stopStoryChecks() {
	s.storyWorkerMu.Lock()
	s.storyWorkerClosed = true
	r := s.storyRunner
	s.storyWorkerMu.Unlock()
	if r != nil {
		r.cancel()
		<-r.done
	}
}
