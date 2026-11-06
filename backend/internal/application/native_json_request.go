package application

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"io"
	"net/http"
	"sort"
)

// Native media is streamed without an application byte quota. Specific
// verified protocol boundaries (currently MiniMax V2) are passed explicitly.
type inlineMedia struct {
	reference referenceMedia
	prefix    string
}

func (inlineMedia) MarshalJSON() ([]byte, error) {
	return nil, errors.New("inline media requires streaming JSON")
}
func referenceOpen(ctx context.Context, r referenceMedia) (io.ReadCloser, int64, error) {
	if r.Path != "" {
		f, e := openLocalBlob(r.Path)
		if e != nil {
			return nil, 0, e
		}
		info, e := f.Stat()
		if e != nil {
			f.Close()
			return nil, 0, e
		}
		return mediaReadCloser{contextBlobReader{ctx, f}, f}, info.Size(), nil
	}
	return io.NopCloser(bytes.NewReader(r.Data)), int64(len(r.Data)), nil
}
func referenceExecutionMedia(ctx context.Context, r referenceMedia) (ExecutionMedia, error) {
	if r.Path != "" {
		return executionFileMedia(ctx, r.Path, r.MIME)
	}
	return ExecutionMedia{Data: r.Data, MIME: r.MIME}, nil
}
func validateReference(ctx context.Context, r referenceMedia) error {
	if r.Path != "" {
		_, _, e := validateMedia(r.Path, r.MIME)
		return e
	}
	if r.MIME == "image/png" || r.MIME == "image/jpeg" {
		_, e := nativeImage(r.Data, r.MIME)
		return e
	}
	if r.MIME == "video/mp4" && len(r.Data) >= 12 && string(r.Data[4:8]) == "ftyp" {
		return nil
	}
	return providerFailure("provider_rejected")
}

type jsonByteCounter struct{ n int64 }

func (c *jsonByteCounter) add(n int64) error {
	if n < 0 || c.n > int64(1<<63-1)-n {
		return providerFailure("provider_rejected")
	}
	c.n += n
	return nil
}
func (c *jsonByteCounter) Write(b []byte) (int, error) {
	if e := c.add(int64(len(b))); e != nil {
		return 0, e
	}
	return len(b), nil
}

func writeNativeJSON(ctx context.Context, w io.Writer, value any) error {
	put := func(s string) error { _, e := io.WriteString(w, s); return e }
	switch v := value.(type) {
	case inlineMedia:
		f, size, e := referenceOpen(ctx, v.reference)
		if e != nil {
			return e
		}
		defer f.Close()
		prefix, _ := json.Marshal(v.prefix)
		if _, e = w.Write(prefix[:len(prefix)-1]); e != nil {
			return e
		}
		if counter, ok := w.(*jsonByteCounter); ok {
			if size < 0 || size > (1<<62)-3 {
				return providerFailure("provider_rejected")
			}
			if e := counter.add((size + 2) / 3 * 4); e != nil {
				return e
			}
		} else {
			encoder := base64.NewEncoder(base64.StdEncoding, w)
			_, e = io.CopyBuffer(encoder, contextBlobReader{ctx, f}, make([]byte, 64<<10))
			e = errors.Join(e, encoder.Close())
			if e != nil {
				return e
			}
		}
		return put(`"`)
	case map[string]any:
		if e := put("{"); e != nil {
			return e
		}
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for i, k := range keys {
			if i > 0 {
				if e := put(","); e != nil {
					return e
				}
			}
			key, _ := json.Marshal(k)
			if _, e := w.Write(key); e != nil {
				return e
			}
			if e := put(":"); e != nil {
				return e
			}
			if e := writeNativeJSON(ctx, w, v[k]); e != nil {
				return e
			}
		}
		return put("}")
	case []any:
		if e := put("["); e != nil {
			return e
		}
		for i, item := range v {
			if i > 0 {
				if e := put(","); e != nil {
					return e
				}
			}
			if e := writeNativeJSON(ctx, w, item); e != nil {
				return e
			}
		}
		return put("]")
	case []map[string]any:
		items := make([]any, len(v))
		for i := range v {
			items[i] = v[i]
		}
		return writeNativeJSON(ctx, w, items)
	default:
		raw, e := json.Marshal(value)
		if e != nil {
			return e
		}
		_, e = w.Write(raw)
		return e
	}
}
func nativeJSONBody(ctx context.Context, value any, limit int64) (io.ReadCloser, int64, <-chan error, error) {
	counter := &jsonByteCounter{}
	if e := writeNativeJSON(ctx, counter, value); e != nil {
		return nil, 0, nil, e
	}
	if limit > 0 && counter.n > limit {
		return nil, 0, nil, providerFailure("provider_inline_capacity")
	}
	// Text-only native envelopes are already bounded by their input schemas.
	// Retain the ordinary small request body; only inline media needs a pipe.
	if !containsInlineMedia(value) {
		raw, e := json.Marshal(value)
		if e != nil {
			return nil, 0, nil, e
		}
		done := make(chan error, 1)
		done <- nil
		return io.NopCloser(bytes.NewReader(raw)), int64(len(raw)), done, nil
	}
	r, w := io.Pipe()
	done := make(chan error, 1)
	go func() { e := writeNativeJSON(ctx, w, value); _ = w.CloseWithError(e); done <- e }()
	return r, counter.n, done, nil
}

