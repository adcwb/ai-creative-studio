package application

import (
	"context"
	"errors"
	"os"
	"reflect"
	"testing"
)

func exerciseRevisionRestore(t *testing.T, s *FileStore) {
	t.Helper()
	ctx := context.Background()
	p, state, capture := revisionFixture(t, s)
	original := state.Timeline
	saved, err := s.CaptureProductionRevision(ctx, p.SpaceID, p.ID, "main", "owner", "restore-source", capture, nil)
	if err != nil {
		t.Fatal(err)
	}
	changed := state.Timeline
	changed.Clips = append([]MediaClip(nil), changed.Clips...)
	changed.Clips[0].Subtitle = "后来对白"
	changed.Clips[0].AudioVolume = .3
	state, err = s.media.SaveTimeline(p.ID, p.SpaceID, "restore-edited", state.Version, changed)
	if err != nil {
		t.Fatal(err)
	}
	in := revisionRestoreInput{ExpectedProjectVersion: p.Version, ExpectedMediaVersion: state.Version, Component: "subtitles", ClipID: "saved-cut", Episode: 1}
	plan, _, err := s.productionRestore(ctx, p.SpaceID, p.ID, saved.Revision.ID, "", in, false, nil)
	if err != nil || !plan.Changed || plan.Timeline.Clips[0].Subtitle != original.Clips[0].Subtitle || plan.Timeline.Clips[0].AudioVolume != .3 {
		t.Fatal("selective preview", err, plan)
	}
	if got := s.media.Get(p.ID, p.SpaceID); !reflect.DeepEqual(got, state) {
		t.Fatal("preview mutated state")
	}
	stale := in
	stale.ExpectedMediaVersion--
	if _, _, err = s.productionRestore(ctx, p.SpaceID, p.ID, saved.Revision.ID, "stale-restore", stale, true, nil); !errors.Is(err, errMediaVersion) {
		t.Fatal("CAS", err)
	}
	_, restored, err := s.productionRestore(ctx, p.SpaceID, p.ID, saved.Revision.ID, "apply-restore", in, true, func() bool { return true })
	if err != nil || restored.Version != state.Version+1 || restored.Timeline.Clips[0].Subtitle != original.Clips[0].Subtitle || restored.Timeline.Clips[0].AudioVolume != .3 {
		t.Fatal("apply", err, restored)
	}
	if !reflect.DeepEqual(restored.Exports, state.Exports) || !reflect.DeepEqual(restored.LibraryBindings, state.LibraryBindings) || !reflect.DeepEqual(restored.Selections, state.Selections) {
		t.Fatal("restore changed non timeline state")
	}
	later := restored.Timeline
	later.Clips[0].Subtitle = "恢复后继续编辑"
	latest, err := s.media.SaveTimeline(p.ID, p.SpaceID, "restore-later", restored.Version, later)
	if err != nil {
		t.Fatal(err)
	}
	_, replay, err := s.productionRestore(ctx, p.SpaceID, p.ID, saved.Revision.ID, "apply-restore", in, true, nil)
	if err != nil || !reflect.DeepEqual(replay, latest) {
		t.Fatal("replay rewound later edits", err)
	}
	if _, _, err = s.productionRestore(ctx, p.SpaceID, p.ID, saved.Revision.ID, "apply-restore", in, true, func() bool { return false }); !errors.Is(err, errRuntimeDenied) {
		t.Fatal("revoked replay", err)
	}
	if _, _, err = s.productionRestore(ctx, "other", p.ID, saved.Revision.ID, "cross-restore", in, true, nil); err == nil {
		t.Fatal("cross tenant")
	}
	in.ExpectedMediaVersion = latest.Version
	in.Component = "audio"
	asset := original.Clips[0].AudioAssetID
	path, err := s.media.localBlob(p.SpaceID, p.ID, asset)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, []byte("changed bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.productionRestore(ctx, p.SpaceID, p.ID, saved.Revision.ID, "corrupt-restore", in, true, nil); !errors.Is(err, ErrBlobIntegrity) {
		t.Fatal("corrupt asset accepted", err)
	}
	if got := s.media.Get(p.ID, p.SpaceID); !reflect.DeepEqual(got, latest) {
		t.Fatal("failed restore changed state")
	}
}
func TestFileRevisionRestore(t *testing.T) { exerciseRevisionRestore(t, testStore(t)) }
func TestPostgresRevisionRestore(t *testing.T) {
	exerciseRevisionRestore(t, postgresTestStore(t, postgresTestDB(t)))
}
func TestRevisionRestoreComponentIsolation(t *testing.T) {
	a := MediaTimeline{Clips: []MediaClip{{ID: "one", AssetID: "a", Subtitle: "current", AudioVolume: .2}, {ID: "two", Subtitle: "keep"}}}
	b := MediaTimeline{Clips: []MediaClip{{ID: "one", AssetID: "b", Subtitle: "old", AudioVolume: .9}}}
	got, err := mergeRevisionTimeline(a, b, "audio", "one")
	if err != nil || got.Clips[0].AssetID != "a" || got.Clips[0].Subtitle != "current" || got.Clips[0].AudioVolume != .9 || got.Clips[1].Subtitle != "keep" || a.Clips[0].AudioVolume != .2 {
		t.Fatal(got, err)
	}
	for _, part := range []string{"unknown", "clip"} {
		if _, err = mergeRevisionTimeline(a, b, part, "missing"); err == nil {
			t.Fatal("missing clip accepted")
		}
	}
}

