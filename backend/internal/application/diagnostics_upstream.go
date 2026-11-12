package application

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sync"

	"github.com/adcwb/ai-creative-studio/backend/internal/platform/diagnostics"
)

// Metadata is parsed only from the bytes consumed by the actual provider. Never
// drain an error body for logging, buffer an entire media response, or change guards.
type diagnosticBody struct {
	io.ReadCloser
	mu        sync.Mutex
	once      sync.Once
	buf       bytes.Buffer
	truncated bool
	ctx       context.Context
	status    int
	requestID string
}

func (b *diagnosticBody) Read(p []byte) (int, error) {
	n, e := b.ReadCloser.Read(p)
	b.mu.Lock()
	if !b.truncated {
		if b.buf.Len()+n <= 64<<10 {
			b.buf.Write(p[:n])
		} else {
			b.truncated = true
			b.buf.Reset()
		}
	}
	b.mu.Unlock()
	return n, e
}
func upstreamMetadata(status int, requestID string, raw []byte) diagnostics.Upstream {
	u := diagnostics.Upstream{HTTPStatus: status, RequestID: diagnostics.SafeID(requestID)}
	var v struct {
		Code      json.RawMessage `json:"code"`
		RequestID string          `json:"request_id"`
		Error     struct {
			Code   json.RawMessage `json:"code"`
			Type   string          `json:"type"`
			Status string          `json:"status"`
		} `json:"error"`
		Base struct {
			Code int `json:"status_code"`
		} `json:"base_resp"`
	}
	if len(raw) > 64<<10 || json.Unmarshal(raw, &v) != nil {
		return u
	}
	if u.RequestID == "" {
		u.RequestID = diagnostics.SafeID(v.RequestID)
	}
	decode := func(raw json.RawMessage) string {
		var s string
		if json.Unmarshal(raw, &s) == nil {
			return s
		}
		if string(raw) != "0" && string(raw) != "null" {
			var n json.Number
			if json.Unmarshal(raw, &n) == nil {
				return n.String()
			}
		}
		return ""
	}
	code := decode(v.Code)
	if code == "" {
		code = decode(v.Error.Code)
	}
	if code == "" {
		code = v.Error.Type
	}
	if code == "" {
		code = v.Error.Status
	}
	if code == "" && v.Base.Code != 0 {
		raw, _ := json.Marshal(v.Base.Code)
		code = string(raw)
	}
	switch code {
	case "0", "200", "OK", "Success", "success":
		code = ""
	}
	u.Code = diagnostics.SafeUpstreamCode(code)
	return u
}
func (b *diagnosticBody) Close() error {
	b.once.Do(func() {
		b.mu.Lock()
		u := upstreamMetadata(b.status, b.requestID, b.buf.Bytes())
		b.buf = bytes.Buffer{}
		b.mu.Unlock()
		if b.status >= 300 || u.Code != "" {
			if state, ok := b.ctx.Value(diagnosticKey{}).(*requestDiagnostic); ok {
				state.mu.Lock()
				state.upstream = &u
				state.mu.Unlock()
			}
			diagnostics.Event(b.ctx, "upstream_error", "provider_error", "", &u)
		}
	})
	return b.ReadCloser.Close()
}
func observeProviderResponse(req *http.Request, res *http.Response, err error) (*http.Response, error) {
	if err != nil {
		diagnostics.Event(req.Context(), "upstream_error", diagnosticFailureCode(err), "", nil)
		return res, err
	}
	if res != nil && res.Body != nil {
		id := upstreamRequestID(res.Header)
		res.Body = &diagnosticBody{ReadCloser: res.Body, ctx: req.Context(), status: res.StatusCode, requestID: id}
	}
	return res, nil
}
func upstreamRequestID(h http.Header) string {
	for _, name := range []string{"x-request-id", "x-dashscope-request-id", "request-id", "x-tt-logid"} {
		if id := h.Get(name); id != "" {
			return id
		}
	}
	return ""
}
