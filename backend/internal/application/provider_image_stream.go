package application

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strings"
)

type fileReferenceImageProvider interface {
	GenerateWithReferenceFiles(context.Context, Input, string, string, string) (GenerationResult, error)
}

func validateEditMaskFiles(reference, mime, mask string) error {
	if _, _, e := validateMedia(reference, mime); e != nil {
		return e
	}
	ref, e := openLocalBlob(reference)
	if e != nil {
		return e
	}
	cfg, _, e := image.DecodeConfig(ref)
	ref.Close()
	if e != nil {
		return e
	}
	f, e := openLocalBlob(mask)
	if e != nil {
		return e
	}
	defer f.Close()
	mc, e := png.DecodeConfig(f)
	if e != nil || mc.Width != cfg.Width || mc.Height != cfg.Height {
		return providerFailure("provider_rejected")
	}
	if _, e = f.Seek(0, 0); e != nil {
		return e
	}
	img, e := png.Decode(f)
	if e != nil {
		return providerFailure("provider_rejected")
	}
	transparent, opaque := false, false
	for y := 0; y < cfg.Height; y++ {
		for x := 0; x < cfg.Width; x++ {
			_, _, _, a := img.At(x, y).RGBA()
			transparent = transparent || a == 0
			opaque = opaque || a == 65535
			if transparent && opaque {
				return nil
			}
		}
	}
	return providerFailure("provider_rejected")
}
func multipartImageFiles(ctx context.Context, p *OpenAICreativeProvider, fields map[string]string, reference, mime, mask string) ([]byte, error) {
	return multipartImageReferences(ctx, p, fields, []referenceMedia{{Path: reference, MIME: mime}}, mask, nil)
}
func multipartImageReferences(ctx context.Context, p *OpenAICreativeProvider, fields map[string]string, refs []referenceMedia, maskPath string, maskData []byte) ([]byte, error) {
	res, e := multipartImageResponse(ctx, p, fields, refs, maskPath, maskData)
	if e != nil {
		return nil, e
	}
	defer res.Body.Close()
	raw, e := io.ReadAll(io.LimitReader(res.Body, maxProviderResponseBytes+1))
	if e != nil || len(raw) > maxProviderResponseBytes {
		return nil, providerFailure("invalid_provider_response")
	}
	return raw, nil
}

