package media_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/media"
)

func TestCrossfadeUsesShortenedTimelineForOverlays(t *testing.T) {
	state := media.MediaState{Assets: []media.MediaAsset{{ID: "image", Kind: "image"}}}
	timeline := media.MediaTimeline{Clips: []media.MediaClip{{ID: "one", AssetID: "image", Duration: 3, Transition: "crossfade", TransitionDuration: 1}, {ID: "two", AssetID: "image", Duration: 3}}}
	if err := media.ValidateTimeline(state, timeline); err != nil {
		t.Fatal(err)
	}
	if media.TimelineDuration(timeline) != 5 {
		t.Fatal("crossfade duration was not subtracted")
	}
	timeline.VideoOverlays = []media.MediaVideoOverlay{{ID: "overlay", AssetID: "image", Start: 4.5, Duration: 1, Width: 1, Height: 1, Opacity: 1}}
	if err := media.ValidateTimeline(state, timeline); !errors.Is(err, media.ErrInput) {
		t.Fatalf("overlay outside shortened timeline accepted: %v", err)
	}
}

func TestReplacementRetainsOnlyValidProjectAudio(t *testing.T) {
	state := media.MediaState{Assets: []media.MediaAsset{{ID: "picture", Kind: "image"}, {ID: "original", Kind: "video", Duration: 6}}}
	clip := media.MediaClip{ID: "one", AssetID: "picture", Duration: 3, PreserveSourceAudio: true, SourceAudioAssetID: "original", SourceAudioTrimStart: 2, SourceAudioVolume: 1}
	timeline := media.MediaTimeline{Clips: []media.MediaClip{clip}}
	if err := media.ValidateTimeline(state, timeline); err != nil {
		t.Fatalf("valid replacement lost its source sound: %v", err)
	}
	for _, change := range []func(*media.MediaClip){
		func(c *media.MediaClip) { c.SourceAudioAssetID = "foreign" },
		func(c *media.MediaClip) { c.SourceAudioAssetID = "picture" },
		func(c *media.MediaClip) { c.SourceAudioTrimStart = -1 },
		func(c *media.MediaClip) { c.SourceAudioTrimStart = 4 },
		func(c *media.MediaClip) { c.PreserveSourceAudio = false },
	} {
		invalid := clip
		change(&invalid)
		timeline.Clips = []media.MediaClip{invalid}
		if err := media.ValidateTimeline(state, timeline); !errors.Is(err, media.ErrInput) {
			t.Fatalf("invalid source sound accepted: %+v, %v", invalid, err)
		}
	}
}

func TestSubtitleRequiredFieldsAndTimeBounds(t *testing.T) {
	var cue media.SubtitleCue
	if err := json.Unmarshal([]byte(`{"start":0,"text":"合成字幕"}`), &cue); err == nil {
		t.Fatal("missing end accepted")
	}
	if err := json.Unmarshal([]byte(`{"start":0,"end":1,"text":"合成字幕","extra":true}`), &cue); err == nil {
		t.Fatal("unknown cue property accepted")
	}
	cues, err := media.EstimateSubtitleCues("第一句。第二句。", 4)
	if err != nil || len(cues) != 2 || cues[1].End != 4 {
		t.Fatalf("subtitle estimate changed: %+v %v", cues, err)
	}
	if err := media.ValidateSubtitleCues(cues, 3); !errors.Is(err, media.ErrInput) {
		t.Fatalf("out of bounds subtitles accepted: %v", err)
	}
}
