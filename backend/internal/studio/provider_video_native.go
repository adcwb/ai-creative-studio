package studio

// Ark uses its native asynchronous task contract, not the OpenAI video API.
// Protocol reference: https://docs.volcengine.com/docs/ark/create-video-generation-task-api
// Production constructors never accept endpoints, resolvers, or download clients.
import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
	"time"
)

type unsupportedCreativeProvider struct{}

func (unsupportedCreativeProvider) Analyze(context.Context, ProjectInput) (BriefAnalysis, error) {
	return BriefAnalysis{}, providerFailure("unsupported_capability")
}
func (unsupportedCreativeProvider) Script(context.Context, ProjectInput) (Script, error) {
	return Script{}, providerFailure("unsupported_capability")
}
func (unsupportedCreativeProvider) Storyboard(context.Context, Script) ([]StoryboardShot, error) {
	return nil, providerFailure("unsupported_capability")
}
func (unsupportedCreativeProvider) Speech(context.Context, string) ([]byte, error) {
	return nil, providerFailure("unsupported_capability")
}
func (unsupportedCreativeProvider) SubmitVideo(context.Context, string) (VideoJob, error) {
	return VideoJob{}, providerFailure("unsupported_capability")
}
func (unsupportedCreativeProvider) PollVideo(context.Context, string) (VideoJob, error) {
	return VideoJob{}, providerFailure("unsupported_capability")
}
func (unsupportedCreativeProvider) DownloadVideo(context.Context, string) ([]byte, error) {
	return nil, providerFailure("unsupported_capability")
}

type ArkVideoProvider struct {
	unsupportedCreativeProvider
	api      *OpenAICreativeProvider
	model    string
	download func(context.Context, string) ([]byte, error)
}

func NewArkVideoProvider(key, model string) *ArkVideoProvider {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.Proxy = nil
	return newArkVideoProvider(key, model, "https://ark.cn-beijing.volces.com/api/v3", &http.Client{Transport: t, Timeout: 120 * time.Second}, safeMediaDownload)
}
func newArkVideoProvider(key, model, endpoint string, client *http.Client, download func(context.Context, string) ([]byte, error)) *ArkVideoProvider {
	return &ArkVideoProvider{api: newCreativeProvider(key, endpoint, client), model: model, download: download}
}

var arkVideoIDPattern = regexp.MustCompile(`^cgt-[A-Za-z0-9_-]{1,120}$`)

type arkVideoResponse struct {
	ID      string `json:"id"`
	Status  string `json:"status"`
	Content struct {
		VideoURL string `json:"video_url"`
	} `json:"content"`
}

func (p *ArkVideoProvider) SubmitVideo(ctx context.Context, prompt string) (VideoJob, error) {
	return p.submit(ctx, prompt, nil)
}

