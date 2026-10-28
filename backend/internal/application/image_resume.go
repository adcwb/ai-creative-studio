package application

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"regexp"
	"time"

	"github.com/adcwb/ai-creative-studio/backend/internal/platform/jobqueue"
)

var upstreamImageIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,160}$`)

func (s *FileStore) SetImageUpstream(t Task, id string) error {
	if !upstreamImageIDPattern.MatchString(id) {
		return providerFailure("invalid_provider_response")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.data.Tasks[t.ID]
	if !ok || current.SpaceID != t.SpaceID {
		return ErrNotFound
	}
	if current.Status != "running" || current.Provider != t.Provider || current.Model != t.Model {
		return ErrConflict
	}
	if current.UpstreamID != "" && current.UpstreamID != id {
		return ErrConflict
	}
	current.UpstreamID = id
	current.UpdatedAt = time.Now().UTC()
	d := s.clone()
	d.Tasks[t.ID] = current
	return s.commit(d)
}
func (s *FileStore) ResumeImageTask(space, id, key string) (Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.data.Tasks[id]
	if !ok || t.SpaceID != space {
		return Task{}, ErrNotFound
	}
	rk := projectReceiptKey(space, key)
	if old, ok := s.data.ImageResumeReceipts[rk]; ok {
		if old != id {
			return Task{}, ErrIdempotencyConflict
		}
		return t, nil
	}
	// Preserve old-key replay even when no new resume receipts can be accepted.
	if len(s.data.ImageResumeReceipts) >= 1000 {
		return Task{}, ErrProviderNotReady
	}
	if t.Provider != "jimeng-images" || t.UpstreamID == "" || t.Status != "failed" {
		return Task{}, ErrConflict
	}
	if s.pg != nil {
		if t.RuntimeSelection == nil {
			return Task{}, ErrExecutionGovernance
		}
		request := imageExecutionRequest(t)
		request.UpstreamID = t.UpstreamID
		if err := s.execution.RecoveryReady(context.Background(), s.runtime, request); err != nil {
			return Task{}, err
		}
	} else if !s.capabilities.Ready || s.capabilities.Provider != t.Provider || s.capabilities.Model != t.Model {
		return Task{}, ErrProviderChanged
	}
	t.Status = "queued"
	t.Progress = 0
	t.Error = ""
	t.ErrorCode = ""
	t.UpdatedAt = time.Now().UTC()
	d := s.clone()
	d.Tasks[id] = t
	d.ImageResumeReceipts[rk] = id
	if s.pg != nil {
		payload, _ := json.Marshal(map[string]string{"task_id": id})
		s.pg.beforeCommit = func(ctx context.Context, tx *sql.Tx) error {
			return jobqueue.ResumeTx(ctx, tx, jobqueue.Job{TenantID: space, ID: id, Kind: "image_resume", IdempotencyKey: key, Payload: payload})
		}
		defer func() { s.pg.beforeCommit = nil }()
	}
	if err := s.commit(d); err != nil {
		return Task{}, err
	}
	return t, nil
}
func registerImageResumeRoute(mux *http.ServeMux, s *FileStore) {
	mux.HandleFunc("POST /api/v1/tasks/{id}/resume", func(w http.ResponseWriter, r *http.Request) {
		space, key, ok := projectWriteScope(w, r)
		if !ok {
			return
		}
		var in struct{}
		if !decodeProject(w, r, &in) {
			return
		}
		t, err := s.ResumeImageTask(space, r.PathValue("id"), key)
		if err != nil {
			storeError(w, err)
			return
		}
		respond(w, 200, t.Public())
	})
}
