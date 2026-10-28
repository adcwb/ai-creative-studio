package application

// Public Visual API, not the Jimeng consumer website. Signature construction
// follows the canonical request documented by volcengine/volc-sdk-golang/base/sign.go.
// Submission is sent once. Workers persist task_id before querying; restart
// resumes reads of known IDs only. Missing ACKs retain billing uncertainty.
import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	urlpkg "net/url"
	"regexp"
	"strings"
	"time"
)

type ReferenceURLBridge interface {
	ReferenceURL(context.Context, []byte, string) (string, error)
}

type JimengImageProvider struct {
	bridge                                 ReferenceURLBridge
	accessKey, secretKey, reqKey, endpoint string
	client                                 *http.Client
	pollInterval                           time.Duration
	now                                    func() time.Time
}

func NewJimengImageProvider(accessKey, secretKey, reqKey string) *JimengImageProvider {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.Proxy = nil
	return newJimengImageProvider(accessKey, secretKey, reqKey, "https://visual.volcengineapi.com", &http.Client{Transport: t, Timeout: 120 * time.Second})
}
func newJimengImageProvider(accessKey, secretKey, reqKey, endpoint string, client *http.Client) *JimengImageProvider {
	c := *client
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &JimengImageProvider{accessKey: accessKey, secretKey: secretKey, reqKey: reqKey, endpoint: endpoint, client: &c, pollInterval: 2 * time.Second, now: time.Now}
}
func jimengSHA(data []byte) string { s := sha256.Sum256(data); return hex.EncodeToString(s[:]) }
func jimengMAC(key []byte, data string) []byte {
	h := hmac.New(sha256.New, key)
	_, _ = h.Write([]byte(data))
	return h.Sum(nil)
}
func signJimeng(req *http.Request, body []byte, accessKey, secretKey string, at time.Time) {
	stamp := at.UTC().Format("20060102T150405Z")
	day := stamp[:8]
	scope := day + "/cn-north-1/cv/request"
	digest := jimengSHA(body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Date", stamp)
	req.Header.Set("X-Content-Sha256", digest)
	query := req.URL.Query().Encode()
	req.URL.RawQuery = query
	signed := "content-type;host;x-content-sha256;x-date"
	headers := "content-type:application/json\nhost:" + req.URL.Host + "\nx-content-sha256:" + digest + "\nx-date:" + stamp + "\n"
	canonical := strings.Join([]string{req.Method, "/", query, headers, signed, digest}, "\n")
	toSign := "HMAC-SHA256\n" + stamp + "\n" + scope + "\n" + jimengSHA([]byte(canonical))
	key := jimengMAC([]byte(secretKey), day)
	for _, part := range []string{"cn-north-1", "cv", "request"} {
		key = jimengMAC(key, part)
	}
	req.Header.Set("Authorization", "HMAC-SHA256 Credential="+accessKey+"/"+scope+", SignedHeaders="+signed+", Signature="+hex.EncodeToString(jimengMAC(key, toSign)))
}

type jimengEnvelope struct {
	Code int `json:"code"`
	Data *struct {
		TaskID string   `json:"task_id"`
		Status string   `json:"status"`
		Images []string `json:"binary_data_base64"`
		URLs   []string `json:"image_urls"`
	} `json:"data"`
}

func (p *JimengImageProvider) request(ctx context.Context, action string, body any) (jimengEnvelope, error) {
	var out jimengEnvelope
	data, err := json.Marshal(body)
	if err != nil || len(data) > 64<<10 {
		return out, providerFailure("provider_rejected")
	}
	q := urlpkg.Values{"Action": {action}, "Version": {"2022-08-31"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint+"/?"+q.Encode(), bytes.NewReader(data))
	if err != nil {
		return out, providerFailure("provider_error")
	}
	req.GetBody = nil
	signJimeng(req, data, p.accessKey, p.secretKey, p.now())
	resp, err := p.client.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return out, providerFailure("provider_timeout")
		}
		return out, providerFailure("provider_error")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		code := "provider_error"
		if resp.StatusCode == 401 || resp.StatusCode == 403 {
			code = "provider_auth_failed"
		} else if resp.StatusCode == 429 {
			code = "provider_rate_limited"
		} else if resp.StatusCode >= 400 && resp.StatusCode < 500 {
			code = "provider_rejected"
		}
		return out, providerFailure(code)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxProviderResponseBytes+1))
	if err != nil || len(raw) > maxProviderResponseBytes || json.Unmarshal(raw, &out) != nil {
		return out, providerFailure("invalid_provider_response")
	}
	if out.Code != 10000 {
		return out, providerFailure("provider_rejected")
	}
	if out.Data == nil {
		return out, providerFailure("invalid_provider_response")
	}
	return out, nil
}

var jimengTaskPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

func (p *JimengImageProvider) SubmitImage(ctx context.Context, in Input) (AsyncImageJob, error) {
	return p.submitImage(ctx, in, "")
}

