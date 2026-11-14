package application

// DashScope media wire contracts are owned here. There is
// no fallback to OpenAI endpoints on an upstream rejection.
import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
)

const bailianMultimodal = "/services/aigc/multimodal-generation/generation"
const bailianVideo = "/services/aigc/video-generation/video-synthesis"

type bailianAPI struct {
	api   *OpenAICreativeProvider
	model GatewayModel
}

// Async submission needs a vendor header while retaining all runtime guards.
type bailianAsyncTransport struct{ base http.RoundTripper }

func (t bailianAsyncTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	r := req.Clone(req.Context())
	r.Header.Set("X-DashScope-Async", "enable")
	return t.base.RoundTrip(r)
}

func (p *bailianAPI) request(ctx context.Context, method, path string, body any, max int) ([]byte, error) {
	var res *http.Response
	var err error
	if method == http.MethodPost {
		client := *p.api.client
		if path == bailianVideo {
			client.Transport = bailianAsyncTransport{base: client.Transport}
		}
		res, err = requestNativeJSON(ctx, &client, p.api.endpoint+path, "Authorization", "Bearer "+p.api.key, body, 0)
	} else {
		var req *http.Request
		req, err = http.NewRequestWithContext(ctx, method, p.api.endpoint+path, nil)
		if err != nil {
			return nil, providerFailure("provider_error")
		}
		req.Header.Set("Authorization", "Bearer "+p.api.key)
		res, err = libraryGuardedRequest(p.api.client, req)
	}
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, providerFailure("provider_timeout")
		}
		return nil, err
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, int64(max)+1))
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, googleMediaStatus(res.StatusCode)
	}
	if err != nil || len(raw) > max {
		return nil, providerFailure("invalid_provider_response")
	}
	var envelope struct {
		Code string `json:"code"`
	}
	if json.Unmarshal(raw, &envelope) != nil {
		return nil, providerFailure("invalid_provider_response")
	}
	if envelope.Code != "" {
		return nil, providerFailure("provider_rejected")
	}
	return raw, nil
}

// Parse only answer content. Thinking, truncated and multiple-choice results are
// never mistaken for a completed structured answer.
func bailianAnswer(raw []byte) (string, error) {
	var env struct {
		Output struct {
			Choices []struct {
				Finish  string `json:"finish_reason"`
				Message struct {
					Content json.RawMessage `json:"content"`
				} `json:"message"`
			} `json:"choices"`
		} `json:"output"`
	}
	if json.Unmarshal(raw, &env) != nil || len(env.Output.Choices) != 1 || env.Output.Choices[0].Finish != "stop" {
		return "", providerFailure("invalid_provider_response")
	}
	content := env.Output.Choices[0].Message.Content
	var answer string
	if json.Unmarshal(content, &answer) == nil && answer != "" {
		return answer, nil
	}
	var parts []struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(content, &parts) != nil {
		return "", providerFailure("invalid_provider_response")
	}
	for _, part := range parts {
		answer += part.Text
	}
	if answer == "" {
		return "", providerFailure("invalid_provider_response")
	}
	return answer, nil
}

// The vendor chooses every protocol: Bailian LLMs use its documented OpenAI
// compatibility endpoint, as does catalog discovery; media uses native endpoints.
func newBailianRuntimeProviders(api *OpenAICreativeProvider, model GatewayModel, q RuntimeSelection, imageDownload, videoDownload, audioDownload func(context.Context, string, io.Writer) error) RuntimeProviders {
	wire := &bailianAPI{api: api, model: model}
	p := &bailianCreativeProvider{OpenAICreativeProvider: api, wire: wire, voice: q.Voice, videoDownload: videoDownload, audioDownload: audioDownload}
	out := RuntimeProviders{}
	switch q.Capability {
	case "text":
		textAPI := *api
		textAPI.endpoint = strings.TrimSuffix(api.endpoint, "/api/v1") + "/compatible-mode/v1"
		textAPI.prepareTextRequest = bailianTextParameters
		out.PromptOptimizer = &textAPI
		out.OutlinePlanner = gatewayOutlinePlanner{api: &textAPI}
		out.Creative = runtimeCreative{p: &gatewayCreativeProvider{OpenAICreativeProvider: &textAPI, model: model}, capability: q.Capability}
		out.StoryCheck = gatewayStoryCheck{api: &textAPI}
	case "vision":
		out.Vision = p
	case "image":
		out.Image = &bailianImageProvider{wire: wire, images: gatewayImageProvider{model: model, downloadTo: imageDownload}}
	case "video", "speech":
		out.Creative = runtimeCreative{p: p, capability: q.Capability}
	case "asr":
		out.Alignment = &bailianAlignmentProvider{wire: wire, plain: q.FeatureKey == "speech_transcription"}
	}
	return out
}

// Bailian documents max_tokens and requires non-thinking mode for JSON mode.
func bailianTextParameters(body map[string]any) {
	body["max_tokens"] = body["max_completion_tokens"]
	delete(body, "max_completion_tokens")
	if body["response_format"] != nil {
		body["enable_thinking"] = false
	}
}

func bailianPrompt(in Input) string {
	return "Style: " + in.Style + ". Aspect ratio: " + in.Ratio + ". " + in.Prompt
}
func bailianSize(in Input) string {
	size, _, _ := imageSize(in.Ratio)
	return strings.ReplaceAll(size, "x", "*")
}
