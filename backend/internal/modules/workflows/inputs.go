package workflows

import (
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/prompts"
)

var variableID = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)

type Variable struct {
	Name     string `json:"name"`
	Label    string `json:"label"`
	Required bool   `json:"required"`
	Default  string `json:"default,omitempty"`
}

func ResolveInputs(g Graph, in map[string]string) (map[string]string, error) {
	resolved := map[string]string{}
	known := map[string]bool{}
	for _, v := range g.Variables {
		if !variableID.MatchString(v.Name) || v.Name == "input" || known[v.Name] || utf8.RuneCountInString(v.Label) > 80 || !utf8.ValidString(v.Default) || strings.IndexByte(v.Default, 0) >= 0 || utf8.RuneCountInString(v.Default) > 2000 {
			return nil, ErrInput
		}
		known[v.Name] = true
		value, ok := in[v.Name]
		if !ok {
			value = v.Default
		}
		if !utf8.ValidString(value) || utf8.RuneCountInString(value) > 2000 || strings.IndexByte(value, 0) >= 0 || v.Required && strings.TrimSpace(value) == "" {
			return nil, ErrInput
		}
		resolved[v.Name] = value
	}
	for name := range in {
		if !known[name] {
			return nil, ErrInput
		}
	}
	return resolved, nil
}

func TemplateValues(body string, values map[string]string, input string) (map[string]string, error) {
	names, err := prompts.LiteralNames(body)
	if err != nil {
		return nil, ErrInput
	}
	result := map[string]string{}
	for _, name := range names {
		if name == "input" {
			result[name] = input
			continue
		}
		value, ok := values[name]
		if !ok {
			return nil, ErrInput
		}
		result[name] = value
	}
	return result, nil
}
