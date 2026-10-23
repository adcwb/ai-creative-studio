package studio

// Official protocols: https://ai.google.dev/api/generate-content and
// https://docs.volcengine.com/docs/ark/image-generation-api?lang=zh .
// This adapter handles one image and one optional inline reference. It never
// downloads a response URL. Configured model access/quality/cost are unverified.
import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
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
	if !validCredential(p.key) || !nativeModelPattern.MatchString(p.model) {
		return GenerationResult{}, providerFailure("provider_unavailable")
	}
	in, err := Normalize(in)
	if err != nil {
		return GenerationResult{}, providerFailure("provider_rejected")
	}
	if len(reference) > 0 {
		if _, err := nativeImage(reference, mime); err != nil {
			return GenerationResult{}, providerFailure("provider_rejected")
		}
	} else if mime != "" {
		return GenerationResult{}, providerFailure("provider_rejected")
	}
	prompt := "Create one image. Template: " + in.Kind + ". Style: " + in.Style + ". Aspect ratio: " + in.Ratio + ". Brief: " + in.Prompt
	var body any
	var path string
	switch p.kind {
	case "gemini-images":
		parts := []map[string]any{{"text": prompt}}
		if len(reference) > 0 {
			parts = append(parts, map[string]any{"inlineData": map[string]string{"mimeType": mime, "data": base64.StdEncoding.EncodeToString(reference)}})
		}
		body = map[string]any{"contents": []map[string]any{{"role": "user", "parts": parts}}, "generationConfig": map[string]any{"candidateCount": 1, "responseModalities": []string{"TEXT", "IMAGE"}, "imageConfig": map[string]string{"aspectRatio": in.Ratio}}}
		path = "/v1beta/models/" + p.model + ":generateContent"
	case "ark-images":
		b := map[string]any{"model": p.model, "prompt": prompt, "size": "2K", "response_format": "b64_json", "watermark": true, "stream": false}
		if len(reference) > 0 {
			b["image"] = "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(reference)
		}
		body = b
		path = "/api/v3/images/generations"
	default:
		return GenerationResult{}, providerFailure("provider_unavailable")
	}
	encoded, err := json.Marshal(body)
	if err != nil || len(encoded) > 24<<20 {
		return GenerationResult{}, providerFailure("provider_rejected")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint+path, bytes.NewReader(encoded))
	if err != nil {
		return GenerationResult{}, providerFailure("provider_error")
	}
	req.Header.Set("Content-Type", "application/json")
	if p.kind == "gemini-images" {
		req.Header.Set("x-goog-api-key", p.key)
	} else {
		req.Header.Set("Authorization", "Bearer "+p.key)
	}
	req.GetBody = nil
	res, err := p.client.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return GenerationResult{}, providerFailure("provider_timeout")
		}
		return GenerationResult{}, providerFailure("provider_error")
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
	data, err := io.ReadAll(io.LimitReader(res.Body, maxProviderResponseBytes+1))
	if err != nil || len(data) > maxProviderResponseBytes {
		return GenerationResult{}, providerFailure("invalid_provider_response")
	}
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
	if b64 == "" || len(b64) > base64.StdEncoding.EncodedLen(maxPNGBytes) {
		return GenerationResult{}, providerFailure("invalid_provider_response")
	}
	raw, err := base64.StdEncoding.Strict().DecodeString(b64)
	if err != nil {
		return GenerationResult{}, providerFailure("invalid_provider_response")
	}
	return nativeImage(raw, outMIME)
}

// Decode before allocation, bound pixels and normalize JPEG/PNG to PNG. Model
// sizes differ from the legacy OpenAI task contract; use actual output metadata.
func nativeImage(data []byte, mime string) (GenerationResult, error) {
	fail := func() (GenerationResult, error) {
		return GenerationResult{}, providerFailure("invalid_provider_response")
	}
	if len(data) == 0 || len(data) > maxPNGBytes {
		return fail()
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width < 1 || cfg.Height < 1 || cfg.Width > 8192 || cfg.Height > 8192 || cfg.Width > 20_000_000/cfg.Height {
		return fail()
	}
	if format != "png" && format != "jpeg" {
		return fail()
	}
	if mime != "" && mime != "image/"+format {
		return fail()
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return fail()
	}
	var b bytes.Buffer
	if png.Encode(&b, img) != nil || b.Len() > maxPNGBytes {
		return fail()
	}
	return GenerationResult{Data: b.Bytes(), MIMEType: "image/png", Width: cfg.Width, Height: cfg.Height}, nil
}
