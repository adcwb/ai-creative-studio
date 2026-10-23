package studio

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/cloudwego/eino/compose"
)

type ScriptProvider interface {
	Generate(context.Context, ProjectInput) (Script, error)
}

type ScriptError struct{ Code string }

func (e *ScriptError) Error() string {
	if e.Code == "invalid_script_output" {
		return "模拟剧本结构无效，未替换已保存的剧本。"
	}
	return "模拟剧本生成失败；请移除 [mock-fail] 后保存简述并重新生成，已保存的剧本仍保留。"
}

func safeScriptFailure(err error) (string, string) {
	var pe *ProviderError
	if errors.As(err, &pe) {
		return creativeFailure(err)
	}
	code := "mock_script_failed"
	var scriptError *ScriptError
	if errors.As(err, &scriptError) && scriptError.Code == "invalid_script_output" {
		code = "invalid_script_output"
	}
	return code, (&ScriptError{Code: code}).Error()
}

type MockScriptProvider struct{ Delay time.Duration }

func shortScriptText(value string, maximum int) string {
	runes := []rune(value)
	if len(runes) > maximum {
		return string(runes[:maximum-1]) + "…"
	}
	return value
}

func (p MockScriptProvider) Generate(ctx context.Context, in ProjectInput) (Script, error) {
	timer := time.NewTimer(p.Delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return Script{}, ctx.Err()
	case <-timer.C:
	}
	if strings.Contains(in.Brief, "[mock-fail]") {
		return Script{}, &ScriptError{Code: "mock_script_failed"}
	}
	brief := shortScriptText(in.Brief, 180)
	theme := shortScriptText(in.Brief, 80)
	return Script{
		Title:   in.Title,
		Logline: "围绕“" + brief + "”，两位角色从不同立场出发，在一次选择中找到共同方向。（程序生成的模拟剧本）",
		Characters: []ScriptCharacter{
			{Name: "林青", Description: "行动的发起者，希望完成“" + theme + "”，需要学会倾听。"},
			{Name: "阿远", Description: "谨慎的同行者，对“" + theme + "”有不同看法，愿意在关键时刻伸出援手。"},
		},
		Scenes: []ScriptScene{
			{Heading: "第一场 · 相遇与目标", Action: "清晨，两人在约定的地点碰面。故事从“" + brief + "”展开。林青展示计划，阿远提出一个被忽略的问题。", Dialogue: []ScriptDialogue{{Character: "林青", Text: "关于“" + theme + "”，我想今天就迈出第一步。"}, {Character: "阿远", Text: "先告诉我，我们最不能失去的是什么？"}}},
			{Heading: "第二场 · 分歧与选择", Action: "推进“" + brief + "”的过程中出现意外。原来的计划不再奏效，两人必须在坚持和调整之间做出选择。", Dialogue: []ScriptDialogue{{Character: "林青", Text: "我以为只要坚持原计划就足够了。"}, {Character: "阿远", Text: "目标可以不变，但我们可以一起换一种办法。"}}},
			{Heading: "第三场 · 行动与回应", Action: "两人重新分工，为“" + brief + "”找到一个可以立即执行的小步骤。事情尚未全部解决，但他们已经走向同一个方向。", Dialogue: []ScriptDialogue{{Character: "林青", Text: "这次，我们一起决定下一步。"}, {Character: "阿远", Text: "走吧，先把眼前这一件事做好。"}}},
		},
	}, nil
}

type ScriptPipeline struct {
	provider string
	model    string
	run      compose.Runnable[ProjectInput, Script]
}

func NewScriptPipeline(ctx context.Context, provider ScriptProvider) (*ScriptPipeline, error) {
	chain := compose.NewChain[ProjectInput, Script]().
		AppendLambda(compose.InvokableLambda(func(_ context.Context, in ProjectInput) (ProjectInput, error) { return NormalizeProjectInput(in) })).
		AppendLambda(compose.InvokableLambda(provider.Generate)).
		AppendLambda(compose.InvokableLambda(func(_ context.Context, script Script) (Script, error) {
			normalized, err := NormalizeScript(script)
			if err != nil {
				return Script{}, &ScriptError{Code: "invalid_script_output"}
			}
			return normalized, nil
		}))
	run, err := chain.Compile(ctx)
	if err != nil {
		return nil, err
	}
	name := "mock-script"
	model := ""
	if a, ok := provider.(creativeScriptAdapter); ok {
		name = creativeProviderName(a.p)
		model = creativeProviderModel(a.p)
	}
	return &ScriptPipeline{run: run, provider: name, model: model}, nil
}

func (p *ScriptPipeline) Invoke(ctx context.Context, in ProjectInput) (Script, error) {
	return p.run.Invoke(ctx, in)
}

func RunScriptWorker(ctx context.Context, repo ProjectRepository, pipeline *ScriptPipeline) error {
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
		}
		generation, ok, err := repo.NextScriptGeneration()
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		var script Script
		var runErr error
		if generation.Provider != pipeline.provider || (generation.Provider != "mock-script" && (generation.Model == "" || generation.Model != pipeline.model)) {
			runErr = providerFailure("provider_unavailable")
		} else {
			script, runErr = pipeline.Invoke(ctx, ProjectInput{Title: generation.SourceTitle, Brief: generation.SourceBrief})
		}
		if ctx.Err() != nil {
			return nil
		}
		if err := repo.FinishScriptGeneration(generation, script, runErr); err != nil {
			return err
		}
	}
}
