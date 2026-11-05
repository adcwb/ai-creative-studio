package application

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/media"
	"github.com/adcwb/ai-creative-studio/backend/internal/platform/jobqueue"
)

type renderHelperInput struct {
	Dir     string
	Space   string
	Project string
	Export  MediaExport
}

func TestRenderCheckpointCrashHelper(t *testing.T) {
	path := os.Getenv("STUDIO_RENDER_HELPER_INPUT")
	if path == "" {
		return
	}
	b, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	var in renderHelperInput
	if e = json.Unmarshal(b, &in); e != nil {
		t.Fatal(e)
	}
	store, e := OpenStore(in.Dir)
	if e != nil {
		t.Fatal(e)
	}
	defer store.Close()
	Handler(store, "")
	store.media.jobs.wait()
	state := store.media.Get(in.Project, in.Space)
	if len(state.Exports) != 1 || state.Exports[0].Status != "succeeded" {
		t.Fatal("render did not complete", state.Exports)
	}

}
func renderRecords(t *testing.T, root string) map[string]renderArtifact {
	t.Helper()
	files, e := os.ReadDir(root)
	if e != nil {
		t.Fatal(e)
	}
	out := map[string]renderArtifact{}
	for _, f := range files {
		if f.Name() == "manifest.json" || !strings.HasSuffix(f.Name(), ".json") {
			continue
		}
		b, e := os.ReadFile(filepath.Join(root, f.Name()))
		var a renderArtifact
		if e != nil || json.Unmarshal(b, &a) != nil {
			t.Fatal(e)
		}
		out[f.Name()] = a
	}
	return out
}
func TestRenderCheckpointProcessKillResumeAndIdempotency(t *testing.T) {
	real, e := exec.LookPath("ffmpeg")
	if e != nil {
		t.Skip("FFmpeg required")
	}
	dir := t.TempDir()
	store, e := OpenStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	project, _, e := store.CreateProject("personal-demo", "render-project", projectInput())
	if e != nil {
		t.Fatal(e)
	}
	space := project.SpaceID
	m := store.media
	p, e := m.Demo(project.ID, space, "demo", 1, nil)
	if e != nil {
		t.Fatal(e)
	}
	timeline := p.Timeline
	timeline.Clips = timeline.Clips[:2]
	for i := range timeline.Clips {
		timeline.Clips[i].Duration = 12.01
	}
	timeline.Clips[0].Transition = "crossfade"
	timeline.Clips[0].TransitionDuration = .37
	timeline.MusicAssetID = ""
	p, e = m.SaveTimeline(project.ID, space, "render-timeline", p.Version, timeline)
	if e != nil {
		t.Fatal(e)
	}
	m.jobs = newLocalJobs(0)
	export, e := m.StartExport(project.ID, space, "render-export", p.Version)
	if e != nil {
		t.Fatal(e)
	}
	if e = store.Close(); e != nil {
		t.Fatal(e)
	}
	tools := t.TempDir()
	counter, marker, fifo := filepath.Join(tools, "count"), filepath.Join(tools, "blocked"), filepath.Join(tools, "gate")
	if e = syscall.Mkfifo(fifo, 0600); e != nil {
		t.Fatal(e)
	}
	shim := fmt.Sprintf("#!/bin/sh\nfor render_arg do\n case \"$render_arg\" in */left.mp4)\n render_count=0\n if [ -f \"$STUDIO_RENDER_COUNT\" ]; then read -r render_count < \"$STUDIO_RENDER_COUNT\"; fi\n render_count=$((render_count+1))\n printf '%%s\\n' \"$render_count\" > \"$STUDIO_RENDER_COUNT\"\n if [ \"$render_count\" = 2 ]; then : > \"$STUDIO_RENDER_MARKER\"; read -r render_gate < \"$STUDIO_RENDER_FIFO\"; fi\n ;; esac\ndone\nexec %q \"$@\"\n", real)
	if e = os.WriteFile(filepath.Join(tools, "ffmpeg"), []byte(shim), 0700); e != nil {
		t.Fatal(e)
	}
	input := filepath.Join(tools, "input.json")
	b, _ := json.Marshal(renderHelperInput{Dir: dir, Space: space, Project: project.ID, Export: export})
	os.WriteFile(input, b, 0600)
	cmd := exec.Command(os.Args[0], "-test.run=^TestRenderCheckpointCrashHelper$", "-test.v")
	cmd.Env = []string{"PATH=" + tools + ":" + os.Getenv("PATH"), "STUDIO_RENDER_HELPER_INPUT=" + input, "STUDIO_RENDER_COUNT=" + counter, "STUDIO_RENDER_MARKER=" + marker, "STUDIO_RENDER_FIFO=" + fifo}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if e = cmd.Start(); e != nil {
		t.Fatal(e)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	running := true
	defer func() {
		if running {
			syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			<-done
		}
	}()
	deadline := time.NewTimer(30 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
wait:
	for {
		select {
		case e := <-done:
			running = false
			t.Fatalf("child finished before interruption: %v %s", e, output.String())
		case <-deadline.C:
			t.Fatal("child did not reach second segment")
		case <-tick.C:
			if _, e := os.Stat(marker); e == nil {
				break wait
			}
		}
	}
	root := filepath.Join(dir, "media", "render-checkpoints", renderOwner(space, project.ID, export.ID))
	before := renderRecords(t, root)
	if len(before) < 2 {
		t.Fatal("crash occurred before accepted checkpoints", before)
	}
	times := map[string]time.Time{}
	for name, a := range before {
		if a.State != "ready" {
			t.Fatal("uncommitted checkpoint", name)
		}
		st, e := os.Stat(filepath.Join(dir, "media", filepath.Base(a.Info.Key)))
		if e != nil {
			t.Fatal(e)
		}
		times[name] = st.ModTime()
	}
	if e = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); e != nil {
		t.Fatal(e)
	}
	if e := <-done; e == nil {
		t.Fatal("child was not killed")
	}
	running = false
	t.Setenv("MEDIA_RENDER_SEGMENT_SECONDS", "1") // an admitted manifest keeps its original segmentation
	store, e = OpenStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer store.Close()
	m = store.media
	if m.Get(project.ID, space).Exports[0].Status != "queued" {
		t.Fatal("interrupted render not restored as queued")
	}
	Handler(store, "")
	m.jobs.wait()
	state := m.Get(project.ID, space)
	renderCtx := context.WithValue(context.Background(), renderScopeKey{}, renderScope{Space: space, Project: project.ID})
	if state.Exports[0].Status != "succeeded" {
		t.Fatal("automatic resume", state.Exports[0], m.render(renderCtx, p, export))
	}

	after := renderRecords(t, root)
	for name, a := range before {
		if after[name] != a {
			t.Fatal("accepted checkpoint replaced", name)
		}
		st, e := os.Stat(filepath.Join(m.dir, filepath.Base(a.Info.Key)))
		if e != nil || st.ModTime() != times[name] {
			t.Fatal("accepted segment re-encoded", name, e)
		}
	}
	plan, e := media.CompileRenderPlan(timeline)
	if e != nil {
		t.Fatal(e)
	}
	if e = verifyRenderArtifact(context.Background(), filepath.Join(m.dir, export.ID+".mp4"), "final", plan.Frames, 640, 360); e != nil {
		t.Fatal(e)
	}
	if e = m.render(renderCtx, p, export); e != nil {
		t.Fatal("replay", e)
	}
	if !reflect.DeepEqual(after, renderRecords(t, root)) {
		t.Fatal("duplicate submission changed checkpoints")
	}
	// A corrupted intermediate is rebuilt; an already verified final remains valid.
	for _, a := range after {
		if a.Kind == "video" {
			if e = os.WriteFile(filepath.Join(m.dir, filepath.Base(a.Info.Key)), []byte("truncated"), 0600); e != nil {
				t.Fatal(e)
			}
			break
		}
	}
	if e = m.render(renderCtx, p, export); e != nil {
		t.Fatal("repair", e)
	}
	changed := export
	changed.Timeline.Clips = append([]MediaClip(nil), timeline.Clips...)
	changed.Timeline.Clips[0].Duration += 1
	if e = m.render(renderCtx, p, changed); e == nil {
		t.Fatal("silently changed accepted manifest")
	}
}

func TestRenderCheckpointQueueRecoveryAndFencing(t *testing.T) {
	db := postgresTestDB(t)
	ctx := context.Background()
	q := jobqueue.Queue{DB: db, Lease: time.Minute}
	for _, j := range []jobqueue.Job{{TenantID: "tenant", ID: "render", Kind: "export", IdempotencyKey: "render", Payload: json.RawMessage(`{"render_version":"1"}`)}, {TenantID: "tenant", ID: "legacy", Kind: "export", IdempotencyKey: "legacy", Payload: json.RawMessage(`{}`)}, {TenantID: "tenant", ID: "model", Kind: "creative", IdempotencyKey: "model", Payload: json.RawMessage(`{"render_version":"1"}`)}} {
		tx, e := db.BeginTx(ctx, nil)
		if e != nil {
			t.Fatal(e)
		}
		if e = jobqueue.EnqueueTx(ctx, tx, j); e != nil {
			t.Fatal(e)
		}
		if e = tx.Commit(); e != nil {
			t.Fatal(e)
		}
	}
	old, ok, e := q.Claim(ctx, "export")
	if e != nil || !ok || old.ID != "render" {
		t.Fatal(old, ok, e)
	}
	if e = q.Dispatch(ctx, old); e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(`UPDATE execution_jobs SET lease_until=now()-interval '1 second' WHERE id='render'`); e != nil {
		t.Fatal(e)
	}
	fresh, ok, e := q.Claim(ctx, "export")
	if e != nil || !ok || fresh.ID != "render" || fresh.Token == old.Token {
		t.Fatal("local render not recovered", fresh, ok, e)
	}
	m := postgresTestStore(t, db).media
	s := &renderSession{m: m, scope: renderScope{Space: "tenant", Project: "project", Lease: &old}, manifest: renderManifest{Export: "render"}, ctx: ctx}
	if e = s.write("manifest", map[string]string{"test": "old"}); !errors.Is(e, jobqueue.ErrLeaseLost) {
		t.Fatal("old writer checkpoint accepted", e)
	}
	s.scope.Lease = &fresh
	if e = s.write("manifest", map[string]string{"test": "new"}); e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(`UPDATE execution_jobs SET state='dispatch_started',lease_until=now()-interval '1 second' WHERE id IN ('legacy','model')`); e != nil {
		t.Fatal(e)
	}
	q.Claim(ctx, "export")
	q.Claim(ctx, "creative")
	for _, id := range []string{"legacy", "model"} {
		j, e := q.Get(ctx, "tenant", id)
		if e != nil || j.State != "unknown" {
			t.Fatal("unsafe job automatically replayed", id, j, e)
		}
	}
}
