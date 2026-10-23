package studio

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"time"
)

type ProjectRepository interface {
	StoryboardRepository
	CreateProject(string, string, ProjectInput) (Project, bool, error)
	UpdateProject(string, string, string, ProjectUpdate) (Project, error)
	GetProject(string, string) (Project, error)
	ListProjects(string) []Project
	CreateScriptGeneration(string, string, string, ScriptGenerationInput) (ScriptGeneration, bool, error)
	ListScriptGenerations(string, string) ([]ScriptGeneration, error)
	SaveProjectScript(string, string, string, ScriptSaveInput) (Project, error)
	NextScriptGeneration() (ScriptGeneration, bool, error)
	FinishScriptGeneration(ScriptGeneration, Script, error) error
}

func newProjectID() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

func projectReceiptKey(space, key string) string { return space + "\x00" + key }

func projectFingerprint(operation, id string, body any) (string, error) {
	b, err := json.Marshal(struct {
		Operation string
		ProjectID string
		Body      any
	}{operation, id, body})
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(b)
	return hex.EncodeToString(hash[:]), nil
}

func (s *FileStore) projectReceiptLocked(space, key, operation, id string, body any) (projectReceipt, bool, error) {
	fingerprint, err := projectFingerprint(operation, id, body)
	if err != nil {
		return projectReceipt{}, false, err
	}
	if receipt, ok := s.data.ProjectReceipts[projectReceiptKey(space, key)]; ok {
		if receipt.SpaceID != space || receipt.Key != key || receipt.Operation != operation || receipt.Fingerprint != fingerprint || (operation != "create" && receipt.ProjectID != id) {
			return projectReceipt{}, false, ErrIdempotencyConflict
		}
		return receipt, true, nil
	}
	return projectReceipt{SpaceID: space, Key: key, Operation: operation, ProjectID: id, Fingerprint: fingerprint}, false, nil
}

func (s *FileStore) projectLocked(space, id string) (Project, error) {
	project, ok := s.data.Projects[id]
	if !ok || project.SpaceID != space {
		return Project{}, ErrNotFound
	}
	return project, nil
}

func (s *FileStore) projectViewLocked(project Project) Project {
	project = cloneProject(project)
	project.LatestGeneration = nil
	if generation, ok := s.data.ScriptGenerations[project.LatestGenerationID]; ok && generation.ProjectID == project.ID && generation.SpaceID == project.SpaceID {
		project.LatestGeneration = &generation
	}
	project.LatestGenerationID = ""
	project.LatestStoryboardGeneration = nil
	if g, ok := s.data.StoryboardGenerations[project.LatestStoryboardGenerationID]; ok && g.ProjectID == project.ID && g.SpaceID == project.SpaceID {
		v := storyboardGenerationView(g)
		project.LatestStoryboardGeneration = &v
	}
	project.LatestStoryboardGenerationID = ""
	project.StoryboardStale = project.Storyboard != nil && project.Storyboard.SourceScriptVersion != project.ScriptVersion
	return project
}

func (s *FileStore) generationActiveLocked(project Project) bool {
	if g, ok := s.data.StoryboardGenerations[project.LatestStoryboardGenerationID]; ok && g.ProjectID == project.ID && g.SpaceID == project.SpaceID && (g.Status == "queued" || g.Status == "running") {
		return true
	}
	generation, ok := s.data.ScriptGenerations[project.LatestGenerationID]
	return ok && generation.ProjectID == project.ID && generation.SpaceID == project.SpaceID && (generation.Status == "queued" || generation.Status == "running")
}

