package studio

import (
	"net/http"
	"reflect"
	"time"
)

// ProjectRevision is an immutable pre-change snapshot. Restoring creates a new
// project/script revision rather than rewinding optimistic concurrency clocks.
type ProjectRevision struct {
	ScriptProvider string      `json:"script_provider,omitempty"`
	ID             string      `json:"id"`
	Kind           string      `json:"kind"`
	CreatedAt      time.Time   `json:"created_at"`
	ScriptVersion  int         `json:"script_version"`
	Script         *Script     `json:"script,omitempty"`
	Storyboard     *Storyboard `json:"storyboard,omitempty"`
	ScriptBrief    string      `json:"script_brief,omitempty"`
}

func cloneRevision(r ProjectRevision) ProjectRevision {
	if r.Script != nil {
		v := cloneScript(*r.Script)
		r.Script = &v
	}
	if r.Storyboard != nil {
		v := *r.Storyboard
		v.Shots = append([]StoryboardShot(nil), v.Shots...)
		r.Storyboard = &v
	}
	return r
}
func cloneHistory(src map[string][]ProjectRevision) map[string][]ProjectRevision {
	out := map[string][]ProjectRevision{}
	for id, list := range src {
		out[id] = make([]ProjectRevision, len(list))
		for i, r := range list {
			out[id][i] = cloneRevision(r)
		}
	}
	return out
}
func (s *FileStore) captureHistory(d *snapshot) error {
	if d.History == nil {
		d.History = cloneHistory(s.data.History)
	}
	for id, p := range d.Projects {
		old, ok := s.data.Projects[id]
		if !ok {
			continue
		}
		for _, kind := range []string{"script", "storyboard"} {
			changed := kind == "script" && old.Script != nil && (!reflect.DeepEqual(old.Script, p.Script) || old.ScriptProvider != p.ScriptProvider) || kind == "storyboard" && old.Storyboard != nil && !reflect.DeepEqual(old.Storyboard, p.Storyboard)
			if !changed {
				continue
			}
			rid, err := newProjectID()
			if err != nil {
				return err
			}
			r := ProjectRevision{ID: rid, Kind: kind, CreatedAt: time.Now().UTC(), ScriptVersion: old.ScriptVersion}
			if kind == "script" {
				r.Script = old.Script
				r.ScriptBrief = old.ScriptBrief
				r.ScriptProvider = old.ScriptProvider
			} else {
				r.Storyboard = old.Storyboard
			}
			d.History[id] = append(d.History[id], cloneRevision(r))
		}
	}
	return nil
}
func (s *FileStore) ProjectHistory(space, id string) ([]ProjectRevision, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.projectLocked(space, id); err != nil {
		return nil, err
	}
	out := []ProjectRevision{}
	for i := len(s.data.History[id]) - 1; i >= 0; i-- {
		out = append(out, cloneRevision(s.data.History[id][i]))
	}
	return out, nil
}
func (s *FileStore) RestoreHistory(space, id, rid, key string, in ScriptGenerationInput) (Project, error) {
	if in.ExpectedVersion < 1 {
		return Project{}, projectInputError("expected_version 必须为正整数")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p, err := s.projectLocked(space, id)
	if err != nil {
		return Project{}, err
	}
	receipt, replay, err := s.projectReceiptLocked(space, key, "restore-"+rid, id, in)
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
	var revision *ProjectRevision
	for _, r := range s.data.History[id] {
		if r.ID == rid {
			v := cloneRevision(r)
			revision = &v
			break
		}
	}
	if revision == nil {
		return Project{}, ErrNotFound
	}
	if revision.Kind == "script" {
		p.Script = revision.Script
		p.ScriptBrief = revision.ScriptBrief
		p.ScriptProvider = revision.ScriptProvider
		p.ScriptVersion++
	} else {
		p.Storyboard = revision.Storyboard
	}
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
func registerHistoryRoutes(mux *http.ServeMux, s *FileStore) {
	mux.HandleFunc("GET /api/v1/projects/{id}/history", func(w http.ResponseWriter, r *http.Request) {
		space, ok := spaceID(w, r)
		if !ok {
			return
		}
		list, err := s.ProjectHistory(space, r.PathValue("id"))
		if err != nil {
			storeError(w, err)
			return
		}
		respond(w, 200, list)
	})
	mux.HandleFunc("POST /api/v1/projects/{id}/history/{revision}/restore", func(w http.ResponseWriter, r *http.Request) {
		space, key, ok := projectWriteScope(w, r)
		if !ok {
			return
		}
		var in ScriptGenerationInput
		if !decodeProject(w, r, &in) {
			return
		}
		p, err := s.RestoreHistory(space, r.PathValue("id"), r.PathValue("revision"), key, in)
		if err != nil {
			storeError(w, err)
			return
		}
		respond(w, 200, p)
	})
}
