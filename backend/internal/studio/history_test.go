package studio

import (
	"errors"
	"testing"
)

func TestHistoryRestoreDurableIdempotentAndIsolated(t *testing.T) {
	dir := t.TempDir()
	s, e := OpenStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	p := makeScript(t, s, createProject(t, s))
	original := p.Script.Title
	draft := cloneScript(*p.Script)
	draft.Title = "修改后的剧本"
	p, e = s.SaveProjectScript(p.SpaceID, p.ID, "history-save-key", ScriptSaveInput{ExpectedVersion: p.Version, Script: draft})
	if e != nil {
		t.Fatal(e)
	}
	list, e := s.ProjectHistory(p.SpaceID, p.ID)
	if e != nil || len(list) != 1 || list[0].Script.Title != original {
		t.Fatalf("history %#v %v", list, e)
	}
	list[0].Script.Title = "外部污染"
	again, _ := s.ProjectHistory(p.SpaceID, p.ID)
	if again[0].Script.Title != original {
		t.Fatal("history escaped deep copy")
	}
	if _, e = s.ProjectHistory("team-demo", p.ID); !errors.Is(e, ErrNotFound) {
		t.Fatal("cross space history", e)
	}
	if _, e = s.RestoreHistory(p.SpaceID, p.ID, list[0].ID, "restore-old-key", ScriptGenerationInput{ExpectedVersion: p.Version - 1}); !errors.Is(e, ErrVersionConflict) {
		t.Fatal("stale version accepted", e)
	}
	version := p.Version
	sv := p.ScriptVersion
	p, e = s.RestoreHistory(p.SpaceID, p.ID, list[0].ID, "restore-good-key", ScriptGenerationInput{ExpectedVersion: version})
	if e != nil || p.Script.Title != original || p.Version != version+1 || p.ScriptVersion != sv+1 {
		t.Fatal("restore", p, e)
	}
	s.Close()
	s, e = OpenStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	p, e = s.RestoreHistory(p.SpaceID, p.ID, list[0].ID, "restore-good-key", ScriptGenerationInput{ExpectedVersion: version})
	if e != nil || p.Version != version+1 {
		t.Fatal("replay", p, e)
	}
	history, _ := s.ProjectHistory(p.SpaceID, p.ID)
	if len(history) != 2 || history[0].Script.Title != draft.Title {
		t.Fatal("restore did not preserve current", history)
	}
}

func TestStoryboardHistoryRestoreKeepsSourceAndIncrementsVersion(t *testing.T) {
	s, e := OpenStore(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	p := boardProject(t, s)
	original := p.Storyboard.Shots[0].Description
	shots := append([]StoryboardShot(nil), p.Storyboard.Shots...)
	shots[0].Description = "改过的镜头"
	p, e = s.SaveProjectStoryboard(p.SpaceID, p.ID, "history-board-save", StoryboardSaveInput{ExpectedVersion: p.Version, Shots: shots})
	if e != nil {
		t.Fatal(e)
	}
	history, _ := s.ProjectHistory(p.SpaceID, p.ID)
	if len(history) != 1 || history[0].Kind != "storyboard" {
		t.Fatal(history)
	}
	script := cloneScript(*p.Script)
	script.Title = "新剧本标题"
	p, e = s.SaveProjectScript(p.SpaceID, p.ID, "history-script-change", ScriptSaveInput{ExpectedVersion: p.Version, Script: script})
	if e != nil {
		t.Fatal(e)
	}
	p, e = s.RestoreHistory(p.SpaceID, p.ID, history[0].ID, "history-board-restore", ScriptGenerationInput{ExpectedVersion: p.Version})
	if e != nil || !p.StoryboardStale || p.Storyboard.Shots[0].Description != original || p.Storyboard.SourceScriptVersion != 1 {
		t.Fatal(p, e)
	}
}
