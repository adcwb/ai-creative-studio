package application

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/projects"
	"github.com/adcwb/ai-creative-studio/backend/internal/modules/series"
	"github.com/adcwb/ai-creative-studio/backend/internal/modules/story"
	"github.com/adcwb/ai-creative-studio/backend/internal/persistence/storyrepo"
)

var storyActor = story.Actor{Space: seriesActor.Space, User: seriesActor.User, Writable: true}

func storyBible() story.Input {
	return story.Input{Title: "归途 · 正式圣经", Kind: "bible", Status: "active", Required: true, Body: story.Body{Evidence: []story.Evidence{}, Bible: &story.Bible{Theme: "寻找真相", WorldRules: "角色只能知道已经向他揭示的信息。", Characters: []story.Character{{ID: fmt.Sprintf("%032x", 1), Name: "小满", Desire: "找到父亲", Arc: "从误认走向独立判断", VoiceIntent: "克制的低声；只是文字意图"}}}}}
}
func storyFixture(t *testing.T, s *FileStore) (string, []Project) {
	t.Helper()
	season := seriesFixture(t, s)
	out := []Project{}
	for _, ep := range []int{1, 7, 120} {
		n, p, e := s.CreateSeriesEpisode(context.Background(), seriesActor, fmt.Sprintf("story-ep-%d", ep), season.ID, series.Input{Title: fmt.Sprintf("原第%d集", ep)})
		if e != nil {
			t.Fatal(e)
		}
		if int64(ep) != n.Ordinal {
			parent, e := s.series.Get(context.Background(), p.SpaceID, "season", season.ID)
			if e != nil {
				t.Fatal(e)
			}
			_, e = s.series.Mutate(context.Background(), seriesActor, fmt.Sprintf("story-move-%d", ep), series.Command{Operation: "move", Kind: "episode", ID: n.ID, Expected: n.Version, ExpectedParent: parent.Version, Ordinal: int64(ep)}, nil)
			if e != nil {
				t.Fatal(e)
			}
		}
		out = append(out, p)
	}
	return season.SeriesID, out
}
func saveStory(t *testing.T, s *FileStore, root, id, key string, in story.Input) story.Version {
	t.Helper()
	v, e := s.SaveStory(context.Background(), storyActor, root, id, key, in)
	if e != nil {
		t.Fatal(e)
	}
	return v
}
func storyContract(t *testing.T, s *FileStore) {
	t.Helper()
	ctx := context.Background()
	root, episodes := storyFixture(t, s)
	q := storyrepo.Scope{Space: storyActor.Space, Series: root}
	b := saveStory(t, s, root, storyrepo.BibleID(root), "story-bible-create", storyBible())
	f := story.Input{Title: "误认并非客观真相", Kind: "fact", Status: "active", Required: true, Body: story.Body{Fact: &story.Fact{Subject: "小满", Predicate: "认定", Value: "门外是父亲", Perspective: "subjective", BelieverID: b.Body.Bible.Characters[0].ID, AudienceFrom: episodes[1].ID, KnownBy: []story.Knowledge{{CharacterID: b.Body.Bible.Characters[0].ID, FromEpisodeID: episodes[1].ID}}}}}
	id := story.NewID(storyActor, root, "story-fact-create")
	v := saveStory(t, s, root, id, "story-fact-create", f)
	before := sourceRecords(t, s, episodes[0])
	preview, e := s.PreviewStoryContext(ctx, q.Space, episodes[0].ID)
	if e != nil || len(preview.Problems) != 0 || len(preview.Included) != 2 || preview.Included[1].AudienceKnows || len(preview.Included[1].KnownBy) != 0 {
		t.Fatal("knowledge boundary failed", e, preview)
	}
	later, e := s.PreviewStoryContext(ctx, q.Space, episodes[1].ID)
	if e != nil || !later.Included[1].AudienceKnows || len(later.Included[1].KnownBy) != 1 {
		t.Fatal(e, later)
	}
	if before != sourceRecords(t, s, episodes[0]) {
		t.Fatal("read-only context changed project")
	}
	again, e := s.PreviewStoryContext(ctx, q.Space, episodes[0].ID)
	if e != nil || !reflect.DeepEqual(preview, again) {
		t.Fatal("same-source context not reproducible", e)
	}
	update := f
	update.ExpectedVersion = v.Number
	update.Title = "保留主观视角的新稿"
	updated := saveStory(t, s, root, id, "story-fact-update", update)
	if updated.Number != 2 {
		t.Fatal("version not advanced")
	}
	if _, e = s.SaveStory(ctx, storyActor, root, id, "story-stale-update", update); !errors.Is(e, story.ErrConflict) {
		t.Fatal("stale overwrite accepted", e)
	}
	replay := saveStory(t, s, root, id, "story-fact-create", f)
	if !reflect.DeepEqual(replay, v) {
		t.Fatal("original receipt replaced by head")
	}
	history, e := s.story.History(ctx, q, id, 0, 1)
	if e != nil || len(history.Items) != 1 || history.Items[0].Number != 2 || history.Next != "2" {
		t.Fatal("history pagination", e, history)
	}
	history, e = s.story.History(ctx, q, id, 2, 1)
	if e != nil || history.Items[0].Number != 1 {
		t.Fatal("old version lost", e)
	}
	page, e := s.story.List(ctx, q, storyrepo.Query{Limit: 1})
	if e != nil || len(page.Items) != 1 || page.Next == "" {
		t.Fatal("summary pagination", e, page)
	}
	if _, e = s.story.Get(ctx, storyrepo.Scope{Space: "other", Series: root}, id, ""); !errors.Is(e, story.ErrNotFound) {
		t.Fatal("cross-space read", e)
	}
	denied := storyActor
	denied.Writable = false
	if _, e = s.SaveStory(ctx, denied, root, id, "story-fact-create", f); !errors.Is(e, story.ErrDenied) {
		t.Fatal("accepted replay bypassed current permission", e)
	}
	// Removing a formal character surfaces dangling knowledge instead of guessing
	// another same-name character. The exact old receipt remains replayable.
	missing := storyBible()
	missing.ExpectedVersion = b.Number
	missing.Body.Bible.Characters = []story.Character{}
	saveStory(t, s, root, b.ID, "story-remove-character", missing)
	p, e := s.PreviewStoryContext(ctx, q.Space, episodes[0].ID)
	if e != nil || len(p.Problems) == 0 {
		t.Fatal("dangling character ignored", e)
	}
	saveStory(t, s, root, id, "story-fact-create", f)
	update.ExpectedVersion = 2
	if _, e = s.SaveStory(ctx, storyActor, root, id, "story-new-invalid-character", update); !errors.Is(e, story.ErrInput) {
		t.Fatal("new invalid formal reference accepted", e)
	}
	if len(s.data.ScriptGenerations) != 0 || len(s.data.StoryboardGenerations) != 0 || len(s.data.Tasks) != 0 {
		t.Fatal("formal metadata called a model")
	}
}
func TestFileStoryFormalVersionsContextAndKnowledge(t *testing.T) { storyContract(t, testStore(t)) }
func TestPostgresStoryFormalVersionsContextAndKnowledge(t *testing.T) {
	storyContract(t, postgresTestStore(t, postgresTestDB(t)))
}
func TestStoryEvidenceVerifiesPinnedActualScene(t *testing.T) {
	s := testStore(t)
	root, eps := storyFixture(t, s)
	in := scriptFixture(t)
	for i := range in.Scenes {
		in.Scenes[i].EpisodeNumber = 1
	}
	stable, _, e := projects.CompileStructure(in, eps[0].SpaceID+":"+eps[0].ID, 1)
	if e != nil {
		t.Fatal(e)
	}
	p, e := s.SaveProjectScript(eps[0].SpaceID, eps[0].ID, "story-evidence-script", ScriptSaveInput{ExpectedVersion: 1, Script: stable})
	if e != nil {
		t.Fatal(e)
	}
	input := story.Input{Title: "来源事件", Kind: "event", Status: "active", Body: story.Body{Event: &story.Event{EpisodeID: p.ID, Description: "原文支持的事件"}, Evidence: []story.Evidence{{EpisodeID: p.ID, SceneID: p.Script.Scenes[0].ID, ScriptVersion: p.ScriptVersion, ScriptSHA256: story.Digest(p.Script), Quote: p.Script.Scenes[0].Action}}}}
	id := story.NewID(storyActor, root, "story-evidence-create")
	saveStory(t, s, root, id, "story-evidence-create", input)
	for name, mutate := range map[string]func(*story.Input){"fake text": func(x *story.Input) { x.Body.Evidence[0].Quote = "not present in this scene" }, "wrong digest": func(x *story.Input) { x.Body.Evidence[0].ScriptSHA256 = story.TextHash("forged") }, "wrong scene": func(x *story.Input) { x.Body.Evidence[0].SceneID = fmt.Sprintf("%032x", 999) }} {
		t.Run(name, func(t *testing.T) {
			copy, _ := story.Clone(input)
			copy.ExpectedVersion = 1
			mutate(&copy)
			if _, e := s.SaveStory(context.Background(), storyActor, root, id, "bad-evidence-"+story.TextHash(name)[:12], copy); e == nil {
				t.Fatal("forged evidence accepted")
			}
		})
	}
	changed := projects.CloneScript(*p.Script)
	changed.Scenes[0].Action = "后来的改稿"
	if _, e = s.SaveProjectScript(p.SpaceID, p.ID, "story-evidence-rewrite", ScriptSaveInput{ExpectedVersion: p.Version, Script: changed}); e != nil {
		t.Fatal(e)
	}
	update := input
	update.ExpectedVersion = 1
	update.Title = "旧证据仍能指向真实历史版本"
	saveStory(t, s, root, id, "story-evidence-old-version", update)
}
func TestPostgresStoryConcurrentInstances(t *testing.T) {
	ctx := context.Background()
	db := postgresTestDB(t)
	a, b := postgresTestStore(t, db), postgresTestStore(t, db)
	root, _ := storyFixture(t, a)
	input := storyBible()
	id := storyrepo.BibleID(root)
	var wg sync.WaitGroup
	results := make(chan story.Version, 8)
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s := a
			if i%2 == 1 {
				s = b
			}
			v, e := s.SaveStory(ctx, storyActor, root, id, "story-concurrent-same", input)
			results <- v
			errs <- e
		}(i)
	}
	wg.Wait()
	close(results)
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	var first story.Version
	for v := range results {
		if first.ID != "" && !reflect.DeepEqual(first, v) {
			t.Fatal("concurrent receipt changed")
		}
		first = v
	}
	history, e := a.story.History(ctx, storyrepo.Scope{Space: storyActor.Space, Series: root}, id, 0, 100)
	if e != nil || len(history.Items) != 1 {
		t.Fatal("duplicate formal version", e)
	}
}
func TestStoryFileRestartEmptyIntentAndCorruptVersionFailClosed(t *testing.T) {
	dir := t.TempDir()
	s, e := OpenStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	root, _ := storyFixture(t, s)
	input := storyBible()
	v := saveStory(t, s, root, storyrepo.BibleID(root), "story-restart-save", input)
	if e = s.Close(); e != nil {
		t.Fatal(e)
	}
	s, e = OpenStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	got := saveStory(t, s, root, v.ID, "story-restart-save", input)
	if !reflect.DeepEqual(v, got) {
		t.Fatal("restart lost original receipt")
	}
	empty := filepath.Join(dir, "story", story.TextHash(storyActor.Space), root, fmt.Sprintf("%032x", 800))
	if e = os.MkdirAll(empty, 0700); e != nil {
		t.Fatal(e)
	}
	page, e := s.story.List(context.Background(), storyrepo.Scope{Space: storyActor.Space, Series: root}, storyrepo.Query{Limit: 100})
	if e != nil || len(page.Items) != 1 {
		t.Fatal("empty precommit directory broke list", e)
	}
	files, e := os.ReadDir(filepath.Join(dir, "story", story.TextHash(storyActor.Space), root, v.ID))
	if e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(dir, "story", story.TextHash(storyActor.Space), root, v.ID, files[0].Name())
	raw, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(path, append(raw, []byte("{}")...), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = s.story.Get(context.Background(), storyrepo.Scope{Space: storyActor.Space, Series: root}, v.ID, ""); !errors.Is(e, storyrepo.ErrStorage) {
		t.Fatal("corrupt formal history accepted", e)
	}
}

func TestPostgresStoryOfflineRoundTripPreservesVersionsReceiptsAndSource(t *testing.T) {
	ctx := context.Background()
	db := postgresTestDB(t)
	source := t.TempDir()
	file, e := OpenStore(source)
	if e != nil {
		t.Fatal(e)
	}
	root, eps := storyFixture(t, file)
	input := storyBible()
	original := saveStory(t, file, root, storyrepo.BibleID(root), "story-offline-create", input)
	input.ExpectedVersion = 1
	input.Body.Bible.WorldRules = "规则的新修订，原版继续保留。"
	head := saveStory(t, file, root, original.ID, "story-offline-update", input)
	preview, e := file.PreviewStoryContext(ctx, storyActor.Space, eps[0].ID)
	if e != nil {
		t.Fatal(e)
	}
	snapshot, e := file.CreateStoryContext(ctx, storyActor, eps[0].ID, "story-offline-context", story.Selection{Query: "寻找"})
	if e != nil {
		t.Fatal(e)
	}
	if e = file.Close(); e != nil {
		t.Fatal(e)
	}
	before, e := os.ReadFile(filepath.Join(source, "state.json"))
	if e != nil {
		t.Fatal(e)
	}
	report, e := ImportPostgresData(ctx, source, db)
	if e != nil || report.Records["story_versions"] != 2 || report.Records["story_contexts"] != 1 {
		t.Fatal("story import", e, report.Records)
	}
	after, e := os.ReadFile(filepath.Join(source, "state.json"))
	if e != nil || string(before) != string(after) {
		t.Fatal("import rewrote source", e)
	}
	pg := postgresTestStore(t, db)
	q := storyrepo.Scope{Space: storyActor.Space, Series: root}
	got, e := pg.story.Get(ctx, q, head.ID, "")
	if e != nil || !reflect.DeepEqual(got, head) {
		t.Fatal("PG head differs", e)
	}
	pinned, e := pg.CreateStoryContext(ctx, storyActor, eps[0].ID, "story-offline-context", story.Selection{Query: "寻找"})
	if e != nil || !reflect.DeepEqual(pinned, snapshot) {
		t.Fatal("PG context snapshot differs", e)
	}
	again, e := pg.PreviewStoryContext(ctx, storyActor.Space, eps[0].ID)
	if e != nil || !reflect.DeepEqual(preview, again) {
		t.Fatal("PG context pins differ", e)
	}
	restoredDir := filepath.Join(t.TempDir(), "restored")
	report, e = ExportPostgresData(ctx, restoredDir, db)
	if e != nil || report.Records["story_versions"] != 2 || report.Records["story_contexts"] != 1 {
		t.Fatal("story export", e, report.Records)
	}
	restored, e := OpenStore(restoredDir)
	if e != nil {
		t.Fatal(e)
	}
	defer restored.Close()
	pinned, e = restored.CreateStoryContext(ctx, storyActor, eps[0].ID, "story-offline-context", story.Selection{Query: "寻找"})
	if e != nil || !reflect.DeepEqual(pinned, snapshot) {
		t.Fatal("restored context receipt differs", e)
	}
	got = saveStory(t, restored, root, original.ID, "story-offline-create", storyBible())
	if !reflect.DeepEqual(got, original) {
		t.Fatal("offline switch lost original receipt")
	}
	history, e := restored.story.History(ctx, q, head.ID, 0, 100)
	if e != nil || len(history.Items) != 2 || history.Items[0].Number != 2 {
		t.Fatal("offline switch lost history", e)
	}
	again, e = restored.PreviewStoryContext(ctx, storyActor.Space, eps[0].ID)
	if e != nil || !reflect.DeepEqual(preview, again) {
		t.Fatal("restored context differs", e)
	}
}