func TestRevisionRestoreLegacyEpisodeIsolation(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	p, state, capture := revisionFixture(t, s)
	second := state.Timeline
	second.EpisodeNumber = 2
	second.Clips = append([]MediaClip(nil), second.Clips...)
	second.Clips[0].ID = "episode-two-cut"
	second.Clips[0].Subtitle = "Second episode source"
	state, err := s.media.SaveTimeline(p.ID, p.SpaceID, "restore-second-source", state.Version, second)
	if err != nil {
		t.Fatal(err)
	}
	capture.ExpectedMediaVersion = state.Version
	saved, err := s.CaptureProductionRevision(ctx, p.SpaceID, p.ID, "main", "owner", "restore-second-snapshot", capture, nil)
	if err != nil {
		t.Fatal(err)
	}
	changed := second
	changed.Clips = append([]MediaClip(nil), second.Clips...)
	changed.Clips[0].Subtitle = "Later second episode"
	state, err = s.media.SaveTimeline(p.ID, p.SpaceID, "restore-second-change", state.Version, changed)
	if err != nil {
		t.Fatal(err)
	}
	before := state.Timeline
	plan, restored, err := s.productionRestore(ctx, p.SpaceID, p.ID, saved.Revision.ID, "restore-second-apply", revisionRestoreInput{ExpectedProjectVersion: p.Version, ExpectedMediaVersion: state.Version, Component: "timeline", Episode: 2}, true, nil)
	if err != nil || plan.Current.Clips[0].Subtitle != "Later second episode" || restored.EpisodeTimelines[2].Clips[0].Subtitle != "Second episode source" || !reflect.DeepEqual(before, restored.Timeline) {
		t.Fatal("episode isolation", err, plan)
	}
}

func TestRevisionRestoreRejectsHistoricalCuesOutsideCurrentClip(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	p, state, capture := revisionFixture(t, s)
	timeline := state.Timeline
	timeline.Clips[0].SubtitleSource = "manual"
	timeline.Clips[0].SubtitleCues = []SubtitleCue{{Start: 0, End: 2, Text: "Historical cue"}}
	state, err := s.media.SaveTimeline(p.ID, p.SpaceID, "restore-cue-source", state.Version, timeline)
	if err != nil {
		t.Fatal(err)
	}
	capture.ExpectedMediaVersion = state.Version
	saved, err := s.CaptureProductionRevision(ctx, p.SpaceID, p.ID, "main", "owner", "restore-cue-snapshot", capture, nil)
	if err != nil {
		t.Fatal(err)
	}
	timeline = state.Timeline
	timeline.Clips[0].Duration = 1
	timeline.Clips[0].SubtitleCues = []SubtitleCue{{Start: 0, End: 1, Text: "Current cue"}}
	state, err = s.media.SaveTimeline(p.ID, p.SpaceID, "restore-cue-shorter", state.Version, timeline)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = s.productionRestore(ctx, p.SpaceID, p.ID, saved.Revision.ID, "restore-invalid-cues", revisionRestoreInput{ExpectedProjectVersion: p.Version, ExpectedMediaVersion: state.Version, Component: "subtitles", ClipID: timeline.Clips[0].ID, Episode: 1}, true, nil)
	if !errors.Is(err, errMediaInput) {
		t.Fatal("out-of-range historical cues accepted", err)
	}
	if got := s.media.Get(p.ID, p.SpaceID); !reflect.DeepEqual(got, state) {
		t.Fatal("rejected restore changed timeline")
	}
}