func (s *FileStore) CreateProject(space, key string, in ProjectInput) (Project, bool, error) {
	in, err := NormalizeProjectInput(in)
	if err != nil {
		return Project{}, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	receipt, replay, err := s.projectReceiptLocked(space, key, "create", "", in)
	if err != nil {
		return Project{}, false, err
	}
	if replay {
		project, err := s.projectLocked(space, receipt.ProjectID)
		if err != nil {
			return Project{}, false, err
		}
		return s.projectViewLocked(project), false, nil
	}
	id, err := newProjectID()
	if err != nil {
		return Project{}, false, err
	}
	now := time.Now().UTC()
	project := Project{ID: id, SpaceID: space, Title: in.Title, Brief: in.Brief, Version: 1, CreatedAt: now, UpdatedAt: now}
	receipt.ProjectID = id
	d := s.clone()
	d.Projects[id] = project
	d.ProjectReceipts[projectReceiptKey(space, key)] = receipt
	if err := s.commit(d); err != nil {
		return Project{}, false, err
	}
	return s.projectViewLocked(project), true, nil
}

func (s *FileStore) GetProject(space, id string) (Project, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	project, err := s.projectLocked(space, id)
	if err != nil {
		return Project{}, err
	}
	return s.projectViewLocked(project), nil
}

func (s *FileStore) ListProjects(space string) []Project {
	s.mu.Lock()
	defer s.mu.Unlock()
	projects := []Project{}
	for _, project := range s.data.Projects {
		if project.SpaceID == space {
			projects = append(projects, s.projectViewLocked(project))
		}
	}
	sort.Slice(projects, func(i, j int) bool {
		if projects[i].UpdatedAt.Equal(projects[j].UpdatedAt) {
			return projects[i].ID < projects[j].ID
		}
		return projects[i].UpdatedAt.After(projects[j].UpdatedAt)
	})
	return projects
}

func (s *FileStore) UpdateProject(space, id, key string, in ProjectUpdate) (Project, error) {
	input, err := NormalizeProjectInput(ProjectInput{Title: in.Title, Brief: in.Brief})
	if err != nil {
		return Project{}, err
	}
	if in.ExpectedVersion < 1 {
		return Project{}, projectInputError("expected_version 必须为正整数")
	}
	in.Title, in.Brief = input.Title, input.Brief
	s.mu.Lock()
	defer s.mu.Unlock()
	project, err := s.projectLocked(space, id)
	if err != nil {
		return Project{}, err
	}
	receipt, replay, err := s.projectReceiptLocked(space, key, "update", id, in)
	if err != nil {
		return Project{}, err
	}
	if replay {
		return s.projectViewLocked(project), nil
	}
	if project.Version != in.ExpectedVersion {
		return Project{}, ErrVersionConflict
	}
	if s.generationActiveLocked(project) {
		return Project{}, ErrGenerationActive
	}
	project.Title, project.Brief = in.Title, in.Brief
	project.Version++
	project.UpdatedAt = time.Now().UTC()
	d := s.clone()
	d.Projects[id] = cloneProject(project)
	d.ProjectReceipts[projectReceiptKey(space, key)] = receipt
	if err := s.commit(d); err != nil {
		return Project{}, err
	}
	return s.projectViewLocked(project), nil
}

func (s *FileStore) CreateScriptGeneration(space, id, key string, in ScriptGenerationInput) (ScriptGeneration, bool, error) {
	if in.ExpectedVersion < 1 {
		return ScriptGeneration{}, false, projectInputError("expected_version 必须为正整数")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	project, err := s.projectLocked(space, id)
	if err != nil {
		return ScriptGeneration{}, false, err
	}
	receipt, replay, err := s.projectReceiptLocked(space, key, "generate", id, in)
	if err != nil {
		return ScriptGeneration{}, false, err
	}
	if replay {
		generation, ok := s.data.ScriptGenerations[receipt.GenerationID]
		if !ok || generation.SpaceID != space || generation.ProjectID != id {
			return ScriptGeneration{}, false, ErrNotFound
		}
		return generation, false, nil
	}
	if project.Version != in.ExpectedVersion {
		return ScriptGeneration{}, false, ErrVersionConflict
	}
	if s.generationActiveLocked(project) {
		return ScriptGeneration{}, false, ErrGenerationActive
	}
	if err := s.guardModelRequestLocked(in.ModelRequest, in.ExpectedProvider, in.ExpectedModel); err != nil {
		return ScriptGeneration{}, false, err
	}
	generationID, err := newProjectID()
	if err != nil {
		return ScriptGeneration{}, false, err
	}
	now := time.Now().UTC()
	generation := ScriptGeneration{TraceParent: in.traceParent, RuntimeSelection: cloneRuntimeSelection(in.selection), ID: generationID, SpaceID: space, ProjectID: id, SourceTitle: project.Title, SourceBrief: project.Brief, BaseVersion: project.Version, Provider: s.creativeGenerationProvider("mock-script"), Model: s.creativeGenerationModel(), Status: "queued", CreatedAt: now, UpdatedAt: now}
	if in.selection != nil {
		generation.Provider, generation.Model = in.selectedCaps.Provider, in.selectedCaps.Model
	}
	project.LatestGenerationID = generation.ID
	project.UpdatedAt = now
	receipt.GenerationID = generation.ID
	d := s.clone()
	d.Projects[id] = cloneProject(project)
	d.ScriptGenerations[generation.ID] = generation
	d.ProjectReceipts[projectReceiptKey(space, key)] = receipt
	if err := s.commit(d); err != nil {
		return ScriptGeneration{}, false, err
	}
	return generation, true, nil
}

func (s *FileStore) ListScriptGenerations(space, id string) ([]ScriptGeneration, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.projectLocked(space, id); err != nil {
		return nil, err
	}
	generations := []ScriptGeneration{}
	for _, generation := range s.data.ScriptGenerations {
		if generation.SpaceID == space && generation.ProjectID == id {
			generations = append(generations, generation)
		}
	}
	sort.Slice(generations, func(i, j int) bool {
		if generations[i].CreatedAt.Equal(generations[j].CreatedAt) {
			return generations[i].ID < generations[j].ID
		}
		return generations[i].CreatedAt.After(generations[j].CreatedAt)
	})
	return generations, nil
}

func (s *FileStore) SaveProjectScript(space, id, key string, in ScriptSaveInput) (Project, error) {
	var err error
	in.Script, err = NormalizeScript(in.Script)
	if err != nil {
		return Project{}, err
	}
	if in.ExpectedVersion < 1 {
		return Project{}, projectInputError("expected_version 必须为正整数")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	project, err := s.projectLocked(space, id)
	if err != nil {
		return Project{}, err
	}
	receipt, replay, err := s.projectReceiptLocked(space, key, "save", id, in)
	if err != nil {
		return Project{}, err
	}
	if replay {
		return s.projectViewLocked(project), nil
	}
	if project.Version != in.ExpectedVersion {
		return Project{}, ErrVersionConflict
	}
	if s.generationActiveLocked(project) {
		return Project{}, ErrGenerationActive
	}
	if project.Script == nil {
		return Project{}, ErrScriptMissing
	}
	project.Script = &in.Script
	project.ScriptVersion++
	project.Version++
	project.UpdatedAt = time.Now().UTC()
	d := s.clone()
	d.Projects[id] = cloneProject(project)
	d.ProjectReceipts[projectReceiptKey(space, key)] = receipt
	if err := s.commit(d); err != nil {
		return Project{}, err
	}
	return s.projectViewLocked(project), nil
}

func (s *FileStore) NextScriptGeneration() (ScriptGeneration, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var next ScriptGeneration
	for _, generation := range s.data.ScriptGenerations {
		if generation.Status == "queued" && (next.ID == "" || generation.CreatedAt.Before(next.CreatedAt) || (generation.CreatedAt.Equal(next.CreatedAt) && generation.ID < next.ID)) {
			next = generation
		}
	}
	if next.ID == "" {
		return ScriptGeneration{}, false, nil
	}
	project, err := s.projectLocked(next.SpaceID, next.ProjectID)
	if err != nil || project.LatestGenerationID != next.ID || project.Version != next.BaseVersion {
		return ScriptGeneration{}, false, errors.New("invalid queued script generation linkage")
	}
	next.Status, next.Progress = "running", 35
	next.UpdatedAt = time.Now().UTC()
	project.UpdatedAt = next.UpdatedAt
	d := s.clone()
	d.ScriptGenerations[next.ID] = next
	d.Projects[project.ID] = cloneProject(project)
	if err := s.commit(d); err != nil {
		return ScriptGeneration{}, false, err
	}
	return next, true, nil
}

func (s *FileStore) FinishScriptGeneration(generation ScriptGeneration, script Script, runErr error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.finishScriptLocked(generation, script, runErr)
}
func (s *FileStore) finishScriptLocked(generation ScriptGeneration, script Script, runErr error) error {
	current, ok := s.data.ScriptGenerations[generation.ID]
	if !ok || current.SpaceID != generation.SpaceID || current.ProjectID != generation.ProjectID {
		return ErrNotFound
	}
	if current.Status != "running" && current.Status != "queued" {
		return ErrConflict
	}
	project, err := s.projectLocked(current.SpaceID, current.ProjectID)
	if err != nil {
		return err
	}
	if project.LatestGenerationID != current.ID || project.Version != current.BaseVersion {
		return ErrVersionConflict
	}
	if runErr == nil {
		script, err = NormalizeScript(script)
		if err != nil {
			runErr = &ScriptError{Code: "invalid_script_output"}
		}
	}
	current.Progress = 100
	current.UpdatedAt = time.Now().UTC()
	project.UpdatedAt = current.UpdatedAt
	if runErr != nil {
		current.Status = "failed"
		current.ErrorCode, current.Error = safeScriptFailure(runErr)
	} else {
		current.Status = "succeeded"
		project.Script = &script
		project.ScriptProvider = current.Provider
		project.ScriptVersion++
		project.ScriptBrief = current.SourceBrief
		project.Version++
	}
	d := s.clone()
	d.ScriptGenerations[current.ID] = current
	d.Projects[project.ID] = cloneProject(project)
	return s.commit(d)
}