// Avoid accidentally retaining a native response's original encoded body.
type contextReadSeeker struct {
	io.Reader
	io.Seeker
}

func requestNativeJSON(ctx context.Context, client *http.Client, url, header, secret string, value any, limit int64) (*http.Response, error) {
	body, size, done, e := nativeJSONBody(ctx, value, limit)
	if e != nil {
		return nil, e
	}
	req, e := http.NewRequestWithContext(ctx, "POST", url, body)
	if e != nil {
		body.Close()
		<-done
		return nil, providerFailure("provider_error")
	}
	req.ContentLength = size
	req.GetBody = nil
	req.Header.Set(header, secret)
	req.Header.Set("Content-Type", "application/json")
	res, e := libraryGuardedRequest(client, req)
	_ = body.Close()
	writeErr := <-done
	if e != nil {
		if res != nil {
			res.Body.Close()
		}
		if errors.Is(e, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, providerFailure("provider_timeout")
		}
		return nil, providerFailure("provider_error")
	}
	if writeErr != nil && res.StatusCode >= 200 && res.StatusCode < 300 {
		res.Body.Close()
		return nil, providerFailure("provider_error")
	}
	return res, nil
}
func (p *GoogleMediaProvider) requestMediaJSON(ctx context.Context, body any) (*http.Response, error) {
	if !p.configured() {
		return nil, providerFailure("provider_unavailable")
	}
	return requestNativeJSON(ctx, p.client, p.endpoint+"/v1beta/interactions", "x-goog-api-key", p.key, body, 0)
}

func (p *OpenAICreativeProvider) requestNativeResponse(ctx context.Context, path string, value any) (*http.Response, error) {
	res, e := requestNativeJSON(ctx, p.client, p.endpoint+path, "Authorization", "Bearer "+p.key, value, nativeRequestLimit(path))
	if e != nil {
		return nil, e
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		res.Body.Close()
		return nil, googleMediaStatus(res.StatusCode)
	}
	return res, nil
}
func (p *OpenAICreativeProvider) requestNativeMetadata(ctx context.Context, path string, value any, max int) ([]byte, error) {
	res, e := p.requestNativeResponse(ctx, path, value)
	if e != nil {
		return nil, e
	}
	defer res.Body.Close()
	b, e := io.ReadAll(io.LimitReader(res.Body, int64(max)+1))
	if e != nil || len(b) > max {
		return nil, providerFailure("invalid_provider_response")
	}
	return b, nil
}

type fileReferenceVideoProvider interface {
	SubmitVideoWithFile(context.Context, string, string, string) (VideoJob, error)
}

func referenceConfig(ctx context.Context, r referenceMedia) (image.Config, string, error) {
	f, _, e := referenceOpen(ctx, r)
	if e != nil {
		return image.Config{}, "", e
	}
	defer f.Close()
	return image.DecodeConfig(f)
}

func nativeRequestLimit(path string) int64 {
	if path == "/v2/video_generation" {
		return 64_000_000
	}
	return 0
}

func containsInlineMedia(v any) bool {
	switch v := v.(type) {
	case inlineMedia:
		return true
	case map[string]any:
		for _, item := range v {
			if containsInlineMedia(item) {
				return true
			}
		}
	case []any:
		for _, item := range v {
			if containsInlineMedia(item) {
				return true
			}
		}
	case []map[string]any:
		for _, item := range v {
			if containsInlineMedia(item) {
				return true
			}
		}
	}
	return false
}
