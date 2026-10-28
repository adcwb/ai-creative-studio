package projects

import (
	"time"
)

type ScriptSaveInput struct {
	Script          Script `json:"script"`
	ExpectedVersion int    `json:"expected_version"`
}

type Script struct {
	Title      string            `json:"title"`
	Logline    string            `json:"logline"`
	Characters []ScriptCharacter `json:"characters"`
	Scenes     []ScriptScene     `json:"scenes"`
}

type ScriptCharacter struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type ScriptScene struct {
	Heading  string           `json:"heading"`
	Action   string           `json:"action"`
	Dialogue []ScriptDialogue `json:"dialogue"`
}

type ScriptDialogue struct {
	Character string `json:"character"`
	Text      string `json:"text"`
}

type ScriptGeneration struct {
	TraceParent      string            `json:"trace_parent,omitempty"`
	RuntimeSelection *RuntimeSelection `json:"runtime_selection,omitempty"`
	Model            string            `json:"model,omitempty"`
	ID               string            `json:"id"`
	SpaceID          string            `json:"space_id"`
	ProjectID        string            `json:"project_id"`
	SourceTitle      string            `json:"source_title"`
	SourceBrief      string            `json:"source_brief"`
	BaseVersion      int               `json:"base_version"`
	Provider         string            `json:"provider"`
	Status           string            `json:"status"`
	Progress         int               `json:"progress"`
	CreatedAt        time.Time         `json:"created_at"`
	UpdatedAt        time.Time         `json:"updated_at"`
	Error            string            `json:"error,omitempty"`
	ErrorCode        string            `json:"error_code,omitempty"`
}

func CloneScript(script Script) Script {
	script.Characters = append([]ScriptCharacter(nil), script.Characters...)
	script.Scenes = append([]ScriptScene(nil), script.Scenes...)
	for i := range script.Scenes {
		script.Scenes[i].Dialogue = append([]ScriptDialogue(nil), script.Scenes[i].Dialogue...)
	}
	return script
}

func NormalizeScript(script Script) (Script, error) {
	script = CloneScript(script)
	var err error
	if script.Title, err = NormalizeText(script.Title, "剧本标题", 1, 80); err != nil {
		return Script{}, err
	}
	if script.Logline, err = NormalizeText(script.Logline, "一句话梗概", 3, 300); err != nil {
		return Script{}, err
	}
	if len(script.Characters) != 2 || len(script.Scenes) != 3 {
		return Script{}, InputError("本轮剧本必须包含 2 个角色和 3 个场景")
	}
	names := map[string]bool{}
	for i := range script.Characters {
		character := &script.Characters[i]
		if character.Name, err = NormalizeText(character.Name, "角色名", 1, 40); err != nil {
			return Script{}, err
		}
		if names[character.Name] {
			return Script{}, InputError("角色名不能重复")
		}
		names[character.Name] = true
		if character.Description, err = NormalizeText(character.Description, "角色描述", 1, 200); err != nil {
			return Script{}, err
		}
	}
	for i := range script.Scenes {
		scene := &script.Scenes[i]
		if scene.Heading, err = NormalizeText(scene.Heading, "场景标题", 1, 100); err != nil {
			return Script{}, err
		}
		if scene.Action, err = NormalizeText(scene.Action, "场景动作", 1, 1000); err != nil {
			return Script{}, err
		}
		if len(scene.Dialogue) < 1 || len(scene.Dialogue) > 6 {
			return Script{}, InputError("每个场景需包含 1–6 条对白")
		}
		for j := range scene.Dialogue {
			line := &scene.Dialogue[j]
			if line.Character, err = NormalizeText(line.Character, "对白角色", 1, 40); err != nil {
				return Script{}, err
			}
			if !names[line.Character] {
				return Script{}, InputError("对白必须引用剧本中的角色")
			}
			if line.Text, err = NormalizeText(line.Text, "对白", 1, 300); err != nil {
				return Script{}, err
			}
		}
	}
	return script, nil
}
