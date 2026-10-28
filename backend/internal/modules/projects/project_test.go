package projects_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/projects"
)

func scriptFixture() projects.Script {
	s := projects.Script{Title: " 合成剧本 ", Logline: " 合成的故事梗概。 ", Characters: []projects.ScriptCharacter{{Name: "甲", Description: "第一个合成角色"}, {Name: "乙", Description: "第二个合成角色"}}}
	for range 3 {
		s.Scenes = append(s.Scenes, projects.ScriptScene{Heading: " 合成场景 ", Action: " 合成动作 ", Dialogue: []projects.ScriptDialogue{{Character: "甲", Text: " 合成对白 "}}})
	}
	return s
}

func TestNormalizeScriptDoesNotMutateCaller(t *testing.T) {
	input := scriptFixture()
	normalized, err := projects.NormalizeScript(input)
	if err != nil {
		t.Fatal(err)
	}
	if normalized.Title != "合成剧本" || input.Title != " 合成剧本 " {
		t.Fatal("normalization changed the source title")
	}
	normalized.Scenes[0].Dialogue[0].Text = "changed"
	if input.Scenes[0].Dialogue[0].Text != " 合成对白 " {
		t.Fatal("dialogue slices share mutable backing storage")
	}
	input.Scenes[0].Dialogue[0].Character = "不存在"
	if _, err := projects.NormalizeScript(input); !errors.Is(err, projects.ErrInvalidProjectInput) {
		t.Fatalf("invalid character reference: %v", err)
	}
}

func TestStoryboardRequiresExplicitDialogue(t *testing.T) {
	var shot projects.StoryboardShot
	if err := json.Unmarshal([]byte(`{"id":"s1","scene_index":1,"description":"合成画面","shot_size":"wide","camera_movement":"static","duration_seconds":3}`), &shot); err == nil {
		t.Fatal("missing dialogue accepted")
	}
	if err := json.Unmarshal([]byte(`{"id":"s1","scene_index":1,"description":"合成画面","shot_size":"wide","camera_movement":"static","duration_seconds":3,"dialogue":""}`), &shot); err != nil {
		t.Fatal(err)
	}
	if _, err := projects.NormalizeStoryboardShots([]projects.StoryboardShot{shot, shot}); !errors.Is(err, projects.ErrInvalidProjectInput) {
		t.Fatalf("duplicate shot identity accepted: %v", err)
	}
}
