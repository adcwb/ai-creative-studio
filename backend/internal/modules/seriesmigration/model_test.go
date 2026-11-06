package seriesmigration

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/media"
	"github.com/adcwb/ai-creative-studio/backend/internal/modules/projects"
	"github.com/adcwb/ai-creative-studio/backend/internal/modules/publishing"
	"github.com/adcwb/ai-creative-studio/backend/internal/modules/series"
)

func fixture() (Source, series.Node, Input) {
	p := projects.Project{ID: strings.Repeat("a", 32), SpaceID: "space", Title: "旧多集", Brief: "保持原稿的确定性迁移", Version: 3, ScriptVersion: 1, Script: &projects.Script{Title: "旧剧本", Logline: "三个离散集号的旧剧本", Characters: []projects.ScriptCharacter{{Name: "小满", Description: "旅人"}}, Scenes: []projects.ScriptScene{{Heading: "河边", Action: "人物经过", Dialogue: []projects.ScriptDialogue{}, EpisodeNumber: 1}, {Heading: "街道", Action: "人物相遇", Dialogue: []projects.ScriptDialogue{{Character: "小满", Text: "你好"}}, EpisodeNumber: 7}, {Heading: "归途", Action: "人物返程", Dialogue: []projects.ScriptDialogue{}, EpisodeNumber: 120}}}}
	p.Storyboard = &projects.Storyboard{SourceScriptVersion: 1, Shots: []projects.StoryboardShot{{ID: "seven", SceneIndex: 2, Description: "街道镜头", ShotSize: "wide", CameraMovement: "static", DurationSeconds: 3}, {ID: "independent", SceneIndex: 0, Description: "空镜", ShotSize: "wide", CameraMovement: "static", DurationSeconds: 3}}}
	image := strings.Repeat("b", 32)
	audio := strings.Repeat("c", 32)
	video := strings.Repeat("d", 32)
	state := media.MediaState{Version: 4, Assets: []media.MediaAsset{{ID: image, Kind: "image", MIMEType: "image/png", ShotID: "seven"}, {ID: audio, Kind: "audio", MIMEType: "audio/wav", Duration: 3}, {ID: video, Kind: "video", MIMEType: "video/mp4", Duration: 3}}, EpisodeTimelines: map[int]media.MediaTimeline{7: {EpisodeNumber: 7, OutputResolution: "720p", AspectRatio: "16:9", MusicAssetID: audio, MusicVolume: .2, Clips: []media.MediaClip{{ID: "clip", AssetID: image, Duration: 1, ShotID: "seven", AudioAssetID: audio, AudioVolume: .8, SourceAudioAssetID: video, PreserveSourceAudio: true, SourceAudioVolume: .4, CropX: .1, CropY: .1, CropWidth: .8, CropHeight: .8, SubtitleSource: "asr", SubtitleAlignmentID: "original-task", SubtitleCues: []media.SubtitleCue{{Start: .1, End: .8, Text: "原字幕"}}}}, AudioTracks: []media.MediaAudioTrack{{ID: "track", AssetID: audio, Duration: 1, Volume: .2}}}}, Social: &publishing.SocialState{Jobs: []publishing.SocialJob{{PublishID: "must-not-copy-publish", PhotoGrantID: "must-not-copy-grant"}}, Connections: []publishing.SocialConnection{{AccountKey: "must-not-copy-connection"}}}}
	season := series.Node{ID: strings.Repeat("e", 32), Kind: "season", Space: "space", State: "ready", Status: "active", Version: 1, Profile: series.DefaultProfile()}
	in := Input{Episode: 7, SeasonID: season.ID, Ordinal: 7, ProjectVersion: 3, MediaVersion: 4, SeasonVersion: 1, OutputMode: "series", BoardPolicy: "include", ShotAssignments: map[string]int{"independent": 0}}
	return Source{Project: p, Media: state}, season, in
}
func TestPreviewRequiresExplicitChoicesAndKeepsSourceImmutable(t *testing.T) {
	src, season, in := fixture()
	before, _ := json.Marshal(src)
	in.ShotAssignments = nil
	p, e := Preview(src, season, in)
	if e != nil || len(p.Issues) != 1 || p.Issues[0].Code != "shot_assignment_required" {
		t.Fatal("unknown shot was guessed", p.Issues, e)
	}
	in.ShotAssignments = map[string]int{"independent": 7}
	p, e = Preview(src, season, in)
	if e != nil || len(p.Issues) != 0 {
		t.Fatal(p.Issues, e)
	}
	if len(p.Script.Scenes) != 1 || p.Script.Scenes[0].EpisodeNumber != 1 || len(p.Storyboard.Shots) != 2 || p.Storyboard.Shots[0].SceneIndex != 1 || p.Storyboard.Shots[0].SceneID != p.Script.Scenes[0].ID || p.Storyboard.Shots[1].SceneIndex != 0 {
		t.Fatal("scene/shot mapping wrong")
	}
	if len(p.Assets) != 3 || p.Timeline.Clips[0].SourceAudioAssetID == "" || p.Timeline.Clips[0].CropWidth != .8 || p.Timeline.Clips[0].SubtitleSource != "imported" || p.Timeline.Clips[0].SubtitleAlignmentID != "" || p.SubtitleOrigins[0].AlignmentID != "original-task" {
		t.Fatal("audio, crop or subtitle provenance dropped")
	}
	encoded, _ := json.Marshal(p)
	for _, secret := range []string{"must-not-copy-publish", "must-not-copy-grant", "must-not-copy-connection"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatal("execution/publishing state copied")
		}
	}
	after, _ := json.Marshal(src)
	again, e := Preview(src, season, in)
	if e != nil || string(before) != string(after) || p.Digest != again.Digest || p.Digest != Seal(p) {
		t.Fatal("preview mutated original or was nondeterministic", e)
	}
}

