package application

import (
	"context"
	"net/http"
	"os"
	"strings"
	"time"
)

type ProviderRegistration struct {
	ID            string            `json:"id"`
	Vendor        string            `json:"vendor"`
	Protocol      string            `json:"protocol"`
	Capabilities  map[string]string `json:"capabilities"`
	Notes         string            `json:"notes"`
	Documentation string            `json:"documentation"`
}

// Values deliberately distinguish code availability from vendor capabilities.
// No registry entry asserts successful authentication, generation or pricing.
func CreativeRegistry() []ProviderRegistration {
	return []ProviderRegistration{
		{"openai", "OpenAI", "native", map[string]string{"text": "adapter", "vision": "adapter", "image": "adapter", "image_edit": "mask_adapter", "video": "retired_2026_09_24", "speech": "adapter"}, "Videos API已关闭且无替代；旧gpt-4o-mini-tts计划2027-01-06关闭；显式模型与权限配置，真实效果和费用未验证。", "https://platform.openai.com/docs/api-reference"},
		{"anthropic", "Anthropic / Claude", "native-messages", map[string]string{"text": "adapter", "vision": "adapter", "image": "unsupported", "image_edit": "unsupported", "video": "unsupported", "speech": "unsupported"}, "Claude 是 Anthropic 模型家族；支持文本及图片理解，不提供原生图像、视频或语音生成。", "https://platform.claude.com/docs/en/api/messages"},
		{"google", "Google / Gemini / Nano Banana", "native-generateContent/native-interactions", map[string]string{"text": "adapter", "vision": "adapter", "image": "adapter", "image_edit": "reference_adapter", "video": "first_frame_and_edit_adapter", "speech": "adapter"}, "Nano Banana 属于 Gemini 图像生态，并非独立供应商；图像可选generateContent或独立Interactions适配；语音与视频使用Interactions。Veo 旧 preview 已关停，不能视作默认视频模型。", "https://ai.google.dev/gemini-api/docs/image-generation"},
		{"ark", "字节 / 火山方舟 / Doubao / Seedream", "openai-compatible-chat/native-images/native-video", map[string]string{"text": "adapter", "vision": "adapter", "image": "adapter", "image_edit": "reference_adapter", "video": "adapter", "speech": "unsupported"}, "Ark API Key 与即梦签名协议不同；模型或接入点需账户开通。", "https://docs.volcengine.com/docs/ark/chat-api?lang=zh"},
		{"minimax", "MiniMax", "native-t2a/native-images/native-video-v2/openai-compatible-chat", map[string]string{"text": "adapter", "vision": "adapter", "image": "adapter", "image_edit": "reference_adapter", "video": "adapter", "speech": "adapter", "music": "existing_customer_adapter", "sound_effects": "unsupported"}, "同步原生语音返回内联WAV；无自动重试，不获取返回URL。", "https://platform.minimax.io/docs/api-reference/speech-t2a-http"},
		{"bytedance-speech", "字节 / 豆包语音", "native-streaming-tts", map[string]string{"speech": "adapter"}, "独立X-Api-Key和资源ID，不沿用Ark认证；仅显式配置音色。", "https://docs.volcengine.com/docs/DoubaoVoice/unidirectional-streaming-text-to-speech-http?lang=zh"},
		{"jimeng", "即梦 / 火山引擎视觉服务", "volcengine-signed-cv", map[string]string{"text": "unsupported", "vision": "unsupported", "image": "adapter", "image_edit": "requires_controlled_object_bridge", "video": "legacy_api_retiring", "speech": "unsupported"}, "即梦消费产品与公开视觉API须区分；已实现独立签名图像协议，仍需账号授权；不能使用Ark Bearer冒充，任务ID落盘后可跨重启查询；参考图通过显式配置的短期签名对象桥接，默认关闭；视频旧版下线中不提供。", "https://docs.volcengine.com/docs/JimengAI/JimengAI-ImageGeneration46-InterfaceDocumentation?lang=zh"},
	}
}
func validCredential(key string) bool {
	if strings.TrimSpace(key) == "" {
		return false
	}
	for _, r := range key {
		if r < 33 || r > 126 {
			return false
		}
	}
	return true
}
func ConfigureCreativeProvider(name, key string) (CreativeProvider, Capabilities) {
	return configureCreative(name, key, os.Getenv("TEXT_MODEL"), os.Getenv("SPEECH_MODEL"), os.Getenv("VIDEO_MODEL"))
}
func ConfigureCreativeFromEnv() (CreativeProvider, Capabilities) {
	name := os.Getenv("TEXT_PROVIDER")
	keyName := map[string]string{"openai-text": "OPENAI_API_KEY", "anthropic-text": "ANTHROPIC_API_KEY", "gemini-text": "GEMINI_API_KEY", "ark-text": "ARK_API_KEY", "minimax-text": "MINIMAX_API_KEY"}[name]
	key := ""
	if keyName != "" {
		key = os.Getenv(keyName)
	}
	return ConfigureCreativeProvider(name, key)
}
func configureCreative(name, key, model, speechModel, videoModel string) (CreativeProvider, Capabilities) {
	if name == "" || name == "mock-text" {
		return nil, Capabilities{Provider: "mock-text", Mode: "mock", Ready: true, Message: "默认本地模拟，真实模型效果及费用未验证。"}
	}
	c := Capabilities{Provider: name, Mode: "real", Model: model, Message: "真实协议未配置，未验证效果或费用。"}
	if name != "openai-text" && name != "anthropic-text" && name != "gemini-text" && name != "ark-text" && name != "minimax-text" {
		c.Mode = "unavailable"
		c.Provider = "unavailable"
		c.ReasonCode = "unsupported_provider"
		return nil, c
	}
	if !validCredential(key) {
		c.ReasonCode = "missing_api_key"
		return nil, c
	}
	if !configToken.MatchString(model) {
		c.ReasonCode = "missing_or_invalid_model"
		return nil, c
	}
	var p CreativeProvider
	switch name {
	case "minimax-text":
		p = NewMiniMaxTextProvider(key, model)
	case "anthropic-text":
		p = NewAnthropicCreativeProvider(key, model)
	case "gemini-text":
		p = NewGeminiCreativeProvider(key, model)
	case "ark-text":
		t := http.DefaultTransport.(*http.Transport).Clone()
		t.Proxy = nil
		core := newCreativeProvider(key, "https://ark.cn-beijing.volces.com/api/v3", &http.Client{Transport: t, Timeout: 120 * time.Second})
		core.SetModels(model, "", "")
		p = &arkTextProvider{OpenAICreativeProvider: core}
	default:
		core := NewOpenAICreativeProvider(key)
		core.SetModels(model, speechModel, videoModel)
		p = core
	}
	c.Ready = true
	c.Message = "官方协议配置就绪不代表认证、模型权限或效果验证；可能计费且不自动重试。"
	return p, c
}

