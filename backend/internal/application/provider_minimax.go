package application

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// MiniMaxSpeechProvider implements the documented synchronous native T2A API.
// It requests inline WAV bytes; provider URLs and subtitle URLs are never fetched.
// https://platform.minimax.io/docs/api-reference/speech-t2a-http
type MiniMaxSpeechProvider struct {
	transport    *OpenAICreativeProvider
	model, voice string
}

var configToken = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,127}$`)

func NewMiniMaxSpeechProvider(key, model, voice string) *MiniMaxSpeechProvider {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.Proxy = nil
	return &MiniMaxSpeechProvider{transport: newCreativeProvider(key, "https://api.minimax.io/v1", &http.Client{Transport: t, Timeout: 120 * time.Second}), model: model, voice: voice}
}
func (p *MiniMaxSpeechProvider) ProviderName() string { return "minimax-speech" }
func (p *MiniMaxSpeechProvider) Speech(ctx context.Context, text string) ([]byte, error) {
	var out bytes.Buffer
	if e := p.SpeechTo(ctx, text, &out); e != nil {
		return nil, e
	}
	return out.Bytes(), nil
}
func (p *MiniMaxSpeechProvider) SpeechTo(ctx context.Context, text string, dst io.Writer) error {
	text = strings.TrimSpace(text)
	if !configToken.MatchString(p.model) || !configToken.MatchString(p.voice) {
		return providerFailure("invalid_configuration")
	}
	if _, err := normalizeProjectText(text, "配音文本", 1, 2000); err != nil {
		return err
	}
	res, err := p.transport.requestNativeResponse(ctx, "/t2a_v2", map[string]any{"model": p.model, "text": text, "stream": false, "output_format": "hex", "voice_setting": map[string]any{"voice_id": p.voice, "speed": 1, "vol": 1, "pitch": 0}, "audio_setting": map[string]any{"sample_rate": 32000, "format": "wav", "channel": 1}})
	if err != nil {
		return err
	}
	defer res.Body.Close()
	media, err := newNativeJSONParser(ctx, res.Body, "hex", func(path []string) bool { return mediaPath(path, "data", "audio") }).next(true)
	if err != nil {
		return nativeResponseError(err)
	}
	defer media.close()
	b := media.metadata
	var out struct {
		Data *struct {
			Audio  string `json:"audio"`
			Status int    `json:"status"`
		} `json:"data"`
		Base *struct {
			Code *int `json:"status_code"`
		} `json:"base_resp"`
	}
	if json.Unmarshal(b, &out) != nil || out.Base == nil || out.Base.Code == nil || *out.Base.Code != 0 || out.Data == nil || out.Data.Status != 2 {
		return providerFailure("invalid_provider_response")
	}
	return media.copyAudio(ctx, out.Data.Audio, dst, "wav")
}

func (p *MiniMaxSpeechProvider) Analyze(context.Context, ProjectInput) (BriefAnalysis, error) {
	return BriefAnalysis{}, providerFailure("unsupported_capability")
}
func (p *MiniMaxSpeechProvider) Script(context.Context, ProjectInput) (Script, error) {
	return Script{}, providerFailure("unsupported_capability")
}
func (p *MiniMaxSpeechProvider) Storyboard(context.Context, Script) ([]StoryboardShot, error) {
	return nil, providerFailure("unsupported_capability")
}
func (p *MiniMaxSpeechProvider) SubmitVideo(context.Context, string) (VideoJob, error) {
	return VideoJob{}, providerFailure("unsupported_capability")
}
func (p *MiniMaxSpeechProvider) PollVideo(context.Context, string) (VideoJob, error) {
	return VideoJob{}, providerFailure("unsupported_capability")
}
func (p *MiniMaxSpeechProvider) DownloadVideo(context.Context, string) ([]byte, error) {
	return nil, providerFailure("unsupported_capability")
}
