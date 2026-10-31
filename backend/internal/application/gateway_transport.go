package application

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// Private gateways require an exact deployment-owned host:port grant. Resolve
// on every new connection and dial that address, rather than resolving twice.
func newGatewayClient(g AIGateway) *http.Client {
	base, _ := url.Parse(g.BaseURL)
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DisableKeepAlives = true
	transport.ResponseHeaderTimeout = time.Duration(g.TimeoutSeconds) * time.Second
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		if base == nil {
			return nil, errRuntimeUnavailable
		}
		allowed := false
		for _, h := range strings.Split(os.Getenv("MODEL_GATEWAY_ALLOWED_HOSTS"), ",") {
			if strings.EqualFold(strings.TrimSpace(h), base.Host) {
				allowed = true
			}
		}
		if base.Scheme != "https" && !allowed {
			return nil, providerFailure("gateway_target_denied")
		}
		host, port, e := net.SplitHostPort(address)
		if e != nil || !strings.EqualFold(host, base.Hostname()) {
			return nil, providerFailure("gateway_target_denied")
		}
		ips, e := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		if e != nil || len(ips) == 0 {
			return nil, providerFailure("gateway_target_denied")
		}
		for _, ip := range ips {
			if !allowed && !publicDownloadIP(ip) {
				return nil, providerFailure("gateway_target_denied")
			}
		}
		dialer := net.Dialer{Timeout: 15 * time.Second}
		return dialer.DialContext(ctx, "tcp", net.JoinHostPort(ips[0].Unmap().String(), port))
	}
	return &http.Client{Transport: transport, Timeout: time.Duration(g.TimeoutSeconds) * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

type gatewayObservation struct {
	mu        sync.Mutex
	store     *ModelUsageStore
	call      ModelCall
	started   bool
	ambiguous bool
}

func (o *gatewayObservation) before() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	// Persist the attempt before handing it to the network so a crash cannot
	// leave a dispatched request with a zero HTTP count.
	o.call.HTTPCalls++
	if e := o.store.save(o.call); e != nil {
		o.call.HTTPCalls--
		return errRuntimeUnavailable
	}
	o.started = true
	return nil
}
func (o *gatewayObservation) markAmbiguous() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.ambiguous = true
}

func safeGatewayRequestID(s string) string {
	if len(s) > 200 {
		return ""
	}
	for _, c := range s {
		if c < 33 || c > 126 {
			return ""
		}
	}
	return s
}
func (o *gatewayObservation) response(status int, requestID string, raw []byte) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.call.HTTPStatus = status
	if id := safeGatewayRequestID(requestID); id != "" {
		o.call.RequestID = id
	}
	var v struct {
		Usage struct {
			Prompt        *int64 `json:"prompt_tokens"`
			Completion    *int64 `json:"completion_tokens"`
			Input         *int64 `json:"input_tokens"`
			Output        *int64 `json:"output_tokens"`
			PromptDetails struct {
				Cached *int64 `json:"cached_tokens"`
			} `json:"prompt_tokens_details"`
			CompletionDetails struct {
				Reasoning *int64 `json:"reasoning_tokens"`
			} `json:"completion_tokens_details"`
		} `json:"usage"`
	}
	if json.Unmarshal(raw, &v) != nil {
		return
	}
	valid := func(n *int64) *int64 {
		if n != nil && (*n < 0 || *n > 1_000_000_000) {
			return nil
		}
		return n
	}
	input, output := v.Usage.Prompt, v.Usage.Completion
	if input == nil {
		input = v.Usage.Input
	}
	if output == nil {
		output = v.Usage.Output
	}
	if input != nil {
		o.call.InputTokens = valid(input)
	}
	if output != nil {
		o.call.OutputTokens = valid(output)
	}
	if v.Usage.PromptDetails.Cached != nil {
		o.call.CachedTokens = valid(v.Usage.PromptDetails.Cached)
	}
	if v.Usage.CompletionDetails.Reasoning != nil {
		o.call.ReasoningTokens = valid(v.Usage.CompletionDetails.Reasoning)
	}
}
func (o *gatewayObservation) finish(runErr error) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if !o.started {
		return nil
	}
	now := time.Now().UTC()
	o.call.FinishedAt = &now
	o.call.DurationMS = now.Sub(o.call.StartedAt).Milliseconds()
	o.call.State = "succeeded"
	if runErr != nil {
		o.call.State = "failed"
		o.call.ErrorCode = "provider_error"
		var p *ProviderError
		if errors.As(runErr, &p) {
			o.call.ErrorCode = p.Code
		}
		if o.ambiguous || errors.Is(runErr, context.Canceled) || errors.Is(runErr, context.DeadlineExceeded) {
			o.call.State = "unknown"
		}
	}
	return o.store.save(o.call)
}

