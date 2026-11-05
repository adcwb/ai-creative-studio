package application

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/projects"
	"github.com/adcwb/ai-creative-studio/backend/internal/modules/story"
	"github.com/adcwb/ai-creative-studio/backend/internal/persistence/storyrepo"
)

func snapshotContract(t *testing.T, s *FileStore) {
	t.Helper()
	ctx := context.Background()
	root, eps := storyFixture(t, s)
	b := saveStory(t, s, root, storyrepo.BibleID(root), "snapshot-bible-create", storyBible())
	script := scriptFixture(t)
	for i := range script.Scenes {
		script.Scenes[i].EpisodeNumber = 1
	}
	stable, _, e := projects.CompileStructure(script, eps[0].SpaceID+":"+eps[0].ID, 1)
	if e != nil {
		t.Fatal(e)
	}
	saved, e := s.SaveProjectScript(eps[0].SpaceID, eps[0].ID, "context-script-before", ScriptSaveInput{ExpectedVersion: eps[0].Version, Script: stable})
	if e != nil {
		t.Fatal(e)
	}
	eps[0] = saved
	selection := story.Selection{Query: "指定关键剧情", RecordIDs: []string{}}
	before := sourceRecords(t, s, eps[0])
	v, e := s.CreateStoryContext(ctx, storyActor, eps[0].ID, "snapshot-original-key", selection)
	if e != nil || !story.ValidSnapshot(v) || len(v.Context.Included) != 1 || v.Context.Sources[0].VersionID != b.VersionID {
		t.Fatal("invalid immutable snapshot", e, v)
	}
	if before != sourceRecords(t, s, eps[0]) {
		t.Fatal("snapshot changed authored project")
	}
	q := storyrepo.Scope{Space: storyActor.Space, Series: root}
	fresh, e := s.CheckStoryContext(ctx, q.Space, eps[0].ID, v.ID)
	if e != nil || !fresh.Current {
		t.Fatal("new snapshot not current", e, fresh)
	}
	// A fresh server may read the exact snapshot even after every source changes.
	changed := storyBible()
	changed.ExpectedVersion = b.Number
	changed.Title = "作者后来修订"
	saveStory(t, s, root, b.ID, "snapshot-bible-update", changed)
	stale, e := s.CheckStoryContext(ctx, q.Space, eps[0].ID, v.ID)
	if e != nil || stale.Current || !stale.MaterialChanged || stale.ScriptChanged {
		t.Fatal("source edits did not invalidate input", e, stale)
	}
	replay, e := s.CreateStoryContext(ctx, storyActor, eps[0].ID, "snapshot-original-key", selection)
	if e != nil || !reflect.DeepEqual(v, replay) {
		t.Fatal("replay rebased old source", e)
	}
	if _, e = s.CreateStoryContext(ctx, storyActor, eps[0].ID, "snapshot-original-key", story.Selection{Query: "different"}); !errors.Is(e, storyrepo.ErrKey) {
		t.Fatal("key reused for changed retrieval", e)
	}
	newer, e := s.CreateStoryContext(ctx, storyActor, eps[0].ID, "snapshot-new-source-key", selection)
	if e != nil || newer.SHA256 == v.SHA256 || newer.Context.Sources[0].Number != 2 {
		t.Fatal("new snapshot did not pin current source", e)
	}
	revision := projects.CloneScript(*saved.Script)
	revision.Scenes[0].Action = "作者后来的改稿，不能覆盖快照里的原文。"
	if _, e = s.SaveProjectScript(saved.SpaceID, saved.ID, "context-script-after", ScriptSaveInput{ExpectedVersion: saved.Version, Script: revision}); e != nil {
		t.Fatal(e)
	}
	edited, e := s.CheckStoryContext(ctx, q.Space, eps[0].ID, newer.ID)
	if e != nil || edited.Current || !edited.ScriptChanged || !edited.ProjectChanged || edited.MaterialChanged {
		t.Fatal("script edits not distinguished from material edits", e, edited)
	}
	got, e := s.story.GetContext(ctx, q, eps[0].ID, v.ID)
	if e != nil || !reflect.DeepEqual(got, v) || got.Script == nil || got.Script.Scenes[0].Action != stable.Scenes[0].Action {
		t.Fatal("snapshot mutated", e)
	}
	page, e := s.story.ListContexts(ctx, q, eps[0].ID, "", 1)
	if e != nil || len(page.Items) != 1 || page.Next == "" {
		t.Fatal("snapshot pagination", e, page)
	}
	next, e := s.story.ListContexts(ctx, q, eps[0].ID, page.Next, 1)
	if e != nil || len(next.Items) != 1 || next.Items[0].ID == page.Items[0].ID || next.Next != "" {
		t.Fatal("snapshot page duplicated", e, next)
	}
	if _, e = s.story.GetContext(ctx, storyrepo.Scope{Space: "other", Series: root}, eps[0].ID, v.ID); !errors.Is(e, story.ErrNotFound) {
		t.Fatal("cross tenant snapshot", e)
	}
	if _, e = s.story.GetContext(ctx, q, eps[1].ID, v.ID); !errors.Is(e, story.ErrNotFound) {
		t.Fatal("cross episode snapshot", e)
	}
	viewer := storyActor
	viewer.Writable = false
	if _, e = s.CreateStoryContext(ctx, viewer, eps[0].ID, "snapshot-original-key", selection); !errors.Is(e, story.ErrDenied) {
		t.Fatal("current permission bypass", e)
	}
	formal, e := s.story.List(ctx, q, storyrepo.Query{Limit: 100})
	if e != nil || len(formal.Items) != 1 {
		t.Fatal("snapshot polluted formal heads", e)
	}
}
func TestFileStoryContextSnapshots(t *testing.T) { snapshotContract(t, testStore(t)) }
func TestPostgresStoryContextSnapshots(t *testing.T) {
	snapshotContract(t, postgresTestStore(t, postgresTestDB(t)))
}
func TestStoryContextConcurrentReceiptAndRestart(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	a, e := OpenStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	root, eps := storyFixture(t, a)
	saveStory(t, a, root, storyrepo.BibleID(root), "snapshot-bible-create", storyBible())
	b := a
	var wg sync.WaitGroup
	out := make([]story.ContextSnapshot, 2)
	errs := make([]error, 2)
	for i, s := range []*FileStore{a, b} {
		wg.Add(1)
		go func(i int, s *FileStore) {
			defer wg.Done()
			out[i], errs[i] = s.CreateStoryContext(ctx, storyActor, eps[0].ID, "snapshot-race-key", story.Selection{})
		}(i, s)
	}
	wg.Wait()
	if errs[0] != nil || errs[1] != nil || !reflect.DeepEqual(out[0], out[1]) {
		t.Fatal("different receipt after race", errs)
	}
	a.Close()
	reopened, e := OpenStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer reopened.Close()
	v, e := reopened.CreateStoryContext(ctx, storyActor, eps[0].ID, "snapshot-race-key", story.Selection{})
	if e != nil || !reflect.DeepEqual(v, out[0]) {
		t.Fatal("receipt lost after restart", e)
	}
	path := filepath.Join(dir, "story", story.TextHash(storyActor.Space), root, ".contexts", eps[0].ID, v.ID+".json")
	raw, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	raw = append(raw, []byte("{}")...)
	if e = os.WriteFile(path, raw, 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = reopened.story.GetContext(ctx, storyrepo.Scope{Space: storyActor.Space, Series: root}, eps[0].ID, v.ID); !errors.Is(e, storyrepo.ErrStorage) {
		t.Fatal("corrupt snapshot accepted", e)
	}
}

func TestPostgresStoryContextIndependentInstancesShareOriginalReceipt(t *testing.T) {
	ctx := context.Background()
	db := postgresTestDB(t)
	a, b := postgresTestStore(t, db), postgresTestStore(t, db)
	root, eps := storyFixture(t, a)
	saveStory(t, a, root, storyrepo.BibleID(root), "context-two-apis-bible", storyBible())
	var wg sync.WaitGroup
	out := make([]story.ContextSnapshot, 2)
	errs := make([]error, 2)
	for i, s := range []*FileStore{a, b} {
		wg.Add(1)
		go func(i int, s *FileStore) {
			defer wg.Done()
			out[i], errs[i] = s.CreateStoryContext(ctx, storyActor, eps[0].ID, "context-two-apis-original", story.Selection{})
		}(i, s)
	}
	wg.Wait()
	if errs[0] != nil || errs[1] != nil || !reflect.DeepEqual(out[0], out[1]) {
		t.Fatal("independent APIs committed different snapshots", errs)
	}
	page, e := b.story.ListContexts(ctx, storyrepo.Scope{Space: storyActor.Space, Series: root}, eps[0].ID, "", 100)
	if e != nil || len(page.Items) != 1 {
		t.Fatal("duplicate snapshot", e, page)
	}
}
