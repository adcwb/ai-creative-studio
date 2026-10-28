package application

import (
	"regexp"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/projects"
)

type ProjectInput = projects.ProjectInput
type ProjectUpdate = projects.ProjectUpdate
type ScriptSaveInput = projects.ScriptSaveInput
type Script = projects.Script
type ScriptCharacter = projects.ScriptCharacter
type ScriptScene = projects.ScriptScene
type ScriptDialogue = projects.ScriptDialogue
type Project = projects.Project
type ScriptGeneration = projects.ScriptGeneration
type StoryboardShot = projects.StoryboardShot
type Storyboard = projects.Storyboard
type StoryboardSaveInput = projects.StoryboardSaveInput
type StoryboardGeneration = projects.StoryboardGeneration

var ErrInvalidProjectInput = projects.ErrInvalidProjectInput
var ErrIdempotencyConflict = projects.ErrIdempotencyConflict
var ErrVersionConflict = projects.ErrVersionConflict
var ErrGenerationActive = projects.ErrGenerationActive
var ErrScriptMissing = projects.ErrScriptMissing
var ErrStoryboardMissing = projects.ErrStoryboardMissing

func projectInputError(message string) error { return projects.InputError(message) }
func normalizeProjectText(value, label string, minimum, maximum int) (string, error) {
	return projects.NormalizeText(value, label, minimum, maximum)
}
func NormalizeProjectInput(in ProjectInput) (ProjectInput, error) {
	return projects.NormalizeProjectInput(in)
}
func NormalizeScript(script Script) (Script, error) { return projects.NormalizeScript(script) }
func NormalizeStoryboardShots(shots []StoryboardShot) ([]StoryboardShot, error) {
	return projects.NormalizeStoryboardShots(shots)
}
func cloneScript(script Script) Script     { return projects.CloneScript(script) }
func cloneProject(project Project) Project { return projects.CloneProject(project) }
func cloneStoryboardGeneration(g StoryboardGeneration) StoryboardGeneration {
	return projects.CloneStoryboardGeneration(g)
}
func storyboardGenerationView(g StoryboardGeneration) StoryboardGeneration {
	return projects.StoryboardGenerationView(g)
}

var shotIDPattern = regexp.MustCompile("^[A-Za-z0-9_-]{1,40}$")
