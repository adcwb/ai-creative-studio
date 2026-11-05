package application

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/adcwb/ai-creative-studio/backend/internal/platform/jobqueue"
)

func TestRenderCheckpointAnotherWorkerUsesSharedArtifactsAndSourceAudio(t *testing.T) {
	db := postgresTestDB(t)
	api := postgresTestStore(t, db)
	root := t.TempDir()
	objects, e := NewFileBlobStore(root)
	if e != nil {
		t.Fatal(e)
	}
	if e = configureBlobStorage(api, objects); e != nil {
		t.Fatal(e)
	}
	project, _, e := api.CreateProject("render-space", "shared-render-project", projectInput())
	if e != nil {
		t.Fatal(e)
	}
	p, e := api.media.Demo(project.ID, project.SpaceID, "demo", 1, nil)
	if e != nil {
		t.Fatal(e)
	}
	video := filepath.Join(t.TempDir(), "source.mp4")
	if e = ffmpeg(context.Background(), "-f", "lavfi", "-i", "color=c=blue:s=64x64:r=24:d=1", "-f", "lavfi", "-i", "sine=frequency=880:sample_rate=48000:duration=1", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", "-t", "1", video); e != nil {
		t.Fatal(e)
	}
	b, e := os.ReadFile(video)
	if e != nil {
		t.Fatal(e)
	}
	p, e = api.media.AddAsset(project.ID, project.SpaceID, "source-audio", "Source audio", "shot", "", "", "synthetic", b, "video/mp4")
	if e != nil {
		t.Fatal(e)
	}
	source := p.Assets[len(p.Assets)-1]
	timeline := p.Timeline
	timeline.Clips = timeline.Clips[:2]
	timeline.MusicAssetID = ""
	for i := range timeline.Clips {
		timeline.Clips[i].Duration = 1
		timeline.Clips[i].AudioAssetID = ""
	}
	timeline.Clips[1].PreserveSourceAudio = true
	timeline.Clips[1].SourceAudioAssetID = source.ID
	timeline.Clips[1].SourceAudioVolume = 1
	p, e = api.media.SaveTimeline(project.ID, project.SpaceID, "timeline", p.Version, timeline)
	if e != nil {
		t.Fatal(e)
	}
	export, e := api.media.StartExport(project.ID, project.SpaceID, "export", p.Version)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = api.media.CancelExport(project.ID, project.SpaceID, "cancel-before-worker", export.ID); e != nil {
		t.Fatal(e)
	}
	for range 2 {
		if resumed, e := api.media.ResumeExport(project.ID, project.SpaceID, "resume-before-worker", export.ID); e != nil || resumed.ID != export.ID || resumed.Status != "queued" {
			t.Fatal("PG resume", resumed, e)
		}
	}
	var jobs int
	if e = db.QueryRow(`SELECT count(*) FROM execution_jobs WHERE id=$1`, export.ID).Scan(&jobs); e != nil || jobs != 1 {
		t.Fatal("resume duplicated queue identity", jobs, e)
	}
	q := jobqueue.Queue{DB: db, Lease: time.Minute}
	old, ok, e := q.Claim(context.Background(), "export")
	if e != nil || !ok {
		t.Fatal(old, ok, e)
	}
	if e = q.Dispatch(context.Background(), old); e != nil {
		t.Fatal(e)
	}
	ctx := context.WithValue(context.Background(), renderScopeKey{}, renderScope{Space: project.SpaceID, Project: project.ID, Lease: &old})
	stop := errors.New("synthetic process interruption")
	e = api.media.withRenderSession(ctx, p, export, func(s *renderSession) error {
		_, e := s.artifact("block-0-0-24-video", "video", 24, func(out string) error {
			return api.media.renderClipVideo(ctx, p, timeline.Clips[0], 0, 24, out, 640, 360, "4M")
		})
		if e != nil {
			return e
		}
		return stop
	})
	if !errors.Is(e, stop) {
		t.Fatal(e)
	}
	step := s3Hash("block-0-0-24-video")
	var before []byte
	if e = db.QueryRow(`SELECT payload FROM studio_render_checkpoints WHERE export_id=$1 AND step=$2`, export.ID, step).Scan(&before); e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(`UPDATE execution_jobs SET lease_until=now()-interval '1 second' WHERE id=$1`, export.ID); e != nil {
		t.Fatal(e)
	}
	worker := postgresTestStore(t, db)
	shared, e := NewFileBlobStore(root)
	if e != nil {
		t.Fatal(e)
	}
	if e = configureBlobStorage(worker, shared); e != nil {
		t.Fatal(e)
	}
	work, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- RunQueuedExportWorker(work, q, worker) }()
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
wait:
	for {
		select {
		case err := <-done:
			t.Fatalf("worker stopped before completion: %v", err)
		case <-work.Done():
			t.Fatal("recovered worker did not finish")
		case <-tick.C:
			j, e := q.Get(work, project.SpaceID, export.ID)
			if e != nil {
				t.Fatal(e)
			}
			if j.State == "succeeded" {
				break wait
			}
			if j.State == "failed" {
				t.Fatal("render worker failed", worker.media.Get(project.ID, project.SpaceID).Exports)
			}
		}
	}
	cancel()
	if e := <-done; e != nil {
		t.Fatal(e)
	}
	var after []byte
	if e = db.QueryRow(`SELECT payload FROM studio_render_checkpoints WHERE export_id=$1 AND step=$2`, export.ID, step).Scan(&after); e != nil || string(before) != string(after) {
		t.Fatal("another worker re-encoded accepted video", e)
	}
	if _, e = os.Stat(filepath.Join(worker.media.dir, source.ID)); e != nil {
		t.Fatal("preserved audio source not restored", e)
	}
	state := api.media.Get(project.ID, project.SpaceID)
	if state.Exports[0].Status != "succeeded" {
		t.Fatal(state.Exports)
	}
	if compositeRMS(t, filepath.Join(worker.media.dir, export.ID+".mp4"), "1.5") < 100 {
		t.Fatal("separate source audio missing from resumed output")
	}
}
func TestRenderCheckpointFilePostgresBlobRoundTrip(t *testing.T) {
	db := postgresTestDB(t)
	dir := t.TempDir()
	store, e := OpenStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	p, _, e := store.CreateProject("space", "render-migration-project", projectInput())
	if e != nil {
		t.Fatal(e)
	}
	state, e := store.media.Demo(p.ID, p.SpaceID, "demo", 1, nil)
	if e != nil {
		t.Fatal(e)
	}
	timeline := state.Timeline
	timeline.Clips = timeline.Clips[:1]
	timeline.Clips[0].Duration = .75
	state, e = store.media.SaveTimeline(p.ID, p.SpaceID, "timeline", state.Version, timeline)
	if e != nil {
		t.Fatal(e)
	}
	out, e := store.media.StartExport(p.ID, p.SpaceID, "export", state.Version)
	if e != nil {
		t.Fatal(e)
	}
	store.media.jobs.wait()
	if store.media.Get(p.ID, p.SpaceID).Exports[0].Status != "succeeded" {
		t.Fatal("fixture export failed")
	}
	store.Close()
	checkpointDir := filepath.Join(dir, "media", "render-checkpoints", renderOwner(p.SpaceID, p.ID, out.ID))
	before := renderRecords(t, checkpointDir)
	shared, e := NewFileBlobStore(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer shared.Close()
	if _, e = MigrateBlobStorage(context.Background(), dir, shared, false); e != nil {
		t.Fatal("checkpoint blob migration", e)
	}
	report, e := ImportPostgresData(context.Background(), dir, db)
	if e != nil || report.Records["render"] != len(before)+1 {
		t.Fatal("render metadata import", report.Records, e)
	}
	target := filepath.Join(t.TempDir(), "restored")
	report, e = ExportPostgresData(context.Background(), target, db)
	if e != nil || report.Records["render"] != len(before)+1 {
		t.Fatal("render metadata export", report.Records, e)
	}
	if _, e = MigrateBlobStorage(context.Background(), target, shared, true); e != nil {
		t.Fatal("checkpoint blob restore", e)
	}
	restored, e := OpenStore(target)
	if e != nil {
		t.Fatal(e)
	}
	defer restored.Close()
	after := renderRecords(t, filepath.Join(target, "media", "render-checkpoints", renderOwner(p.SpaceID, p.ID, out.ID)))
	if !reflect.DeepEqual(before, after) {
		t.Fatal("migration changed checkpoints")
	}
	for _, a := range after {
		actual, e := localBlobInfo(context.Background(), a.Info.Key, filepath.Join(restored.media.dir, filepath.Base(a.Info.Key)))
		if e != nil || actual != a.Info {
			t.Fatal("migration lost immutable artifact", e)
		}
	}
	if e = restored.media.render(context.WithValue(context.Background(), renderScopeKey{}, renderScope{Space: p.SpaceID, Project: p.ID}), restored.media.Get(p.ID, p.SpaceID), out); e != nil {
		t.Fatal("restored render replay", e)
	}
}

func TestRenderCheckpointCancelledJobIsNeverRecovered(t *testing.T) {
	db := postgresTestDB(t)
	q := jobqueue.Queue{DB: db}
	ctx := context.Background()
	id := mediaID()
	tx, e := db.BeginTx(ctx, nil)
	if e != nil {
		t.Fatal(e)
	}
	if e = jobqueue.EnqueueTx(ctx, tx, jobqueue.Job{TenantID: "space", ID: id, Kind: "export", IdempotencyKey: id, Payload: json.RawMessage(`{"render_version":"1"}`)}); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	j, ok, e := q.Claim(ctx, "export")
	if e != nil || !ok {
		t.Fatal(e)
	}
	if e = q.Dispatch(ctx, j); e != nil {
		t.Fatal(e)
	}
	if e = q.Cancel(ctx, "space", id); e != nil {
		t.Fatal(e)
	}
	if _, ok, e = q.Claim(ctx, "export"); e != nil || ok {
		t.Fatal("cancelled render was re-admitted", ok, e)
	}
}
