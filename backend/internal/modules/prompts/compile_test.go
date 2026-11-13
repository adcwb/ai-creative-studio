package prompts_test

import (
	"errors"
	"github.com/adcwb/ai-creative-studio/backend/internal/modules/prompts"
	"strings"
	"testing"
)

func variableRecipe() prompts.Recipe {
	return prompts.Recipe{Syntax: "variables_v1", Body: "{{subject}}，{{palette}}光线，{{subject}}位于左侧", Defaults: prompts.Defaults{Kind: "poster", Ratio: "4:5", Style: "natural"}, Variables: []prompts.Variable{{Name: "subject", Type: "text", Required: true, MaxLength: 40}, {Name: "palette", Type: "enum", MaxLength: 10, Default: ptr("暖色"), Options: []string{"暖色", "冷灰"}}}}
}
func ptr(v string) *string { return &v }
func TestVariableCompilerExplicitLiteralAndFieldErrors(t *testing.T) {
	recipe := variableRecipe()
	out, e := prompts.Compile(recipe, map[string]string{"subject": "花朵 🌿"})
	if e != nil || out.Text != "花朵 🌿，暖色光线，花朵 🌿位于左侧" || out.Variables["palette"] != "暖色" {
		t.Fatal(out, e)
	}
	for _, test := range []struct {
		name, code, field string
		recipe            prompts.Recipe
		values            map[string]string
	}{
		{"missing", "prompt_variable_missing", "subject", recipe, nil},
		{"unknown", "prompt_variable_unknown", "variables", recipe, map[string]string{"subject": "flower", "secret": "never"}},
		{"enum", "invalid_prompt_parameter", "palette", recipe, map[string]string{"subject": "flower", "palette": "red"}},
		{"control", "invalid_prompt_parameter", "subject", recipe, map[string]string{"subject": "flower\x00hidden"}},
		{"value-length", "invalid_prompt_parameter", "subject", recipe, map[string]string{"subject": strings.Repeat("花", 41)}},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, e := prompts.Compile(test.recipe, test.values)
			var field *prompts.FieldError
			if !errors.As(e, &field) || field.Code != test.code || field.Field != test.field {
				t.Fatal(e)
			}
		})
	}
	for _, body := range []string{"{{subject.name}}", "{{subject + palette}}", "{{{{subject}}}}", "{{subject", "plain }}"} {
		r := recipe
		r.Body = body
		if _, e = prompts.NormalizeRecipe(r); e == nil {
			t.Fatal("expression accepted", body)
		}
	}
	plain := prompts.Recipe{Syntax: "plain", Body: "{{literal}} 花园", Defaults: recipe.Defaults}
	out, e = prompts.Compile(plain, nil)
	if e != nil || out.Text != plain.Body {
		t.Fatal("plain changed", e)
	}
	// Values are inserted once as text, never recursively parsed or evaluated.
	r := recipe
	r.Variables[0].MaxLength = 1000
	out, e = prompts.Compile(r, map[string]string{"subject": "{{palette}}"})
	if e != nil || !strings.Contains(out.Text, "{{palette}}，暖色") {
		t.Fatal("recursive value evaluation", e)
	}
}
func TestVariableCompilerUnicodeBoundaryAndOldRecipeDigest(t *testing.T) {
	recipe := prompts.Recipe{Syntax: "variables_v1", Body: "{{body}}", Defaults: prompts.Defaults{Kind: "poster", Ratio: "4:5", Style: "natural"}, Variables: []prompts.Variable{{Name: "body", Type: "text", Required: true, MaxLength: 1000}}}
	out, e := prompts.Compile(recipe, map[string]string{"body": strings.Repeat("🌿", 1000)})
	if e != nil || out.Characters != 1000 {
		t.Fatal("1000 unicode", e)
	}
	recipe.Body = "{{body}}。"
	if _, e = prompts.Compile(recipe, map[string]string{"body": strings.Repeat("花", 1000)}); e == nil {
		t.Fatal("expanded >1000 accepted")
	}
	plain := prompts.Recipe{Syntax: "plain", Body: "literal {{body}}", Defaults: recipe.Defaults}
	const legacy = `{"syntax":"plain","body":"literal {{body}}","defaults":{"kind":"poster","ratio":"4:5","style":"natural"},"notes":""}`
	normalized, e := prompts.NormalizeRecipe(plain)
	if e != nil || prompts.Digest(normalized) != prompts.TextSHA(legacy) {
		t.Fatal("P1 persisted recipe digest changed", e)
	}
}
func FuzzVariableCompilerDoesNotEvaluate(f *testing.F) {
	f.Add("{{subject}}", "花朵")
	f.Add("{{subject.name}}", "${secret}")
	f.Fuzz(func(t *testing.T, body, value string) {
		r := variableRecipe()
		r.Body = body
		_, _ = prompts.Compile(r, map[string]string{"subject": value})
	})
}

func TestExpandLiteralDoesNotEvaluateOrReexpandValues(t *testing.T) {
	got, err := prompts.ExpandLiteral("Poster: {{input}} / {{input}}", map[string]string{"input": "{{secret}} $(whoami)"}, 2000)
	if err != nil || got != "Poster: {{secret}} $(whoami) / {{secret}} $(whoami)" {
		t.Fatal(got, err)
	}
	for _, body := range []string{"{{input.name}}", "{{ input }}", "{{input|html}}", "{{unknown}}", "{{input", "plain"} {
		if _, err = prompts.ExpandLiteral(body, map[string]string{"input": "literal"}, 2000); err == nil {
			t.Fatal("accepted undeclared expression", body)
		}
	}
	if _, err = prompts.ExpandLiteral("{{input}}", nil, 2000); err == nil {
		t.Fatal("missing input")
	}
}

func TestLiteralExpansionStopsAtActualTextWindow(t *testing.T) {
	if _, err := prompts.ExpandLiteral(strings.Repeat("{{input}}", 1000), map[string]string{"input": strings.Repeat("x", 1000)}, 2000); err == nil {
		t.Fatal("expanded past the creative input window")
	}
}
