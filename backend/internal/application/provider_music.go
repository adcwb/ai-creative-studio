package application

// Official existing-customer music API. New accounts are not eligible according
// to https://platform.minimax.io/docs/pricing/overview (checked 2026-10-23).
import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"time"
)

type streamingMusicProvider interface {
	MusicTo(context.Context, string, io.Writer) error
}
type MusicProvider interface {
	Music(context.Context, string) ([]byte, error)
}
type MiniMaxMusicProvider struct {
	transport *OpenAICreativeProvider
	model     string
}

func NewMiniMaxMusicProvider(key, model string) *MiniMaxMusicProvider {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.Proxy = nil
	return &MiniMaxMusicProvider{transport: newCreativeProvider(key, "https://api.minimax.io/v1", &http.Client{Transport: t, Timeout: 180 * time.Second}), model: model}
}
func (p *MiniMaxMusicProvider) Music(ctx context.Context, prompt string) ([]byte, error) {
	var out bytes.Buffer
	if e := p.MusicTo(ctx, prompt, &out); e != nil {
		return nil, e
	}
	return out.Bytes(), nil
}
func (p *MiniMaxMusicProvider) MusicTo(ctx context.Context, prompt string, dst io.Writer) error {
	if p.model != "music-3.0" && p.model != "music-2.6" {
		return providerFailure("unsupported_music_model")
	}
	prompt, err := normalizeProjectText(prompt, "音乐描述", 3, 2000)
	if err != nil {
		return err
	}
	res, err := p.transport.requestNativeResponse(ctx, "/music_generation", map[string]any{"model": p.model, "prompt": prompt, "is_instrumental": true, "stream": false, "output_format": "hex", "audio_setting": map[string]any{"sample_rate": 44100, "bitrate": 128000, "format": "mp3"}})
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
	if json.Unmarshal(b, &out) != nil || out.Data == nil || out.Base == nil || out.Base.Code == nil || *out.Base.Code != 0 || out.Data.Status != 2 {
		return providerFailure("invalid_provider_response")
	}
	return media.copyAudio(ctx, out.Data.Audio, dst, "mp3")
}

func ConfigureMusicFromEnv() (MusicProvider, Capabilities) {
	name, model := os.Getenv("MUSIC_PROVIDER"), os.Getenv("MUSIC_MODEL")
	c := Capabilities{Provider: name, Mode: "real", Model: model, Message: "MiniMax纯音乐协议，仅已获服务权限的存量付费账户可用；真实效果与费用未验证。"}
	if name == "" {
		c.Provider = "unavailable"
		c.Mode = "unavailable"
		c.Message = "未配置音乐生成，可上传音乐与音效。"
		return nil, c
	}
	if name != "minimax-music" {
		c.ReasonCode = "unsupported_provider"
		return nil, c
	}
	if os.Getenv("MUSIC_EXISTING_ACCOUNT_ACK") != "true" {
		c.ReasonCode = "requires_existing_account_entitlement"
		return nil, c
	}
	if model != "music-3.0" && model != "music-2.6" {
		c.ReasonCode = "unsupported_music_model"
		return nil, c
	}
	key := os.Getenv("MINIMAX_API_KEY")
	if !validCredential(key) {
		c.ReasonCode = "missing_api_key"
		return nil, c
	}
	c.Ready = true
	return NewMiniMaxMusicProvider(key, model), c
}
func SetCreativeMusic(s *FileStore, p MusicProvider, caps Capabilities) {
	s.creative.music = p
	s.creative.musicCaps = caps
}
