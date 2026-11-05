package application

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func TestRenderCheckpointUnknownBlobWriteResumesSameArtifactAfterRestart(t *testing.T) {
	fixture := newMultipartFixture(t)
	dir := t.TempDir()
	store, e := OpenStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { store.Close() }()
	remote := fixture.store(t, t.TempDir())
	if e = configureBlobStorage(store, remote); e != nil {
		t.Fatal(e)
	}
	project, _, e := store.CreateProject("space", "s3-render-project", projectInput())
	if e != nil {
		t.Fatal(e)
	}
	p, e := store.media.Demo(project.ID, project.SpaceID, "demo", 1, nil)
	if e != nil {
		t.Fatal(e)
	}
	clip := p.Timeline.Clips[0]
	clip.Duration = 1
	export := MediaExport{ID: mediaID(), RenderVersion: 1, Timeline: MediaTimeline{Clips: []MediaClip{clip}, SubtitleMode: "none"}}
	ctx := context.WithValue(context.Background(), renderScopeKey{}, renderScope{Space: project.SpaceID, Project: project.ID})
	transport := remote.client.Transport
	var lost atomic.Bool
	remote.client.Transport = s3RoundTrip(func(r *http.Request) (*http.Response, error) {
		response, e := transport.RoundTrip(r)
		if e == nil && r.Method == "PUT" && !lost.Swap(true) {
			closeBlobResponse(response)
			return nil, errors.New("synthetic lost acknowledgement after accepted object")
		}
		return response, e
	})
	calls := 0
	run := func(s *renderSession) error {
		_, e := s.artifact("block-0-0-24-video", "video", 24, func(path string) error {
			calls++
			return s.m.renderClipVideo(ctx, p, clip, 0, 24, path, 640, 360, "4M")
		})
		return e
	}
	if e = store.media.withRenderSession(ctx, p, export, run); e == nil {
		t.Fatal("unknown storage write acknowledged")
	}
	root := filepath.Join(store.media.dir, "render-checkpoints", renderOwner(project.SpaceID, project.ID, export.ID))
	records := renderRecords(t, root)
	pending := records[s3Hash("block-0-0-24-video")+".json"]
	if pending.State != "staging" {
		t.Fatal("unknown write lost recovery identity", pending)
	}
	fixture.mu.Lock()
	_, exists := fixture.objects[pending.Info.Key]
	count := len(fixture.objects)
	fixture.mu.Unlock()
	if !exists {
		t.Fatal("fault was not after actual storage acceptance")
	}
	store.Close()
	store, e = OpenStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	if e = configureBlobStorage(store, fixture.store(t, t.TempDir())); e != nil {
		t.Fatal("pending checkpoint blocked restart", e)
	}
	if e = store.media.withRenderSession(ctx, p, export, run); e != nil {
		t.Fatal("pending artifact resume", e)
	}
	ready := renderRecords(t, root)[s3Hash("block-0-0-24-video")+".json"]
	if calls != 1 || ready.State != "ready" || ready.Info != pending.Info {
		t.Fatal("retry re-encoded or changed storage key", calls, ready, pending)
	}
	if _, e = os.Stat(filepath.Join(store.media.dir, filepath.Base(ready.Info.Key))); e != nil {
		t.Fatal(e)
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if len(fixture.objects) != count {
		t.Fatal("duplicate object after unknown write")
	}
}