// Only text reuses the documented compatible envelope. Media endpoints must not
// accidentally inherit OpenAI paths on an Ark hostname.
type arkTextProvider struct{ *OpenAICreativeProvider }

func (p *arkTextProvider) ProviderName() string { return "ark-text" }
func (p *arkTextProvider) Analyze(ctx context.Context, in ProjectInput) (BriefAnalysis, error) {
	a, e := p.OpenAICreativeProvider.Analyze(ctx, in)
	a.Provider = p.ProviderName()
	return a, e
}
func (p *arkTextProvider) Speech(context.Context, string) ([]byte, error) {
	return nil, providerFailure("unsupported_capability")
}
func (p *arkTextProvider) SubmitVideo(context.Context, string) (VideoJob, error) {
	return VideoJob{}, providerFailure("unsupported_capability")
}
func (p *arkTextProvider) PollVideo(context.Context, string) (VideoJob, error) {
	return VideoJob{}, providerFailure("unsupported_capability")
}
func (p *arkTextProvider) DownloadVideo(context.Context, string) ([]byte, error) {
	return nil, providerFailure("unsupported_capability")
}
func ConfigureSpeechFromEnv() (CreativeProvider, Capabilities) {
	name := os.Getenv("SPEECH_PROVIDER")
	model := os.Getenv("SPEECH_MODEL")
	c := Capabilities{Provider: name, Model: model, Mode: "real", Message: "真实语音未配置/未验证。"}
	if name == "" || name == "mock-speech" {
		c.Provider = "unavailable"
		c.Mode = "unavailable"
		c.Ready = false
		c.Message = "未配置配音服务；可上传WAV/MP3，合成演示音轨不是语音。"
		return nil, c
	}
	if !configToken.MatchString(model) {
		c.ReasonCode = "missing_or_invalid_model"
		return nil, c
	}
	switch name {
	case "gemini-speech":
		key := os.Getenv("GEMINI_API_KEY")
		voice := os.Getenv("SPEECH_VOICE")
		if !validCredential(key) || !configToken.MatchString(voice) {
			c.ReasonCode = "missing_configuration"
			return nil, c
		}
		c.Ready = true
		return NewGoogleSpeechProvider(key, model, voice), c
	case "bytedance-speech":
		key := os.Getenv("BYTEDANCE_SPEECH_API_KEY")
		voice := os.Getenv("SPEECH_VOICE")
		if !validCredential(key) || !configToken.MatchString(voice) {
			c.ReasonCode = "missing_configuration"
			return nil, c
		}
		c.Ready = true
		return NewByteDanceSpeechProvider(key, model, voice), c
	case "minimax-speech":
		key := os.Getenv("MINIMAX_API_KEY")
		voice := os.Getenv("SPEECH_VOICE")
		if !validCredential(key) || !configToken.MatchString(voice) {
			c.ReasonCode = "missing_configuration"
			return nil, c
		}
		c.Ready = true
		return NewMiniMaxSpeechProvider(key, model, voice), c
	case "openai-speech":
		key := os.Getenv("OPENAI_API_KEY")
		if !validCredential(key) {
			c.ReasonCode = "missing_api_key"
			return nil, c
		}
		p := NewOpenAICreativeProvider(key)
		p.SetModels("", model, "")
		c.Ready = true
		return p, c
	default:
		c.Mode = "unavailable"
		c.Provider = "unavailable"
		c.ReasonCode = "unsupported_provider"
		return nil, c
	}
}
func ConfigureVideoFromEnv() (CreativeProvider, Capabilities) {
	name := os.Getenv("VIDEO_PROVIDER")
	model := os.Getenv("VIDEO_MODEL")
	c := Capabilities{Provider: name, Model: model, Mode: "real", Message: "真实视频效果和费用未验证；取消本地等待不保证供应商停止计费。"}
	if name == "" || name == "local-video" {
		c.Provider = "unavailable"
		c.Mode = "unavailable"
		c.Ready = false
		c.Message = "未配置视频模型；可上传MP4或用静帧在时间轴合成本地视频。"
		return nil, c
	}
	if name == "openai-video" {
		c.Mode = "unavailable"
		c.ReasonCode = "provider_retired"
		c.Message = "OpenAI Videos API已于2026-09-24关闭，无替代；历史记录保留，不会发出请求或切换供应商。"
		return nil, c
	}

	if name != "openai-video" && name != "ark-video" && name != "minimax-video" && name != "gemini-video" {
		c.Mode = "unavailable"
		c.Provider = "unavailable"
		c.ReasonCode = "unsupported_provider"
		return nil, c
	}
	if !configToken.MatchString(model) {
		c.ReasonCode = "missing_or_invalid_model"
		return nil, c
	}
	key := os.Getenv("OPENAI_API_KEY")
	if name == "ark-video" {
		key = os.Getenv("ARK_API_KEY")
	}
	if name == "minimax-video" {
		key = os.Getenv("MINIMAX_API_KEY")
	}
	if name == "gemini-video" {
		key = os.Getenv("GEMINI_API_KEY")
	}
	if !validCredential(key) {
		c.ReasonCode = "missing_api_key"
		return nil, c
	}
	if name == "ark-video" {
		c.Ready = true
		return NewArkVideoProvider(key, model), c
	}
	if name == "gemini-video" {
		c.Ready = true
		return NewGoogleVideoProvider(key, model), c
	}
	if name == "minimax-video" {
		c.Ready = true
		return NewMiniMaxVideoProvider(key, model), c
	}
	p := NewOpenAICreativeProvider(key)
	p.SetModels("", "", model)
	c.Ready = true
	return p, c
}