// SetReferenceBridge is configured once before workers start. It never makes objects public.
func (p *JimengImageProvider) SetReferenceBridge(bridge ReferenceURLBridge) { p.bridge = bridge }
func (p *JimengImageProvider) SubmitImageWithReference(ctx context.Context, in Input, reference []byte, mime string) (AsyncImageJob, error) {
	if p.bridge == nil {
		return AsyncImageJob{}, providerFailure("reference_bridge_unavailable")
	}
	ref, err := nativeImage(reference, mime)
	if err != nil || len(reference) > 15<<20 || ref.Width > 4096 || ref.Height > 4096 {
		return AsyncImageJob{}, providerFailure("provider_rejected")
	}
	if !validCredential(p.accessKey) || !validCredential(p.secretKey) || !nativeModelPattern.MatchString(p.reqKey) {
		return AsyncImageJob{}, providerFailure("provider_unavailable")
	}
	in, err = Normalize(in)
	if err != nil {
		return AsyncImageJob{}, err
	}
	if len([]rune("Create one image. Style: "+in.Style+". Aspect ratio: "+in.Ratio+". Brief: "+in.Prompt)) > 800 {
		return AsyncImageJob{}, providerFailure("provider_rejected")
	}
	url, err := p.bridge.ReferenceURL(ctx, reference, mime)
	if err != nil {
		return AsyncImageJob{}, providerFailure("reference_bridge_unavailable")
	}
	u, err := urlpkg.Parse(url)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Fragment != "" {
		return AsyncImageJob{}, providerFailure("unsafe_media_url")
	}
	return p.submitImage(ctx, in, url)
}
func (p *JimengImageProvider) GenerateWithReference(ctx context.Context, in Input, reference []byte, mime string) (GenerationResult, error) {
	job, err := p.SubmitImageWithReference(ctx, in, reference, mime)
	if err != nil {
		return GenerationResult{}, err
	}
	return awaitAsyncImage(ctx, p, job.ID, p.pollInterval)
}
func (p *JimengImageProvider) submitImage(ctx context.Context, in Input, referenceURL string) (AsyncImageJob, error) {
	if !validCredential(p.accessKey) || !validCredential(p.secretKey) || !nativeModelPattern.MatchString(p.reqKey) {
		return AsyncImageJob{}, providerFailure("provider_unavailable")
	}
	in, err := Normalize(in)
	if err != nil {
		return AsyncImageJob{}, providerFailure("provider_rejected")
	}
	prompt := "Create one image. Style: " + in.Style + ". Aspect ratio: " + in.Ratio + ". Brief: " + in.Prompt
	if len([]rune(prompt)) > 800 {
		return AsyncImageJob{}, providerFailure("provider_rejected")
	}
	_, w, h := imageSize(in.Ratio)
	body := map[string]any{"req_key": p.reqKey, "prompt": prompt, "force_single": true, "width": w, "height": h}
	if referenceURL != "" {
		body["image_urls"] = []string{referenceURL}
	}
	out, err := p.request(ctx, "CVSync2AsyncSubmitTask", body)
	if err != nil {
		return AsyncImageJob{}, err
	}
	if !jimengTaskPattern.MatchString(out.Data.TaskID) {
		return AsyncImageJob{}, providerFailure("invalid_provider_response")
	}
	return AsyncImageJob{ID: out.Data.TaskID, Status: "queued"}, nil
}
func (p *JimengImageProvider) queryImage(ctx context.Context, id string) (jimengEnvelope, error) {
	if !jimengTaskPattern.MatchString(id) {
		return jimengEnvelope{}, providerFailure("provider_rejected")
	}
	return p.request(ctx, "CVSync2AsyncGetResult", map[string]string{"req_key": p.reqKey, "task_id": id, "req_json": `{"return_url":false,"logo_info":{"add_logo":true}}`})
}
func (p *JimengImageProvider) PollImage(ctx context.Context, id string) (AsyncImageJob, error) {
	out, err := p.queryImage(ctx, id)
	if err != nil {
		return AsyncImageJob{}, err
	}
	status := map[string]string{"done": "succeeded", "in_queue": "queued", "generating": "running", "failed": "failed", "not_found": "failed", "expired": "failed"}[out.Data.Status]
	if status == "" {
		return AsyncImageJob{}, providerFailure("invalid_provider_response")
	}
	return AsyncImageJob{ID: id, Status: status}, nil
}
func (p *JimengImageProvider) DownloadImage(ctx context.Context, id string) (GenerationResult, error) {
	out, err := p.queryImage(ctx, id)
	if err != nil {
		return GenerationResult{}, err
	}
	if out.Data.Status != "done" || len(out.Data.Images) != 1 || out.Data.Images[0] == "" || len(out.Data.Images[0]) > base64.StdEncoding.EncodedLen(maxPNGBytes) {
		return GenerationResult{}, providerFailure("invalid_provider_response")
	}
	raw, err := base64.StdEncoding.Strict().DecodeString(out.Data.Images[0])
	if err != nil {
		return GenerationResult{}, providerFailure("invalid_provider_response")
	}
	return nativeImage(raw, "")
}

// Generate is retained for direct protocol users. Durable application workers
// use the three-phase interface and persist the ACK before the first query.
func (p *JimengImageProvider) Generate(ctx context.Context, in Input) (GenerationResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	job, err := p.SubmitImage(ctx, in)
	if err != nil {
		return GenerationResult{}, err
	}
	return awaitAsyncImage(ctx, p, job.ID, p.pollInterval)
}
