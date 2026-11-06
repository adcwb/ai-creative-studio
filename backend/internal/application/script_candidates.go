package application

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/projects"
)

type ScriptAdoptionInput struct {
	actor           string
	ExpectedVersion int     `json:"expected_version"`
	ReviewedChanges bool    `json:"reviewed_changes,omitempty"`
	Script          *Script `json:"script,omitempty"`
	Reason          string  `json:"reason,omitempty"`
}

// Replays are receipts, not permission for a new execution. Current membership
// is checked by HTTP middleware; a later disabled model must not hide acceptance.
func (s *FileStore) replayScriptCandidate(space, project, key string, in ScriptGenerationInput) (ScriptGeneration, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, e := s.projectLocked(space, project); e != nil {
		return ScriptGeneration{}, false, e
	}
	receipt, found, e := s.projectReceiptLocked(space, key, "generate", project, in)
	if e != nil || !found {
		return ScriptGeneration{}, false, e
	}
	g, ok := s.data.ScriptGenerations[receipt.GenerationID]
	if !ok || g.SpaceID != space || g.ProjectID != project {
		return ScriptGeneration{}, false, ErrNotFound
	}
	return scriptGenerationSummary(g), true, nil
}

// Candidate bodies are read on demand, never in project polling or list rows.
func (s *FileStore) GetScriptGeneration(space, project, id string) (ScriptGeneration, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, e := s.projectLocked(space, project); e != nil {
		return ScriptGeneration{}, e
	}
	g, ok := s.data.ScriptGenerations[id]
	if !ok || g.SpaceID != space || g.ProjectID != project {
		return ScriptGeneration{}, ErrNotFound
	}
	return cloneScriptGeneration(g), nil
}

// AdoptScriptCandidate is an artistic decision, not another provider call. The
// candidate and its original source stay immutable, including after later edits.
func (s *FileStore) AdoptScriptCandidate(space, project, id, key string, in ScriptAdoptionInput) (Project, error) {
	if in.ExpectedVersion < 1 || !utf8.ValidString(in.Reason) || utf8.RuneCountInString(in.Reason) > 2000 {
		return Project{}, projectInputError("请核对当前版本与采纳说明")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p, e := s.projectLocked(space, project)
	if e != nil {
		return Project{}, e
	}
	receipt, replay, e := s.projectReceiptLocked(space, key, "adopt-script:"+id, project, in)
	if e != nil {
		return Project{}, e
	}
	if replay {
		return s.projectViewLocked(p), nil
	}
	g, ok := s.data.ScriptGenerations[id]
	if !ok || g.SpaceID != space || g.ProjectID != project {
		return Project{}, ErrNotFound
	}
	if !g.CandidateOnly || g.Status != "succeeded" || g.Candidate == nil || g.Adoption != nil {
		return Project{}, ErrConflict
	}
	if p.Version != in.ExpectedVersion || (p.Version != g.BaseVersion && !in.ReviewedChanges) {
		return Project{}, ErrVersionConflict
	}
	if s.generationActiveLocked(p) {
		return Project{}, ErrGenerationActive
	}
	chosen := cloneScript(*g.Candidate)
	if in.Script != nil {
		chosen = cloneScript(*in.Script)
	}
	chosen, e = NormalizeScript(chosen)
	if e != nil {
		return Project{}, e
	}
	if p.StructureVersion == 2 && chosen.StructureVersion != 2 {
		return Project{}, projectInputError("采纳稿需保留稳定角色与场次标识")
	}
	if p.StructureVersion == 0 && chosen.StructureVersion != 0 {
		return Project{}, ErrConflict
	}
	if p.SeriesEpisodeID != "" {
		if e = singleEpisodeScript(&chosen); e != nil {
			return Project{}, e
		}
	}
	p.Script = &chosen
	p.ScriptProvider = g.Provider
	p.ScriptBrief = g.SourceBrief
	p.ScriptVersion++
	p.Version++
	p.UpdatedAt = time.Now().UTC()
	raw, _ := json.Marshal(chosen)
	digest := sha256.Sum256(raw)
	g = cloneScriptGeneration(g)
	actor := in.actor
	if actor == "" {
		actor = "demo-user"
	}
	g.Adoption = &projects.ScriptAdoption{Actor: actor, GenerationID: id, ProjectVersion: p.Version, ScriptVersion: p.ScriptVersion, SHA256: hex.EncodeToString(digest[:]), Edited: !reflect.DeepEqual(chosen, *g.Candidate), Reason: strings.TrimSpace(in.Reason), CreatedAt: p.UpdatedAt}
	d := s.clone()
	d.Projects[p.ID] = cloneProject(p)
	d.ScriptGenerations[id] = g
	d.ProjectReceipts[projectReceiptKey(space, key)] = receipt
	if e = s.commit(d); e != nil {
		return Project{}, e
	}
	return s.projectViewLocked(p), nil
}

func registerScriptCandidateRoutes(mux *http.ServeMux, s *FileStore) {
	base := "/api/v1/projects/{id}/generations/{generation}"
	mux.HandleFunc("GET "+base, func(w http.ResponseWriter, r *http.Request) {
		space, ok := spaceID(w, r)
		if !ok {
			return
		}
		g, e := s.GetScriptGeneration(space, r.PathValue("id"), r.PathValue("generation"))
		if e != nil {
			storeError(w, e)
			return
		}
		respond(w, 200, g)
	})
	mux.HandleFunc("POST "+base+"/adoptions", func(w http.ResponseWriter, r *http.Request) {
		space, key, ok := projectWriteScope(w, r)
		if !ok {
			return
		}
		var in ScriptAdoptionInput
		if !decodeProject(w, r, &in) {
			return
		}
		in.actor = seriesActorFor(r, space).User
		p, e := s.AdoptScriptCandidate(space, r.PathValue("id"), r.PathValue("generation"), key, in)
		if e != nil {
			storeError(w, e)
			return
		}
		respond(w, 200, p)
	})
}
