package studio

// Current native Interactions contracts, not legacy Veo/PCM examples:
// https://ai.google.dev/gemini-api/docs/omni
// https://ai.google.dev/gemini-api/docs/speech-generation
// Raw REST returns steps[].content[], NOT SDK output_audio/output_video helpers.
import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

type GoogleMediaProvider struct {
	unsupportedCreativeProvider
	kind, key, model, voice, endpoint string
	client                            *http.Client
	download                          func(context.Context, string) ([]byte, error)
}

func NewGoogleSpeechProvider(key, model, voice string) *GoogleMediaProvider {
	return newGoogleMediaProvider("gemini-speech", key, model, voice, "https://generativelanguage.googleapis.com", nil, nil)
}
func NewGoogleVideoProvider(key, model string) *GoogleMediaProvider {
	return newGoogleMediaProvider("gemini-video", key, model, "", "https://generativelanguage.googleapis.com", nil, nil)
}
func newGoogleMediaProvider(kind, key, model, voice, endpoint string, client *http.Client, download func(context.Context, string) ([]byte, error)) *GoogleMediaProvider {
	if client == nil {
		t := http.DefaultTransport.(*http.Transport).Clone()
		t.Proxy = nil
		client = &http.Client{Transport: t, Timeout: 120 * time.Second}
	}
	c := *client
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if download == nil {
		download = safeMediaDownload
	}
	return &GoogleMediaProvider{kind: kind, key: key, model: model, voice: voice, endpoint: endpoint, client: &c, download: download}
}
func (p *GoogleMediaProvider) ProviderName() string { return p.kind }
func (p *GoogleMediaProvider) configured() bool {
	return validCredential(p.key) && nativeModelPattern.MatchString(p.model)
}
func (p *GoogleMediaProvider) request(ctx context.Context, method, path string, body any) (*http.Response, error) {
	return p.requestLimit(ctx, method, path, body, 64<<10)
}
func (p *GoogleMediaProvider) requestLimit(ctx context.Context, method, path string, body any, maxRequest int) (*http.Response, error) {
	if !p.configured() {
		return nil, providerFailure("provider_unavailable")
	}
	var data []byte
	var err error
	if body != nil {
		data, err = json.Marshal(body)
		if err != nil || len(data) > maxRequest {
			return nil, providerFailure("provider_rejected")
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, p.endpoint+path, bytes.NewReader(data))
	if err != nil {
		return nil, providerFailure("provider_error")
	}
	req.Header.Set("x-goog-api-key", p.key)
	req.Header.Set("Content-Type", "application/json")
	req.GetBody = nil
	res, err := p.client.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, providerFailure("provider_timeout")
		}
		return nil, providerFailure("provider_error")
	}
	return res, nil
}
func googleMediaStatus(code int) error {
	switch code {
	case 401, 403:
		return providerFailure("provider_auth_failed")
	case 429:
		return providerFailure("provider_rate_limited")
	case 400, 404, 422:
		return providerFailure("provider_rejected")
	default:
		return providerFailure("provider_error")
	}
}
func googleMediaRead(res *http.Response, max int) ([]byte, error) {
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, googleMediaStatus(res.StatusCode)
	}
	if res.ContentLength > int64(max) {
		return nil, providerFailure("invalid_provider_response")
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, int64(max)+1))
	if err != nil || len(b) > max {
		return nil, providerFailure("invalid_provider_response")
	}
	return b, nil
}

type googleMediaBlock struct {
	Type string `json:"type"`
	MIME string `json:"mime_type"`
	Data string `json:"data"`
	URI  string `json:"uri"`
}
type googleInteraction struct {
	Status string `json:"status"`
	Steps  []struct {
		Type    string             `json:"type"`
		Content []googleMediaBlock `json:"content"`
	} `json:"steps"`
}

