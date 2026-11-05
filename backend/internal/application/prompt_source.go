package application

import (
	"github.com/adcwb/ai-creative-studio/backend/internal/modules/prompts"
	"net/http"
	"reflect"
)

type promptSourceInput struct {
	TemplateID string            `json:"template_id"`
	VersionID  string            `json:"version_id"`
	Edited     bool              `json:"edited"`
	Variables  map[string]string `json:"variables,omitempty"`
}

// Resolve only at acceptance. Workers consume the immutable task input, never
// the template's future head. Accepted-key replay and exact failed-task retries
// use the original accessible task snapshot even after recipe archival.
func resolvePromptSource(w http.ResponseWriter, r *http.Request, repo Repository, space, key, parentID string, in *Input, source *promptSourceInput) bool {
	if source == nil {
		return true
	}
	if !prompts.ID.MatchString(source.TemplateID) || !prompts.ID.MatchString(source.VersionID) {
		promptError(w, prompts.ErrInput)
		return false
	}
	store, ok := repo.(*FileStore)
	if !ok || store.prompts == nil {
		promptError(w, prompts.ErrNotFound)
		return false
	}
	store.mu.Lock()
	var original *Task
	for _, t := range store.data.Tasks {
		if t.SpaceID == space && ((t.Key == key && t.RetryOf == parentID) || (parentID != "" && t.ID == parentID)) {
			copy := t
			original = &copy
			if t.Key == key {
				break
			}
		}
	}
	store.mu.Unlock()
	if original != nil && original.PromptSource != nil {
		old := original.PromptSource
		if old.TemplateID == source.TemplateID && old.VersionID == source.VersionID && reflect.DeepEqual(old.Variables, canonicalVariables(source.Variables)) && original.Prompt == in.Prompt && original.Kind == in.Kind && original.Ratio == in.Ratio && original.Style == in.Style {
			copy := *old
			in.PromptSource = &copy
			return true
		}
	}
	a := promptActor(r, space)
	current, e := store.prompts.Get(r.Context(), a, source.TemplateID)
	if e != nil {
		promptError(w, e)
		return false
	}
	if current.Template.Status != "active" {
		promptError(w, prompts.ErrArchived)
		return false
	}
	version, e := store.prompts.Version(r.Context(), a, source.TemplateID, source.VersionID)
	if e != nil {
		promptError(w, e)
		return false
	}
	result, e := prompts.Compile(version.Recipe, source.Variables)
	if e != nil {
		promptError(w, e)
		return false
	}
	compiled := result.Text
	edited := in.Prompt != compiled
	if edited && !source.Edited {
		promptError(w, prompts.ErrInput)
		return false
	}
	in.PromptSource = &prompts.Source{TemplateID: source.TemplateID, VersionID: source.VersionID, CompiledSHA256: prompts.TextSHA(compiled), EffectiveSHA256: prompts.TextSHA(in.Prompt), Edited: edited, Variables: canonicalVariables(source.Variables)}
	return true
}

func canonicalVariables(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := map[string]string{}
	for k, v := range values {
		out[k] = v
	}
	return out
}
