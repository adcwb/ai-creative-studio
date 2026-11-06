package application

// Doubao speech uses the separate openspeech native protocol, never Ark auth.
// https://docs.volcengine.com/docs/DoubaoVoice/unidirectional-streaming-text-to-speech-http?lang=zh
import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
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
	var out bytes.Buffer
	if e := p.SpeechTo(ctx, text, &out); e != nil {
		return nil, e
	}
	return out.Bytes(), nil
}
func (p *ByteDanceSpeechProvider) SpeechTo(ctx context.Context, text string, dst io.Writer) error {
	if !validCredential(p.key) || !configToken.MatchString(p.resource) || !configToken.MatchString(p.voice) {
		return providerFailure("invalid_configuration")
	}
	text, err := normalizeProjectText(text, "配音文本", 1, 2000)
	if err != nil {
		return err
	}
	id, err := newProjectID()
	if err != nil {
		return providerFailure("provider_error")
	}
	uuid := id[:8] + "-" + id[8:12] + "-" + id[12:16] + "-" + id[16:20] + "-" + id[20:]
	body, _ := json.Marshal(map[string]any{"req_params": map[string]any{"text": text, "speaker": p.voice, "audio_params": map[string]any{"format": "wav", "sample_rate": 24000}}})
	req, err := http.NewRequestWithContext(ctx, "POST", p.endpoint, bytes.NewReader(body))
	if err != nil {
		return providerFailure("provider_error")
	}
	req.GetBody = nil
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Api-Key", p.key)
	req.Header.Set("X-Api-Resource-Id", p.resource)
	req.Header.Set("X-Api-Request-Id", uuid)
	res, err := libraryGuardedRequest(p.client, req)
	if err != nil {
		return providerFailure("provider_error")
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		if res.StatusCode == 429 {
			return providerFailure("provider_rate_limited")
		}
		return providerFailure("provider_rejected")
	}
	parser := newNativeJSONParser(ctx, res.Body, "base64", func(path []string) bool { return mediaPath(path, "data") })
	// Keep all chunks private until the final frame validates, including late errors.
	file, err := os.CreateTemp("", "studio-native-audio-*")
	if err != nil {
		return err
	}
	defer func() { name := file.Name(); file.Close(); os.Remove(name) }()
	for {
		media, e := parser.next(false)
		if e == io.EOF {
			break
		}
		if e != nil {
			return nativeResponseError(e)
		}
		var chunk struct {
			Code *int   `json:"code"`
			Data string `json:"data"`
		}
		if json.Unmarshal(media.metadata, &chunk) != nil || chunk.Code == nil || *chunk.Code != 0 {
			media.close()
			return providerFailure("invalid_provider_response")
		}
		if part, ok := media.parts[chunk.Data]; ok && part[1] > 0 {
			e = media.copy(ctx, chunk.Data, file)
		}
		media.close()
		if e != nil {
			return e
		}
	}
	info, err := file.Stat()
	if err != nil {
		return err
	}
	media := nativeJSONMedia{file: file, parts: map[string][2]int64{"audio": {0, info.Size()}}}
	return media.copyAudio(ctx, "audio", dst, "wav")
}
