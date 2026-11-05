package projects

import (
	"time"
)

type ScriptSaveInput struct {
	Script          Script `json:"script"`
	ExpectedVersion int    `json:"expected_version"`
}

type Script struct {
	StructureVersion int               `json:"structure_version,omitempty"`
	Title            string            `json:"title"`
	Logline          string            `json:"logline"`
	Characters       []ScriptCharacter `json:"characters"`
	Scenes           []ScriptScene     `json:"scenes"`
}

type ScriptCharacter struct {
	ID          string `json:"id,omitempty"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

type ScriptScene struct {
	ID            string           `json:"id,omitempty"`
	EpisodeNumber int              `json:"episode_number,omitempty"`
	Heading       string           `json:"heading"`
	Action        string           `json:"action"`
	Dialogue      []ScriptDialogue `json:"dialogue"`
}

type ScriptDialogue struct {
	CharacterID string `json:"character_id,omitempty"`
	Character   string `json:"character"`
	Text        string `json:"text"`
}

type ScriptGeneration struct {
	SourceEpisodeID       string            `json:"source_episode_id,omitempty"`
	TargetDurationSeconds int64             `json:"target_duration_seconds,omitempty"`
	TraceParent           string            `json:"trace_parent,omitempty"`
	RuntimeSelection      *RuntimeSelection `json:"runtime_selection,omitempty"`
	Model                 string            `json:"model,omitempty"`
	ID                    string            `json:"id"`
	SpaceID               string            `json:"space_id"`
	ProjectID             string            `json:"project_id"`
	SourceTitle           string            `json:"source_title"`
	SourceBrief           string            `json:"source_brief"`
	BaseVersion           int               `json:"base_version"`
	Provider              string            `json:"provider"`
	Status                string            `json:"status"`
	Progress              int               `json:"progress"`
	CreatedAt             time.Time         `json:"created_at"`
	UpdatedAt             time.Time         `json:"updated_at"`
	Error                 string            `json:"error,omitempty"`
	ErrorCode             string            `json:"error_code,omitempty"`
}

func CloneScript(script Script) Script {
	script.Characters = append([]ScriptCharacter{}, script.Characters...)
	script.Scenes = append([]ScriptScene(nil), script.Scenes...)
	for i := range script.Scenes {
		script.Scenes[i].Dialogue = append([]ScriptDialogue{}, script.Scenes[i].Dialogue...)
	}
	return script
}

func NormalizeScript(script Script) (Script, error) {
	script = CloneScript(script)
	if script.StructureVersion != 0 && script.StructureVersion != 2 {
		return Script{}, InputError("剧本结构版本不受支持，请使用兼容版本的编辑器")
	}
	var err error
	if script.Title, err = NormalizeText(script.Title, "剧本标题", 1, 80); err != nil {
		return Script{}, err
	}
	if script.Logline, err = NormalizeText(script.Logline, "一句话梗概", 3, 300); err != nil {
		return Script{}, err
	}
	if len(script.Scenes) < 1 {
		return Script{}, InputError("剧本需包含场景，角色与对白引用必须有效")
	}
	names := map[string]bool{}
	characterIDs := map[string]string{}
	sceneIDs := map[string]bool{}
	for i := range script.Characters {
		character := &script.Characters[i]
		if script.StructureVersion == 2 {
			if !structureIDPattern.MatchString(character.ID) || characterIDs[character.ID] != "" {
				return Script{}, InputError("角色标识缺失、无效或重复")
			}
		} else if character.ID != "" {
			return Script{}, InputError("稳定角色标识需要剧本结构版本2")
		}
		if character.Name, err = NormalizeText(character.Name, "角色名", 1, 40); err != nil {
			return Script{}, err
		}
		if names[character.Name] {
			return Script{}, InputError("角色名不能重复")
		}
		names[character.Name] = true
		characterIDs[character.ID] = character.Name
		if character.Description, err = NormalizeText(character.Description, "角色描述", 1, 200); err != nil {
			return Script{}, err
		}
	}
	for i := range script.Scenes {
		scene := &script.Scenes[i]
		if script.StructureVersion == 2 {
			if !structureIDPattern.MatchString(scene.ID) || sceneIDs[scene.ID] {
				return Script{}, InputError("场次标识缺失、无效或重复")
			}
			sceneIDs[scene.ID] = true
		} else if scene.ID != "" {
			return Script{}, InputError("稳定场次标识需要剧本结构版本2")
		}
		if scene.EpisodeNumber < 0 || scene.EpisodeNumber > 9007199254740991 {
			return Script{}, InputError("集数需为有效正整数")
		}
		if scene.Heading, err = NormalizeText(scene.Heading, "场景标题", 1, 100); err != nil {
			return Script{}, err
		}
		if scene.Action, err = NormalizeText(scene.Action, "场景动作", 1, 1000); err != nil {
			return Script{}, err
		}
		if len(scene.Dialogue) > 100 {
			return Script{}, InputError("每个场景最多100条对白")
		}
		for j := range scene.Dialogue {
			line := &scene.Dialogue[j]
			if script.StructureVersion == 2 {
				name, ok := characterIDs[line.CharacterID]
				if !ok || line.CharacterID == "" {
					return Script{}, InputError("对白必须引用有效的角色标识")
				}
				// Names are display snapshots; the identity owns the association.
				line.Character = name
			} else if line.CharacterID != "" {
				return Script{}, InputError("稳定对白引用需要剧本结构版本2")
			}
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