// A first frame is actual image conditioning, not a guarantee of identity or motion.
func (p *ArkVideoProvider) SubmitVideoWithImage(ctx context.Context, prompt string, data []byte, mime string) (VideoJob, error) {
	if len(data) > 10<<20 || (mime != "image/png" && mime != "image/jpeg") {
		return VideoJob{}, providerFailure("provider_rejected")
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width < 300 || cfg.Height < 300 || cfg.Width > 6000 || cfg.Height > 6000 || float64(cfg.Width)/float64(cfg.Height) < 0.4 || float64(cfg.Width)/float64(cfg.Height) > 2.5 || (format == "png" && mime != "image/png") || (format == "jpeg" && mime != "image/jpeg") {
		return VideoJob{}, providerFailure("provider_rejected")
	}
	ref := map[string]any{"type": "image_url", "image_url": map[string]string{"url": "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)}, "role": "first_frame"}
	return p.submit(ctx, prompt, ref)
}
func (p *ArkVideoProvider) submit(ctx context.Context, prompt string, reference map[string]any) (VideoJob, error) {
	if !configToken.MatchString(p.model) {
		return VideoJob{}, providerFailure("provider_unavailable")
	}
	prompt, err := normalizeProjectText(prompt, "video prompt", 3, 2000)
	if err != nil {
		return VideoJob{}, err
	}
	content := []map[string]any{{"type": "text", "text": prompt}}
	if reference != nil {
		content = append(content, reference)
	}
	seconds, resolution, ratio := 5, "720p", "16:9"
	// The reviewed mini candidate supports 480p. Bind the small-sample preset
	// to the exact model ID, retaining the existing contract for other models.
	if spec := videoCandidateSpecification("ark-video", p.model, reference != nil); spec != nil {
		seconds, resolution, ratio = spec.Seconds, spec.Resolution, spec.Ratio
	}
	body := map[string]any{"model": p.model, "content": content, "duration": seconds, "resolution": resolution, "ratio": ratio, "watermark": true}
	b, err := p.api.request(ctx, "POST", "/contents/generations/tasks", body, 64<<10)
	if err != nil {
		return VideoJob{}, err
	}
	var v arkVideoResponse
	if json.Unmarshal(b, &v) != nil || !arkVideoIDPattern.MatchString(v.ID) {
		return VideoJob{}, providerFailure("invalid_provider_response")
	}
	return VideoJob{ID: v.ID, Status: "queued"}, nil
}
func (p *ArkVideoProvider) get(ctx context.Context, id string) (arkVideoResponse, error) {
	var v arkVideoResponse
	if !arkVideoIDPattern.MatchString(id) {
		return v, providerFailure("provider_rejected")
	}
	b, err := p.api.request(ctx, "GET", "/contents/generations/tasks/"+id, nil, 64<<10)
	if err != nil {
		return v, err
	}
	if json.Unmarshal(b, &v) != nil || v.ID != id {
		return v, providerFailure("invalid_provider_response")
	}
	switch v.Status {
	case "queued", "running", "succeeded", "failed", "cancelled", "expired":
	default:
		return v, providerFailure("invalid_provider_response")
	}
	return v, nil
}
func (p *ArkVideoProvider) PollVideo(ctx context.Context, id string) (VideoJob, error) {
	v, err := p.get(ctx, id)
	if err != nil {
		return VideoJob{}, err
	}
	status := map[string]string{"queued": "queued", "running": "in_progress", "succeeded": "completed", "failed": "failed", "cancelled": "failed", "expired": "failed"}[v.Status]
	progress := 0
	if status == "in_progress" {
		progress = 50
	} else if status == "completed" || status == "failed" {
		progress = 100
	}
	return VideoJob{ID: id, Status: status, Progress: progress}, nil
}
func (p *ArkVideoProvider) DownloadVideo(ctx context.Context, id string) ([]byte, error) {
	v, err := p.get(ctx, id)
	if err != nil {
		return nil, err
	}
	if v.Status != "succeeded" || v.Content.VideoURL == "" {
		return nil, providerFailure("provider_rejected")
	}
	b, err := p.download(ctx, v.Content.VideoURL)
	if err != nil {
		return nil, err
	}
	if len(b) < 12 || string(b[4:8]) != "ftyp" {
		return nil, providerFailure("invalid_provider_response")
	}
	return b, nil
}

// DELETE on a terminal task deletes its record. Never perform it for cancellation.
// A queued->running race may still be rejected remotely; callers must retain the
// remote task ID and disclose that stopping local polling does not stop billing.
func (p *ArkVideoProvider) CancelVideo(ctx context.Context, id string) error {
	v, err := p.get(ctx, id)
	if err != nil {
		return err
	}
	if v.Status != "queued" {
		return providerFailure("remote_cancel_unavailable")
	}
	_, err = p.api.request(ctx, "DELETE", "/contents/generations/tasks/"+id, nil, 64<<10)
	return err
}

// Only globally routable unicast destinations can be downloaded. Reject mixed
// public/private DNS answers rather than choosing the convenient public answer.
var blockedDownloadNetworks = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("100.64.0.0/10"), netip.MustParsePrefix("127.0.0.0/8"), netip.MustParsePrefix("169.254.0.0/16"), netip.MustParsePrefix("192.0.0.0/24"), netip.MustParsePrefix("192.0.2.0/24"), netip.MustParsePrefix("192.88.99.0/24"), netip.MustParsePrefix("198.18.0.0/15"), netip.MustParsePrefix("198.51.100.0/24"), netip.MustParsePrefix("203.0.113.0/24"), netip.MustParsePrefix("224.0.0.0/3"), netip.MustParsePrefix("::/96"), netip.MustParsePrefix("64:ff9b::/96"), netip.MustParsePrefix("64:ff9b:1::/48"), netip.MustParsePrefix("100::/64"), netip.MustParsePrefix("2001::/23"), netip.MustParsePrefix("2001:db8::/32"), netip.MustParsePrefix("2002::/16"), netip.MustParsePrefix("3fff::/20"),
}

func publicDownloadIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsValid() || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.Zone() != "" {
		return false
	}
	for _, p := range blockedDownloadNetworks {
		if p.Contains(ip) {
			return false
		}
	}
	return true
}

type mediaIPLookup func(context.Context, string, string) ([]netip.Addr, error)

func downloadDestination(ctx context.Context, raw string, lookup mediaIPLookup) (*url.URL, netip.Addr, error) {
	u, err := url.Parse(raw)
	if err != nil || len(raw) > 8192 || u.Scheme != "https" || u.User != nil || u.Hostname() == "" || u.Fragment != "" || (u.Port() != "" && u.Port() != "443") || strings.ContainsAny(u.Hostname(), "%\\") {
		return nil, netip.Addr{}, providerFailure("unsafe_media_url")
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".internal") {
		return nil, netip.Addr{}, providerFailure("unsafe_media_url")
	}
	ips, err := lookup(ctx, "ip", host)
	if err != nil || len(ips) == 0 {
		return nil, netip.Addr{}, providerFailure("unsafe_media_url")
	}
	for _, ip := range ips {
		if !publicDownloadIP(ip) {
			return nil, netip.Addr{}, providerFailure("unsafe_media_url")
		}
	}
	return u, ips[0].Unmap(), nil
}
func pinnedDownloadTransport(ip netip.Addr, dial func(context.Context, string, string) (net.Conn, error)) *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.Proxy = nil
	t.DisableKeepAlives = true
	t.ResponseHeaderTimeout = 15 * time.Second
	t.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		return dial(ctx, "tcp", net.JoinHostPort(ip.String(), "443"))
	}
	return t
}
func safeMediaDownload(ctx context.Context, raw string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	u, ip, err := downloadDestination(ctx, raw, net.DefaultResolver.LookupNetIP)
	if err != nil {
		return nil, err
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	transport := pinnedDownloadTransport(ip, dialer.DialContext)
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 90 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if err != nil {
		return nil, providerFailure("unsafe_media_url")
	}
	response, err := client.Do(req)
	if err != nil {
		return nil, providerFailure("media_download_failed")
	}
	defer response.Body.Close()
	if response.StatusCode != 200 || response.ContentLength > 32<<20 {
		return nil, providerFailure("invalid_provider_response")
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, (32<<20)+1))
	if err != nil || len(data) > 32<<20 {
		return nil, providerFailure("invalid_provider_response")
	}
	return data, nil
}
