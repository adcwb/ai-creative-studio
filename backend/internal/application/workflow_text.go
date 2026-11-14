package application

import "context"

type workflowTextProvider interface {
	TransformCreativeText(context.Context, string, string) (string, error)
}

// TransformCreativeText uses the provider's existing guarded JSON text protocol,
// including native input counting, refusal detection and bounded output parsing.
func (p *OpenAICreativeProvider) TransformCreativeText(ctx context.Context, instruction, input string) (string, error) {
	var result struct {
		Text string `json:"text"`
	}
	err := p.json(ctx, struct {
		Instruction string `json:"instruction"`
		Input       string `json:"input"`
	}{instruction, input}, `Transform the supplied creative text according to its instruction. Return {"text":string}. Return only the transformed text in that field; do not claim to generate media or publish.`, &result)
	if err != nil {
		return "", err
	}
	text, err := normalizeProjectText(result.Text, "text", 1, 2000)
	if err != nil {
		return "", providerFailure("invalid_provider_response")
	}
	return text, nil
}

func workflowCreativeKind(kind string) string {
	if kind == "llm" {
		return "text"
	}
	return kind
}

func (p runtimeCreative) TransformCreativeText(ctx context.Context, instruction, input string) (string, error) {
	if p.capability != "text" {
		return "", errRuntimeDenied
	}
	adapter, ok := p.p.(workflowTextProvider)
	if !ok {
		return "", providerFailure("unsupported_capability")
	}
	return adapter.TransformCreativeText(ctx, instruction, input)
}
