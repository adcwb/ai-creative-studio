package studio

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strings"
)

// GenerateWithReference sends one validated local image as multipart image[].
// Reference conditioning is probabilistic; it cannot guarantee character identity.
// Only the fixed official /images/edits endpoint is used in production. No
// uploaded filename, asset URL or model output URL is interpreted as a path.
func (p *OpenAIProvider) GenerateWithReference(ctx context.Context, in Input, reference []byte, mime string) (GenerationResult, error) {
	in, err := Normalize(in)
	if err != nil {
		return GenerationResult{}, providerFailure("provider_rejected")
	}
	if mime != "image/png" && mime != "image/jpeg" {
		return GenerationResult{}, providerFailure("provider_rejected")
	}
	if _, err = nativeImage(reference, mime); err != nil {
		return GenerationResult{}, providerFailure("provider_rejected")
	}
	size, width, height := imageSize(in.Ratio)
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	fields := map[string]string{"model": p.model, "prompt": "Use this reference for character appearance while following the scene. Identity consistency is a creative goal, not guaranteed. Visual style: " + in.Style + ". Scene: " + in.Prompt, "size": size, "n": "1", "quality": "low", "output_format": "png", "background": "opaque"}
	for name, value := range fields {
		if err = writer.WriteField(name, value); err != nil {
			return GenerationResult{}, providerFailure("provider_error")
		}
	}
	ext := "png"
	if mime == "image/jpeg" {
		ext = "jpg"
	}
	headers := textproto.MIMEHeader{}
	headers.Set("Content-Disposition", `form-data; name="image[]"; filename="reference.`+ext+`"`)
	headers.Set("Content-Type", mime)
	part, err := writer.CreatePart(headers)
	if err != nil {
		return GenerationResult{}, providerFailure("provider_error")
	}
	if _, err = part.Write(reference); err != nil {
		return GenerationResult{}, providerFailure("provider_error")
	}
	if err = writer.Close(); err != nil {
		return GenerationResult{}, providerFailure("provider_error")
	}
	endpoint := strings.TrimSuffix(p.endpoint, "/generations") + "/edits"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body.Bytes()))
	if err != nil {
		return GenerationResult{}, providerFailure("provider_error")
	}
	req.GetBody = nil
	req.Header.Set("Authorization", "Bearer "+p.key)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	response, err := p.client.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return GenerationResult{}, providerFailure("provider_timeout")
		}
		return GenerationResult{}, providerFailure("provider_error")
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		code := "provider_error"
		switch response.StatusCode {
		case 401, 403:
			code = "provider_auth_failed"
		case 429:
			code = "provider_rate_limited"
		case 400, 404, 422:
			code = "provider_rejected"
		}
		return GenerationResult{}, providerFailure(code)
	}
	encoded, err := io.ReadAll(io.LimitReader(response.Body, maxProviderResponseBytes+1))
	if err != nil || len(encoded) > maxProviderResponseBytes {
		return GenerationResult{}, providerFailure("invalid_provider_response")
	}
	var out struct {
		Data []struct {
			Base64 string `json:"b64_json"`
		} `json:"data"`
	}
	if json.Unmarshal(encoded, &out) != nil || len(out.Data) != 1 || out.Data[0].Base64 == "" || len(out.Data[0].Base64) > base64.StdEncoding.EncodedLen(maxPNGBytes) {
		return GenerationResult{}, providerFailure("invalid_provider_response")
	}
	data, err := base64.StdEncoding.Strict().DecodeString(out.Data[0].Base64)
	if err != nil || validatePNG(data, width, height) != nil {
		return GenerationResult{}, providerFailure("invalid_provider_response")
	}
	return GenerationResult{Data: data, MIMEType: "image/png", Width: width, Height: height}, nil
}
