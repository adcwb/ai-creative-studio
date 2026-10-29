package projects

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/governance"
)

type RuntimeSelection = governance.RuntimeSelection

type ProjectInput struct {
	Kind  string `json:"kind,omitempty"`
	Title string `json:"title"`
	Brief string `json:"brief"`
}

type ProjectUpdate struct {
	Title           string `json:"title"`
	Brief           string `json:"brief"`
	ExpectedVersion int    `json:"expected_version"`
}

type Project struct {
	Kind                         string                `json:"kind,omitempty"`
	ScriptProvider               string                `json:"script_provider,omitempty"`
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

func NormalizeText(value, label string, minimum, maximum int) (string, error) {
	value = strings.TrimSpace(value)
	length := utf8.RuneCountInString(value)
	if length < minimum || length > maximum {
		return "", InputError(fmt.Sprintf("%s需为 %d–%d 个字符", label, minimum, maximum))
	}
	return value, nil
}

func NormalizeProjectInput(in ProjectInput) (ProjectInput, error) {
	if in.Kind != "" && in.Kind != "drama" && in.Kind != "image" && in.Kind != "digital-human" {
		return ProjectInput{}, InputError("无效的创作类型")
	}
	var err error
	if in.Title, err = NormalizeText(in.Title, "项目标题", 1, 80); err != nil {
		return ProjectInput{}, err
	}
	if in.Brief, err = NormalizeText(in.Brief, "故事简述", 3, 2000); err != nil {
		return ProjectInput{}, err
	}
	return in, nil
}

func CloneProject(project Project) Project {
	if project.Storyboard != nil {
		plan := *project.Storyboard
		plan.Shots = append([]StoryboardShot(nil), plan.Shots...)
		project.Storyboard = &plan
	}
	if project.LatestStoryboardGeneration != nil {
		generation := CloneStoryboardGeneration(*project.LatestStoryboardGeneration)
		project.LatestStoryboardGeneration = &generation
	}
	if project.Script != nil {
		script := CloneScript(*project.Script)
		project.Script = &script
	}
	if project.LatestGeneration != nil {
		generation := *project.LatestGeneration
		project.LatestGeneration = &generation
	}
	return project
}
