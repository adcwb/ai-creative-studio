package series

import "testing"

func TestOutputIntentHasNoEpisodeCountOrTwoMinuteQuota(t *testing.T) {
	for _, duration := range []int64{1, 60, 90, 120, 121, 7200} {
		for _, resolution := range []string{"1080p", "1440p", "1800p", "2160p"} {
			p := Profile{duration, resolution, "9:16"}
			if !p.Valid() {
				t.Fatalf("valid intent rejected: %+v", p)
			}
		}
	}
	for _, p := range []Profile{{0, "1080p", "9:16"}, {90, "8K", "16:9"}, {90, "2160p", "invalid"}, {MaxOrdinal + 1, "1080p", "9:16"}} {
		if p.Valid() {
			t.Fatal("unsupported intent accepted", p)
		}
	}
}
func TestInputRejectsAmbiguousOrMalformedMetadata(t *testing.T) {
	in := Input{Title: " 创作系列 ", Summary: " 成长与冒险 ", Profile: DefaultProfile()}
	got, err := Normalize(in, "series")
	if err != nil || got.Title != "创作系列" || got.Format != "motion-comic" {
		t.Fatal(got, err)
	}
	in.Title = "bad\x00title"
	if _, err = Normalize(in, "series"); err == nil {
		t.Fatal("nul accepted")
	}
	in.Title = "valid"
	in.Format = "unknown"
	if _, err = Normalize(in, "series"); err == nil {
		t.Fatal("unknown format")
	}
}
