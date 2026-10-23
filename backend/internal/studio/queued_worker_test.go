package studio

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
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
	// Simulate loss after the domain running marker but before dispatch.
	abandoned, ok, e := q.Claim(ctx, "image")
	if e != nil || !ok || abandoned.ID != task.ID {
		t.Fatalf("pre-dispatch claim %v", e)
	}
	if e = api.markQueuedRunning(ctx, abandoned); e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(`UPDATE execution_jobs SET lease_until=clock_timestamp()-interval '1 second' WHERE tenant_id=$1 AND id=$2`, tenant, task.ID); e != nil {
		t.Fatal(e)
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
		deadline := time.Now().Add(30 * time.Second)
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
	recovered, e := q.Get(ctx, tenant, task.ID)
	if e != nil || recovered.Attempt != 2 {
		t.Fatal("undispatched lease was not safely reclaimed")
	}
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

func TestPostgresRecoveryCrashHelper(t *testing.T) {
	if os.Getenv("STUDIO_RECOVERY_HELPER") != "1" {
		return
	}
	db, e := sql.Open("pgx", os.Getenv("STUDIO_WORKER_TEST_DSN"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	q := jobqueue.Queue{DB: db}
	j, ok, e := q.Claim(context.Background(), "image_resume")
	if e != nil || !ok || j.ID != os.Getenv("STUDIO_RECOVERY_ID") {
		t.Fatalf("claim %v %v", ok, e)
	}
	if e = q.Dispatch(context.Background(), j); e != nil {
		t.Fatal(e)
	}
	fmt.Println("DISPATCHED")
	time.Sleep(time.Hour)
}

func TestPostgresRecoveryCrashRetainsUpstreamAndBudget(t *testing.T) {
	dsn := os.Getenv("STUDIO_WORKER_TEST_DSN")
	if dsn == "" {
		t.Skip("STUDIO_WORKER_TEST_DSN required")
	}
	db, e := sql.Open("pgx", dsn)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	ctx := context.Background()
	if e = InitPostgres(ctx, db); e != nil {
		t.Fatal(e)
	}
	s, e := OpenPostgresStore(t.TempDir(), db)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	tenant := fmt.Sprintf("resume-crash-%d", time.Now().UnixNano())
	defer db.Exec(`DELETE FROM execution_jobs WHERE tenant_id=$1`, tenant)
	task, _, e := s.Create(tenant, "original", Input{Prompt: "Synthetic recovery ownership", Kind: "poster", Ratio: "1:1", Style: "natural"})
	if e != nil {
		t.Fatal(e)
	}
	task.Model = "synthetic-model"
	s.mu.Lock()
	seed := s.clone()
	seed.Tasks[task.ID] = task
	e = s.commit(seed)
	s.mu.Unlock()
	if e != nil {
		t.Fatal(e)
	}
	if e = s.budget.SetLimit(tenant, "USD", 100); e != nil {
		t.Fatal(e)
	}
	if _, e = s.budget.ClaimDispatch("image:"+task.ID, tenant, task.Provider, task.Model, "USD", 10); e != nil {
		t.Fatal(e)
	}
	q := jobqueue.Queue{DB: db}
	original, ok, e := q.Claim(ctx, "image")
	if e != nil || !ok || original.ID != task.ID {
		t.Fatalf("claim original id=%s state=%s %v", original.ID, original.State, e)
	}
	if e = s.markQueuedRunning(ctx, original); e != nil {
		t.Fatal(e)
	}
	if e = q.Dispatch(ctx, original); e != nil {
		t.Fatal(e)
	}
	if e = s.SetImageUpstreamQueued(ctx, original, task, "synthetic-upstream"); e != nil {
		t.Fatal(e)
	}
	if e = q.Finish(ctx, original, "unknown", nil); e != nil {
		t.Fatal(e)
	}
	if e = ReconcilePostgresJobs(ctx, db); e != nil {
		t.Fatal(e)
	}
	// Explicit query-only resume keeps the original task and cost identity.
	s.mu.Lock()
	d := s.clone()
	resumed := d.Tasks[task.ID]
	resumed.Status = "queued"
	d.Tasks[task.ID] = resumed
	payload, _ := json.Marshal(map[string]string{"task_id": task.ID})
	s.pg.beforeCommit = func(ctx context.Context, tx *sql.Tx) error {
		return jobqueue.ResumeTx(ctx, tx, jobqueue.Job{TenantID: tenant, ID: task.ID, Kind: "image_resume", IdempotencyKey: "explicit-query", Payload: payload})
	}
	e = s.commit(d)
	s.pg.beforeCommit = nil
	s.mu.Unlock()
	if e != nil {
		t.Fatal(e)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestPostgresRecoveryCrashHelper$")
	cmd.Env = []string{"STUDIO_RECOVERY_HELPER=1", "STUDIO_WORKER_TEST_DSN=" + dsn, "STUDIO_RECOVERY_ID=" + task.ID}
	stdout, e := cmd.StdoutPipe()
	if e != nil {
		t.Fatal(e)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if e = cmd.Start(); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		if cmd.Process != nil {
			cmd.Process.Kill()
		}
	})
	scanner := bufio.NewScanner(stdout)
	line := make(chan string, 1)
	go func() {
		if scanner.Scan() {
			line <- scanner.Text()
		} else {
			line <- ""
		}
	}()
	select {
	case value := <-line:
		if value != "DISPATCHED" {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			t.Fatalf("helper failed: %s %s", value, stderr.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("helper dispatch timed out")
	}
	if e = cmd.Process.Kill(); e != nil {
		t.Fatal(e)
	}
	_ = cmd.Wait()
	if _, e = db.Exec(`UPDATE execution_jobs SET lease_until=clock_timestamp()-interval '1 second' WHERE tenant_id=$1 AND id=$2`, tenant, task.ID); e != nil {
		t.Fatal(e)
	}
	if _, ok, e = q.Claim(ctx, "image_resume"); e != nil || ok {
		t.Fatalf("recovery auto replayed %v %v", ok, e)
	}
	if e = ReconcilePostgresJobs(ctx, db); e != nil {
		t.Fatal(e)
	}
	got, e := s.Get(tenant, task.ID)
	if e != nil || got.Status != "failed" || got.ErrorCode != "execution_outcome_unknown" || got.UpstreamID != "synthetic-upstream" {
		t.Fatalf("recovery domain %+v %v", got, e)
	}
	entries, e := s.budget.Entries(tenant, "USD")
	if e != nil || len(entries) != 1 || entries[0].State != "unknown" || entries[0].Reserved != 10 {
		t.Fatalf("prior billing changed %+v %v", entries, e)
	}
	if e = s.SetImageUpstreamQueued(ctx, original, task, "late-upstream"); e == nil {
		t.Fatal("stale worker replaced upstream")
	}
}
