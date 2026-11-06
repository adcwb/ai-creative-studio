package application

// Official protocols: https://ai.google.dev/api/generate-content and
// https://docs.volcengine.com/docs/ark/image-generation-api?lang=zh .
// This adapter handles one image and one optional inline reference. It never
// downloads a response URL. Configured model access/quality/cost are unverified.
import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	_ "image/jpeg"
	"image/png"
	"io"
	"net/http"
	"time"
)

type ReferenceImageProvider interface {
	GenerateWithReference(context.Context, Input, []byte, string) (GenerationResult, error)
}
type NativeImageProvider struct {
	kind, key, model, endpoint string
	client                     *http.Client
}

func NewGeminiImageProvider(key, model string) *NativeImageProvider {
	return newNativeImageProvider("gemini-images", key, model, "https://generativelanguage.googleapis.com", nil)
}
func NewArkImageProvider(key, model string) *NativeImageProvider {
	return newNativeImageProvider("ark-images", key, model, "https://ark.cn-beijing.volces.com", nil)
}
func newNativeImageProvider(kind, key, model, endpoint string, client *http.Client) *NativeImageProvider {
	if client == nil {
		t := http.DefaultTransport.(*http.Transport).Clone()
		t.Proxy = nil
		client = &http.Client{Transport: t, Timeout: 120 * time.Second}
	}
	c := *client
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &NativeImageProvider{kind: kind, key: key, model: model, endpoint: endpoint, client: &c}
}
func (p *NativeImageProvider) Generate(ctx context.Context, in Input) (GenerationResult, error) {
	return p.GenerateWithReference(ctx, in, nil, "")
}
func (p *NativeImageProvider) GenerateWithReference(ctx context.Context, in Input, reference []byte, mime string) (GenerationResult, error) {
	return p.generateReferenceMedia(ctx, in, referenceMedia{Data: reference, MIME: mime})
}
func (p *NativeImageProvider) GenerateWithReferenceFiles(ctx context.Context, in Input, path, mime, mask string) (GenerationResult, error) {
	if mask != "" {
		return GenerationResult{}, providerFailure("unsupported_capability")
	}
	return p.generateReferenceMedia(ctx, in, referenceMedia{Path: path, MIME: mime})
}
func (p *NativeImageProvider) generateReferenceMedia(ctx context.Context, in Input, reference referenceMedia) (GenerationResult, error) {
	mime := reference.MIME

	if !validCredential(p.key) || !nativeModelPattern.MatchString(p.model) {
		return GenerationResult{}, providerFailure("provider_unavailable")
	}
	in, err := Normalize(in)
	if err != nil {
		return GenerationResult{}, providerFailure("provider_rejected")
	}
	if reference.Path != "" || len(reference.Data) > 0 {
		if mime != "image/png" && mime != "image/jpeg" {
			return GenerationResult{}, providerFailure("provider_rejected")
		}
		if err := validateReference(ctx, reference); err != nil {
			return GenerationResult{}, providerFailure("provider_rejected")
		}
	} else if mime != "" {
		return GenerationResult{}, providerFailure("provider_rejected")
	}
	prompt := appendLibraryInputContext(ctx, "Create one image. Template: "+in.Kind+". Style: "+in.Style+". Aspect ratio: "+in.Ratio+". Brief: "+in.Prompt)
	var body any
	var path string
	switch p.kind {
	case "gemini-images":
		parts := []map[string]any{{"text": prompt}}
		if reference.Path != "" || len(reference.Data) > 0 {
			parts = append(parts, map[string]any{"inlineData": map[string]any{"mimeType": mime, "data": inlineMedia{reference: reference}}})
		}
		body = map[string]any{"contents": []map[string]any{{"role": "user", "parts": parts}}, "generationConfig": map[string]any{"candidateCount": 1, "responseModalities": []string{"TEXT", "IMAGE"}, "imageConfig": map[string]string{"aspectRatio": in.Ratio}}}
		path = "/v1beta/models/" + p.model + ":generateContent"
	case "ark-images":
		b := map[string]any{"model": p.model, "prompt": prompt, "size": "2K", "response_format": "b64_json", "watermark": true, "stream": false}
		if spec := imageCandidateSpecification(p.kind, p.model); spec != nil {
			b["size"], b["response_format"] = spec.Size, spec.ResponseFormat
			b["output_format"], b["sequential_image_generation"] = spec.OutputFormat, spec.SequentialImageGeneration
		}
		if reference.Path != "" || len(reference.Data) > 0 {
			b["image"] = inlineMedia{reference: reference, prefix: "data:" + mime + ";base64,"}
		}
		body = b
		path = "/api/v3/images/generations"
	default:
		return GenerationResult{}, providerFailure("provider_unavailable")
	}
	header, secret := "Authorization", "Bearer "+p.key
	if p.kind == "gemini-images" {
		header, secret = "x-goog-api-key", p.key
	}
	res, err := requestNativeJSON(ctx, p.client, p.endpoint+path, header, secret, body, 0)
	if err != nil {
		return GenerationResult{}, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		code := "provider_error"
		if res.StatusCode == 401 || res.StatusCode == 403 {
			code = "provider_auth_failed"
		} else if res.StatusCode == 429 {
			code = "provider_rate_limited"
		} else if res.StatusCode >= 400 && res.StatusCode < 500 {
			code = "provider_rejected"
		}
		return GenerationResult{}, providerFailure(code)
	}
	media, err := newNativeJSONParser(ctx, res.Body, "base64", func(path []string) bool {
		return mediaPath(path, "candidates", "*", "content", "parts", "*", "inlinedata", "data") || mediaPath(path, "data", "*", "b64_json")
	}).next(true)
	if err != nil {
		return GenerationResult{}, nativeResponseError(err)
	}
	defer media.close()
	data := media.metadata
	var b64, outMIME string
	if p.kind == "gemini-images" {
		var out struct {
			Candidates []struct {
				FinishReason string `json:"finishReason"`
				Content      struct {
					Parts []struct {
						Thought    bool `json:"thought"`
						InlineData *struct {
							MIME string `json:"mimeType"`
							Data string `json:"data"`
						} `json:"inlineData"`
					} `json:"parts"`
				} `json:"content"`
			} `json:"candidates"`
		}
		if json.Unmarshal(data, &out) != nil || len(out.Candidates) != 1 || out.Candidates[0].FinishReason != "STOP" {
			return GenerationResult{}, providerFailure("invalid_provider_response")
		}
		for _, part := range out.Candidates[0].Content.Parts {
			if part.Thought {
				continue
			}
			if part.InlineData != nil {
				if b64 != "" {
					return GenerationResult{}, providerFailure("invalid_provider_response")
				}
				b64 = part.InlineData.Data
				outMIME = part.InlineData.MIME
			}
		}
	} else {
		var out struct {
			Data []struct {
				Base64 string `json:"b64_json"`
			} `json:"data"`
		}
		if json.Unmarshal(data, &out) != nil || len(out.Data) != 1 {
			return GenerationResult{}, providerFailure("invalid_provider_response")
		}
		b64 = out.Data[0].Base64
	}
	if p.kind == "gemini-images" && outMIME != "image/png" && outMIME != "image/jpeg" {
		return GenerationResult{}, providerFailure("invalid_provider_response")
	}
	return media.image(ctx, b64, outMIME)
}

