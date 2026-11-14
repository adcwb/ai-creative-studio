package application

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/series"
	"github.com/adcwb/ai-creative-studio/backend/internal/modules/story"
)

type writingPayloadKey struct{}
type scriptWritingPayload struct {
	Project   ProjectInput        `json:"project"`
	EpisodeID string              `json:"episode_id"`
	Script    *Script             `json:"current_script"`
	Material  []story.ContextItem `json:"material"`
}

func writingPayload(ctx context.Context, in ProjectInput) any {
	if payload, ok := ctx.Value(writingPayloadKey{}).(scriptWritingPayload); ok {
		payload.Project = in
		return payload
	}
	return in
}
func (s *FileStore) writingSnapshot(ctx context.Context, space, episode, id string) (story.ContextSnapshot, error) {
	q, e := s.storySnapshotScope(ctx, space, episode)
	if e != nil {
		return story.ContextSnapshot{}, writingInputError(e)
	}
	v, e := s.story.GetContext(ctx, q, episode, id)
	return v, writingInputError(e)
}
func writingInputError(e error) error {
	if errors.Is(e, story.ErrNotFound) || errors.Is(e, series.ErrNotFound) {
		return ErrNotFound
	}
	if errors.Is(e, story.ErrConflict) || errors.Is(e, series.ErrConflict) {
		return ErrVersionConflict
	}
	if errors.Is(e, story.ErrInput) || errors.Is(e, series.ErrInput) {
		return projectInputError("写作资料来源或范围无效")
	}
	return e
}
func writingProblems(snapshot story.ContextSnapshot) bool {
	for _, p := range snapshot.Context.Problems {
		if p.Code != "script_required" {
			return true
		}
	}
	return false
}
func (s *FileStore) prepareWritingContext(space, id string, in ScriptGenerationInput) (story.ContextSnapshot, error) {
	if !story.ID.MatchString(in.ContextID) || in.selection == nil || in.selection.GatewayID == "" || in.selection.FeatureKey != "script_generation" {
		return story.ContextSnapshot{}, projectInputError("正式故事上下文需要选择可用的网关文本模型")
	}
	ctx := context.Background()
	snapshot, e := s.writingSnapshot(ctx, space, id, in.ContextID)
	if e != nil {
		return snapshot, e
	}
	if snapshot.ProjectVersion != in.ExpectedVersion || writingProblems(snapshot) {
		return snapshot, projectInputError("本集资料存在待处理项，请核对正式资料后重新准备写作上下文")
	}
	fresh, e := s.CheckStoryContext(ctx, space, id, in.ContextID)
	if e != nil {
		return snapshot, e
	}
	if !fresh.Current {
		return snapshot, ErrVersionConflict
	}
	return snapshot, nil
}
func (s *FileStore) managedScript(ctx context.Context, g ScriptGeneration, dispatch func(context.Context) error) (Script, error) {
	var out Script
	text := g.SourceTitle + "\n" + g.SourceBrief
	if g.ContextID != "" {
		snapshot, e := s.writingSnapshot(ctx, g.SpaceID, g.ProjectID, g.ContextID)
		if e != nil {
			return out, e
		}
		if snapshot.SHA256 != g.ContextSHA256 || writingProblems(snapshot) {
			return out, providerFailure("writing_context_changed")
		}
		payload := scriptWritingPayload{Project: generationProjectInput(g), EpisodeID: g.SourceEpisodeID, Script: snapshot.Script, Material: snapshot.Context.Included}
		raw, e := json.Marshal(payload)
		if e != nil {
			return out, e
		}
		text = string(raw)
		ctx = context.WithValue(ctx, writingPayloadKey{}, payload)
		ctx = context.WithValue(ctx, textGenerationGuardKey{}, func(c context.Context) error {
			fresh, e := s.CheckStoryContext(c, g.SpaceID, g.ProjectID, g.ContextID)
			if e != nil {
				return e
			}
			if !fresh.Current {
				return providerFailure("writing_context_changed")
			}
			return nil
		})
	}
	request := ExecutionRequest{ID: "script:" + g.ID, Tenant: g.SpaceID, Provider: g.Provider, Model: g.Model, Capability: "text", Text: text, Selection: g.RuntimeSelection}
	e := s.executeManaged(ctx, request, func(c context.Context, providers RuntimeProviders) error {
		if providers.Creative == nil {
			return ErrExecutionGovernance
		}
		if e := checkTextGenerationSource(c); e != nil {
			return e
		}
		if dispatch != nil {
			if e := dispatch(c); e != nil {
				return e
			}
		}
		pipeline, e := NewScriptPipeline(c, CreativeScriptProvider(providers.Creative))
		if e != nil {
			return e
		}
		out, e = pipeline.Invoke(c, generationProjectInput(g))
		if e != nil {
			return e
		}
		raw, _ := json.Marshal(out)
		request.Text = string(raw)
		return s.execution.ReviewResult(c, request)
	})
	return out, e
}
