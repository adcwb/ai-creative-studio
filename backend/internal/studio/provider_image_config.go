package studio

import (
	"bytes"
	"image/png"
	"os"
)

func realImageProvider(name string) bool {
	return name == "openai-images" || name == "gemini-images" || name == "gemini-interactions-images" || name == "ark-images" || name == "jimeng-images" || name == "minimax-images"
}
func validateLargePNG(data []byte, width, height int) error {
	if len(data) == 0 || len(data) > maxPNGBytes || width <= 0 || height <= 0 || width > 20_000_000/height {
		return providerFailure("invalid_provider_response")
	}
	c, e := png.DecodeConfig(bytes.NewReader(data))
	if e != nil || c.Width != width || c.Height != height {
		return providerFailure("invalid_provider_response")
	}
	if _, e = png.Decode(bytes.NewReader(data)); e != nil {
		return providerFailure("invalid_provider_response")
	}
	return nil
}
func ConfigureImageFromEnv() (ImageProvider, Capabilities) {
	name := os.Getenv("IMAGE_PROVIDER")
	if name == "" || name == "mock-svg" {
		return ConfigureProvider(name, "")
	}
	model := os.Getenv("IMAGE_MODEL")
	if name == "jimeng-images" {
		model = os.Getenv("JIMENG_REQ_KEY")
	}
	c := Capabilities{Provider: name, Mode: "real", Model: model, Message: "真实图像协议尚未验证认证、效果或费用。"}
	if !realImageProvider(name) {
		c.Mode = "unavailable"
		c.Provider = "unavailable"
		c.ReasonCode = "unsupported_provider"
		return unavailableProvider{}, c
	}
	if !configToken.MatchString(model) {
		c.ReasonCode = "missing_or_invalid_model"
		return unavailableProvider{}, c
	}
	if name == "jimeng-images" {
		ak, sk := os.Getenv("JIMENG_ACCESS_KEY"), os.Getenv("JIMENG_SECRET_KEY")
		if !validCredential(ak) || !validCredential(sk) {
			c.ReasonCode = "missing_api_key"
			return unavailableProvider{}, c
		}
		c.Ready = true
		p := NewJimengImageProvider(ak, sk, model)
		bridge, err := ConfigureReferenceBridgeFromEnv()
		if err != nil {
			c.Ready = false
			c.ReasonCode = "invalid_reference_bridge"
			return unavailableProvider{}, c
		}
		if bridge != nil {
			p.SetReferenceBridge(bridge)
		}
		return p, c
	}
	keyName := map[string]string{"openai-images": "OPENAI_API_KEY", "gemini-images": "GEMINI_API_KEY", "gemini-interactions-images": "GEMINI_API_KEY", "ark-images": "ARK_API_KEY", "minimax-images": "MINIMAX_API_KEY"}[name]
	key := os.Getenv(keyName)
	if !validCredential(key) {
		c.ReasonCode = "missing_api_key"
		return unavailableProvider{}, c
	}
	c.Ready = true
	switch name {
	case "minimax-images":
		return NewMiniMaxImageProvider(key, model), c
	case "gemini-interactions-images":
		return NewGoogleInteractionsImageProvider(key, model), c
	case "gemini-images":
		return NewGeminiImageProvider(key, model), c
	case "ark-images":
		return NewArkImageProvider(key, model), c
	default:
		p := NewOpenAIProvider(key)
		p.model = model
		return p, c
	}
}
