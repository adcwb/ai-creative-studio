package generation_test

import (
	"strings"
	"testing"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/generation"
)

func TestNormalizeKeepsLegacyDefaultsAndRuneLimits(t *testing.T) {
	input, err := generation.Normalize(generation.Parameters{Prompt: " 合成测试画面 ", Kind: "poster"})
	if err != nil || input.Prompt != "合成测试画面" || input.Ratio != "4:5" || input.Style != "natural" {
		t.Fatalf("legacy defaults changed: %+v %v", input, err)
	}
	for _, in := range []generation.Parameters{{Prompt: "两个", Kind: "poster"}, {Prompt: strings.Repeat("画", 1001), Kind: "poster"}, {Prompt: "合成测试画面", Kind: "other"}, {Prompt: "合成测试画面", Kind: "poster", Ratio: "3:2"}} {
		if _, err := generation.Normalize(in); err == nil {
			t.Fatalf("invalid input accepted: %+v", in)
		}
	}
}