func TestPreviewKeepsMetadataOnlyVoiceAndStyleBindingsInTheirShotScope(t *testing.T) {
	src, season, in := fixture()
	src.Media.LibraryBindings = []media.LibraryBinding{
		{ID: "voice", Kind: "voice", Usage: "voice", AssetID: "voice-card", VersionID: "voice-v1", MediaAssetIDs: []string{}},
		{ID: "style", Kind: "style", Usage: "style", AssetID: "style-card", VersionID: "style-v1", ShotID: "seven", MediaAssetIDs: []string{}},
		{ID: "other-shot", Kind: "voice", Usage: "voice", AssetID: "other-voice", VersionID: "other-v1", ShotID: "independent", MediaAssetIDs: []string{}},
	}
	before, _ := json.Marshal(src)
	plan, err := Preview(src, season, in)
	if err != nil || len(plan.Bindings) != 2 || plan.Bindings[0].VersionID != "voice-v1" || plan.Bindings[1].VersionID != "style-v1" || len(plan.Bindings[0].MediaAssetIDs) != 0 {
		t.Fatal("metadata-only native/intent bindings were lost or broadened", plan.Bindings, err)
	}
	after, _ := json.Marshal(src)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("rewrote source bindings")
	}
}
func TestPreviewStableSceneAssociationSurvivesReorderAndLegacyStaleNeedsChoice(t *testing.T) {
	src, season, in := fixture()
	stable, _, e := projects.CompileStructure(*src.Project.Script, "space:"+src.Project.ID, 1)
	if e != nil {
		t.Fatal(e)
	}
	old := projects.CloneScript(stable)
	src.Project.Script = &stable
	src.Project.ScriptVersion = 2
	stable.Scenes[0], stable.Scenes[1] = stable.Scenes[1], stable.Scenes[0]
	src.Project.Storyboard.StructureVersion = 2
	src.Project.Storyboard.SourceSceneIDs = projects.SceneIDs(old)
	src.Project.Storyboard.Shots[0].SceneID = old.Scenes[1].ID
	p, e := Preview(src, season, in)
	if e != nil || len(p.Issues) != 0 || p.Storyboard.Shots[0].SceneID != stable.Scenes[0].ID {
		t.Fatal("stale board rebound by current index", p.Issues, e)
	}
	src.Project.Storyboard.StructureVersion = 0
	src.Project.Storyboard.SourceSceneIDs = nil
	src.Project.Storyboard.Shots[0].SceneID = ""
	src.BoardSource = nil
	p, e = Preview(src, season, in)
	if e != nil || len(p.Issues) == 0 {
		t.Fatal("missing legacy source guessed", e)
	}
	in.BoardPolicy = "omit"
	p, e = Preview(src, season, in)
	if e != nil || len(p.Issues) != 0 || p.Storyboard != nil || p.Timeline.Clips[0].ShotID != "" || len(p.Assets) != 3 {
		t.Fatal("explicit omission lost timeline or assets", p.Issues, e)
	}
}
func TestPreviewRejectsCorruptAndCrossContextReferences(t *testing.T) {
	for name, mutate := range map[string]func(*Source, *series.Node, *Input){
		"wrong space":  func(_ *Source, n *series.Node, _ *Input) { n.Space = "other" },
		"stale source": func(_ *Source, _ *series.Node, in *Input) { in.ProjectVersion++ },
		"duplicate shot": func(s *Source, _ *series.Node, _ *Input) {
			s.Project.Storyboard.Shots[1].ID = s.Project.Storyboard.Shots[0].ID
		},
		"scene out of range": func(s *Source, _ *series.Node, in *Input) {
			s.Project.Storyboard.Shots[0].SceneIndex = 99
			in.ShotAssignments["seven"] = 7
		},
		"missing asset":      func(s *Source, _ *series.Node, _ *Input) { s.Media.Assets = s.Media.Assets[:1] },
		"unknown assignment": func(_ *Source, _ *series.Node, in *Input) { in.ShotAssignments["not-a-shot"] = 7 },
		"future structure":   func(s *Source, _ *series.Node, _ *Input) { s.Project.Storyboard.StructureVersion = 3 },
	} {
		t.Run(name, func(t *testing.T) {
			src, n, in := fixture()
			mutate(&src, &n, &in)
			p, e := Preview(src, n, in)
			if e == nil && len(p.Issues) == 0 {
				t.Fatal("invalid plan accepted")
			}
		})
	}
	src, n, in := fixture()
	in.OutputMode = "preserve"
	p, e := Preview(src, n, in)
	if e != nil || p.Timeline.OutputResolution != "720p" || p.Timeline.AspectRatio != "16:9" {
		t.Fatal("legacy output silently changed", e)
	}
	clone, e := Clone(src)
	if e != nil || !reflect.DeepEqual(clone.Project.Script, src.Project.Script) {
		t.Fatal("detached source clone changed text", e)
	}
}