func googleOutput(b []byte, kind string) (googleMediaBlock, error) {
	var out googleInteraction
	if json.Unmarshal(b, &out) != nil || out.Status != "completed" {
		return googleMediaBlock{}, providerFailure("invalid_provider_response")
	}
	var found *googleMediaBlock
	for _, step := range out.Steps {
		if step.Type != "model_output" {
			continue
		}
		for _, block := range step.Content {
			if block.Type == kind {
				if found != nil {
					return googleMediaBlock{}, providerFailure("invalid_provider_response")
				}
				v := block
				found = &v
			}
		}
	}
	if found == nil {
		return googleMediaBlock{}, providerFailure("invalid_provider_response")
	}
	return *found, nil
}
func (p *GoogleMediaProvider) Speech(ctx context.Context, text string) ([]byte, error) {
	if p.kind != "gemini-speech" {
		return nil, providerFailure("unsupported_capability")
	}
	if !nativeModelPattern.MatchString(p.voice) {
		return nil, providerFailure("provider_unavailable")
	}
	text, err := normalizeProjectText(text, "speech", 1, 2000)
	if err != nil {
		return nil, err
	}
	body := map[string]any{"model": p.model, "input": []map[string]any{{"type": "user_input", "content": []map[string]string{{"type": "text", "text": text}}}}, "response_format": map[string]any{"type": "audio", "mime_type": "audio/wav", "sample_rate": 24000}, "generation_config": map[string]any{"speech_config": []map[string]string{{"voice": p.voice}}}}
	res, err := p.request(ctx, "POST", "/v1beta/interactions", body)
	if err != nil {
		return nil, err
	}
	b, err := googleMediaRead(res, 16<<20)
	if err != nil {
		return nil, err
	}
	block, err := googleOutput(b, "audio")
	if err != nil {
		return nil, err
	}
	if block.MIME != "audio/wav" || block.URI != "" || len(block.Data) > base64.StdEncoding.EncodedLen(10<<20) {
		return nil, providerFailure("invalid_provider_response")
	}
	audio, err := base64.StdEncoding.Strict().DecodeString(block.Data)
	if err != nil || !googleWAV(audio) {
		return nil, providerFailure("invalid_provider_response")
	}
	return audio, nil
}

// Require the requested unary 24 kHz mono PCM16 WAV, never wrap unknown PCM as WAV.
func googleWAV(b []byte) bool {
	if len(b) < 44 || len(b) > 10<<20 || string(b[:4]) != "RIFF" || string(b[8:12]) != "WAVE" || uint64(binary.LittleEndian.Uint32(b[4:8]))+8 != uint64(len(b)) {
		return false
	}
	format, data := false, false
	for at := 12; at < len(b); {
		if len(b)-at < 8 {
			return false
		}
		name := string(b[at : at+4])
		n := uint64(binary.LittleEndian.Uint32(b[at+4 : at+8]))
		start := at + 8
		if n > uint64(len(b)-start) {
			return false
		}
		chunk := b[start : start+int(n)]
		if name == "fmt " {
			if format || len(chunk) < 16 || binary.LittleEndian.Uint16(chunk[:2]) != 1 || binary.LittleEndian.Uint16(chunk[2:4]) != 1 || binary.LittleEndian.Uint32(chunk[4:8]) != 24000 || binary.LittleEndian.Uint32(chunk[8:12]) != 48000 || binary.LittleEndian.Uint16(chunk[12:14]) != 2 || binary.LittleEndian.Uint16(chunk[14:16]) != 16 {
				return false
			}
			format = true
		}
		if name == "data" {
			if data || n == 0 || n%2 != 0 || n > 180*48000 {
				return false
			}
			data = true
		}
		at = start + int(n) + int(n%2)
		if at > len(b) {
			return false
		}
	}
	return format && data
}

