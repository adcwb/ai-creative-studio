package governance_test

import (
	"context"
	"testing"
	"time"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/governance"
)

func TestCurrencyPrecisionAndMissingExchangeProvider(t *testing.T) {
	for currency, want := range map[string]int{"USD": 2, "JPY": 0, "KWD": 3} {
		got, err := governance.CurrencyMinorUnits(currency)
		if err != nil || got != want {
			t.Fatalf("%s precision: %d %v", currency, got, err)
		}
	}
	if _, err := governance.CurrencyMinorUnits("UNKNOWN"); err == nil {
		t.Fatal("unknown currency accepted")
	}
	if _, err := governance.GetExchangeQuote(context.Background(), nil, "USD", "CNY", time.Now(), time.Minute); err == nil {
		t.Fatal("missing exchange evidence accepted")
	}
}

func TestExecutionDigestBindsMediaAndVoice(t *testing.T) {
	in := governance.ExecutionRequest{Provider: "synthetic", Model: "fixture", Capability: "image", Text: "synthetic request", Selection: &governance.RuntimeSelection{ActorID: "actor-one"}, Media: []governance.ExecutionMedia{{MIME: "image/png", Data: []byte("synthetic bytes")}}}
	original := governance.ExecutionRequestDigest(in)
	in.Selection.ActorID = "actor-two"
	if governance.ExecutionRequestDigest(in) != original {
		t.Fatal("actor identity leaked into the content quotation digest")
	}
	in.Selection.Voice = "different-voice"
	if governance.ExecutionRequestDigest(in) == original {
		t.Fatal("voice change did not invalidate quotation")
	}
	in.Selection.Voice = ""
	in.Media[0].Data = []byte("different synthetic bytes")
	if governance.ExecutionRequestDigest(in) == original {
		t.Fatal("media change did not invalidate quotation")
	}
}
