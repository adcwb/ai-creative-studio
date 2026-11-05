package application

import (
	"context"
	"errors"
	"testing"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/projects"
	"github.com/adcwb/ai-creative-studio/backend/internal/modules/story"
)

func storyEvidenceContract(t *testing.T, s *FileStore) {
	t.Helper()
	ctx := context.Background()
	root, eps := storyFixture(t, s)
	space, episode := eps[0].SpaceID, eps[0].ID
	page, e := s.StoryEvidenceScripts(ctx, space, root, episode, 0, 1)
	if e != nil || len(page.Items) != 0 || page.Next != "" {
		t.Fatal("empty metadata", page, e)
	}
	script := scriptFixture(t)
	for i := range script.Scenes {
		script.Scenes[i].EpisodeNumber = 1
	}
	stable, _, e := projects.CompileStructure(script, space+":"+episode, 1)
	if e != nil {
		t.Fatal(e)
	}
	p, e := s.SaveProjectScript(space, episode, "evidence-picker-first", ScriptSaveInput{ExpectedVersion: 1, Script: stable})
	if e != nil {
		t.Fatal(e)
	}
	fixed, e := s.StoryEvidenceScript(ctx, space, root, episode, p.ScriptVersion)
	if e != nil || fixed.SHA256 != story.Digest(p.Script) || fixed.Script.Scenes[0].ID != p.Script.Scenes[0].ID {
		t.Fatal("fixed original", fixed, e)
	}
	// A writer can quote the fixed original even after subsequent script edits.
	newScript := projects.CloneScript(*p.Script)
	newScript.Title = "后来的剧本"
	newScript.Scenes[0].Action = "后来改写，不覆盖证据来源。"
	next, e := s.SaveProjectScript(space, episode, "evidence-picker-later", ScriptSaveInput{ExpectedVersion: p.Version, Script: newScript})
	if e != nil {
		t.Fatal(e)
	}
	page, e = s.StoryEvidenceScripts(ctx, space, root, episode, 0, 1)
	if e != nil || len(page.Items) != 1 || page.Items[0].Version != next.ScriptVersion || !page.Items[0].Current || !page.Items[0].Stable || page.Next == "" {
		t.Fatal("current page", page, e)
	}
	older, e := s.StoryEvidenceScripts(ctx, space, root, episode, next.ScriptVersion, 1)
	if e != nil || len(older.Items) != 1 || older.Items[0].Version != p.ScriptVersion || older.Items[0].Current || older.Next != "" {
		t.Fatal("history page", older, e)
	}
	read, e := s.StoryEvidenceScript(ctx, space, root, episode, p.ScriptVersion)
	if e != nil || story.Digest(read) != story.Digest(fixed) {
		t.Fatal("immutable source changed", e)
	}
	// The returned script is a copy, not mutable backing data.
	read.Script.Scenes[0].Action = "untrusted reader mutation"
	read, e = s.StoryEvidenceScript(ctx, space, root, episode, p.ScriptVersion)
	if e != nil || story.Digest(read) != story.Digest(fixed) {
		t.Fatal("source alias", e)
	}
	input := story.Input{Title: "由创作者明确选取的来源", Kind: "event", Status: "active", Body: story.Body{Event: &story.Event{EpisodeID: episode, Description: "保留原场次证据"}, Evidence: []story.Evidence{{EpisodeID: episode, SceneID: fixed.Script.Scenes[0].ID, ScriptVersion: fixed.Version, ScriptSHA256: fixed.SHA256, Quote: fixed.Script.Scenes[0].Action}}}}
	id := story.NewID(storyActor, root, "evidence-picker-formal")
	v, e := s.SaveStory(ctx, storyActor, root, id, "evidence-picker-formal", input)
	if e != nil || v.Body.Evidence[0].ScriptSHA256 != fixed.SHA256 {
		t.Fatal("selected evidence save", e)
	}
	bad, _ := story.Clone(input)
	bad.ExpectedVersion = 1
	bad.Body.Evidence[0].Quote = "原场次不存在的拼接原文"
	if _, e = s.SaveStory(ctx, storyActor, root, id, "evidence-picker-forged", bad); !errors.Is(e, story.ErrInput) {
		t.Fatal("forged quote", e)
	}
	for _, args := range []struct {
		space, root, ep string
		version         int
	}{{"other-space", root, episode, p.ScriptVersion}, {space, eps[1].ID, episode, p.ScriptVersion}, {space, root, eps[1].ID, p.ScriptVersion}, {space, root, episode, next.ScriptVersion + 1}, {space, root, episode, 0}} {
		if _, e = s.StoryEvidenceScript(ctx, args.space, args.root, args.ep, args.version); e == nil {
			t.Fatal("foreign/missing source accepted", args)
		}
	}
	if _, e = s.StoryEvidenceScripts(ctx, space, root, episode, 0, 101); !errors.Is(e, story.ErrInput) {
		t.Fatal("unbounded page", e)
	}
	persisted, e := s.GetProject(space, episode)
	if e != nil || persisted.Version != next.Version || persisted.Script.Scenes[0].Action != newScript.Scenes[0].Action {
		t.Fatal("read changed current script", e)
	}
}
func TestFileStoryEvidencePickerHistoricalSources(t *testing.T) {
	storyEvidenceContract(t, testStore(t))
}
func TestPostgresStoryEvidencePickerHistoricalSources(t *testing.T) {
	storyEvidenceContract(t, postgresTestStore(t, postgresTestDB(t)))
}