var googleFileID = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,38}[a-z0-9])?$`)

func googleVideoFileID(raw string) (string, error) {
	path := raw
	if strings.HasPrefix(raw, "https://") {
		u, err := url.Parse(raw)
		if err != nil || u.Scheme != "https" || u.Host != "generativelanguage.googleapis.com" || u.User != nil || u.Fragment != "" || u.RawPath != "" {
			return "", providerFailure("unsafe_media_url")
		}
		if u.RawQuery != "" && u.RawQuery != "alt=media" {
			return "", providerFailure("unsafe_media_url")
		}
		path = strings.TrimPrefix(u.Path, "/v1beta/")
	}
	path = strings.TrimSuffix(path, ":download")
	if !strings.HasPrefix(path, "files/") {
		return "", providerFailure("unsafe_media_url")
	}
	id := strings.TrimPrefix(path, "files/")
	if !googleFileID.MatchString(id) {
		return "", providerFailure("unsafe_media_url")
	}
	return id, nil
}
func (p *GoogleMediaProvider) SubmitVideo(ctx context.Context, prompt string) (VideoJob, error) {
	if p.kind != "gemini-video" {
		return VideoJob{}, providerFailure("unsupported_capability")
	}
	prompt, err := normalizeProjectText(prompt, "video", 3, 2000)
	if err != nil {
		return VideoJob{}, err
	}
	return p.submitVideoInput(ctx, prompt)
}

// SubmitVideoWithImage uses the documented first-frame control, not a claim of identity preservation.
func (p *GoogleMediaProvider) SubmitVideoWithImage(ctx context.Context, prompt string, data []byte, mime string) (VideoJob, error) {
	if len(data) > 10<<20 || (mime != "image/png" && mime != "image/jpeg") {
		return VideoJob{}, providerFailure("provider_rejected")
	}
	if _, err := nativeImage(data, mime); err != nil {
		return VideoJob{}, providerFailure("provider_rejected")
	}
	return p.submitVideoReference(ctx, "[# Sources <FIRST_FRAME>@Image1] "+prompt+" Use this image as the starting frame.", data, mime, "image")
}

// SubmitVideoWithVideo edits an already validated local MP4. No caller-controlled remote URI is accepted.
func (p *GoogleMediaProvider) SubmitVideoWithVideo(ctx context.Context, prompt string, data []byte, mime string) (VideoJob, error) {
	if mime != "video/mp4" || len(data) < 12 || len(data) > 10<<20 || string(data[4:8]) != "ftyp" {
		return VideoJob{}, providerFailure("provider_rejected")
	}
	return p.submitVideoReference(ctx, "[# Sources <VIDEO_0>@Video1] "+prompt, data, mime, "video")
}
func (p *GoogleMediaProvider) submitVideoReference(ctx context.Context, prompt string, data []byte, mime, kind string) (VideoJob, error) {
	prompt, err := normalizeProjectText(prompt, "video", 3, 2200)
	if err != nil {
		return VideoJob{}, err
	}
	input := []map[string]any{{"type": "user_input", "content": []map[string]string{{"type": kind, "mime_type": mime, "data": base64.StdEncoding.EncodeToString(data)}, {"type": "text", "text": prompt}}}}
	return p.submitVideoInput(ctx, input)
}
func (p *GoogleMediaProvider) submitVideoInput(ctx context.Context, input any) (VideoJob, error) {
	if p.kind != "gemini-video" {
		return VideoJob{}, providerFailure("unsupported_capability")
	}
	body := map[string]any{"model": p.model, "input": input, "response_format": map[string]string{"type": "video", "delivery": "uri", "resolution": "720p", "aspect_ratio": "16:9"}, "background": false, "store": false, "stream": false}
	res, err := p.requestLimit(ctx, "POST", "/v1beta/interactions", body, 15<<20)
	if err != nil {
		return VideoJob{}, err
	}
	b, err := googleMediaRead(res, 64<<10)
	if err != nil {
		return VideoJob{}, err
	}
	block, err := googleOutput(b, "video")
	if err != nil {
		return VideoJob{}, err
	}
	if block.MIME != "video/mp4" || block.Data != "" {
		return VideoJob{}, providerFailure("invalid_provider_response")
	}
	id, err := googleVideoFileID(block.URI)
	if err != nil {
		return VideoJob{}, err
	}
	// Persist the Files ID: GET interactions may return inline data and lose URI.
	return VideoJob{ID: id, Status: "queued"}, nil
}
func (p *GoogleMediaProvider) PollVideo(ctx context.Context, id string) (VideoJob, error) {
	if p.kind != "gemini-video" {
		return VideoJob{}, providerFailure("unsupported_capability")
	}
	if !googleFileID.MatchString(id) {
		return VideoJob{}, providerFailure("provider_rejected")
	}
	res, err := p.request(ctx, "GET", "/v1beta/files/"+id, nil)
	if err != nil {
		return VideoJob{}, err
	}
	b, err := googleMediaRead(res, 64<<10)
	if err != nil {
		return VideoJob{}, err
	}
	var f struct {
		Name  string `json:"name"`
		State string `json:"state"`
		MIME  string `json:"mimeType"`
	}
	if json.Unmarshal(b, &f) != nil || f.Name != "files/"+id || (f.MIME != "" && f.MIME != "video/mp4") {
		return VideoJob{}, providerFailure("invalid_provider_response")
	}
	switch f.State {
	case "PROCESSING":
		return VideoJob{ID: id, Status: "in_progress", Progress: 50}, nil
	case "ACTIVE":
		return VideoJob{ID: id, Status: "completed", Progress: 100}, nil
	case "FAILED":
		return VideoJob{ID: id, Status: "failed", Progress: 100}, nil
	default:
		return VideoJob{}, providerFailure("invalid_provider_response")
	}
}
func (p *GoogleMediaProvider) DownloadVideo(ctx context.Context, id string) ([]byte, error) {
	job, err := p.PollVideo(ctx, id)
	if err != nil {
		return nil, err
	}
	if job.Status != "completed" {
		return nil, providerFailure("provider_rejected")
	}
	// Only this canonical Google origin receives the key; ignore arbitrary URI
	// fields in metadata. A Google redirect is downloaded without any credentials.
	res, err := p.request(ctx, "GET", "/v1beta/files/"+id+":download?alt=media", nil)
	if err != nil {
		return nil, err
	}
	var b []byte
	switch res.StatusCode {
	case 301, 302, 303, 307, 308:
		location := res.Header.Get("Location")
		res.Body.Close()
		b, err = p.download(ctx, location)
	default:
		b, err = googleMediaRead(res, 32<<20)
	}
	if err != nil {
		return nil, err
	}
	if len(b) < 12 || len(b) > 32<<20 || string(b[4:8]) != "ftyp" {
		return nil, providerFailure("invalid_provider_response")
	}
	return b, nil
}
