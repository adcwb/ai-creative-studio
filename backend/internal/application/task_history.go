package application

import "net/http"

// History removal is permanent in the gallery. Keep execution receipts and
// source bytes so billing, idempotent submissions and saved assets remain valid.
func (s *FileStore) DeleteTaskHistory(space, id string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	task, ok := s.data.Tasks[id]
	if !ok || task.SpaceID != space {
		return 0, ErrNotFound
	}
	if task.Status != "succeeded" && task.Status != "failed" {
		return 0, ErrConflict
	}
	if task.HistoryDeleted {
		return 0, nil
	}
	task.HistoryDeleted = true
	d := s.clone()
	d.Tasks[id] = task
	if err := s.commit(d); err != nil {
		return 0, err
	}
	return 1, nil
}

func (s *FileStore) ClearTaskHistory(space string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.clone()
	count := 0
	for id, task := range d.Tasks {
		if task.SpaceID != space || task.HistoryDeleted || task.Status != "succeeded" && task.Status != "failed" {
			continue
		}
		task.HistoryDeleted = true
		d.Tasks[id] = task
		count++
	}
	if count == 0 {
		return 0, nil
	}
	if err := s.commit(d); err != nil {
		return 0, err
	}
	return count, nil
}

func registerTaskHistory(mux *http.ServeMux, s *FileStore) {
	remove := func(w http.ResponseWriter, r *http.Request) {
		space, ok := spaceID(w, r)
		if !ok {
			return
		}
		var count int
		var err error
		if id := r.PathValue("id"); id != "" {
			count, err = s.DeleteTaskHistory(space, id)
		} else {
			count, err = s.ClearTaskHistory(space)
		}
		if err != nil {
			storeError(w, err)
			return
		}
		respond(w, 200, map[string]int{"deleted": count})
	}
	mux.HandleFunc("DELETE /api/v1/tasks/{id}/history", remove)
	mux.HandleFunc("DELETE /api/v1/tasks/history", remove)
}
