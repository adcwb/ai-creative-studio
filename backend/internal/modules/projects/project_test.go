package projects_test

import (
	"encoding/json"
	"errors"
	"fmt"
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

func TestScriptCountsAreNotProjectQuotas(t *testing.T) {
	input := scriptFixture()
	for i := 0; i < 35; i++ {
		input.Characters = append(input.Characters, projects.ScriptCharacter{Name: fmt.Sprintf("角色%d", i), Description: "合成角色说明"})
	}
	for range 140 {
		input.Scenes[0].Dialogue = append(input.Scenes[0].Dialogue, projects.ScriptDialogue{Character: "甲", Text: "保持有效角色关联的对白"})
	}
	if _, e := projects.NormalizeScript(input); e != nil {
		t.Fatal("valid authored counts rejected", e)
	}
	input.Scenes[0].Dialogue[len(input.Scenes[0].Dialogue)-1].Character = "没有声明的角色"
	if _, e := projects.NormalizeScript(input); e == nil {
		t.Fatal("count removal weakened speaker integrity")
	}
}

func TestDirectorIntentsSurviveStrictStoryboardNormalization(t *testing.T) {
	shot := projects.StoryboardShot{ID: "directed", Description: "门框中的人物", ShotSize: "medium", CameraMovement: "static", DurationSeconds: 121, Dialogue: "", Angle: " 低机位 ", Composition: "右侧留白", Axis: "保持门内门外轴线", Eyeline: "看向画右", Blocking: "停顿后推门", Lighting: "青灰夜色", Sound: "雨声中停顿"}
	raw, e := json.Marshal(shot)
	if e != nil {
		t.Fatal(e)
	}
	var decoded projects.StoryboardShot
	if e = json.Unmarshal(raw, &decoded); e != nil {
		t.Fatal(e)
	}
	got, e := projects.NormalizeStoryboardShots([]projects.StoryboardShot{decoded})
	if e != nil || got[0].Angle != "低机位" || got[0].Sound != shot.Sound || got[0].DurationSeconds != 121 || shot.Angle != " 低机位 " {
		t.Fatal("authored intent lost or caller mutated", got, e)
	}
	for i := 0; i < 1001; i++ {
		shot.Blocking += "动"
	}
	if _, e = projects.NormalizeStoryboardShots([]projects.StoryboardShot{shot}); e == nil {
		t.Fatal("oversized authored field accepted")
	}
}
