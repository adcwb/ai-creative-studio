package studio

// The adapter classifies text only. An allow signal is not proof of safe content;
// publishing decisions and audio/video review require separate policies.
import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

type ModerationDecision struct {
	Provider   string    `json:"provider"`
	Model      string    `json:"model"`
	Status     string    `json:"status"` // allow, review, unavailable; never infer allow on error
	Categories []string  `json:"categories"`
	CheckedAt  time.Time `json:"checked_at"`
}

type ContentModerator interface {
	CheckText(context.Context, string) (ModerationDecision, error)
}

type UnavailableModerator struct{}

func (UnavailableModerator) CheckText(context.Context, string) (ModerationDecision, error) {
	return ModerationDecision{Status: "unavailable", Categories: []string{}}, errors.New("content moderation unavailable")
}

type OpenAITextModerator struct {
	key, model, endpoint string
	client               *http.Client
}

// Construction requires explicit data-sending authorization; no environment or
// saved credentials are read, and model selection is never implicit.
func NewOpenAITextModerator(key, model string, authorized bool) (*OpenAITextModerator, error) {
	if !authorized || strings.TrimSpace(key) == "" || model != "omni-moderation-latest" {
		return nil, errors.New("moderation requires authorization, credential and supported explicit model")
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = nil
	return newTextModerator(key, model, "https://api.openai.com/v1/moderations", &http.Client{Transport: tr, Timeout: 20 * time.Second}), nil
}
func newTextModerator(key, model, endpoint string, client *http.Client) *OpenAITextModerator {
	c := *client
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &OpenAITextModerator{key: key, model: model, endpoint: endpoint, client: &c}
}
func (p *OpenAITextModerator) CheckText(ctx context.Context, text string) (ModerationDecision, error) {
	decision := ModerationDecision{Provider: "openai-moderation", Model: p.model, Status: "unavailable", Categories: []string{}}
	if !utf8.ValidString(text) || strings.TrimSpace(text) == "" || len(text) > 32768 {
		return decision, errors.New("invalid moderation input")
	}
	b, _ := json.Marshal(map[string]string{"model": p.model, "input": text})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint, bytes.NewReader(b))
	if err != nil {
		return decision, errors.New("moderation unavailable")
	}
	req.GetBody = nil
	req.Header.Set("Authorization", "Bearer "+p.key)
	req.Header.Set("Content-Type", "application/json")
	res, err := p.client.Do(req)
	if err != nil {
		return decision, errors.New("moderation unavailable")
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return decision, errors.New("moderation unavailable")
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, 65537))
	if err != nil || len(data) > 65536 {
		return decision, errors.New("invalid moderation response")
	}
	var body struct {
		Model   string `json:"model"`
		Results []struct {
			Flagged    *bool           `json:"flagged"`
			Categories map[string]bool `json:"categories"`
		} `json:"results"`
	}
	if json.Unmarshal(data, &body) != nil || len(body.Results) != 1 || body.Results[0].Flagged == nil || body.Results[0].Categories == nil || body.Model == "" {
		return decision, errors.New("invalid moderation response")
	}
	decision.Model = body.Model
	decision.CheckedAt = time.Now().UTC()
	decision.Status = "allow"
	if *body.Results[0].Flagged {
		decision.Status = "review"
	}
	for category, flagged := range body.Results[0].Categories {
		if flagged {
			decision.Categories = append(decision.Categories, category)
			decision.Status = "review"
		}
	}
	return decision, nil
}
