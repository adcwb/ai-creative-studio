package library_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/adcwb/ai-creative-studio/backend/internal/modules/library"
	"strings"
	"testing"
)

func TestTypedCardNormalizationAndIsolation(t *testing.T) {
	in := library.Card{Name: "  主角  ", Character: &library.Character{Hair: "  黑发  "}, ProhibitedChanges: []string{"服装颜色"}}
	out, err := library.Normalize("character", in)
	if err != nil || out.Name != "主角" || out.Character.Hair != "黑发" {
		t.Fatalf("normalize: %+v %v", out, err)
	}
	out.Character.Hair = "红发"
	out.ProhibitedChanges[0] = "发型"
	if in.Character.Hair != "  黑发  " || in.ProhibitedChanges[0] != "服装颜色" {
		t.Fatal("mutated input")
	}
	in.Product = &library.Product{}
	if _, err = library.Normalize("character", in); !errors.Is(err, library.ErrInput) {
		t.Fatal("mixed card accepted")
	}
	in.Product = nil
	in.ProhibitedChanges = []string{"发型", " 发型 "}
	if _, err = library.Normalize("character", in); err == nil {
		t.Fatal("duplicate constraints accepted")
	}
}

func TestPropIsDistinctImmutableCardWithoutChangingOldDigest(t *testing.T) {
	old := library.Card{Name: "旧商品", Product: &library.Product{Model: "clock"}}
	digest, err := library.Digest(old)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(old)
	if err != nil || strings.Contains(string(raw), `"prop"`) {
		t.Fatal("old JSON shape changed", err)
	}
	var reread library.Card
	if err = json.Unmarshal(raw, &reread); err != nil {
		t.Fatal(err)
	}
	after, err := library.Digest(reread)
	if err != nil || after != digest {
		t.Fatal("old digest changed", err)
	}
	input := library.Card{Name: " 铜钥匙 ", Prop: &library.Prop{Condition: " 缺失一齿 ", StoryFunction: "打开密室", ContinuityNotes: "本集不能恢复"}}
	result, err := library.Normalize("prop", input)
	if err != nil || result.Prop.Condition != "缺失一齿" || result.Name != "铜钥匙" {
		t.Fatal(result, err)
	}
	result.Prop.Condition = "完好"
	if input.Prop.Condition != " 缺失一齿 " {
		t.Fatal("prop version aliased input")
	}
	input.Product = &library.Product{}
	if _, err = library.Normalize("prop", input); err == nil {
		t.Fatal("prop/product mixed content accepted")
	}
	if _, err = library.Normalize("product", library.Card{Name: "wrong", Prop: &library.Prop{}}); err == nil {
		t.Fatal("prop silently treated as product")
	}
}

func TestNativeVoicePresetIsImmutablePublicMetadataWithExactModelAlias(t *testing.T) {
	model := "vendor/" + strings.Repeat("a", 170) + "@latest"
	in := library.Card{Name: "音色", Voice: &library.Voice{Performance: "只作创作备注", NativePreset: &library.NativeVoicePreset{ModelConfigID: "model", Provider: "gateway-speech", Model: model, Voice: "native-voice"}}}
	normalized, err := library.Normalize("voice", in)
	if err != nil || normalized.Voice.NativePreset.Model != model {
		t.Fatal("valid configured alias was narrowed", err)
	}
	normalized.Voice.NativePreset.Voice = "other"
	if in.Voice.NativePreset.Voice != "native-voice" {
		t.Fatal("normalization mutated old preset")
	}
	for _, mutate := range []func(*library.NativeVoicePreset){func(p *library.NativeVoicePreset) { p.Model = "model\nunsafe" }, func(p *library.NativeVoicePreset) { p.Model = strings.Repeat("a", 201) }, func(p *library.NativeVoicePreset) { p.Voice = "has space" }, func(p *library.NativeVoicePreset) { p.ModelConfigID = "other/tenant" }} {
		copy := *in.Voice.NativePreset
		mutate(&copy)
		bad := in
		bad.Voice = &library.Voice{NativePreset: &copy}
		if _, err := library.Normalize("voice", bad); err == nil {
			t.Fatal("malformed preset accepted")
		}
	}
}

