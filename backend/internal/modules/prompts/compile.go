package prompts

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

var variableName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)

type Variable struct {
	Name      string   `json:"name"`
	Label     string   `json:"label"`
	Type      string   `json:"type"`
	Required  bool     `json:"required"`
	Default   *string  `json:"default,omitempty"`
	MaxLength int      `json:"max_length"`
	Options   []string `json:"options,omitempty"`
}
type FieldError struct{ Code, Field string }

func (e *FieldError) Error() string { return fmt.Sprintf("%s: %s", e.Code, e.Field) }
func (e *FieldError) Unwrap() error { return ErrInput }

type token struct{ text, name string }

func parse(body string) ([]token, error) {
	out := []token{}
	for body != "" {
		at := strings.Index(body, "{{")
		if at < 0 {
			if strings.Contains(body, "}}") {
				return nil, &FieldError{"invalid_prompt_parameter", "body"}
			}
			out = append(out, token{text: body})
			break
		}
		if strings.Contains(body[:at], "}}") {
			return nil, &FieldError{"invalid_prompt_parameter", "body"}
		}
		if at > 0 {
			out = append(out, token{text: body[:at]})
		}
		body = body[at+2:]
		end := strings.Index(body, "}}")
		if end < 0 || !variableName.MatchString(body[:end]) {
			return nil, &FieldError{"invalid_prompt_parameter", "body"}
		}
		out = append(out, token{name: body[:end]})
		body = body[end+2:]
	}
	return out, nil
}
func validValue(v Variable, value string) bool {
	if !utf8.ValidString(value) || utf8.RuneCountInString(value) > v.MaxLength {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return false
		}
	}
	if v.Required && strings.TrimSpace(value) == "" {
		return false
	}
	if v.Type == "enum" && value != "" {
		found := false
		for _, option := range v.Options {
			found = found || option == value
		}
		if !found {
			return false
		}
	}
	return true
}
func normalizeVariables(r Recipe) (Recipe, error) {
	if r.Syntax == "plain" {
		if len(r.Variables) > 0 {
			return r, &FieldError{"invalid_prompt_parameter", "variables"}
		}
		r.Variables = nil
		return r, nil
	}
	if r.Syntax != "variables_v1" {
		return r, &FieldError{"invalid_prompt_parameter", "syntax"}
	}
	tokens, e := parse(r.Body)
	if e != nil {
		return r, e
	}
	used := map[string]bool{}
	for _, t := range tokens {
		if t.name != "" {
			used[t.name] = true
		}
	}
	variables := append([]Variable(nil), r.Variables...)
	known := map[string]bool{}
	for i, v := range variables {
		if !variableName.MatchString(v.Name) || known[v.Name] || !used[v.Name] || !utf8.ValidString(v.Label) || utf8.RuneCountInString(v.Label) > 80 || (v.Type != "text" && v.Type != "enum") {
			return r, &FieldError{"invalid_prompt_parameter", "variables"}
		}
		if v.MaxLength == 0 {
			v.MaxLength = 1000
		}
		if v.MaxLength < 1 || v.MaxLength > 1000 {
			return r, &FieldError{"invalid_prompt_parameter", v.Name}
		}
		if v.Type == "text" && len(v.Options) > 0 || v.Type == "enum" && len(v.Options) == 0 {
			return r, &FieldError{"invalid_prompt_parameter", v.Name}
		}
		seen := map[string]bool{}
		for _, option := range v.Options {
			if option == "" || seen[option] || !validValue(Variable{Type: "text", MaxLength: v.MaxLength}, option) {
				return r, &FieldError{"invalid_prompt_parameter", v.Name}
			}
			seen[option] = true
		}
		if v.Default != nil && !validValue(v, *v.Default) {
			return r, &FieldError{"invalid_prompt_parameter", v.Name}
		}
		known[v.Name] = true
		variables[i] = v
	}
	for name := range used {
		if !known[name] {
			return r, &FieldError{"prompt_variable_unknown", name}
		}
	}
	r.Variables = variables
	return r, nil
}

type Compilation struct {
	Text       string            `json:"prompt"`
	Characters int               `json:"characters"`
	SHA256     string            `json:"compiled_sha256"`
	Defaults   Defaults          `json:"defaults"`
	Variables  map[string]string `json:"variables,omitempty"`
}

func Compile(r Recipe, values map[string]string) (Compilation, error) {
	r, e := NormalizeRecipe(r)
	if e != nil {
		return Compilation{}, e
	}
	known := map[string]Variable{}
	for _, v := range r.Variables {
		known[v.Name] = v
	}
	for name := range values {
		if _, ok := known[name]; !ok {
			return Compilation{}, &FieldError{"prompt_variable_unknown", "variables"}
		}
	}
	resolved := map[string]string{}
	for _, v := range r.Variables {
		value, ok := values[v.Name]
		if !ok && v.Default != nil {
			value = *v.Default
			ok = true
		}
		if !ok && v.Required {
			return Compilation{}, &FieldError{"prompt_variable_missing", v.Name}
		}
		if !validValue(v, value) {
			return Compilation{}, &FieldError{"invalid_prompt_parameter", v.Name}
		}
		resolved[v.Name] = value
	}
	expanded := r.Body
	if r.Syntax == "variables_v1" {
		tokens, _ := parse(r.Body)
		var b strings.Builder
		count := 0
		for _, t := range tokens {
			value := t.text
			if t.name != "" {
				value = resolved[t.name]
			}
			count += utf8.RuneCountInString(value)
			if count > 1000 {
				return Compilation{}, &FieldError{"prompt_expanded_too_long", "body"}
			}
			b.WriteString(value)
		}
		expanded = b.String()
	}
	text := strings.TrimSpace(expanded)
	n := utf8.RuneCountInString(text)
	if utf8.RuneCountInString(expanded) > 1000 || n < 3 {
		return Compilation{}, &FieldError{"prompt_expanded_too_long", "body"}
	}
	return Compilation{Text: text, Characters: n, SHA256: TextSHA(text), Defaults: r.Defaults, Variables: resolved}, nil
}
