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
	return renderMockSVG(withDefaults(in)), nil
}

type mockPalette struct {
	background string
	foreground string
	accent     string
	secondary  string
	label      string
}

func renderMockSVG(in Input) string {
	width, height := 800, 1000
	switch in.Ratio {
	case "1:1":
		height = 800
	case "16:9":
		width, height = 1600, 900
	}
	palette := mockPalette{"#eeeadd", "#293d36", "#71917d", "#cfad77", "自然"}
	switch in.Style {
	case "studio":
		palette = mockPalette{"#182333", "#f3f6ed", "#bde088", "#607899", "棚拍"}
	case "illustration":
		palette = mockPalette{"#eee4f5", "#392b58", "#fa985e", "#9c86c9", "插画"}
	}
	hash := sha256.Sum256([]byte(in.Prompt))
	if hash[0]%2 == 1 {
		palette.accent, palette.secondary = palette.secondary, palette.accent
	}
	margin := width * 8 / 100
	artHeight := height - 340
	title := []rune(in.Prompt)
	if len(title) > 24 {
		title = append(title[:23], '…')
	}
	kindLabel := map[string]string{"poster": "创意海报", "portrait": "肖像概念", "general": "通用创作"}[in.Kind]
	var svg strings.Builder
	fmt.Fprintf(&svg, `<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d" data-kind="%s" data-ratio="%s" data-style="%s"><title>%s · %s · %s</title><desc>模拟图像，非 AI 生成。%s</desc><rect width="100%%" height="100%%" fill="%s"/><g fill="%s" font-family="Arial, sans-serif"><text x="%d" y="64" font-size="20" letter-spacing="3">CREATIVE STUDIO / MOCK</text><text x="%d" y="100" font-size="18">%s / %s / %s</text></g>`, width, height, width, height, html.EscapeString(in.Kind), html.EscapeString(in.Ratio), html.EscapeString(in.Style), kindLabel, palette.label, html.EscapeString(in.Ratio), html.EscapeString(in.Prompt), palette.background, palette.foreground, margin, margin, kindLabel, palette.label, html.EscapeString(in.Ratio))
	fmt.Fprintf(&svg, `<svg x="%d" y="136" width="%d" height="%d" viewBox="0 0 640 500" preserveAspectRatio="xMidYMid meet" data-composition="%s">`, margin, width-2*margin, artHeight, html.EscapeString(in.Kind))
	switch in.Kind {
	case "portrait":
		fmt.Fprintf(&svg, `<path d="M110 490V215a210 210 0 0 1 420 0v275Z" fill="%s"/><ellipse cx="320" cy="482" rx="225" ry="125" fill="%s"/><path d="M188 490v-45a132 132 0 0 1 264 0v45Z" fill="%s"/><rect x="281" y="279" width="78" height="99" rx="32" fill="%s"/><ellipse cx="320" cy="215" rx="103" ry="132" fill="%s"/><path d="M216 225c-30-165 221-211 209-12-66-14-121-48-147-89-1 42-19 79-62 101Z" fill="%s"/><circle cx="281" cy="228" r="6" fill="%s"/><circle cx="359" cy="228" r="6" fill="%s"/>`, palette.secondary, palette.foreground, palette.accent, palette.secondary, palette.background, palette.foreground, palette.foreground, palette.foreground)
	case "general":
		fmt.Fprintf(&svg, `<rect x="30" y="40" width="580" height="420" rx="50" fill="%s"/><circle cx="220" cy="230" r="150" fill="%s"/><path d="M320 420 470 80 610 420Z" fill="%s"/><circle cx="452" cy="173" r="70" fill="%s"/><path d="M66 402C180 292 273 468 391 355" fill="none" stroke="%s" stroke-width="22" stroke-linecap="round"/>`, palette.secondary, palette.accent, palette.foreground, palette.background, palette.background)
	default:
		fmt.Fprintf(&svg, `<rect x="30" y="30" width="330" height="440" rx="165" fill="%s"/><circle cx="195" cy="195" r="112" fill="%s"/><path d="M30 340h330v130H30Z" fill="%s"/><rect x="394" y="30" width="216" height="118" rx="14" fill="%s"/><path d="M502 184 610 292 502 400 394 292Z" fill="%s"/><path d="M394 444h216" stroke="%s" stroke-width="24"/>`, palette.accent, palette.background, palette.secondary, palette.foreground, palette.secondary, palette.foreground)
	}
	// Style changes both the palette and the graphic treatment of the scene.
	switch in.Style {
	case "studio":
		fmt.Fprintf(&svg, `<path d="M30 30h90M30 30v90M610 30h-90M610 30v90M30 470h90M30 470v-90M610 470h-90M610 470v-90" fill="none" stroke="%s" stroke-width="3"/><circle cx="570" cy="70" r="12" fill="%s"/>`, palette.foreground, palette.accent)
	case "illustration":
		fmt.Fprintf(&svg, `<path d="m45 18 9 24 24 9-24 9-9 24-9-24L12 51l24-9Zm535 370 12 31 31 12-31 12-12 31-12-31-31-12 31-12Z" fill="%s" stroke="%s" stroke-width="3"/><path d="m534 117 28-18m-18 49 33-2" stroke="%s" stroke-width="7" stroke-linecap="round"/>`, palette.accent, palette.foreground, palette.foreground)
	default:
		fmt.Fprintf(&svg, `<path d="M565 454c-62-44-66-107-35-144 45 43 56 99 35 144Zm0 0c-2-64 27-102 67-108 8 58-19 99-67 108Z" fill="%s" opacity=".8"/>`, palette.accent)
	}
	fmt.Fprintf(&svg, `</svg><g fill="%s" font-family="Arial, sans-serif"><text x="%d" y="%d" font-size="28">%s</text><text x="%d" y="%d" font-size="20">模拟图像 · 非 AI 生成</text><path d="M%d %dH%d" stroke="%s" opacity=".35"/><text x="%d" y="%d" font-size="16">%s / %s / %s · SVG 流程演示</text></g></svg>`, palette.foreground, margin, height-150, html.EscapeString(string(title)), margin, height-105, margin, height-76, width-margin, palette.foreground, margin, height-42, html.EscapeString(in.Kind), html.EscapeString(in.Ratio), html.EscapeString(in.Style))
	return svg.String()
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
