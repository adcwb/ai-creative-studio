package application

import (
	"context"
)

// Separate identity from gemini-images (generateContent), so protocol changes
// cannot silently replay an already accepted paid job. Official native schema:
// https://ai.google.dev/gemini-api/docs/image-generation
// Returns one image; accepts ordered inline references, never file URLs.
type GoogleInteractionsImageProvider struct{ api *GoogleMediaProvider }

func NewGoogleInteractionsImageProvider(key, model string) *GoogleInteractionsImageProvider {
	return &GoogleInteractionsImageProvider{api: newGoogleMediaProvider("gemini-interactions-images", key, model, "", "https://generativelanguage.googleapis.com", nil, nil)}
}
func (p *GoogleInteractionsImageProvider) Generate(ctx context.Context, in Input) (GenerationResult, error) {
	return p.GenerateWithReference(ctx, in, nil, "")
}
func (p *GoogleInteractionsImageProvider) GenerateWithReference(ctx context.Context, in Input, reference []byte, mime string) (GenerationResult, error) {
	refs := []referenceMedia{}
	if len(reference) > 0 {
		refs = append(refs, referenceMedia{Data: reference, MIME: mime, Role: "reference"})
	} else if mime != "" {
		return GenerationResult{}, providerFailure("provider_rejected")
	}
	return p.generateReferences(ctx, in, refs)
}
func (p *GoogleInteractionsImageProvider) GenerateWithReferences(ctx context.Context, in Input, refs []referenceMedia) (GenerationResult, error) {
	if len(refs) == 0 || len(refs) > creativeControls("gemini-interactions-images", p.api.model, "image").MaxReferences {
		return GenerationResult{}, providerFailure("unsupported_capability")
	}
	return p.generateReferences(ctx, in, refs)
}
func (p *GoogleInteractionsImageProvider) generateReferences(ctx context.Context, in Input, refs []referenceMedia) (GenerationResult, error) {
	in, err := Normalize(in)
	if err != nil {
		return GenerationResult{}, providerFailure("provider_rejected")
	}
	for _, r := range refs {
		if r.MIME != "image/png" && r.MIME != "image/jpeg" {
			return GenerationResult{}, providerFailure("provider_rejected")
		}
		if err = validateReference(ctx, r); err != nil {
			return GenerationResult{}, err
		}
	}
	prompt := appendLibraryInputContext(ctx, "Create one image. Template: "+in.Kind+". Style: "+in.Style+". Brief: "+in.Prompt)
	input := []map[string]any{{"type": "text", "text": prompt}}
	for _, r := range refs {
		input = append(input, map[string]any{"type": "image", "mime_type": r.MIME, "data": inlineMedia{reference: r}})
	}
	body := map[string]any{"model": p.api.model, "input": input, "response_format": map[string]string{"type": "image", "mime_type": "image/png", "aspect_ratio": in.Ratio, "image_size": "1K"}, "background": false, "store": false, "stream": false}
	res, err := p.api.requestMediaJSON(ctx, body)
	if err != nil {
		return GenerationResult{}, err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return GenerationResult{}, googleMediaStatus(res.StatusCode)
	}
	media, err := newNativeJSONParser(ctx, res.Body, "base64", func(path []string) bool { return mediaPath(path, "steps", "*", "content", "*", "data") }).next(true)
	if err != nil {
		return GenerationResult{}, nativeResponseError(err)
	}
	defer media.close()
	block, err := googleOutput(media.metadata, "image")
	if err != nil {
		return GenerationResult{}, err
	}
	if (block.MIME != "image/png" && block.MIME != "image/jpeg") || block.URI != "" {
		return GenerationResult{}, providerFailure("invalid_provider_response")
	}
	return media.image(ctx, block.Data, block.MIME)
}

func (p *GoogleInteractionsImageProvider) GenerateWithReferenceFiles(ctx context.Context, in Input, path, mime, mask string) (GenerationResult, error) {
	if mask != "" {
		return GenerationResult{}, providerFailure("unsupported_capability")
	}
	return p.generateReferences(ctx, in, []referenceMedia{{Path: path, MIME: mime, Role: "reference"}})
}
