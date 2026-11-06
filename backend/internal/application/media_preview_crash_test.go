package application

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"syscall"
	"testing"
	"time"
)

func TestAnimaticProcessCrashResumesOriginalCheckpoint(t *testing.T) {
	real, e := exec.LookPath("ffmpeg")
	if e != nil {
		t.Fatal(e)
	}
	dir := t.TempDir()
	store, e := OpenStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	p, state, in := animaticFixture(t, store)
	in.Shots[0].Duration = 12.1
	store.media.jobs = newLocalJobs(0)
	original, e := store.CreateMediaPreview(context.Background(), p.SpaceID, p.ID, "crash-preview", in, "", nil)
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
	shim := fmt.Sprintf(`#!/bin/sh
for render_arg do render_output="$render_arg"; done
case "$render_output" in */left.mp4)
 render_count=0
 if [ -f "$STUDIO_RENDER_COUNT" ]; then read -r render_count < "$STUDIO_RENDER_COUNT"; fi
 render_count=$((render_count+1))
 printf '%%s\n' "$render_count" > "$STUDIO_RENDER_COUNT"
 if [ "$render_count" = 2 ]; then : > "$STUDIO_RENDER_MARKER"; read -r render_gate < "$STUDIO_RENDER_FIFO"; fi
 ;; esac
exec %q "$@"
`, real)
	if e = os.WriteFile(filepath.Join(tools, "ffmpeg"), []byte(shim), 0700); e != nil {
		t.Fatal(e)
	}
	input := filepath.Join(tools, "input.json")
	raw, _ := json.Marshal(renderHelperInput{Dir: dir, Space: p.SpaceID, Project: p.ID, Export: original})
	if e = os.WriteFile(input, raw, 0600); e != nil {
		t.Fatal(e)
	}
	child := exec.Command(os.Args[0], "-test.run=^TestRenderCheckpointCrashHelper$", "-test.v")
	child.Env = []string{"PATH=" + tools + ":" + os.Getenv("PATH"), "STUDIO_RENDER_HELPER_INPUT=" + input, "STUDIO_RENDER_COUNT=" + counter, "STUDIO_RENDER_MARKER=" + marker, "STUDIO_RENDER_FIFO=" + fifo}
	child.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	var output bytes.Buffer
	child.Stdout = &output
	child.Stderr = &output
	if e = child.Start(); e != nil {
		t.Fatal(e)
	}
	done := make(chan error, 1)
	go func() { done <- child.Wait() }()
	running := true
	defer func() {
		if running {
			syscall.Kill(-child.Process.Pid, syscall.SIGKILL)
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
			t.Fatalf("child exited before checkpoint: %v %s", e, output.String())
		case <-deadline.C:
			t.Fatal("preview did not reach second render block")
		case <-tick.C:
			if _, e := os.Stat(marker); e == nil {
				break wait
			}
		}
	}
	root := filepath.Join(dir, "media", "render-checkpoints", renderOwner(p.SpaceID, p.ID, original.ID))
	before := renderRecords(t, root)
	if len(before) < 2 {
		t.Fatal("no accepted preview checkpoint before crash")
	}
	times := map[string]time.Time{}
	for name, a := range before {
		if a.State != "ready" {
			t.Fatal("uncommitted stage")
		}
		st, e := os.Stat(filepath.Join(dir, "media", filepath.Base(a.Info.Key)))
		if e != nil {
			t.Fatal(e)
		}
		times[name] = st.ModTime()
	}
	if e = syscall.Kill(-child.Process.Pid, syscall.SIGKILL); e != nil {
		t.Fatal(e)
	}
	if e = <-done; e == nil {
		t.Fatal("child was not killed")
	}
	running = false
	t.Setenv("MEDIA_RENDER_SEGMENT_SECONDS", "1")
	restored, e := OpenStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer restored.Close()
	// A later formal cut must not replace the original preview during recovery.
	cut := state.Timeline
	cut.Clips = []MediaClip{{ID: "later", AssetID: in.Shots[1].AssetID, Duration: .5, AudioVolume: 1, Subtitle: "later formal cut"}}
	saved, e := restored.media.SaveTimeline(p.ID, p.SpaceID, "later-cut", state.Version, cut)
	if e != nil {
		t.Fatal(e)
	}
	Handler(restored, "")
	restored.media.jobs.wait()
	final, e := restored.media.preview(p.ID, p.SpaceID, original.ID)
	if e != nil || final.Status != "succeeded" || !reflect.DeepEqual(final.PreviewSource, original.PreviewSource) {
		t.Fatal("preview source not preserved after process crash", final.Status, final.Error, e)
	}
	after := renderRecords(t, root)
	for name, a := range before {
		if after[name] != a {
			t.Fatal("accepted preview segment replaced", name)
		}
		st, e := os.Stat(filepath.Join(dir, "media", filepath.Base(a.Info.Key)))
		if e != nil || st.ModTime() != times[name] {
			t.Fatal("accepted preview segment re-encoded", name, e)
		}
	}
	current := restored.media.Get(p.ID, p.SpaceID)
	if !reflect.DeepEqual(current.Timeline, saved.Timeline) {
		t.Fatal("resume overwrote formal cut")
	}
	if e = verifyRenderArtifact(context.Background(), filepath.Join(restored.media.dir, original.ID+".mp4"), "final", 307, 640, 360); e != nil {
		t.Fatal(e)
	}
}