func TestStoryEvidenceLegacyAndAmbiguousSourcesAreNeverGuessedOrUpgraded(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	root, eps := storyFixture(t, s)
	ep := eps[0]
	legacy := scriptFixture(t)
	for i := range legacy.Scenes {
		legacy.Scenes[i].EpisodeNumber = 1
	}
	p, e := s.SaveProjectScript(ep.SpaceID, ep.ID, "evidence-legacy", ScriptSaveInput{ExpectedVersion: 1, Script: legacy})
	// The episode save contract already upgrades new saves. Inject an actual
	// pre-upgrade persisted value to exercise a legacy read, not a fake UI flag.
	if e != nil {
		t.Fatal(e)
	}
	s.mu.Lock()
	old := s.data.Projects[p.ID]
	old.Script.StructureVersion = 0
	for i := range old.Script.Scenes {
		old.Script.Scenes[i].ID = ""
	}
	for i := range old.Script.Characters {
		old.Script.Characters[i].ID = ""
	}
	s.data.Projects[p.ID] = old
	s.mu.Unlock()
	page, e := s.StoryEvidenceScripts(ctx, ep.SpaceID, root, ep.ID, 0, 20)
	if e != nil || len(page.Items) != 1 || page.Items[0].Stable {
		t.Fatal("legacy metadata", page, e)
	}
	if _, e = s.StoryEvidenceScript(ctx, ep.SpaceID, root, ep.ID, p.ScriptVersion); !errors.Is(e, story.ErrInput) {
		t.Fatal("legacy guessed", e)
	}
	s.mu.Lock()
	original := s.data.Projects[p.ID]
	original.Script.StructureVersion = 2
	for i := range original.Script.Scenes {
		original.Script.Scenes[i].ID = story.TextHash(original.Script.Scenes[i].Heading)[:32]
	}
	s.data.Projects[p.ID] = original
	duplicate := projects.CloneScript(*original.Script)
	duplicate.Scenes[0].Action = "ambiguous historical content"
	s.data.History[p.ID] = append(s.data.History[p.ID], ProjectRevision{Kind: "script", ScriptVersion: p.ScriptVersion, Script: &duplicate})
	s.mu.Unlock()
	if _, e = s.StoryEvidenceScript(ctx, ep.SpaceID, root, ep.ID, p.ScriptVersion); e == nil {
		t.Fatal("ambiguous source silently chosen")
	}
}
