package application

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/adcwb/ai-creative-studio/backend/internal/platform/jobqueue"
)

func animaticWave() []byte {
	const rate = 8000
	n := rate
	raw := make([]byte, 44+n*2)
	copy(raw, "RIFF")
	binary.LittleEndian.PutUint32(raw[4:], uint32(len(raw)-8))
	copy(raw[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(raw[16:], 16)
	binary.LittleEndian.PutUint16(raw[20:], 1)
	binary.LittleEndian.PutUint16(raw[22:], 1)
	binary.LittleEndian.PutUint32(raw[24:], rate)
	binary.LittleEndian.PutUint32(raw[28:], rate*2)
	binary.LittleEndian.PutUint16(raw[32:], 2)
	binary.LittleEndian.PutUint16(raw[34:], 16)
	copy(raw[36:], "data")
	binary.LittleEndian.PutUint32(raw[40:], uint32(n*2))
	for i := 0; i < n; i++ {
		v := int16(10000 * math.Sin(2*math.Pi*440*float64(i)/rate))
		binary.LittleEndian.PutUint16(raw[44+i*2:], uint16(v))
	}
	return raw
}
func animaticFixture(t *testing.T, s *FileStore) (Project, MediaState, PreviewInput) {
	t.Helper()
	p := directorFixture(t, s)
	var state MediaState
	var e error
	in := PreviewInput{ExpectedProjectVersion: p.Version, EpisodeNumber: 1, AspectRatio: "16:9", SoundMode: "temporary", Shots: []previewShotInput{}}
	for i, shot := range p.Storyboard.Shots[:2] {
		state, e = s.media.AddAsset(p.ID, p.SpaceID, "animatic-image-"+shot.ID, shot.ID+".png", "shot", "", shot.ID, "uploaded", syntheticCreativePNG(), "image/png")
		if e != nil {
			t.Fatal(e)
		}
		in.Shots = append(in.Shots, previewShotInput{ShotID: shot.ID, AssetID: state.Assets[len(state.Assets)-1].ID, Duration: float64(i+6) / 10})
	}
	state, e = s.media.AddAsset(p.ID, p.SpaceID, "animatic-audio", "temporary.wav", "voice", "", "", "uploaded", animaticWave(), "audio/wav")
	if e != nil {
		t.Fatal(e)
	}
	in.Shots[0].AudioAssetID = state.Assets[len(state.Assets)-1].ID
	in.ExpectedMediaVersion = state.Version
	return p, state, in
}
func TestAnimaticActualRenderAndOriginalSource(t *testing.T) {
	for _, storage := range []string{"file", "postgres"} {
		t.Run(storage, func(t *testing.T) {
			s := testStore(t)
			var remote *FileBlobStore
			if storage == "postgres" {
				s = postgresTestStore(t, postgresTestDB(t))
				var err error
				remote, err = NewFileBlobStore(t.TempDir())
				if err != nil {
					t.Fatal(err)
				}
				defer remote.Close()
				if err = configureBlobStorage(s, remote); err != nil {
					t.Fatal(err)
				}
			}
			p, before, in := animaticFixture(t, s)
			if storage == "file" {
				s.media.jobs = newLocalJobs(0)
			}
			e, err := s.CreateMediaPreview(context.Background(), p.SpaceID, p.ID, "preview-original", in, "", nil)
			if err != nil {
				t.Fatal(err)
			}
			if e.Purpose != "animatic" || e.PreviewSource == nil || len(e.PreviewSource.Assets) != 3 || e.Timeline.SubtitleMode != "burn" || e.Duration != 31.0/24 {
				t.Fatal("source or timing not pinned", e)
			}
			original := cloneMediaExport(e)
			e.PreviewSource.Shots[0].Shot.Sound = "caller mutation"
			e.Timeline.Clips[0].Duration = 999
			replay, err := s.CreateMediaPreview(context.Background(), p.SpaceID, p.ID, "preview-original", in, "", nil)
			if err != nil || !reflect.DeepEqual(replay.PreviewSource, original.PreviewSource) || mediaDigest(replay.Timeline) != mediaDigest(original.Timeline) {
				t.Fatal("returned snapshot shared state", err)
			}
			changed := append([]StoryboardShot{}, p.Storyboard.Shots...)
			changed[0].Description = "LATER_BOARD_NOT_USED"
			newer, err := s.SaveProjectStoryboard(p.SpaceID, p.ID, "animatic-change-board", StoryboardSaveInput{ExpectedVersion: p.Version, Shots: changed})
			if err != nil {
				t.Fatal(err)
			}
			formal := before.Timeline
			formal.Clips = []MediaClip{{ID: "formal-clip", AssetID: in.Shots[1].AssetID, Duration: 2, AudioVolume: 1, Subtitle: "formal cut"}}
			after, err := s.media.SaveTimeline(p.ID, p.SpaceID, "animatic-change-cut", before.Version, formal)
			if err != nil {
				t.Fatal(err)
			}
			replay, err = s.CreateMediaPreview(context.Background(), p.SpaceID, p.ID, "preview-original", in, "", nil)
			if err != nil || replay.ID != original.ID {
				t.Fatal("accepted replay lost after source changes", err)
			}
			mutated := in
			mutated.Shots = append([]previewShotInput{}, in.Shots...)
			mutated.Shots[0].Duration = 4
			if _, err = s.CreateMediaPreview(context.Background(), p.SpaceID, p.ID, "preview-original", mutated, "", nil); !errors.Is(err, errMediaKey) {
				t.Fatal("different meaning replayed", err)
			}
			if storage == "file" {
				ctx := context.WithValue(context.Background(), renderScopeKey{}, renderScope{Space: p.SpaceID, Project: p.ID})
				if err = s.media.render(ctx, after, replay); err != nil {
					t.Fatal("actual animatic render", err)
				}
				if err = s.media.render(ctx, after, replay); err != nil {
					t.Fatal("duplicate render", err)
				}
				s.media.mu.Lock()
				err = s.media.finishExportLocked(mediaKey(p.SpaceID, p.ID), replay.ID, p.SpaceID, p.ID, nil)
				s.media.mu.Unlock()
				if err != nil {
					t.Fatal(err)
				}
			} else {
				db := s.media.pg.db
				worker, err := OpenPostgresStore(t.TempDir(), db)
				if err != nil {
					t.Fatal(err)
				}
				defer worker.Close()
				if err = configureBlobStorage(worker, remote); err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithCancel(context.Background())
				done := make(chan error, 1)
				go func() { done <- RunQueuedExportWorker(ctx, jobqueue.Queue{DB: db}, worker) }()
				defer func() { cancel(); <-done }()
				deadline := time.Now().Add(20 * time.Second)
				for {
					got, _ := s.media.preview(p.ID, p.SpaceID, replay.ID)
					if got.Status == "succeeded" {
						break
					}
					if got.Status == "failed" || time.Now().After(deadline) {
						t.Fatal("queued preview failed", got.Status, got.Error)
					}
					time.Sleep(20 * time.Millisecond)
				}
			}
			final, err := s.media.preview(p.ID, p.SpaceID, replay.ID)
			if err != nil || final.Status != "succeeded" || !reflect.DeepEqual(final.PreviewSource, original.PreviewSource) {
				t.Fatal("completion changed fixed source", final, err)
			}
			got := s.media.Get(p.ID, p.SpaceID)
			if got.Version != after.Version || !reflect.DeepEqual(got.Timeline, after.Timeline) || !reflect.DeepEqual(got.Selections, before.Selections) || len(got.Assets) != len(before.Assets) {
				t.Fatal("preview changed edit or selections")
			}
			current, _ := s.GetProject(p.SpaceID, p.ID)
			if current.Version != newer.Version || current.Storyboard.Shots[0].Description != changed[0].Description {
				t.Fatal("preview changed current plan")
			}
			if _, _, err = socialSource(got, p.SpaceID, p.ID, SocialCreate{ExportID: final.ID}); !errors.Is(err, errMediaInput) {
				t.Fatal("animatic offered as formal delivery", err)
			}
			file, _, err := s.media.file(p.ID, p.SpaceID, final.ID, true, false)
			if err != nil {
				t.Fatal(err)
			}
			if err = verifyRenderArtifact(context.Background(), file, "final", 31, 640, 360); err != nil {
				t.Fatal(err)
			}
			pcm, err := exec.Command("ffmpeg", "-v", "error", "-i", file, "-map", "0:a:0", "-t", frameSeconds(31), "-f", "s16le", "-ac", "2", "-ar", "48000", "-").Output()
			if err != nil || len(pcm) != 31*2000*4 {
				t.Fatal("audio sample count", len(pcm), err)
			}
			energy := func(begin, end int) float64 {
				sum := 0.0
				for i := begin * 4; i < end*4; i += 4 {
					sum += math.Abs(float64(int16(binary.LittleEndian.Uint16(pcm[i:]))))
				}
				return sum / float64(end-begin)
			}
			if energy(2000, 10000) < 500 || energy(50000, 60000) > 5 {
				t.Fatal("temporary voice lost, looped or leaked into silent shot", energy(2000, 10000), energy(50000, 60000))
			}
		})
	}
}
func TestAnimaticAdmissionScopePaginationAndBytePins(t *testing.T) {
	s := testStore(t)
	p, state, in := animaticFixture(t, s)
	s.media.jobs = newLocalJobs(0)
	for _, bad := range []PreviewInput{func() PreviewInput { v := in; v.SoundMode = "silent"; return v }(), func() PreviewInput { v := in; v.SoundMode = "unknown"; return v }(), func() PreviewInput {
		v := in
		v.Shots = []previewShotInput{{ShotID: "missing", AssetID: in.Shots[0].AssetID, Duration: 1}}
		return v
	}()} {
		if _, e := s.CreateMediaPreview(context.Background(), p.SpaceID, p.ID, "bad-preview", bad, "", nil); e == nil {
			t.Fatal("invalid preview accepted", bad)
		}
	}
	if _, e := s.CreateMediaPreview(context.Background(), p.SpaceID, p.ID, "denied-preview", in, "", func() bool { return false }); !errors.Is(e, errRuntimeDenied) {
		t.Fatal("revoked write accepted", e)
	}
	v, e := s.CreateMediaPreview(context.Background(), p.SpaceID, p.ID, "bad-preview", in, "", nil)
	if e != nil {
		t.Fatal("invalid attempt consumed key", e)
	}
	repeated := in
	repeated.Shots = append(append([]previewShotInput{}, in.Shots...), in.Shots[0])
	other, e := s.CreateMediaPreview(context.Background(), p.SpaceID, p.ID, "repeat-shot", repeated, "", nil)
	if e != nil || other.Timeline.Clips[0].ID == other.Timeline.Clips[2].ID {
		t.Fatal("repeated shot not supported", e)
	}
	page, e := s.media.previews(p.ID, p.SpaceID, "", 1)
	if e != nil || len(page.Items) != 1 || page.Items[0].ID != other.ID || page.NextCursor == "" {
		t.Fatal("metadata page", page, e)
	}
	older, e := s.media.previews(p.ID, p.SpaceID, page.NextCursor, 1)
	if e != nil || older.Items[0].ID != v.ID || older.NextCursor != "" {
		t.Fatal("page traversal", older, e)
	}
	raw, _ := json.Marshal(page)
	if bytes.Contains(raw, []byte("PRIVATE_SOUND")) || bytes.Contains(raw, []byte("\"preview_source\"")) {
		t.Fatal("list returned large source body")
	}
	if _, e = s.media.previews(p.ID, "team-demo", page.NextCursor, 1); e == nil {
		t.Fatal("cursor crossed scope")
	}
	if _, e = s.CreateMediaPreview(context.Background(), "team-demo", p.ID, "foreign", in, "", nil); !errors.Is(e, ErrNotFound) {
		t.Fatal("cross-space create", e)
	}
	// Pinning occurs at admission rather than at the first worker checkpoint.
	path, _, e := s.media.file(p.ID, p.SpaceID, in.Shots[0].AssetID, false, false)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(path, syntheticPNG(9), 0600); e != nil {
		t.Fatal(e)
	}
	ctx := context.WithValue(context.Background(), renderScopeKey{}, renderScope{Space: p.SpaceID, Project: p.ID})
	if e = s.media.render(ctx, state, v); !errors.Is(e, errRenderInputsChanged) {
		t.Fatal("changed image accepted before first render", e)
	}
}
func TestAnimaticHistoricalEpisodeScope(t *testing.T) {
	s := testStore(t)
	p, _, e := s.CreateProject("personal-demo", "legacy-preview", projectInput())
	if e != nil {
		t.Fatal(e)
	}
	script := scriptFixture(t)
	script.Scenes[0].EpisodeNumber = 1
	script.Scenes[1].EpisodeNumber = 7
	p, e = s.SaveProjectScript(p.SpaceID, p.ID, "legacy-script", ScriptSaveInput{ExpectedVersion: p.Version, Script: script})
	if e != nil {
		t.Fatal(e)
	}
	shots := []StoryboardShot{{ID: "old-one", SceneIndex: 1, Description: "original episode one", ShotSize: "wide", CameraMovement: "static", DurationSeconds: 1, Dialogue: ""}, {ID: "old-seven", SceneIndex: 2, Description: "original episode seven", ShotSize: "medium", CameraMovement: "static", DurationSeconds: 1, Dialogue: ""}}
	p, e = s.SaveProjectStoryboard(p.SpaceID, p.ID, "legacy-board", StoryboardSaveInput{ExpectedVersion: p.Version, Shots: shots})
	if e != nil {
		t.Fatal(e)
	}
	script.Scenes[0], script.Scenes[1] = script.Scenes[1], script.Scenes[0]
	p, e = s.SaveProjectScript(p.SpaceID, p.ID, "legacy-script-reorder", ScriptSaveInput{ExpectedVersion: p.Version, Script: script})
	if e != nil {
		t.Fatal(e)
	}
	options, e := s.MediaPreviewOptions(p.SpaceID, p.ID)
	if e != nil || options.ShotEpisodes["old-one"] != 1 || options.ShotEpisodes["old-seven"] != 7 {
		t.Fatal("current reordered script relabelled old plan", options.ShotEpisodes, e)
	}
	options.Project.Storyboard.Shots[0].Description = "caller change"
	again, _ := s.MediaPreviewOptions(p.SpaceID, p.ID)
	if again.Project.Storyboard.Shots[0].Description == "caller change" {
		t.Fatal("options shared current source")
	}
}
func TestPostgresAnimaticOfflineRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s, e := OpenStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	p, _, in := animaticFixture(t, s)
	s.media.jobs = newLocalJobs(0)
	original, e := s.CreateMediaPreview(context.Background(), p.SpaceID, p.ID, "offline-preview", in, "", nil)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Close(); e != nil {
		t.Fatal(e)
	}
	db := postgresTestDB(t)
	if _, e = ImportPostgresData(context.Background(), dir, db); e != nil {
		t.Fatal(e)
	}
	pg := postgresTestStore(t, db)
	got, e := pg.media.preview(p.ID, p.SpaceID, original.ID)
	if e != nil || validatePreviewExport(got) != nil || !reflect.DeepEqual(got.PreviewSource, original.PreviewSource) {
		t.Fatal("source changed on import", e)
	}
	target := filepath.Join(t.TempDir(), "restored")
	if _, e = ExportPostgresData(context.Background(), target, db); e != nil {
		t.Fatal(e)
	}
	restored, e := OpenStore(target)
	if e != nil {
		t.Fatal(e)
	}
	defer restored.Close()
	got, e = restored.CreateMediaPreview(context.Background(), p.SpaceID, p.ID, "offline-preview", in, "", nil)
	if e != nil || got.ID != original.ID || !reflect.DeepEqual(got.PreviewSource, original.PreviewSource) || !strings.Contains(got.Timeline.Clips[0].Subtitle, "ANIMATIC") {
		t.Fatal("offline preview receipt/source lost", e)
	}
}

func TestAnimaticCancelResumeKeepsIdentityAndSource(t *testing.T) {
	s := testStore(t)
	p, _, in := animaticFixture(t, s)
	s.media.jobs = newLocalJobs(0)
	original, e := s.CreateMediaPreview(context.Background(), p.SpaceID, p.ID, "cancel-preview", in, "", nil)
	if e != nil {
		t.Fatal(e)
	}
	canceled, e := s.media.CancelExport(p.ID, p.SpaceID, "cancel-original", original.ID)
	if e != nil || canceled.Status != "cancelled" || !canceled.ResumeAvailable {
		t.Fatal(canceled, e)
	}
	for range 2 {
		v, e := s.media.ResumeExport(p.ID, p.SpaceID, "resume-original", original.ID)
		if e != nil || v.ID != original.ID || v.Status != "queued" || !reflect.DeepEqual(v.PreviewSource, original.PreviewSource) {
			t.Fatal("resume replaced source", v, e)
		}
		v.PreviewSource.Shots[0].Shot.Description = "response mutation"
	}
	if got, _ := s.media.preview(p.ID, p.SpaceID, original.ID); got.PreviewSource.Shots[0].Shot.Description != original.PreviewSource.Shots[0].Shot.Description {
		t.Fatal("resume response mutated stored source")
	}
	state := s.media.Get(p.ID, p.SpaceID)
	if len(state.Exports) != 1 {
		t.Fatal("resume duplicated preview")
	}
	if _, e = s.media.preview(p.ID, p.SpaceID, "missing"); !errors.Is(e, ErrNotFound) {
		t.Fatal(e)
	}
}

func TestAnimaticTwoMinuteSilentOutputHasBurnedIdentity(t *testing.T) {
	s := testStore(t)
	p, _, in := animaticFixture(t, s)
	var imageData bytes.Buffer
	if e := png.Encode(&imageData, image.NewRGBA(image.Rect(0, 0, 640, 360))); e != nil {
		t.Fatal(e)
	}
	state, e := s.media.AddAsset(p.ID, p.SpaceID, "animatic-black-source", "own-black-frame.png", "shot", "", in.Shots[0].ShotID, "uploaded", imageData.Bytes(), "image/png")
	if e != nil {
		t.Fatal(e)
	}
	in.ExpectedMediaVersion = state.Version
	in.SoundMode = "silent"
	in.Shots = []previewShotInput{{ShotID: in.Shots[0].ShotID, AssetID: state.Assets[len(state.Assets)-1].ID, Duration: 120}}
	result, e := s.CreateMediaPreview(context.Background(), p.SpaceID, p.ID, "two-minute-animatic", in, "", nil)
	if e != nil {
		t.Fatal(e)
	}
	s.media.jobs.wait()
	got, e := s.media.preview(p.ID, p.SpaceID, result.ID)
	if e != nil || got.Status != "succeeded" || got.Duration != 120 {
		t.Fatal("two-minute output", got.Status, got.Error, e)
	}
	path, e := s.media.localBlob(p.SpaceID, p.ID, result.ID+".mp4")
	if e != nil {
		t.Fatal(e)
	}
	if e = verifyRenderArtifact(context.Background(), path, "final", 2880, 640, 360); e != nil {
		t.Fatal(e)
	}
	// The owned source is entirely black. Bright pixels in the rendered frame
	// therefore establish the preview label was burned into the actual pixels.
	frame, e := exec.Command("ffmpeg", "-v", "error", "-ss", "1", "-i", path, "-frames:v", "1", "-f", "rawvideo", "-pix_fmt", "rgb24", "-").Output()
	if e != nil || len(frame) != 640*360*3 {
		t.Fatal("preview identity frame", len(frame), e)
	}
	bright := 0
	for i := 0; i < len(frame); i += 3 {
		if frame[i] > 200 && frame[i+1] > 200 && frame[i+2] > 200 {
			bright++
		}
	}
	if bright < 30 {
		t.Fatal("preview label absent from rendered pixels", bright)
	}
	current := s.media.Get(p.ID, p.SpaceID)
	if len(current.Timeline.Clips) != 0 || len(current.Selections) != 0 {
		t.Fatal("long preview wrote the formal edit")
	}
}
