package studio

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

type ProjectInput struct {
	Title string `json:"title"`
	Brief string `json:"brief"`
}

type ProjectUpdate struct {
	Title           string `json:"title"`
	Brief           string `json:"brief"`
	ExpectedVersion int    `json:"expected_version"`
}

type ScriptGenerationInput struct {
	ExpectedVersion int `json:"expected_version"`
}

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

type Project struct {
	ScriptVersion                int                   `json:"script_version"`
	Storyboard                   *Storyboard           `json:"storyboard,omitempty"`
	StoryboardStale              bool                  `json:"storyboard_stale"`
	LatestStoryboardGeneration   *StoryboardGeneration `json:"latest_storyboard_generation,omitempty"`
	LatestStoryboardGenerationID string                `json:"latest_storyboard_generation_id,omitempty"`
	ID                           string                `json:"id"`
	SpaceID                      string                `json:"space_id"`
	Title                        string                `json:"title"`
	Brief                        string                `json:"brief"`
	Version                      int                   `json:"version"`
	Script                       *Script               `json:"script,omitempty"`
	ScriptBrief                  string                `json:"script_brief,omitempty"`
	LatestGeneration             *ScriptGeneration     `json:"latest_generation,omitempty"`
	CreatedAt                    time.Time             `json:"created_at"`
	UpdatedAt                    time.Time             `json:"updated_at"`
	LatestGenerationID           string                `json:"latest_generation_id,omitempty"`
}

type ScriptGeneration struct {
	ID          string    `json:"id"`
	SpaceID     string    `json:"space_id"`
	ProjectID   string    `json:"project_id"`
	SourceTitle string    `json:"source_title"`
	SourceBrief string    `json:"source_brief"`
	BaseVersion int       `json:"base_version"`
	Provider    string    `json:"provider"`
	Status      string    `json:"status"`
	Progress    int       `json:"progress"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	Error       string    `json:"error,omitempty"`
	ErrorCode   string    `json:"error_code,omitempty"`
}

type projectReceipt struct {
	SpaceID      string `json:"space_id"`
	Key          string `json:"key"`
	Operation    string `json:"operation"`
	ProjectID    string `json:"project_id"`
	Fingerprint  string `json:"fingerprint"`
	GenerationID string `json:"generation_id,omitempty"`
}

var (
	ErrInvalidProjectInput = errors.New("项目输入无效")
	ErrIdempotencyConflict = errors.New("幂等键已用于不同的项目操作或内容")
	ErrVersionConflict     = errors.New("项目版本已更新，请保留草稿并读取最新版本")
	ErrGenerationActive    = errors.New("项目正在生成，请等待本次生成完成")
	ErrScriptMissing       = errors.New("请先成功生成一版模拟剧本")
)

func projectInputError(message string) error {
	return fmt.Errorf("%w：%s", ErrInvalidProjectInput, message)
}

func normalizeProjectText(value, label string, minimum, maximum int) (string, error) {
	value = strings.TrimSpace(value)
	length := utf8.RuneCountInString(value)
	if length < minimum || length > maximum {
		return "", projectInputError(fmt.Sprintf("%s需为 %d–%d 个字符", label, minimum, maximum))
	}
	return value, nil
}

func NormalizeProjectInput(in ProjectInput) (ProjectInput, error) {
	var err error
	if in.Title, err = normalizeProjectText(in.Title, "项目标题", 1, 80); err != nil {
		return ProjectInput{}, err
	}
	if in.Brief, err = normalizeProjectText(in.Brief, "故事简述", 3, 2000); err != nil {
		return ProjectInput{}, err
	}
	return in, nil
}

func cloneScript(script Script) Script {
	script.Characters = append([]ScriptCharacter(nil), script.Characters...)
	script.Scenes = append([]ScriptScene(nil), script.Scenes...)
	for i := range script.Scenes {
		script.Scenes[i].Dialogue = append([]ScriptDialogue(nil), script.Scenes[i].Dialogue...)
	}
	return script
}

func cloneProject(project Project) Project {
	if project.Storyboard != nil {
		plan := *project.Storyboard
		plan.Shots = append([]StoryboardShot(nil), plan.Shots...)
		project.Storyboard = &plan
	}
	if project.LatestStoryboardGeneration != nil {
		generation := cloneStoryboardGeneration(*project.LatestStoryboardGeneration)
		project.LatestStoryboardGeneration = &generation
	}
	if project.Script != nil {
		script := cloneScript(*project.Script)
		project.Script = &script
	}
	if project.LatestGeneration != nil {
		generation := *project.LatestGeneration
		project.LatestGeneration = &generation
	}
	return project
}

func NormalizeScript(script Script) (Script, error) {
	script = cloneScript(script)
	var err error
	if script.Title, err = normalizeProjectText(script.Title, "剧本标题", 1, 80); err != nil {
		return Script{}, err
	}
	if script.Logline, err = normalizeProjectText(script.Logline, "一句话梗概", 3, 300); err != nil {
		return Script{}, err
	}
	if len(script.Characters) != 2 || len(script.Scenes) != 3 {
		return Script{}, projectInputError("本轮剧本必须包含 2 个角色和 3 个场景")
	}
	names := map[string]bool{}
	for i := range script.Characters {
		character := &script.Characters[i]
		if character.Name, err = normalizeProjectText(character.Name, "角色名", 1, 40); err != nil {
			return Script{}, err
		}
		if names[character.Name] {
			return Script{}, projectInputError("角色名不能重复")
		}
		names[character.Name] = true
		if character.Description, err = normalizeProjectText(character.Description, "角色描述", 1, 200); err != nil {
			return Script{}, err
		}
	}
	for i := range script.Scenes {
		scene := &script.Scenes[i]
		if scene.Heading, err = normalizeProjectText(scene.Heading, "场景标题", 1, 100); err != nil {
			return Script{}, err
		}
		if scene.Action, err = normalizeProjectText(scene.Action, "场景动作", 1, 1000); err != nil {
			return Script{}, err
		}
		if len(scene.Dialogue) < 1 || len(scene.Dialogue) > 6 {
			return Script{}, projectInputError("每个场景需包含 1–6 条对白")
		}
		for j := range scene.Dialogue {
			line := &scene.Dialogue[j]
			if line.Character, err = normalizeProjectText(line.Character, "对白角色", 1, 40); err != nil {
				return Script{}, err
			}
			if !names[line.Character] {
				return Script{}, projectInputError("对白必须引用剧本中的角色")
			}
			if line.Text, err = normalizeProjectText(line.Text, "对白", 1, 300); err != nil {
				return Script{}, err
			}
		}
	}
	return script, nil
}
