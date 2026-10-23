package studio

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/adcwb/ai-creative-studio/backend/internal/jobqueue"
)

func TestPostgresQueuedCreationAndWorkers(t *testing.T) {
	dsn := os.Getenv("STUDIO_WORKER_TEST_DSN")
	if dsn == "" {
		t.Skip("STUDIO_WORKER_TEST_DSN required: isolated real PostgreSQL")
	}
	db, e := sql.Open("pgx", dsn)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if e = InitPostgres(ctx, db); e != nil {
		t.Fatal(e)
	}
	// Independent store handles use the database, never a shared in-memory cache.
	api, e := OpenPostgresStore(t.TempDir(), db)
	if e != nil {
		t.Fatal(e)
	}
	defer api.Close()
	worker, e := OpenPostgresStore(t.TempDir(), db)
	if e != nil {
		t.Fatal(e)
	}
	defer worker.Close()
	remote, err := NewFileBlobStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer remote.Close()
	if err = configureBlobStorage(api, remote); err != nil {
		t.Fatal(err)
	}
	if err = configureBlobStorage(worker, remote); err != nil {
		t.Fatal(err)
	}
	q := jobqueue.Queue{DB: db}
	if e = q.Init(ctx); e != nil {
		t.Fatal(e)
	}
	tenant := fmt.Sprintf("worker-%d", time.Now().UnixNano())
	task, created, e := api.Create(tenant, "image", Input{Prompt: "合成工作队列图片", Kind: "poster", Ratio: "1:1", Style: "natural"})
	if e != nil || !created {
		t.Fatalf("image accept: %v", e)
	}
	if _, e = q.Get(ctx, tenant, task.ID); e != nil {
		t.Fatalf("atomic queue missing: %v", e)
	}
	project, _, e := api.CreateProject(tenant, "project", projectInput())
	if e != nil {
		t.Fatal(e)
	}
	script, _, e := api.CreateScriptGeneration(tenant, project.ID, "script", ScriptGenerationInput{ExpectedVersion: project.Version})
	if e != nil {
		t.Fatal(e)
	}
	ip, e := NewPipeline(ctx, MockProvider{})
	if e != nil {
		t.Fatal(e)
	}
	sp, e := NewScriptPipeline(ctx, MockScriptProvider{})
	if e != nil {
		t.Fatal(e)
	}
	bp, e := NewStoryboardPipeline(ctx, MockStoryboardProvider{})
	if e != nil {
		t.Fatal(e)
	}
	done := make(chan error, 6)
	go func() { done <- RunQueuedImageWorker(ctx, q, worker, ip) }()
	go func() { done <- RunQueuedScriptWorker(ctx, q, worker, sp) }()
	go func() { done <- RunQueuedStoryboardWorker(ctx, q, worker, bp) }()
	go func() { done <- RunQueuedExportWorker(ctx, q, worker) }()
	go func() { done <- RunQueuedAlignmentWorker(ctx, q, worker) }()
	go func() { done <- RunQueuedCreativeWorker(ctx, q, worker) }()
	defer func() {
		cancel()
		for range 6 {
			if e := <-done; e != nil && e != context.Canceled {
				t.Error(e)
			}
		}
	}()
	wait := func(id string) {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			j, e := q.Get(ctx, tenant, id)
			if e != nil {
				t.Fatal(e)
			}
			if j.State == "succeeded" {
				return
			}
			if j.State == "failed" || j.State == "unknown" {
				t.Fatalf("job %s: %s", id, j.State)
			}
			time.Sleep(30 * time.Millisecond)
		}
		t.Fatalf("job timeout %s", id)
	}
	wait(task.ID)
	wait(script.ID)
	got, e := api.Get(tenant, task.ID)
	if e != nil || got.Status != "succeeded" {
		t.Fatalf("image domain result %+v %v", got, e)
	}
	project, e = api.GetProject(tenant, project.ID)
	if e != nil || project.Script == nil {
		t.Fatalf("script domain result %v", e)
	}
	board, _, e := api.CreateStoryboardGeneration(tenant, project.ID, "storyboard", ScriptGenerationInput{ExpectedVersion: project.Version})
	if e != nil {
		t.Fatal(e)
	}
	wait(board.ID)
	project, e = api.GetProject(tenant, project.ID)
	if e != nil || project.Storyboard == nil {
		t.Fatalf("storyboard domain result %v", e)
	}

	creative, _, e := api.createCreativeJob(tenant, project.ID, "creative", CreativeJobInput{Kind: "analysis", ExpectedVersion: project.Version})
	if e != nil {
		t.Fatal(e)
	}
	wait(creative.ID)
	media, e := api.media.Demo(project.ID, tenant, "media-demo", 1, nil)
	if e != nil {
		t.Fatal(e)
	}
	alignment, _, e := api.media.CreateAlignment(project.ID, tenant, "alignment", AlignmentInput{ExpectedVersion: media.Version, ClipID: media.Timeline.Clips[0].ID, Mode: "estimate", Text: "字幕队列合成文本"})
	if e != nil {
		t.Fatal(e)
	}
	wait(alignment.ID)
	export, e := api.media.StartExport(project.ID, tenant, "export", media.Version)
	if e != nil {
		t.Fatal(e)
	}
	wait(export.ID)
	current := api.media.Get(project.ID, tenant)
	if len(current.Exports) != 1 || current.Exports[0].Status != "succeeded" {
		t.Fatal("export domain state missing")
	}
	if _, _, e = api.media.file(project.ID, tenant, export.ID, true, false); e != nil {
		t.Fatalf("separate cache export download: %v", e)
	}
	if _, e = api.Get("other", task.ID); e == nil {
		t.Fatal("cross tenant read")
	}
}
