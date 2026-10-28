package generation

import (
	"errors"
	"strings"
	"unicode/utf8"
)

type Parameters struct {
	Prompt string `json:"prompt"`
	Kind   string `json:"kind"`
	Ratio  string `json:"ratio"`
	Style  string `json:"style"`
}

func Normalize(in Parameters) (Parameters, error) {
	in.Prompt = strings.TrimSpace(in.Prompt)
	in = WithDefaults(in)
	if utf8.RuneCountInString(in.Prompt) < 3 || utf8.RuneCountInString(in.Prompt) > 1000 {
		return in, errors.New("描述需为 3–1000 个字符")
	}
	if in.Kind != "poster" && in.Kind != "portrait" && in.Kind != "general" {
		return in, errors.New("类型必须为 poster、portrait 或 general")
	}
	if in.Ratio != "1:1" && in.Ratio != "4:5" && in.Ratio != "16:9" {
		return in, errors.New("比例必须为 1:1、4:5 或 16:9")
	}
	if in.Style != "natural" && in.Style != "studio" && in.Style != "illustration" {
		return in, errors.New("风格必须为 natural、studio 或 illustration")
	}
	return in, nil
}

// WithDefaults also upgrades older snapshots without changing stored content.
func WithDefaults(in Parameters) Parameters {
	if in.Ratio == "" {
		in.Ratio = "4:5"
	}
	if in.Style == "" {
		in.Style = "natural"
	}
	return in
}

// Expectation is a submission guard, not part of the persisted creative input
// or the idempotency identity. Older clients may omit it.
type Expectation struct {
	Provider string
	Model    string
}
