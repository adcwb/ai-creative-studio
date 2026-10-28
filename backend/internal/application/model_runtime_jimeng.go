package application

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

type runtimeJimengImage struct{ p *JimengImageProvider }

func (p runtimeJimengImage) Generate(ctx context.Context, in Input) (GenerationResult, error) {
	if executionReadOnly(ctx) {
		return GenerationResult{}, errRuntimeDenied
	}
	return p.p.Generate(ctx, in)
}
func (p runtimeJimengImage) GenerateWithReference(ctx context.Context, in Input, data []byte, mime string) (GenerationResult, error) {
	if executionReadOnly(ctx) {
		return GenerationResult{}, errRuntimeDenied
	}
	return p.p.GenerateWithReference(ctx, in, data, mime)
}
func (p runtimeJimengImage) SubmitImage(ctx context.Context, in Input) (AsyncImageJob, error) {
	if executionReadOnly(ctx) {
		return AsyncImageJob{}, errRuntimeDenied
	}
	return p.p.SubmitImage(ctx, in)
}
func (p runtimeJimengImage) SubmitImageWithReference(ctx context.Context, in Input, data []byte, mime string) (AsyncImageJob, error) {
	if executionReadOnly(ctx) {
		return AsyncImageJob{}, errRuntimeDenied
	}
	return p.p.SubmitImageWithReference(ctx, in, data, mime)
}
func (p runtimeJimengImage) PollImage(ctx context.Context, id string) (AsyncImageJob, error) {
	if executionReadOnly(ctx) && (id == "" || id != executionRecoveryUpstream(ctx)) {
		return AsyncImageJob{}, errRuntimeDenied
	}
	return p.p.PollImage(ctx, id)
}
func (p runtimeJimengImage) DownloadImage(ctx context.Context, id string) (GenerationResult, error) {
	if executionReadOnly(ctx) && (id == "" || id != executionRecoveryUpstream(ctx)) {
		return GenerationResult{}, errRuntimeDenied
	}
	return p.p.DownloadImage(ctx, id)
}

type jimengCredentialPair struct {
	AccessKey string `json:"access_key"`
	SecretKey string `json:"secret_key"`
}

func decodeJimengCredential(secret string) (jimengCredentialPair, error) {
	var pair jimengCredentialPair
	d := json.NewDecoder(strings.NewReader(secret))
	d.DisallowUnknownFields()
	if len(secret) > 4096 || d.Decode(&pair) != nil || !validCredential(pair.AccessKey) || !validCredential(pair.SecretKey) {
		return jimengCredentialPair{}, errModelInput
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return jimengCredentialPair{}, errModelInput
	}
	return pair, nil
}
func validModelSecret(provider, secret string) bool {
	if provider == "jimeng-images" {
		_, err := decodeJimengCredential(secret)
		return err == nil
	}
	return validCredential(secret)
}

// The factory attaches this predicate only to the native signed Jimeng adapter.
// Its documented query uses POST; no caller flag can make an arbitrary POST
// read-only. Exact action, version, origin and the entire bounded body are checked.
func jimengReadOnlyRequest(reqKey string) func(*http.Request) bool {
	return func(req *http.Request) bool {
		if req.Method != http.MethodPost || req.URL.Scheme != "https" || req.URL.Host != "visual.volcengineapi.com" || req.URL.User != nil || req.URL.Path != "/" || req.URL.RawPath != "" || req.URL.Fragment != "" {
			return false
		}
		q := req.URL.Query()
		if len(q) != 2 || len(q["Action"]) != 1 || q.Get("Action") != "CVSync2AsyncGetResult" || len(q["Version"]) != 1 || q.Get("Version") != "2022-08-31" || req.Body == nil {
			return false
		}
		raw, err := io.ReadAll(io.LimitReader(req.Body, 4097))
		_ = req.Body.Close()
		req.Body = io.NopCloser(bytes.NewReader(raw))
		if err != nil || len(raw) > 4096 {
			return false
		}
		var body struct {
			ReqKey  string `json:"req_key"`
			TaskID  string `json:"task_id"`
			ReqJSON string `json:"req_json"`
		}
		d := json.NewDecoder(bytes.NewReader(raw))
		d.DisallowUnknownFields()
		if d.Decode(&body) != nil || body.ReqKey != reqKey || !jimengTaskPattern.MatchString(body.TaskID) || body.TaskID != executionRecoveryUpstream(req.Context()) || body.ReqJSON != `{"return_url":false,"logo_info":{"add_logo":true}}` {
			return false
		}
		// Match the exact envelope produced by the trusted adapter, excluding
		// duplicate JSON keys whose interpretation could differ upstream.
		expected, _ := json.Marshal(map[string]string{"req_key": reqKey, "task_id": body.TaskID, "req_json": body.ReqJSON})
		if !bytes.Equal(raw, expected) {
			return false
		}
		var extra any
		return d.Decode(&extra) == io.EOF
	}
}
