package application

import (
	"net/http"
	"reflect"
	"time"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/projects"
)

type StructureUpgradeInput struct {
	ExpectedVersion int `json:"expected_version"`
}

func structureNamespace(p Project) string { return p.SpaceID + ":" + p.ID }

// The source revision is authoritative. A missing or inconsistent historical
// script cannot be replaced by today's scene order, even when indexes fit.
func (s *FileStore) structureSourceLocked(p Project, version int) (*Script, error) {
	var source *Script
	if p.Script != nil && p.ScriptVersion == version {
		v := cloneScript(*p.Script)
		source = &v
	}
	for _, r := range s.data.History[p.ID] {
		if r.Kind != "script" || r.ScriptVersion != version || r.Script == nil {
			continue
		}
		if source != nil && !reflect.DeepEqual(*source, *r.Script) {
			return nil, projectInputError("来源剧本版本存在歧义，未更改原项目，请先核对历史")
		}
		v := cloneScript(*r.Script)
		source = &v
	}
	if source == nil {
		return nil, projectInputError("找不到分镜的来源剧本，未更改原项目，请先核对历史")
	}
	return source, nil
}

func compileProjectStructure(p *Project, script Script, version int) (Script, error) {
	v, mapping, err := projects.CompileStructure(script, structureNamespace(*p), version)
	if err != nil {
		return Script{}, err
	}
	for _, old := range p.StructureMappings {
		if old.SourceScriptVersion == mapping.SourceScriptVersion {
			if !reflect.DeepEqual(old, mapping) {
				return Script{}, projectInputError("保存的稳定关联与来源版本不一致，未更改原项目")
			}
			return v, nil
		}
	}
	p.StructureMappings = append(p.StructureMappings, mapping)
	return v, nil
}

func (s *FileStore) bindProjectStoryboardLocked(p *Project, board Storyboard) (Storyboard, error) {
	if board.StructureVersion != 0 && board.StructureVersion != 2 {
		return Storyboard{}, projectInputError("分镜结构版本不受支持，请使用兼容版本的编辑器")
	}
	ids := append([]string(nil), board.SourceSceneIDs...)
	if board.StructureVersion != 2 {
		if len(board.SourceSceneIDs) != 0 {
			return Storyboard{}, projectInputError("旧分镜包含无法核验的场次映射")
		}
		if board.SourceScriptVersion > 0 {
			source, err := s.structureSourceLocked(*p, board.SourceScriptVersion)
			if err != nil {
				return Storyboard{}, err
			}
			stable := *source
			if stable.StructureVersion != 2 {
				stable, err = compileProjectStructure(p, *source, board.SourceScriptVersion)
				if err != nil {
					return Storyboard{}, err
				}
			}
			ids = projects.SceneIDs(stable)
		}
	}
	shots, err := projects.BindStoryboard(board.Shots, ids)
	if err != nil {
		return Storyboard{}, err
	}
	board.Shots, board.SourceSceneIDs, board.StructureVersion = shots, ids, 2
	return board, nil
}

// An explicit CAS write upgrades identities and the pinned storyboard together.
// It never dispatches a model, rewrites old history or changes media/exports.
func (s *FileStore) UpgradeProjectStructure(space, id, key string, in StructureUpgradeInput) (Project, error) {
	if in.ExpectedVersion < 1 {
		return Project{}, projectInputError("expected_version 必须为正整数")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pg != nil && s.pg.err != nil {
		return Project{}, s.pg.err
	}
	p, err := s.projectLocked(space, id)
	if err != nil {
		return Project{}, err
	}
	receipt, replay, err := s.projectReceiptLocked(space, key, "structure-upgrade", id, in)
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
	p = cloneProject(p)
	if p.StructureVersion != 2 {
		// Bind before incrementing ScriptVersion: this source still describes the
		// legacy order. Stale boards keep their original revision and identity list.
		if p.Storyboard != nil {
			board, err := s.bindProjectStoryboardLocked(&p, *p.Storyboard)
			if err != nil {
				return Project{}, err
			}
			p.Storyboard = &board
		}
		if p.Script != nil {
			stable, err := compileProjectStructure(&p, *p.Script, p.ScriptVersion)
			if err != nil {
				return Project{}, err
			}
			if p.Storyboard != nil && p.Storyboard.SourceScriptVersion == p.ScriptVersion {
				p.Storyboard.SourceScriptVersion++
			}
			p.Script, p.ScriptVersion = &stable, p.ScriptVersion+1
		}
		p.StructureVersion = 2
		p.Version++
		p.UpdatedAt = time.Now().UTC()
	}
	d := s.clone()
	d.Projects[id] = p
	d.ProjectReceipts[projectReceiptKey(space, key)] = receipt
	if err = s.commit(d); err != nil {
		return Project{}, err
	}
	return s.projectViewLocked(p), nil
}

// A model produces content, not authoritative project identities. Each accepted
// generation gets a deterministic namespace; retries cannot create new IDs.
func generatedStructure(p Project, script Script, generationID string) (Script, error) {
	v, _, err := projects.CompileStructure(script, structureNamespace(p)+":"+generationID, p.ScriptVersion+1)
	return v, err
}

func registerStructureRoutes(mux *http.ServeMux, s *FileStore) {
	mux.HandleFunc("POST /api/v1/projects/{id}/structure-upgrades", func(w http.ResponseWriter, r *http.Request) {
		space, key, ok := projectWriteScope(w, r)
		if !ok {
			return
		}
		var in StructureUpgradeInput
		if !decodeProject(w, r, &in) {
			return
		}
		p, err := s.UpgradeProjectStructure(space, r.PathValue("id"), key, in)
		if err != nil {
			storeError(w, err)
			return
		}
		respond(w, 200, p)
	})
}

// Match a stored legacy revision to its original mapping, instead of generating
// new identities when the user restores it. compileProjectStructure checks the
// original normalized content digest before returning the deterministic upgrade.
func (s *FileStore) restoreStructureLocked(p *Project, revision *ProjectRevision) error {
	if p.StructureVersion != 2 {
		return nil
	}
	if revision.Kind == "script" && revision.Script != nil {
		v, err := NormalizeScript(*revision.Script)
		if err == nil && v.StructureVersion != 2 {
			v, err = compileProjectStructure(p, v, revision.ScriptVersion)
		}
		if err != nil {
			return err
		}
		revision.Script = &v
	}
	if revision.Kind == "storyboard" && revision.Storyboard != nil {
		b, err := s.bindProjectStoryboardLocked(p, *revision.Storyboard)
		if err != nil {
			return err
		}
		revision.Storyboard = &b
	}
	return nil
}