type gatewayObservedTransport struct {
	base        http.RoundTripper
	origin      *url.URL
	observation *gatewayObservation
}

func (t gatewayObservedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Scheme != t.origin.Scheme || req.URL.Host != t.origin.Host || !strings.HasPrefix(req.URL.Path, t.origin.Path+"/") || req.URL.User != nil || req.URL.Fragment != "" {
		return nil, providerFailure("gateway_target_denied")
	}
	if t.observation != nil {
		if e := t.observation.before(); e != nil {
			return nil, e
		}
	}
	res, e := t.base.RoundTrip(req)
	if e != nil {
		if t.observation != nil {
			t.observation.markAmbiguous()
		}
		return nil, e
	}
	if t.observation != nil {
		res.Body = &gatewayObservedBody{ReadCloser: res.Body, observation: t.observation, status: res.StatusCode, requestID: res.Header.Get("x-request-id")}
	}
	return res, nil
}

type gatewayObservedBody struct {
	io.ReadCloser
	observation *gatewayObservation
	buf         bytes.Buffer
	status      int
	requestID   string
}

func (b *gatewayObservedBody) Read(p []byte) (int, error) {
	n, e := b.ReadCloser.Read(p)
	if e != nil && e != io.EOF {
		b.observation.markAmbiguous()
	}
	if b.buf.Len()+n <= 256<<10 {
		_, _ = b.buf.Write(p[:n])
	}
	return n, e
}
func (b *gatewayObservedBody) Close() error {
	b.observation.response(b.status, b.requestID, b.buf.Bytes())
	return b.ReadCloser.Close()
}
func (s *ModelConfigStore) gatewaySecretLocked(tenant string, g storedGateway) ([]byte, error) {
	if s.aead == nil || g.Gateway.SecretStatus != "encrypted" {
		return nil, errRuntimeUnavailable
	}
	raw, e := base64.StdEncoding.DecodeString(g.Ciphertext)
	if e != nil || len(raw) < s.aead.NonceSize() {
		return nil, errRuntimeUnavailable
	}
	plain, e := s.aead.Open(nil, raw[:s.aead.NonceSize()], raw[s.aead.NonceSize():], gatewayAAD(tenant, g.Gateway))
	if e != nil {
		return nil, errRuntimeUnavailable
	}
	return plain, nil
}
func (s *ModelConfigStore) checkGateway(ctx context.Context, tenant, id string, version int64) (map[string]any, error) {
	s.mu.Lock()
	g, ok := s.data.Tenants[tenant].Gateways[id]
	if !ok || s.failed != nil {
		s.mu.Unlock()
		return nil, ErrNotFound
	}
	if g.Gateway.Version != version {
		s.mu.Unlock()
		return nil, errModelConflict
	}
	plain, e := s.gatewaySecretLocked(tenant, g)
	s.mu.Unlock()
	if e != nil {
		return nil, e
	}
	defer clear(plain)
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	client := newGatewayClient(g.Gateway)
	req, e := http.NewRequestWithContext(ctx, "GET", g.Gateway.BaseURL+"/models", nil)
	if e != nil {
		return nil, errModelInput
	}
	req.Header.Set("Authorization", "Bearer "+string(plain))
	res, e := client.Do(req)
	result := map[string]any{"reachable": false, "models": []string{}, "generation_verified": false, "checked_at": time.Now().UTC()}
	if e != nil {
		result["error_code"] = "gateway_connection_failed"
		return result, nil
	}
	defer res.Body.Close()
	result["http_status"] = res.StatusCode
	if res.StatusCode != 200 {
		result["error_code"] = "gateway_catalog_unavailable"
		return result, nil
	}
	raw, e := io.ReadAll(io.LimitReader(res.Body, (1<<20)+1))
	if e != nil || len(raw) > 1<<20 {
		return nil, errRuntimeUnavailable
	}
	var catalog struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &catalog) != nil {
		result["error_code"] = "invalid_gateway_catalog"
		return result, nil
	}
	models := []string{}
	for _, m := range catalog.Data {
		if validGatewayModelID(m.ID) && len(models) < 200 {
			models = append(models, m.ID)
		}
	}
	result["reachable"] = true
	result["models"] = models
	return result, nil
}