func TestStyleAndVoiceAreDistinctIntentWithNoFixedListAllowances(t *testing.T) {
	for kind, card := range map[string]library.Card{"style": {Name: "  墨色  ", Style: &library.Style{Palette: "  青灰  "}}, "voice": {Name: "  低声  ", Voice: &library.Voice{Performance: "  克制  "}}} {
		for i := 0; i < 35; i++ {
			card.ProhibitedChanges = append(card.ProhibitedChanges, fmt.Sprintf("限制内容 %d", i))
		}
		v, e := library.Normalize(kind, card)
		if e != nil || v.Name != strings.TrimSpace(card.Name) || len(v.ProhibitedChanges) != 35 {
			t.Fatal(kind, e)
		}
		if v.Style != nil {
			v.Style.Palette = "changed"
			if card.Style.Palette != "  青灰  " {
				t.Fatal("style alias")
			}
		}
		if v.Voice != nil {
			v.Voice.Performance = "changed"
			if card.Voice.Performance != "  克制  " {
				t.Fatal("voice alias")
			}
		}
		v.Character = &library.Character{}
		if _, e = library.Normalize(kind, v); !errors.Is(e, library.ErrInput) {
			t.Fatal("mixed card", kind, e)
		}
	}
	old := library.Card{Name: "旧角色", Character: &library.Character{}, ProhibitedChanges: []string{}}
	b, _ := json.Marshal(old)
	if strings.Contains(string(b), `"style"`) || strings.Contains(string(b), `"voice"`) {
		t.Fatal("old content digest shape changed")
	}
	refs := []library.Reference{}
	for i := 0; i < 65; i++ {
		refs = append(refs, library.Reference{ID: fmt.Sprintf("ref%d", i), Role: "reference", MIMEType: "image/png", Width: 1, Height: 1, SizeBytes: 70, SHA256: strings.Repeat("a", 64), SourceProjectID: "p", SourceAssetID: fmt.Sprintf("image%d", i)})
	}
	if e := library.ValidateReferences(refs); e != nil {
		t.Fatal("fixed reference allowance survived", e)
	}
	if e := library.ValidateContent(old, refs); e != nil {
		t.Fatal(e)
	}
	voice := library.Card{Name: "声音意图", Voice: &library.Voice{}}
	if e := library.ValidateContent(voice, refs); !errors.Is(e, library.ErrInput) {
		t.Fatal("image declared voice identity", e)
	}
	oversized := old
	oversized.ProhibitedChanges = []string{strings.Repeat("x", library.ContentBytes)}
	if e := library.ValidateContent(oversized, nil); !errors.Is(e, library.ErrPayload) {
		t.Fatal("unbounded metadata transaction", e)
	}
}

func TestVersionPinningAndArchive(t *testing.T) {
	a := library.Asset{ID: "asset", SpaceID: "space", Kind: "character", HeadVersionID: "v1", Status: "active"}
	v1 := library.Version{ID: "v1", AssetID: a.ID, SpaceID: a.SpaceID}
	v2 := library.Version{ID: "v2", AssetID: a.ID, SpaceID: a.SpaceID, Card: library.Card{Name: "新版"}}
	updated, err := library.AppendVersion(a, "v1", v2)
	if err != nil || updated.HeadVersionID != "v2" || a.HeadVersionID != "v1" {
		t.Fatal("head mutation")
	}
	if err = library.CanBind(updated, v1, "space"); err != nil {
		t.Fatal("historical version lost")
	}
	if _, err = library.AppendVersion(updated, "v1", v2); !errors.Is(err, library.ErrConflict) {
		t.Fatal("stale version accepted")
	}
	if library.CanBind(updated, v1, "other") == nil {
		t.Fatal("cross space accepted")
	}
	updated.Status = "archived"
	if !errors.Is(library.CanBind(updated, v1, "space"), library.ErrArchived) {
		t.Fatal("archived binding accepted")
	}
}
