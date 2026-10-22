package studio

import (
	"context"
	"crypto/sha256"
	"fmt"
	"html"
	"strings"
	"time"

	"github.com/cloudwego/eino/compose"
)

// ImageProvider can later adapt a real model without changing the HTTP layer.
type ImageProvider interface {
	Generate(context.Context, Input) (string, error)
}
type MockProvider struct{ Delay time.Duration }

func (p MockProvider) Generate(ctx context.Context, in Input) (string, error) {
	timer := time.NewTimer(p.Delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-timer.C:
	}
	if strings.Contains(in.Prompt, "[mock-fail]") {
		return "", fmt.Errorf("requested mock failure")
	}
	hash := sha256.Sum256([]byte(in.Prompt))
	color := fmt.Sprintf("#%02x%02x%02x", hash[0]/2+40, hash[1]/2+40, hash[2]/2+40)
	title := []rune(in.Prompt)
	if len(title) > 24 {
		title = title[:24]
	}
	return fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" width="800" height="1000" viewBox="0 0 800 1000"><rect width="800" height="1000" fill="%s"/><circle cx="620" cy="260" r="260" fill="#ffffff" opacity=".12"/><path d="M0 780L800 380V1000H0Z" fill="#ffffff" opacity=".08"/><text x="64" y="120" fill="white" font-family="sans-serif" font-size="24">CREATIVE STUDIO / MOCK</text><text x="64" y="500" fill="white" font-family="sans-serif" font-size="38">模拟图像 · 非 AI 生成</text><text x="64" y="570" fill="white" font-family="sans-serif" font-size="22">%s</text><text x="64" y="880" fill="white" font-family="sans-serif" font-size="20">SVG 流程演示 / %s</text></svg>`, color, html.EscapeString(string(title)), html.EscapeString(in.Kind)), nil
}

type Pipeline struct {
	run compose.Runnable[Input, string]
}

func NewPipeline(ctx context.Context, provider ImageProvider) (*Pipeline, error) {
	chain := compose.NewChain[Input, string]().AppendLambda(compose.InvokableLambda(func(_ context.Context, in Input) (Input, error) { return Normalize(in) })).AppendLambda(compose.InvokableLambda(provider.Generate))
	run, err := chain.Compile(ctx)
	if err != nil {
		return nil, err
	}
	return &Pipeline{run: run}, nil
}
func (p *Pipeline) Invoke(ctx context.Context, in Input) (string, error) {
	return p.run.Invoke(ctx, in)
}

// RunWorker polls durable queued records; fatal persistence errors stop the process.
func RunWorker(ctx context.Context, repo Repository, p *Pipeline) error {
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
		}
		t, ok, err := repo.Next()
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		svg, runErr := p.Invoke(ctx, t.Input)
		if ctx.Err() != nil {
			return nil
		}
		if err = repo.Finish(t, svg, runErr); err != nil {
			return err
		}
	}
}
