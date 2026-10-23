package studio

import (
	"context"
	"errors"
	"fmt"
	"github.com/cloudwego/eino/compose"
	"strings"
	"time"
)

type StoryboardProvider interface {
	Generate(context.Context, Script) ([]StoryboardShot, error)
}
type StoryboardError struct{ Code string }

func (e *StoryboardError) Error() string {
	if e.Code == "invalid_storyboard_output" {
		return "模拟分镜结构无效，已保存的计划仍保留。"
	}
	return "模拟分镜生成失败；请移除剧本动作中的 [mock-fail] 并保存后重试，已保存的计划仍保留。"
}
func safeStoryboardFailure(err error) (string, string) {
	var pe *ProviderError
	if errors.As(err, &pe) {
		return creativeFailure(err)
	}
	code := "mock_storyboard_failed"
	var e *StoryboardError
	if errors.As(err, &e) && e.Code == "invalid_storyboard_output" {
		code = e.Code
	}
	return code, (&StoryboardError{Code: code}).Error()
}

type MockStoryboardProvider struct{ Delay time.Duration }

func (p MockStoryboardProvider) Generate(ctx context.Context, script Script) ([]StoryboardShot, error) {
	timer := time.NewTimer(p.Delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
	}
	shots := []StoryboardShot{}
	for i, scene := range script.Scenes {
		if strings.Contains(scene.Action, "[mock-fail]") {
			return nil, &StoryboardError{Code: "mock_storyboard_failed"}
		}
		lines := []string{}
		for _, line := range scene.Dialogue {
			lines = append(lines, line.Character+"："+line.Text)
		}
		shots = append(shots, StoryboardShot{ID: fmt.Sprintf("scene-%d-shot-1", i+1), SceneIndex: i + 1, Description: scene.Action, ShotSize: "wide", CameraMovement: "static", DurationSeconds: 8, Dialogue: shortScriptText(strings.Join(lines, "\n"), 2000)})
	}
	return shots, nil
}

type StoryboardPipeline struct {
	provider string
	model    string
	run      compose.Runnable[Script, []StoryboardShot]
}

func NewStoryboardPipeline(ctx context.Context, provider StoryboardProvider) (*StoryboardPipeline, error) {
	chain := compose.NewChain[Script, []StoryboardShot]().AppendLambda(compose.InvokableLambda(func(_ context.Context, in Script) (Script, error) { return NormalizeScript(in) })).AppendLambda(compose.InvokableLambda(provider.Generate)).AppendLambda(compose.InvokableLambda(func(_ context.Context, shots []StoryboardShot) ([]StoryboardShot, error) {
		out, err := NormalizeStoryboardShots(shots)
		if err != nil {
			return nil, &StoryboardError{Code: "invalid_storyboard_output"}
		}
		return out, nil
	}))
	run, err := chain.Compile(ctx)
	if err != nil {
		return nil, err
	}
	name := "mock-storyboard"
	model := ""
	if a, ok := provider.(creativeStoryboardAdapter); ok {
		name = creativeProviderName(a.p)
		model = creativeProviderModel(a.p)
	}
	return &StoryboardPipeline{run: run, provider: name, model: model}, nil
}
func (p *StoryboardPipeline) Invoke(ctx context.Context, in Script) ([]StoryboardShot, error) {
	return p.run.Invoke(ctx, in)
}
func RunStoryboardWorker(ctx context.Context, repo StoryboardRepository, pipeline *StoryboardPipeline) error {
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
		}
		g, ok, err := repo.NextStoryboardGeneration()
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		var shots []StoryboardShot
		var runErr error
		if g.Provider != pipeline.provider || (g.Provider != "mock-storyboard" && (g.Model == "" || g.Model != pipeline.model)) {
			runErr = providerFailure("provider_unavailable")
		} else {
			shots, runErr = pipeline.Invoke(ctx, *g.SourceScript)
		}
		if ctx.Err() != nil {
			return nil
		}
		if err := repo.FinishStoryboardGeneration(g, shots, runErr); err != nil {
			return err
		}
	}
}
