package application

import (
	"context"
	"encoding/binary"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/media"
	m "github.com/adcwb/ai-creative-studio/backend/internal/modules/seriesmigration"
)

func TestEpisodeMigrationKeepsOldExportAndRendersCopiedAudioVideoSubtitles(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("FFmpeg required")
	}
	s := testStore(t)
	p, season := episodeMigrationFixture(t, s)
	video := filepath.Join(t.TempDir(), "source.mp4")
	if err := ffmpeg(context.Background(), "-f", "lavfi", "-i", "color=c=blue:s=128x72:r=24:d=1", "-f", "lavfi", "-i", "sine=frequency=440:duration=1", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", "-t", "1", "-f", "mp4", video); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(video)
	if err != nil {
		t.Fatal(err)
	}
	state, err := s.media.AddAsset(p.ID, p.SpaceID, "migration-av-video", "source.mp4", "shot", "", "shot-2", "uploaded", data, "video/mp4")
	if err != nil {
		t.Fatal(err)
	}
	videoID := state.Assets[len(state.Assets)-1].ID
	state, err = s.media.AddAsset(p.ID, p.SpaceID, "migration-av-audio", "voice.wav", "audio", "", "", "uploaded", syntheticWAV(1), "audio/wav")
	if err != nil {
		t.Fatal(err)
	}
	audioID := state.Assets[len(state.Assets)-1].ID
	timeline := MediaTimeline{EpisodeNumber: 7, OutputResolution: "360p", AspectRatio: "16:9", SubtitleMode: "burn", MusicVolume: .2, Clips: []MediaClip{{ID: "av-clip", AssetID: videoID, Duration: 1, PreserveSourceAudio: true, SourceAudioVolume: 1, SubtitleSource: "manual", SubtitleCues: []media.SubtitleCue{{Start: .1, End: .9, Text: "KEPT SUBTITLE"}}}}, AudioTracks: []MediaAudioTrack{{ID: "av-track", AssetID: audioID, Start: .2, Duration: .5, Volume: .2}}}
	state, err = s.media.SaveTimeline(p.ID, p.SpaceID, "migration-av-cut", state.Version, timeline)
	if err != nil {
		t.Fatal(err)
	}
	old, err := s.media.StartEpisodeExport(p.ID, p.SpaceID, "migration-old-export", state.Version, 7)
	if err != nil {
		t.Fatal(err)
	}
	s.media.jobs.wait()
	state, err = s.media.GetChecked(p.ID, p.SpaceID)
	if err != nil || state.Exports[len(state.Exports)-1].Status != "succeeded" {
		t.Fatal("source export failed", err, state.Exports)
	}
	before := sourceRecords(t, s, p)
	oldPath, _, err := s.media.file(p.ID, p.SpaceID, old.ID, true, false)
	if err != nil {
		t.Fatal(err)
	}
	oldProof, err := migrationFileProof(context.Background(), oldPath)
	if err != nil {
		t.Fatal(err)
	}
	parent, err := s.series.Get(context.Background(), p.SpaceID, "season", season.ID)
	if err != nil {
		t.Fatal(err)
	}
	in := m.Input{Episode: 7, SeasonID: season.ID, Ordinal: 7, ProjectVersion: p.Version, MediaVersion: state.Version, SeasonVersion: parent.Version, OutputMode: "preserve", BoardPolicy: "include"}
	plan, err := s.PreviewEpisodeMigration(context.Background(), p.SpaceID, p.ID, in)
	if err != nil || len(plan.Issues) != 0 || len(plan.KeptExports) != 1 || plan.KeptExports[0] != old.ID {
		t.Fatal("old export provenance missing", err, plan.Issues)
	}
	j, err := s.CreateEpisodeMigration(context.Background(), seriesActor, p.ID, "migration-av-create", in, plan.Digest)
	if err != nil {
		t.Fatal(err)
	}
	for j.State != "completed" {
		j = migrationStep(t, s, j)
	}
	if sourceRecords(t, s, p) != before {
		t.Fatal("migration modified old export records")
	}
	afterProof, err := migrationFileProof(context.Background(), oldPath)
	if err != nil || afterProof != oldProof {
		t.Fatal("old encoded bytes changed", err)
	}
	target, err := s.media.GetChecked(j.TargetID, p.SpaceID)
	if err != nil || target.Timeline.EpisodeNumber != 1 || target.Timeline.Clips[0].SourceAudioVolume != 1 || target.Timeline.AudioTracks[0].AssetID == audioID || target.Timeline.Clips[0].AssetID == videoID {
		t.Fatal("AV remapping failed", err)
	}
	exported, err := s.media.StartExport(j.TargetID, p.SpaceID, "migration-new-export", target.Version)
	if err != nil {
		t.Fatal(err)
	}
	s.media.jobs.wait()
	path, _, err := s.media.file(j.TargetID, p.SpaceID, exported.ID, true, false)
	if err != nil {
		t.Fatal(err)
	}
	probe, err := probeMedia(path)
	if err != nil || len(probe.Streams) != 2 || probe.Streams[0].Width != 640 || probe.Streams[0].Height != 360 {
		t.Fatal("copied output streams invalid", err, probe)
	}
	pcm, err := exec.Command("ffmpeg", "-v", "error", "-i", path, "-map", "0:a:0", "-t", "0.15", "-f", "s16le", "-ac", "1", "-").Output()
	if err != nil {
		t.Fatal(err)
	}
	var energy float64
	for i := 0; i+1 < len(pcm); i += 2 {
		value := float64(int16(binary.LittleEndian.Uint16(pcm[i : i+2])))
		energy += value * value
	}
	if len(pcm) == 0 || math.Sqrt(energy/float64(len(pcm)/2)) < 100 {
		t.Fatal("copied source audio missing")
	}
	frame, err := exec.Command("ffmpeg", "-v", "error", "-ss", "0.5", "-i", path, "-frames:v", "1", "-f", "rawvideo", "-pix_fmt", "rgb24", "-").Output()
	if err != nil {
		t.Fatal(err)
	}
	white := 0
	for i := 0; i+2 < len(frame); i += 3 {
		if frame[i] > 160 && frame[i+1] > 160 && frame[i+2] > 160 {
			white++
		}
	}
	if white < 20 {
		t.Fatal("copied subtitle was not burned into actual video")
	}
}
