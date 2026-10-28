package application

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image/png"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	OpenAIModel              = "gpt-image-2-2026-04-21"
	openAIEndpoint           = "https://api.openai.com/v1/images/generations"
	maxProviderResponseBytes = 24 << 20
	maxPNGBytes              = 16 << 20
	maxImagePixels           = 2_000_000
)

func mockCapabilities() Capabilities {
	return Capabilities{Provider: "mock-svg", Mode: "mock", Ready: true, Message: "模拟模式：程序绘制 SVG，不调用 AI 模型或产生模型费用。"}
}

// ConfigureProvider validates local settings without probing the upstream API.
// It intentionally accepts explicit values so tests never need host credentials.
func ConfigureProvider(name, key string) (ImageProvider, Capabilities) {
	if name == "" || name == "mock-svg" {
		return MockProvider{Delay: 900 * time.Millisecond}, mockCapabilities()
	}
	if name != "openai-images" {
		caps := Capabilities{Provider: "unavailable", Mode: "unavailable", ReasonCode: "invalid_provider", Message: "生成服务配置无效，历史记录仍可查看。"}
		return unavailableProvider{}, caps
	}
	caps := Capabilities{Provider: "openai-images", Mode: "real", Model: OpenAIModel}
	if strings.TrimSpace(key) == "" {
		caps.ReasonCode, caps.Message = "missing_api_key", "缺少服务端 API 密钥，历史记录仍可查看。"
		return unavailableProvider{}, caps
	}
	for _, char := range key {
		if char < 33 || char > 126 {
			caps.ReasonCode, caps.Message = "invalid_configuration", "生成服务配置无效，历史记录仍可查看。"
			return unavailableProvider{}, caps
		}
	}
	caps.Ready = true
	caps.Message = "真实图像模式：本地配置已就绪，未验证密钥或额度；提交可能产生费用。"
	return NewOpenAIProvider(key), caps
}

type unavailableProvider struct{}

func (unavailableProvider) Generate(context.Context, Input) (GenerationResult, error) {
	return GenerationResult{}, providerFailure("provider_unavailable")
}

// ProviderError only contains a safe code. Upstream bodies, headers, URLs and
// transport errors must never become task messages, logs, or wrapped errors.
type ProviderError struct{ Code string }

func (e *ProviderError) Error() string { return providerMessage(e.Code) }

func providerFailure(code string) error { return &ProviderError{Code: code} }

func providerMessage(code string) string {
	switch code {
	case "mock_failed":
		return "模拟流程失败；请移除 [mock-fail] 后编辑重试。"
	case "provider_auth_failed":
		return "图像服务鉴权失败，请检查服务端配置。"
	case "provider_rate_limited":
		return "图像服务限流或额度不足，请检查后再显式重试。"
	case "provider_rejected":
		return "图像服务拒绝了请求，请修改描述或检查服务端配置。"
	case "provider_timeout":
		return "图像请求超时，远端结果或费用可能不确定；未自动重试。"
	case "invalid_provider_response":
		return "图像服务未返回可用的 PNG，远端结果或费用可能不确定；未自动重试。"
	case "execution_interrupted":
		return "执行被中断，远端结果或费用可能不确定；未自动重试，请确认后再手动提交。"
	case "provider_unavailable":
		return "任务绑定的生成服务当前不可用；未发送生成请求，请检查配置后显式重试。"
	default:
		return "图像服务请求失败，远端结果或费用可能不确定；未自动重试。"
	}
}

func safeFailure(err error, provider string) (string, string) {
	code := "provider_error"
	var failure *ProviderError
	if errors.As(err, &failure) {
		switch failure.Code {
		case "mock_failed", "provider_auth_failed", "provider_rate_limited", "provider_rejected", "provider_error", "provider_timeout", "invalid_provider_response", "execution_interrupted", "provider_unavailable":
			code = failure.Code
		}
	} else if provider == "mock-svg" {
		code = "mock_failed"
	}
	return code, providerMessage(code)
}

type OpenAIProvider struct {
	key      string
	model    string
	endpoint string
	client   *http.Client
}

