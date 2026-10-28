package publishing_test

import (
	"encoding/json"
	"testing"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/publishing"
)

func TestPlatformAndTextBounds(t *testing.T) {
	for _, platform := range []string{"tiktok", "douyin", "xiaohongshu"} {
		if !publishing.ValidPlatform(platform) {
			t.Fatalf("platform rejected: %s", platform)
		}
	}
	if publishing.ValidPlatform("other") || publishing.ValidText("含有\x00空字节", 100) || publishing.ValidText("四个汉字", 3) {
		t.Fatal("unsupported platform or invalid text accepted")
	}
}

func TestPhotoIntentRoundTripPreservesConsent(t *testing.T) {
	in := publishing.SocialJob{ID: "synthetic", Intent: &publishing.SocialCreate{MediaType: "PHOTO", PhotoAssetIDs: []string{"p1", "p2"}, PhotoCoverIndex: 1, PublicMediaConfirmed: true, RightsConfirmed: true, Confirmed: true}}
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out publishing.SocialJob
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if out.Intent == nil || !out.Intent.RightsConfirmed || !out.Intent.PublicMediaConfirmed || !out.Intent.Confirmed || out.Intent.PhotoCoverIndex != 1 || len(out.Intent.PhotoAssetIDs) != 2 {
		t.Fatal("publishing intent changed during persistence round trip")
	}
}
