package studio

// MiniMax native image v1 and asynchronous video v2 protocols. Models are explicit;
// no compatibility fallback or automatic submission retry is used.
import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"net/http"
	"regexp"
	"time"
)

type MiniMaxImageProvider struct {
	api   *OpenAICreativeProvider
	model string
}
type MiniMaxVideoProvider struct {
	unsupportedCreativeProvider
	api      *OpenAICreativeProvider
	model    string
	download func(context.Context, string) ([]byte, error)
}

func miniMaxMediaClient() *http.Client {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.Proxy = nil
	return &http.Client{Transport: t, Timeout: 120 * time.Second}
}
func NewMiniMaxImageProvider(key, model string) *MiniMaxImageProvider {
	return newMiniMaxImageProvider(key, model, "https://api.minimax.io", miniMaxMediaClient())
}
func newMiniMaxImageProvider(key, model, endpoint string, client *http.Client) *MiniMaxImageProvider {
	return &MiniMaxImageProvider{api: newCreativeProvider(key, endpoint, client), model: model}
}
func NewMiniMaxVideoProvider(key, model string) *MiniMaxVideoProvider {
	return newMiniMaxVideoProvider(key, model, "https://api.minimax.io", miniMaxMediaClient(), safeMediaDownload)
}
func newMiniMaxVideoProvider(key, model, endpoint string, client *http.Client, download func(context.Context, string) ([]byte, error)) *MiniMaxVideoProvider {
	return &MiniMaxVideoProvider{api: newCreativeProvider(key, endpoint, client), model: model, download: download}
}
func (p *MiniMaxVideoProvider) ProviderName() string { return "minimax-video" }
func (p *MiniMaxImageProvider) Generate(ctx context.Context, in Input) (GenerationResult, error) {
	return p.GenerateWithReference(ctx, in, nil, "")
}
func (p *MiniMaxImageProvider) GenerateWithReference(ctx context.Context, in Input, reference []byte, mime string) (GenerationResult, error) {
	if !validCredential(p.api.key) || !nativeModelPattern.MatchString(p.model) {
		return GenerationResult{}, providerFailure("provider_unavailable")
	}
	in, err := Normalize(in)
	if err != nil {
		return GenerationResult{}, providerFailure("provider_rejected")
	}
	prompt := "Create one image. Style: " + in.Style + ". Brief: " + in.Prompt
	if len([]rune(prompt)) > 1500 {
		return GenerationResult{}, providerFailure("provider_rejected")
	}
	body := map[string]any{"model": p.model, "prompt": prompt, "n": 1, "response_format": "base64", "prompt_optimizer": false}
	// MiniMax has no 4:5 aspect_ratio enum. Exact width/height is documented only
	// for image-01; never silently change the user's ratio for other models.
	if in.Ratio == "4:5" {
		if p.model != "image-01" {
			return GenerationResult{}, providerFailure("provider_rejected")
		}
		body["width"] = 1024
		body["height"] = 1280
	} else {
		body["aspect_ratio"] = in.Ratio
	}
	if len(reference) > 0 {
		if len(reference) > 10<<20 {
			return GenerationResult{}, providerFailure("provider_rejected")
		}
		if _, err = nativeImage(reference, mime); err != nil || mime == "" {
			return GenerationResult{}, providerFailure("provider_rejected")
		}
		body["subject_reference"] = []map[string]string{{"type": "character", "image_file": "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(reference)}}
	} else if mime != "" {
		return GenerationResult{}, providerFailure("provider_rejected")
	}
	raw, err := p.api.request(ctx, "POST", "/v1/image_generation", body, maxProviderResponseBytes)
	if err != nil {
		return GenerationResult{}, err
	}
	var out struct {
		Base *struct {
			Code *int `json:"status_code"`
		} `json:"base_resp"`
		Data *struct {
			Images []string `json:"image_base64"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &out) != nil || out.Base == nil || out.Base.Code == nil || *out.Base.Code != 0 || out.Data == nil || len(out.Data.Images) != 1 || out.Data.Images[0] == "" || len(out.Data.Images[0]) > base64.StdEncoding.EncodedLen(maxPNGBytes) {
		return GenerationResult{}, providerFailure("invalid_provider_response")
	}
	data, err := base64.StdEncoding.Strict().DecodeString(out.Data.Images[0])
	if err != nil {
		return GenerationResult{}, providerFailure("invalid_provider_response")
	}
	return nativeImage(data, "")
}

var miniMaxVideoIDPattern = regexp.MustCompile(`^[0-9]{1,64}$`)

func (p *MiniMaxVideoProvider) SubmitVideo(ctx context.Context, prompt string) (VideoJob, error) {
	return p.submit(ctx, prompt, nil)
}
func (p *MiniMaxVideoProvider) SubmitVideoWithImage(ctx context.Context, prompt string, data []byte, mime string) (VideoJob, error) {
	if len(data) > 10<<20 || (mime != "image/png" && mime != "image/jpeg") {
		return VideoJob{}, providerFailure("provider_rejected")
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width < 256 || cfg.Height < 256 || cfg.Width > 5760 || cfg.Height > 5760 || float64(cfg.Width)/float64(cfg.Height) < 0.4 || float64(cfg.Width)/float64(cfg.Height) > 2.5 || "image/"+format != mime {
		return VideoJob{}, providerFailure("provider_rejected")
	}
	if _, err := nativeImage(data, mime); err != nil {
		return VideoJob{}, providerFailure("provider_rejected")
	}
	ref := map[string]any{"type": "image_url", "role": "first_frame", "image_url": map[string]string{"url": "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)}}
	return p.submit(ctx, prompt, ref)
}
func (p *MiniMaxVideoProvider) submit(ctx context.Context, prompt string, reference map[string]any) (VideoJob, error) {
	if !validCredential(p.api.key) || !nativeModelPattern.MatchString(p.model) {
		return VideoJob{}, providerFailure("provider_unavailable")
	}
	prompt, err := normalizeProjectText(prompt, "video prompt", 3, 2000)
	if err != nil {
		return VideoJob{}, err
	}
	content := []map[string]any{{"type": "text", "text": prompt}}
	ratio := "16:9"
	if reference != nil {
		content = append(content, reference)
		ratio = "adaptive"
	}
	raw, err := p.api.request(ctx, "POST", "/v2/video_generation", map[string]any{"model": p.model, "content": content, "duration": 5, "resolution": "768P", "ratio": ratio}, 64<<10)
	if err != nil {
		return VideoJob{}, err
	}
	if err = miniMaxV2Envelope(raw); err != nil {
		return VideoJob{}, err
	}
	var out struct {
		ID string `json:"task_id"`
	}
	if json.Unmarshal(raw, &out) != nil || !miniMaxVideoIDPattern.MatchString(out.ID) {
		return VideoJob{}, providerFailure("invalid_provider_response")
	}
	return VideoJob{ID: out.ID, Status: "queued"}, nil
}

// V2 documents HTTP/error envelopes instead of base_resp. If a legacy business
// status is present, reject missing/nonzero values rather than treating HTTP 200
// as success. No upstream error text is propagated.
func miniMaxV2Envelope(raw []byte) error {
	var out struct {
		Type  string          `json:"type"`
		Error json.RawMessage `json:"error"`
		Base  json.RawMessage `json:"base_resp"`
	}
	if json.Unmarshal(raw, &out) != nil || out.Type == "error" || (len(out.Error) > 0 && string(out.Error) != "null") {
		return providerFailure("invalid_provider_response")
	}
	if len(out.Base) > 0 {
		var b struct {
			Code *int `json:"status_code"`
		}
		if json.Unmarshal(out.Base, &b) != nil || b.Code == nil || *b.Code != 0 {
			return providerFailure("invalid_provider_response")
		}
	}
	return nil
}

type miniMaxVideoTask struct {
	ID      string `json:"id"`
	Status  string `json:"status"`
	Content *struct {
		URL string `json:"url"`
	} `json:"content"`
}

func (p *MiniMaxVideoProvider) get(ctx context.Context, id string) (miniMaxVideoTask, error) {
	var zero miniMaxVideoTask
	if !miniMaxVideoIDPattern.MatchString(id) {
		return zero, providerFailure("provider_rejected")
	}
	raw, err := p.api.request(ctx, "GET", "/v2/query/video_generation/"+id, nil, 64<<10)
	if err != nil {
		return zero, err
	}
	if err = miniMaxV2Envelope(raw); err != nil {
		return zero, err
	}
	var out struct {
		Task *miniMaxVideoTask `json:"task"`
	}
	if json.Unmarshal(raw, &out) != nil || out.Task == nil || out.Task.ID != id {
		return zero, providerFailure("invalid_provider_response")
	}
	switch out.Task.Status {
	case "queued", "running", "succeeded", "failed", "cancelled":
	default:
		return zero, providerFailure("invalid_provider_response")
	}
	return *out.Task, nil
}
func (p *MiniMaxVideoProvider) PollVideo(ctx context.Context, id string) (VideoJob, error) {
	task, err := p.get(ctx, id)
	if err != nil {
		return VideoJob{}, err
	}
	status := map[string]string{"queued": "queued", "running": "in_progress", "succeeded": "completed", "failed": "failed", "cancelled": "failed"}[task.Status]
	progress := 0
	if status == "in_progress" {
		progress = 50
	} else if status == "completed" || status == "failed" {
		progress = 100
	}
	return VideoJob{ID: id, Status: status, Progress: progress}, nil
}
func (p *MiniMaxVideoProvider) DownloadVideo(ctx context.Context, id string) ([]byte, error) {
	task, err := p.get(ctx, id)
	if err != nil {
		return nil, err
	}
	if task.Status != "succeeded" || task.Content == nil || task.Content.URL == "" {
		return nil, providerFailure("provider_rejected")
	}
	data, err := p.download(ctx, task.Content.URL)
	if err != nil {
		return nil, err
	}
	if len(data) < 12 || len(data) > 32<<20 || string(data[4:8]) != "ftyp" {
		return nil, providerFailure("invalid_provider_response")
	}
	return data, nil
}

// DELETE also deletes terminal records. Only attempt cancellation after a queued
// read and only claim cancellation on the explicit cancelled response. The API
// has no conditional DELETE, so the queued->terminal race cannot be eliminated.
// This optional method is separate from stopping local polling.
func (p *MiniMaxVideoProvider) CancelVideo(ctx context.Context, id string) error {
	task, err := p.get(ctx, id)
	if err != nil {
		return err
	}
	if task.Status != "queued" {
		return providerFailure("remote_cancel_unavailable")
	}
	raw, err := p.api.request(ctx, "DELETE", "/v2/video_generation/"+id, nil, 64<<10)
	if err != nil {
		return err
	}
	if err = miniMaxV2Envelope(raw); err != nil {
		return err
	}
	var out struct {
		ID     string `json:"task_id"`
		Action string `json:"action"`
		Status string `json:"status"`
	}
	if json.Unmarshal(raw, &out) != nil || out.ID != id || out.Action != "cancelled" || out.Status != "cancelled" {
		return providerFailure("remote_cancel_unavailable")
	}
	return nil
}