// NewOpenAIProvider is the production constructor. Its endpoint, timeout,
// redirect policy and proxy behavior cannot be overridden by environment.
func NewOpenAIProvider(key string) *OpenAIProvider {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	return newOpenAIProvider(key, openAIEndpoint, &http.Client{Transport: transport, Timeout: 120 * time.Second})
}

// Package tests inject a loopback httptest server here; no production setting
// or HTTP API can supply an endpoint or client.
func newOpenAIProvider(key, endpoint string, client *http.Client) *OpenAIProvider {
	copyClient := *client
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &OpenAIProvider{key: key, model: OpenAIModel, endpoint: endpoint, client: &copyClient}
}

func imageSize(ratio string) (string, int, int) {
	switch ratio {
	case "1:1":
		return "1024x1024", 1024, 1024
	case "16:9":
		return "1536x864", 1536, 864
	default:
		return "1024x1280", 1024, 1280
	}
}

func (p *OpenAIProvider) Generate(ctx context.Context, in Input) (GenerationResult, error) {
	var err error
	in, err = Normalize(in)
	if err != nil {
		return GenerationResult{}, providerFailure("provider_rejected")
	}
	size, width, height := imageSize(in.Ratio)
	prompt := "Creative brief\nTemplate: " + in.Kind + "\nVisual style: " + in.Style + "\nRequested aspect ratio: " + in.Ratio + "\nDescription: " + in.Prompt
	body, _ := json.Marshal(struct {
		Model        string `json:"model"`
		Prompt       string `json:"prompt"`
		Size         string `json:"size"`
		N            int    `json:"n"`
		Quality      string `json:"quality"`
		OutputFormat string `json:"output_format"`
		Background   string `json:"background"`
	}{p.model, prompt, size, 1, "low", "png", "opaque"})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint, bytes.NewReader(body))
	if err != nil {
		return GenerationResult{}, providerFailure("provider_error")
	}
	req.Header.Set("Authorization", "Bearer "+p.key)
	req.Header.Set("Content-Type", "application/json")
	// A generation POST is a one-shot operation, including at the transport
	// layer; do not make its request body replayable after a connection error.
	req.GetBody = nil
	response, err := p.client.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return GenerationResult{}, providerFailure("provider_timeout")
		}
		return GenerationResult{}, providerFailure("provider_error")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		code := "provider_error"
		switch {
		case response.StatusCode == 401 || response.StatusCode == 403:
			code = "provider_auth_failed"
		case response.StatusCode == 429:
			code = "provider_rate_limited"
		case response.StatusCode >= 400 && response.StatusCode < 500:
			code = "provider_rejected"
		}
		return GenerationResult{}, providerFailure(code)
	}
	encoded, err := io.ReadAll(io.LimitReader(response.Body, maxProviderResponseBytes+1))
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return GenerationResult{}, providerFailure("provider_timeout")
		}
		return GenerationResult{}, providerFailure("provider_error")
	}
	if len(encoded) > maxProviderResponseBytes {
		return GenerationResult{}, providerFailure("invalid_provider_response")
	}
	var result struct {
		Data []struct {
			B64JSON string `json:"b64_json"`
		} `json:"data"`
	}
	if json.Unmarshal(encoded, &result) != nil || len(result.Data) != 1 || result.Data[0].B64JSON == "" || len(result.Data[0].B64JSON) > base64.StdEncoding.EncodedLen(maxPNGBytes) {
		return GenerationResult{}, providerFailure("invalid_provider_response")
	}
	data, err := base64.StdEncoding.Strict().DecodeString(result.Data[0].B64JSON)
	if err != nil || validatePNG(data, width, height) != nil {
		return GenerationResult{}, providerFailure("invalid_provider_response")
	}
	return GenerationResult{Data: data, MIMEType: "image/png", Width: width, Height: height}, nil
}

func validatePNG(data []byte, width, height int) error {
	if len(data) == 0 || len(data) > maxPNGBytes || width <= 0 || height <= 0 || width > maxImagePixels/height {
		return providerFailure("invalid_provider_response")
	}
	config, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width != width || config.Height != height || config.Width > maxImagePixels/config.Height {
		return providerFailure("invalid_provider_response")
	}
	if _, err := png.Decode(bytes.NewReader(data)); err != nil {
		return providerFailure("invalid_provider_response")
	}
	return nil
}
