package prompts_test

import (
	"github.com/adcwb/ai-creative-studio/backend/internal/modules/prompts"
	"strings"
	"testing"
)

func TestPlainRecipeAndVisibility(t *testing.T) {
	r := prompts.Recipe{Syntax: "plain", Body: "  {{subject}} <script>alert(1)</script> 中文 🌿  ", Defaults: prompts.Defaults{Kind: "poster", Ratio: "4:5", Style: "natural"}}
	got, e := prompts.NormalizeRecipe(r)
	if e != nil || got.Body != r.Body {
		t.Fatal("plain text changed", e)
	}
	for _, bad := range []prompts.Recipe{{Syntax: "variables_v1", Body: r.Body, Defaults: r.Defaults}, {Syntax: "plain", Body: strings.Repeat("花", 1001), Defaults: r.Defaults}, {Syntax: "plain", Body: "ab", Defaults: r.Defaults}, {Syntax: "plain", Body: r.Body, Defaults: prompts.Defaults{Kind: "image", Ratio: "4K", Style: "natural"}}} {
		if _, e = prompts.NormalizeRecipe(bad); e == nil {
			t.Fatal("invalid recipe accepted")
		}
	}
	if _, e = prompts.NormalizeRecipe(prompts.Recipe{Syntax: "plain", Body: strings.Repeat("花", 1000), Defaults: r.Defaults}); e != nil {
		t.Fatal("Unicode boundary", e)
	}
	tpl := prompts.Template{SpaceID: "a", OwnerID: "owner", Metadata: prompts.Metadata{Visibility: "private"}}
	if prompts.CanRead(prompts.Actor{Space: "a", User: "editor", Writable: true}, tpl) || prompts.CanRead(prompts.Actor{Space: "b", User: "owner", Writable: true}, tpl) {
		t.Fatal("private leak")
	}
	tpl.Visibility = "organization"
	if !prompts.CanRead(prompts.Actor{Space: "a", User: "viewer"}, tpl) || prompts.CanWrite(prompts.Actor{Space: "a", User: "viewer"}, tpl) {
		t.Fatal("role matrix")
	}
}

func TestVerticalRecipeCompilationPreservesChosenDefaults(t *testing.T) {
	r := prompts.Recipe{Syntax: "plain", Body: "固定角色竖屏对白画面", Defaults: prompts.Defaults{Kind: "general", Ratio: "9:16", Style: "natural"}}
	compiled, err := prompts.Compile(r, nil)
	if err != nil || compiled.Text != r.Body || compiled.Defaults != r.Defaults {
		t.Fatal("vertical recipe changed during preview", compiled, err)
	}
}