// Caller owns the successful response; media consumers parse it incrementally.
func multipartImageResponse(ctx context.Context, p *OpenAICreativeProvider, fields map[string]string, refs []referenceMedia, maskPath string, maskData []byte) (*http.Response, error) {
	for _, ref := range refs {
		if e := validateReference(ctx, ref); e != nil {
			return nil, e
		}
	}
	if maskPath != "" {
		if len(refs) != 1 || refs[0].Path == "" {
			return nil, providerFailure("provider_rejected")
		}
		if e := validateEditMaskFiles(refs[0].Path, refs[0].MIME, maskPath); e != nil {
			return nil, e
		}
	}
	reader, writer := io.Pipe()
	form := multipart.NewWriter(writer)
	done := make(chan error, 1)
	go func() {
		e := func() error {
			for k, v := range fields {
				if e := form.WriteField(k, v); e != nil {
					return e
				}
			}
			items := append([]referenceMedia(nil), refs...)
			if maskPath != "" || maskData != nil {
				items = append(items, referenceMedia{Path: maskPath, Data: maskData, MIME: "image/png"})
			}
			for i, ref := range items {
				name := "image[]"
				if i >= len(refs) {
					name = "mask"
				}
				header := textproto.MIMEHeader{}
				ext := "png"
				if ref.MIME == "image/jpeg" {
					ext = "jpg"
				}
				header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="%s"; filename="%d.%s"`, name, i, ext))
				header.Set("Content-Type", ref.MIME)
				part, e := form.CreatePart(header)
				if e != nil {
					return e
				}
				f, _, e := referenceOpen(ctx, ref)
				if e != nil {
					return e
				}
				_, e = io.CopyBuffer(part, contextBlobReader{ctx, f}, make([]byte, 64<<10))
				e = errors.Join(e, f.Close())
				if e != nil {
					return e
				}
			}

			return form.Close()
		}()
		_ = writer.CloseWithError(e)
		done <- e
	}()
	req, e := http.NewRequestWithContext(ctx, "POST", p.endpoint+"/images/edits", reader)
	if e != nil {
		reader.Close()
		<-done
		return nil, e
	}
	req.GetBody = nil
	req.Header.Set("Authorization", "Bearer "+p.key)
	req.Header.Set("Content-Type", form.FormDataContentType())
	res, e := libraryGuardedRequest(p.client, req)
	// Close the request pipe even when authorization or an early response stopped
	// consumption. A partial send never becomes an accepted reviewed request.
	_ = reader.Close()
	writeErr := <-done
	if e != nil || (writeErr != nil && res.StatusCode >= 200 && res.StatusCode < 300) {
		if res != nil {
			res.Body.Close()
		}
		var failure *ProviderError
		if errors.As(e, &failure) && failure.Code == "library_use_denied" {
			return nil, e
		}
		if errors.Is(e, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, providerFailure("provider_timeout")
		}
		return nil, providerFailure("provider_error")
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		res.Body.Close()
		code := "provider_rejected"
		if res.StatusCode == 401 || res.StatusCode == 403 {
			code = "provider_auth_failed"
		}
		if res.StatusCode == 429 {
			code = "provider_rate_limited"
		}
		return nil, providerFailure(code)
	}
	return res, nil
}

func (p *gatewayImageProvider) GenerateWithReferenceFiles(ctx context.Context, in Input, reference, mime, mask string) (GenerationResult, error) {
	if !modelHasCapability(p.model, "image_edit") {
		return GenerationResult{}, providerFailure("unsupported_capability")
	}
	in, e := Normalize(in)
	if e != nil {
		return GenerationResult{}, e
	}
	size, _, _ := imageSize(in.Ratio)
	res, e := multipartImageResponse(ctx, p.api, map[string]string{"model": p.model.Model, "prompt": appendLibraryInputContext(ctx, "Style: "+in.Style+". Aspect ratio: "+in.Ratio+". "+in.Prompt), "size": size, "n": "1"}, []referenceMedia{{Path: reference, MIME: mime}}, mask, nil)
	if e != nil {
		return GenerationResult{}, e
	}
	defer res.Body.Close()
	return p.decodeImageReader(ctx, res.Body)
}
func (p *OpenAIProvider) GenerateWithReferenceFiles(ctx context.Context, in Input, reference, mime, mask string) (GenerationResult, error) {
	in, e := Normalize(in)
	if e != nil {
		return GenerationResult{}, e
	}
	size, w, h := imageSize(in.Ratio)
	direction := "Use this reference for character appearance while following the scene. Identity consistency is a creative goal, not guaranteed."
	if value, _ := ctx.Value(libraryInputContextKey{}).(string); value != "" {
		direction = "Use each reference according to its assigned creative role. Character identity and style consistency are creative goals, not guarantees."
	}
	api := newCreativeProvider(p.key, strings.TrimSuffix(p.endpoint, "/images/generations"), p.client)
	raw, e := multipartImageFiles(ctx, api, map[string]string{"model": p.model, "prompt": appendLibraryInputContext(ctx, direction+" Visual style: "+in.Style+". Scene: "+in.Prompt), "size": size, "n": "1", "quality": "low", "output_format": "png", "background": "opaque"}, reference, mime, mask)
	if e != nil {
		return GenerationResult{}, e
	}
	var out struct {
		Data []struct {
			Base64 string `json:"b64_json"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &out) != nil || len(out.Data) != 1 {
		return GenerationResult{}, providerFailure("invalid_provider_response")
	}
	b, e := base64.StdEncoding.Strict().DecodeString(out.Data[0].Base64)
	if e != nil || validatePNG(b, w, h) != nil {
		return GenerationResult{}, providerFailure("invalid_provider_response")
	}
	return GenerationResult{Data: b, MIMEType: "image/png", Width: w, Height: h}, nil
}
