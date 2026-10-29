package application

import (
	"errors"
	"sort"
	"time"
)

type StoryboardRepository interface {
	CreateStoryboardGeneration(string, string, string, ScriptGenerationInput) (StoryboardGeneration, bool, error)
	ListStoryboardGenerations(string, string) ([]StoryboardGeneration, error)
	SaveProjectStoryboard(string, string, string, StoryboardSaveInput) (Project, error)
	NextStoryboardGeneration() (StoryboardGeneration, bool, error)
	FinishStoryboardGeneration(StoryboardGeneration, []StoryboardShot, error) error
}

func (s *FileStore) CreateStoryboardGeneration(space, id, key string, in ScriptGenerationInput) (StoryboardGeneration, bool, error) {
	if in.ExpectedVersion < 1 {
		return StoryboardGeneration{}, false, projectInputError("expected_version 必须为正整数")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p, err := s.projectLocked(space, id)
	if err != nil {
		return StoryboardGeneration{}, false, err
	}
	receipt, replay, err := s.projectReceiptLocked(space, key, "storyboard-generate", id, in)
	if err != nil {
		return StoryboardGeneration{}, false, err
	}
	if replay {
		g, ok := s.data.StoryboardGenerations[receipt.GenerationID]
		if !ok || g.ProjectID != id || g.SpaceID != space {
			return StoryboardGeneration{}, false, ErrNotFound
		}
		return storyboardGenerationView(g), false, nil
	}
	if p.Version != in.ExpectedVersion {
		return StoryboardGeneration{}, false, ErrVersionConflict
	}
	if s.generationActiveLocked(p) {
		return StoryboardGeneration{}, false, ErrGenerationActive
	}
	if p.Script == nil {
		return StoryboardGeneration{}, false, ErrScriptMissing
	}
	if err := s.guardModelRequestLocked(in.ModelRequest, in.ExpectedProvider, in.ExpectedModel); err != nil {
		return StoryboardGeneration{}, false, err
	}
	gid, err := newProjectID()
	if err != nil {
		return StoryboardGeneration{}, false, err
	}
	now := time.Now().UTC()
	script := cloneScript(*p.Script)
	g := StoryboardGeneration{TraceParent: in.traceParent, RuntimeSelection: cloneRuntimeSelection(in.selection), ID: gid, SpaceID: space, ProjectID: id, BaseVersion: p.Version, SourceScriptVersion: p.ScriptVersion, SourceScript: &script, Provider: s.creativeGenerationProvider("mock-storyboard"), Model: s.creativeGenerationModel(), Status: "queued", CreatedAt: now, UpdatedAt: now}
	if in.selection != nil {
		g.Provider, g.Model = in.selectedCaps.Provider, in.selectedCaps.Model
	}
	p.LatestStoryboardGenerationID = gid
	p.UpdatedAt = now
	receipt.GenerationID = gid
	d := s.clone()
	d.Projects[id] = cloneProject(p)
	d.StoryboardGenerations[gid] = g
	d.ProjectReceipts[projectReceiptKey(space, key)] = receipt
	if err := s.commit(d); err != nil {
		return StoryboardGeneration{}, false, err
	}
	return storyboardGenerationView(g), true, nil
}
func (s *FileStore) ListStoryboardGenerations(space, id string) ([]StoryboardGeneration, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.projectLocked(space, id); err != nil {
		return nil, err
	}
	list := []StoryboardGeneration{}
	for _, g := range s.data.StoryboardGenerations {
		if g.SpaceID == space && g.ProjectID == id {
			list = append(list, storyboardGenerationView(g))
		}
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].CreatedAt.Equal(list[j].CreatedAt) {
			return list[i].ID < list[j].ID
		}
		return list[i].CreatedAt.After(list[j].CreatedAt)
	})
	return list, nil
}
func (s *FileStore) SaveProjectStoryboard(space, id, key string, in StoryboardSaveInput) (Project, error) {
	var err error
	in.Shots, err = NormalizeStoryboardShots(in.Shots)
	if err != nil {
		return Project{}, err
	}
	if in.ExpectedVersion < 1 {
		return Project{}, projectInputError("expected_version 必须为正整数")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p, err := s.projectLocked(space, id)
	if err != nil {
		return Project{}, err
	}
	receipt, replay, err := s.projectReceiptLocked(space, key, "storyboard-save", id, in)
	if err != nil {
		return Project{}, err
	}
	if replay {
		return s.projectViewLocked(p), nil
	}
	if p.Version != in.ExpectedVersion {
		return Project{}, ErrVersionConflict
	}
	if s.generationActiveLocked(p) {
		return Project{}, ErrGenerationActive
	}
	// Manual shots may be created before a script. Scene zero is independent.
	if p.Storyboard == nil {
		p.Storyboard = &Storyboard{Provider: "manual", SourceScriptVersion: p.ScriptVersion}
	}
	for _, shot := range in.Shots {
		if shot.SceneIndex == 0 {
			continue
		}
		if p.Storyboard.SourceScriptVersion == p.ScriptVersion {
			if p.Script == nil || shot.SceneIndex > len(p.Script.Scenes) {
				return Project{}, projectInputError("镜头引用了不存在的场景")
			}
		} else {
			found := false
			for _, old := range p.Storyboard.Shots {
				if old.SceneIndex == shot.SceneIndex {
					found = true
				}
			}
			if !found {
				return Project{}, projectInputError("过期分镜不能新增旧剧本场景引用，请使用独立镜头或重新生成")
			}
		}
	}
	plan := *p.Storyboard
	plan.Shots = in.Shots
	p.Storyboard = &plan
	p.Version++
	p.UpdatedAt = time.Now().UTC()
	d := s.clone()
	d.Projects[id] = cloneProject(p)
	d.ProjectReceipts[projectReceiptKey(space, key)] = receipt
	if err := s.commit(d); err != nil {
		return Project{}, err
	}
	return s.projectViewLocked(p), nil
}
func (s *FileStore) NextStoryboardGeneration() (StoryboardGeneration, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var next StoryboardGeneration
	for _, g := range s.data.StoryboardGenerations {
		if g.Status == "queued" && (next.ID == "" || g.CreatedAt.Before(next.CreatedAt) || (g.CreatedAt.Equal(next.CreatedAt) && g.ID < next.ID)) {
			next = g
		}
	}
	if next.ID == "" {
		return StoryboardGeneration{}, false, nil
	}
	p, err := s.projectLocked(next.SpaceID, next.ProjectID)
	if err != nil || p.LatestStoryboardGenerationID != next.ID || p.Version != next.BaseVersion || p.ScriptVersion != next.SourceScriptVersion || next.SourceScript == nil {
		return StoryboardGeneration{}, false, errors.New("invalid queued storyboard linkage")
	}
	next.Status = "running"
	next.Progress = 35
	next.UpdatedAt = time.Now().UTC()
	p.UpdatedAt = next.UpdatedAt
	d := s.clone()
	d.StoryboardGenerations[next.ID] = cloneStoryboardGeneration(next)
	d.Projects[p.ID] = cloneProject(p)
	if err := s.commit(d); err != nil {
		return StoryboardGeneration{}, false, err
	}
	return cloneStoryboardGeneration(next), true, nil
}
func (s *FileStore) FinishStoryboardGeneration(g StoryboardGeneration, shots []StoryboardShot, runErr error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.finishStoryboardLocked(g, shots, runErr)
}
func (s *FileStore) finishStoryboardLocked(g StoryboardGeneration, shots []StoryboardShot, runErr error) error {
	current, ok := s.data.StoryboardGenerations[g.ID]
	if !ok || current.SpaceID != g.SpaceID || current.ProjectID != g.ProjectID {
		return ErrNotFound
	}
	if current.Status != "running" && current.Status != "queued" {
		return ErrConflict
	}
	p, err := s.projectLocked(current.SpaceID, current.ProjectID)
	if err != nil {
		return err
	}
	if p.LatestStoryboardGenerationID != current.ID || p.Version != current.BaseVersion || p.ScriptVersion != current.SourceScriptVersion {
		return ErrVersionConflict
	}
	if runErr == nil {
		shots, err = NormalizeStoryboardShots(shots)
		if err == nil {
			for _, shot := range shots {
				if current.SourceScript == nil || shot.SceneIndex > len(current.SourceScript.Scenes) {
					err = projectInputError("镜头引用了不存在的场景")
					break
				}
			}
		}
		if err != nil {
			runErr = &StoryboardError{Code: "invalid_storyboard_output"}
		}
	}
	current.Progress = 100
	current.UpdatedAt = time.Now().UTC()
	p.UpdatedAt = current.UpdatedAt
	if runErr != nil {
		current.Status = "failed"
		current.ErrorCode, current.Error = safeStoryboardFailure(runErr)
	} else {
		current.Status = "succeeded"
		p.Storyboard = &Storyboard{Provider: current.Provider, SourceScriptVersion: current.SourceScriptVersion, Shots: shots}
		p.Version++
	}
	d := s.clone()
	d.StoryboardGenerations[current.ID] = cloneStoryboardGeneration(current)
	d.Projects[p.ID] = cloneProject(p)
	return s.commit(d)
}
