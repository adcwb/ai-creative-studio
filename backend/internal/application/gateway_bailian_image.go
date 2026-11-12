package application

import (
	"context"
	"encoding/json"
	"net/http"
)

type bailianImageProvider struct {
	wire   *bailianAPI
	images gatewayImageProvider
}

func (p *bailianImageProvider) Generate(ctx context.Context, in Input) (GenerationResult, error) {
	if !modelHasCapability(p.wire.model, "image") {
		return GenerationResult{}, providerFailure("unsupported_capability")
	}
	return p.generate(ctx, in, nil)
}
func (p *bailianImageProvider) GenerateWithReference(ctx context.Context, in Input, data []byte, mime string) (GenerationResult, error) {
	return p.GenerateWithReferences(ctx, in, []referenceMedia{{Data: data, MIME: mime, Role: "reference"}})
}
func (p *bailianImageProvider) GenerateWithReferences(ctx context.Context, in Input, refs []referenceMedia) (GenerationResult, error) {
	if len(refs) < 1 || len(refs) > gatewayReferenceLimit(p.wire.model) {
		return GenerationResult{}, providerFailure("unsupported_capability")
	}
	return p.generate(ctx, in, refs)
}
func (p *bailianImageProvider) GenerateWithReferenceFiles(ctx context.Context, in Input, path, mime, mask string) (GenerationResult, error) {
	if mask != "" {
		return GenerationResult{}, providerFailure("unsupported_capability")
	}
	return p.GenerateWithReferences(ctx, in, []referenceMedia{{Path: path, MIME: mime, Role: "reference"}})
}

// DashScope reference editing does not implement OpenAI transparent-mask edits.
func (p *bailianImageProvider) GenerateWithMask(context.Context, Input, []byte, string, []byte) (GenerationResult, error) {
	return GenerationResult{}, providerFailure("unsupported_capability")
}
func (p *bailianImageProvider) generate(ctx context.Context, in Input, refs []referenceMedia) (GenerationResult, error) {
	in, err := Normalize(in)
	if err != nil {
		return GenerationResult{}, err
	}
	content := []map[string]any{}
	for _, ref := range refs {
		if ref.Role != "reference" || (ref.MIME != "image/png" && ref.MIME != "image/jpeg") {
			return GenerationResult{}, providerFailure("unsupported_capability")
		}
		if err := validateReference(ctx, ref); err != nil {
			return GenerationResult{}, err
		}
		content = append(content, map[string]any{"image": inlineMedia{reference: ref, prefix: "data:" + ref.MIME + ";base64,"}})
	}
	content = append(content, map[string]any{"text": appendLibraryInputContext(ctx, bailianPrompt(in))})
	body := map[string]any{"model": p.wire.model.Model, "input": map[string]any{"messages": []map[string]any{{"role": "user", "content": content}}}, "parameters": map[string]any{"size": bailianSize(in), "n": 1}}
	raw, err := p.wire.request(ctx, http.MethodPost, bailianMultimodal, body, 256<<10)
	if err != nil {
		return GenerationResult{}, err
	}
	var env struct {
		Output struct {
			Choices []struct {
				Message struct {
					Content []struct {
						Image string `json:"image"`
					} `json:"content"`
				} `json:"message"`
			} `json:"choices"`
		} `json:"output"`
	}
	if json.Unmarshal(raw, &env) != nil || len(env.Output.Choices) != 1 {
		return GenerationResult{}, providerFailure("invalid_provider_response")
	}
	url := ""
	for _, part := range env.Output.Choices[0].Message.Content {
		if part.Image == "" {
			continue
		}
		if url != "" {
			return GenerationResult{}, providerFailure("invalid_provider_response")
		}
		url = part.Image
	}
	if url == "" {
		return GenerationResult{}, providerFailure("invalid_provider_response")
	}
	return p.images.downloadImage(ctx, url)
}
