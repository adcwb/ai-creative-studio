package studio

// Doubao speech uses the separate openspeech native protocol, never Ark auth.
// https://docs.volcengine.com/docs/DoubaoVoice/unidirectional-streaming-text-to-speech-http?lang=zh
import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"time"
)

type ByteDanceSpeechProvider struct {
	unsupportedCreativeProvider
	key, resource, voice, endpoint string
	client                         *http.Client
}

func NewByteDanceSpeechProvider(key, resource, voice string) *ByteDanceSpeechProvider {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.Proxy = nil
	return &ByteDanceSpeechProvider{key: key, resource: resource, voice: voice, endpoint: "https://openspeech.bytedance.com/api/v3/tts/unidirectional", client: &http.Client{Transport: t, Timeout: 120 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}
func (p *ByteDanceSpeechProvider) ProviderName() string { return "bytedance-speech" }
func (p *ByteDanceSpeechProvider) Speech(ctx context.Context, text string) ([]byte, error) {
	if !validCredential(p.key) || !configToken.MatchString(p.resource) || !configToken.MatchString(p.voice) {
		return nil, providerFailure("invalid_configuration")
	}
	text, err := normalizeProjectText(text, "配音文本", 1, 2000)
	if err != nil {
		return nil, err
	}
	id, err := newProjectID()
	if err != nil {
		return nil, providerFailure("provider_error")
	}
	uuid := id[:8] + "-" + id[8:12] + "-" + id[12:16] + "-" + id[16:20] + "-" + id[20:]
	body, _ := json.Marshal(map[string]any{"req_params": map[string]any{"text": text, "speaker": p.voice, "audio_params": map[string]any{"format": "wav", "sample_rate": 24000}}})
	req, err := http.NewRequestWithContext(ctx, "POST", p.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, providerFailure("provider_error")
	}
	req.GetBody = nil
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Api-Key", p.key)
	req.Header.Set("X-Api-Resource-Id", p.resource)
	req.Header.Set("X-Api-Request-Id", uuid)
	res, err := p.client.Do(req)
	if err != nil {
		return nil, providerFailure("provider_error")
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		if res.StatusCode == 429 {
			return nil, providerFailure("provider_rate_limited")
		}
		return nil, providerFailure("provider_rejected")
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, 24<<20+1))
	if err != nil || len(raw) > 24<<20 {
		return nil, providerFailure("invalid_provider_response")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	audio := []byte{}
	chunks := 0
	for {
		var chunk struct {
			Code *int   `json:"code"`
			Data string `json:"data"`
		}
		err = decoder.Decode(&chunk)
		if err == io.EOF {
			break
		}
		if err != nil || chunk.Code == nil || *chunk.Code != 0 {
			return nil, providerFailure("invalid_provider_response")
		}
		chunks++
		if chunks > 4096 {
			return nil, providerFailure("invalid_provider_response")
		}
		if chunk.Data != "" {
			data, e := base64.StdEncoding.DecodeString(chunk.Data)
			if e != nil || len(audio)+len(data) > 16<<20 {
				return nil, providerFailure("invalid_provider_response")
			}
			audio = append(audio, data...)
		}
	}
	if len(audio) < 44 || string(audio[:4]) != "RIFF" || string(audio[8:12]) != "WAVE" {
		return nil, providerFailure("invalid_provider_response")
	}
	return audio, nil
}
