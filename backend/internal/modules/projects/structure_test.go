package projects

import (
	"reflect"
	"testing"
)

func legacyStructureFixture() Script {
	return Script{Title: "旧剧本", Logline: "两个场景的合成测试", Characters: []ScriptCharacter{{Name: "小满", Description: "故事主角"}}, Scenes: []ScriptScene{{Heading: "河边", Action: "主角走来", Dialogue: []ScriptDialogue{{Character: "小满", Text: "你好"}}}, {EpisodeNumber: 7, Heading: "街道", Action: "主角离去", Dialogue: []ScriptDialogue{}}}}
}

func TestStructureCompilerDeterminismScopeAndNoReadMutation(t *testing.T) {
	original := legacyStructureFixture()
	before := CloneScript(original)
	a, mapping, err := CompileStructure(original, "space:project", 1)
	if err != nil {
		t.Fatal(err)
	}
	b, again, err := CompileStructure(original, "space:project", 1)
	if err != nil || !reflect.DeepEqual(a, b) || !reflect.DeepEqual(mapping, again) || !reflect.DeepEqual(original, before) {
		t.Fatal("compiler changed source or assigned unstable IDs", err)
	}
	for _, other := range []struct {
		namespace string
		version   int
	}{{"other:project", 1}, {"space:other", 1}, {"space:project", 2}} {
		v, _, _ := CompileStructure(original, other.namespace, other.version)
		if v.Scenes[0].ID == a.Scenes[0].ID || v.Characters[0].ID == a.Characters[0].ID {
			t.Fatal("source identities alias across revisions or owners")
		}
	}
	if len(mapping.SourceSHA256) != 64 || mapping.CompilerVersion != 1 || mapping.Scenes[1].Index != 2 || a.Scenes[0].Dialogue[0].CharacterID != a.Characters[0].ID {
		t.Fatal("incomplete original mapping", mapping)
	}
	stable, _, err := CompileStructure(a, "same-source-now-stable", 2)
	if err != nil || !reflect.DeepEqual(stable, a) {
		t.Fatal("stable source was reidentified", err)
	}
}

func TestStableStructureRenameReorderAndInvalidReferences(t *testing.T) {
	stable, _, _ := CompileStructure(legacyStructureFixture(), "space:project", 1)
	ids := SceneIDs(stable)
	stable.Characters[0].Name = "秋生"
	stable.Scenes[0], stable.Scenes[1] = stable.Scenes[1], stable.Scenes[0]
	v, err := NormalizeScript(stable)
	if err != nil || v.Scenes[1].Dialogue[0].Character != "秋生" || v.Scenes[1].ID != ids[0] {
		t.Fatal("identity association lost on rename or reorder", err)
	}
	for name, mutate := range map[string]func(*Script){
		"deleted character":         func(s *Script) { s.Characters = nil },
		"missing character id":      func(s *Script) { s.Characters[0].ID = "" },
		"missing scene id":          func(s *Script) { s.Scenes[0].ID = "" },
		"duplicate scene id":        func(s *Script) { s.Scenes[0].ID = s.Scenes[1].ID },
		"unknown reference":         func(s *Script) { s.Scenes[1].Dialogue[0].CharacterID = "unknown" },
		"unknown version":           func(s *Script) { s.StructureVersion = 3 },
		"legacy partial identities": func(s *Script) { s.StructureVersion = 0 },
	} {
		t.Run(name, func(t *testing.T) {
			s := CloneScript(stable)
			mutate(&s)
			if _, err := NormalizeScript(s); err == nil {
				t.Fatal("invalid association accepted")
			}
		})
	}
	shots := []StoryboardShot{{ID: "one", SceneIndex: 1, Description: "合成镜头", ShotSize: "medium", CameraMovement: "static", DurationSeconds: 3}}
	board, err := BindStoryboard(shots, ids)
	if err != nil || board[0].SceneID != ids[0] || shots[0].SceneID != "" {
		t.Fatal("original source not pinned or caller mutated", err)
	}
	if _, err := BindStoryboard(board, SceneIDs(stable)); err == nil {
		t.Fatal("reordered current script silently rebound old source")
	}
	board[0].SceneIndex = 0
	if _, err := BindStoryboard(board, ids); err == nil {
		t.Fatal("independent shot retained contradictory association")
	}
}