// Decode before allocation, bound pixels and normalize JPEG/PNG to PNG. Model
// sizes differ from the legacy OpenAI task contract; use actual output metadata.
func nativeImage(data []byte, mime string) (GenerationResult, error) {
	return nativeImageReader(bytes.NewReader(data), mime)
}
func nativeImageReader(reader io.ReadSeeker, mime string) (GenerationResult, error) {
	fail := func() (GenerationResult, error) {
		return GenerationResult{}, providerFailure("invalid_provider_response")
	}
	cfg, format, err := image.DecodeConfig(reader)
	if err != nil || cfg.Width < 1 || cfg.Height < 1 || cfg.Width > 8192 || cfg.Height > 8192 || cfg.Width > 20_000_000/cfg.Height {
		return fail()
	}
	if format != "png" && format != "jpeg" {
		return fail()
	}
	if mime != "" && mime != "image/"+format {
		return fail()
	}
	if _, err = reader.Seek(0, 0); err != nil {
		return fail()
	}
	img, _, err := image.Decode(reader)
	if err != nil {
		return fail()
	}
	var b bytes.Buffer
	// Prefer fast lossless normalization for large decoded results.
	// Pixel and decode guards remain unchanged.
	encoder := png.Encoder{CompressionLevel: png.BestSpeed}
	if encoder.Encode(&b, img) != nil {
		return fail()
	}
	return GenerationResult{Data: b.Bytes(), MIMEType: "image/png", Width: cfg.Width, Height: cfg.Height}, nil
}
