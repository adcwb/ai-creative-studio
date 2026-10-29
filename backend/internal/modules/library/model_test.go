package library_test

import (
	"errors"
	"github.com/adcwb/ai-creative-studio/backend/internal/modules/library"
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
