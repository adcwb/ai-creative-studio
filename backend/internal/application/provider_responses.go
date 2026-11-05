package application

// Native input count uses the SAME Responses input and format as generation:
// https://developers.openai.com/api/reference/resources/responses/subresources/input_tokens/methods/count
// It is enabled only by an explicit gateway capability declaration. No local
// byte/token conversion, tokenizer inference, silent truncation or chat fallback.
import (
	"context"
	"encoding/json"
	"errors"
	"strings"
)

func (p *OpenAICreativeProvider) responsesJSON(ctx context.Context, input, instruction string, out any, maxRequest, maxResponse, limit int) error {
	body := map[string]any{"model": p.textModel, "instructions": instruction, "input": []map[string]string{{"role": "user", "content": input}}, "tools": []any{}, "tool_choice": "none", "truncation": "disabled", "text": map[string]any{"format": map[string]string{"type": "text"}}}
	if p.jsonOutput == nil || *p.jsonOutput {
		body["text"] = map[string]any{"format": map[string]string{"type": "json_object"}}
	}
	encoded, e := json.Marshal(body)
	if e != nil || len(encoded) > maxRequest {
		return providerFailure("provider_rejected")
	}
	if p.nativeInputCounting {
		if p.contextWindowTokens <= 0 {
			return providerFailure("provider_input_count_unavailable")
		}
		raw, e := p.request(ctx, "POST", "/responses/input_tokens", body, 8<<10)
		// No generation has been sent when the counter fails. Never switch protocol
		// or send the generation anyway after malformed/unsupported count responses.
		if e != nil {
			var pe *ProviderError
			if errors.As(e, &pe) && pe.Code == "provider_input_capacity" {
				return e
			}
			return providerFailure("provider_input_count_unavailable")
		}
		var count struct {
			Object string `json:"object"`
			Tokens *int64 `json:"input_tokens"`
		}
		if json.Unmarshal(raw, &count) != nil || count.Object != "response.input_tokens" || count.Tokens == nil || *count.Tokens < 1 || *count.Tokens > 9007199254740991 {
			return providerFailure("provider_input_count_unavailable")
		}
		if int64(limit) > p.contextWindowTokens || *count.Tokens > p.contextWindowTokens-int64(limit) {
			return providerFailure("provider_input_capacity")
		}
	}
	body["max_output_tokens"] = limit
	body["store"] = false
	body["stream"] = false
	body["background"] = false
	body["truncation"] = "disabled"
	encoded, e = json.Marshal(body)
	if e != nil || len(encoded) > maxRequest {
		return providerFailure("provider_rejected")
	}
	if e = checkTextGenerationSource(ctx); e != nil {
		return e
	}
	raw, e := p.request(ctx, "POST", "/responses", body, maxResponse)
	if e != nil {
		return e
	}
	var response struct {
		Object string          `json:"object"`
		Status string          `json:"status"`
		Error  json.RawMessage `json:"error"`
		Output []struct {
			Type    string `json:"type"`
			Role    string `json:"role"`
			Status  string `json:"status"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
	}
	if json.Unmarshal(raw, &response) != nil || response.Object != "response" || response.Status != "completed" || len(response.Error) > 0 && string(response.Error) != "null" {
		return providerFailure("invalid_provider_response")
	}
	messages := 0
	var result strings.Builder
	for _, item := range response.Output {
		if item.Type == "reasoning" {
			continue
		} // Never expose or persist private reasoning.
		if item.Type != "message" || item.Role != "assistant" || item.Status != "completed" {
			return providerFailure("invalid_provider_response")
		}
		messages++
		for _, part := range item.Content {
			if part.Type != "output_text" {
				return providerFailure("invalid_provider_response")
			}
			result.WriteString(part.Text)
		}
	}
	if messages != 1 || result.Len() == 0 {
		return providerFailure("invalid_provider_response")
	}
	return strictCreativeJSON([]byte(result.String()), out)
}

// Only recognized native capacity codes from bounded 4xx error envelopes are
// surfaced. Upstream prose, secrets and arbitrary fields never enter UI/logs.
func nativeCapacityError(raw []byte) bool {
	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if json.Unmarshal(raw, &envelope) != nil {
		return false
	}
	switch envelope.Error.Code {
	case "context_length_exceeded", "context_window_exceeded", "input_too_long":
		return true
	}
	return false
}
