package studio

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"syscall"
	"time"
)

type snapshot struct {
	Version int              `json:"version"`
	Tasks   map[string]Task  `json:"tasks"`
	Assets  map[string]Asset `json:"assets"`
}

// Repository is the boundary for a future transactional PostgreSQL implementation.
type Repository interface {
	Create(string, string, Input) (Task, bool, error)
	Retry(string, string, string, Input) (Task, bool, error)
	Get(string, string) (Task, error)
	List(string) []Task
	Assets(string) []Asset
	SaveAsset(string, string) (Asset, error)
	Next() (Task, bool, error)
	Finish(Task, string, error) error
}
type FileStore struct {
	mu   sync.Mutex
	path string
	data snapshot
	lock *os.File
}

func OpenStore(dir string) (*FileStore, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	lock, err := os.OpenFile(filepath.Join(dir, "store.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lock.Close()
		return nil, fmt.Errorf("data directory already in use: %w", err)
	}
	s := &FileStore{path: filepath.Join(dir, "state.json"), lock: lock, data: snapshot{Version: 1, Tasks: map[string]Task{}, Assets: map[string]Asset{}}}
	b, err := os.ReadFile(s.path)
	if err == nil {
		err = json.Unmarshal(b, &s.data)
	} else if os.IsNotExist(err) {
		err = nil
	}
	if err != nil {
		s.Close()
		return nil, fmt.Errorf("read state (refusing to reset): %w", err)
	}
	if s.data.Version != 1 || s.data.Tasks == nil || s.data.Assets == nil {
		s.Close()
		return nil, fmt.Errorf("unsupported or invalid state")
	}
	for id, t := range s.data.Tasks {
		t.Input = withDefaults(t.Input)
		if t.Status == "running" {
			t.Status = "queued"
			t.Progress = 0
			t.UpdatedAt = time.Now().UTC()
		}
		s.data.Tasks[id] = t
	}
	for id, a := range s.data.Assets {
		// Only use a linked task from the asset's space; old snapshots need no
		// rewrite of the rendered SVG or their existing task identifiers.
		if t, ok := s.data.Tasks[a.TaskID]; ok && t.SpaceID == a.SpaceID {
			if a.Ratio == "" {
				a.Ratio = t.Ratio
			}
			if a.Style == "" {
				a.Style = t.Style
			}
		}
		defaults := withDefaults(Input{Ratio: a.Ratio, Style: a.Style})
		a.Ratio, a.Style = defaults.Ratio, defaults.Style
		s.data.Assets[id] = a
	}
	if err = s.commit(s.data); err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}
func (s *FileStore) Close() error { return s.lock.Close() }
func (s *FileStore) clone() snapshot {
	d := snapshot{Version: 1, Tasks: map[string]Task{}, Assets: map[string]Asset{}}
	for k, v := range s.data.Tasks {
		d.Tasks[k] = v
	}
	for k, v := range s.data.Assets {
		d.Assets[k] = v
	}
	return d
}
func (s *FileStore) commit(d snapshot) error {
	b, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(s.path), ".state-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(f.Name(), s.path); err != nil {
		return err
	}
	// Atomic rename protects process-crash recovery; see docs for power-loss limits.
	s.data = d
	return nil
}
func (s *FileStore) Create(space, key string, in Input) (Task, bool, error) {
	var err error
	in, err = Normalize(in)
	if err != nil {
		return Task{}, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.createLocked(space, key, in, "")
}

func (s *FileStore) Retry(space, parentID, key string, in Input) (Task, bool, error) {
	var err error
	in, err = Normalize(in)
	if err != nil {
		return Task{}, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	parent, ok := s.data.Tasks[parentID]
	if !ok || parent.SpaceID != space {
		return Task{}, false, ErrNotFound
	}
	if parent.Status != "failed" {
		return Task{}, false, ErrConflict
	}
	return s.createLocked(space, key, in, parentID)
}

// createLocked keeps a retry's parent check and idempotency decision in the
// same critical section as the durable write. The original task is unchanged.
func (s *FileStore) createLocked(space, key string, in Input, retryOf string) (Task, bool, error) {
	for _, t := range s.data.Tasks {
		if t.SpaceID == space && t.Key == key {
			storedInput, err := Normalize(t.Input)
			if err != nil || storedInput != in || t.RetryOf != retryOf {
				return Task{}, false, ErrConflict
			}
			return t, false, nil
		}
	}
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return Task{}, false, err
	}
	id := hex.EncodeToString(raw)
	now := time.Now().UTC()
	t := Task{ID: id, SpaceID: space, Input: in, Status: "queued", Provider: "mock-svg", RetryOf: retryOf, CreatedAt: now, UpdatedAt: now, Key: key}
	d := s.clone()
	d.Tasks[id] = t
	return t, true, s.commit(d)
}
func (s *FileStore) Get(space, id string) (Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.data.Tasks[id]
	if !ok || t.SpaceID != space {
		return Task{}, ErrNotFound
	}
	return t, nil
}
func (s *FileStore) List(space string) []Task {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []Task{}
	for _, t := range s.data.Tasks {
		if t.SpaceID == space {
			out = append(out, t.Public())
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out
}
func (s *FileStore) Assets(space string) []Asset {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []Asset{}
	for _, a := range s.data.Assets {
		if a.SpaceID == space {
			out = append(out, a)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out
}
func (s *FileStore) SaveAsset(space, id string) (Asset, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.data.Tasks[id]
	if !ok || t.SpaceID != space {
		return Asset{}, ErrNotFound
	}
	if t.Status != "succeeded" {
		return Asset{}, ErrConflict
	}
	if a, ok := s.data.Assets[id]; ok {
		return a, nil
	}
	a := Asset{ID: id, SpaceID: space, TaskID: id, Prompt: t.Prompt, Kind: t.Kind, Ratio: t.Ratio, Style: t.Style, Provider: t.Provider, ResultURL: t.ResultURL, CreatedAt: time.Now().UTC()}
	d := s.clone()
	d.Assets[id] = a
	return a, s.commit(d)
}
func (s *FileStore) Next() (Task, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var next Task
	for _, t := range s.data.Tasks {
		if t.Status == "queued" && (next.ID == "" || t.CreatedAt.Before(next.CreatedAt)) {
			next = t
		}
	}
	if next.ID == "" {
		return Task{}, false, nil
	}
	next.Status = "running"
	next.Progress = 35
	next.UpdatedAt = time.Now().UTC()
	d := s.clone()
	d.Tasks[next.ID] = next
	return next, true, s.commit(d)
}
func (s *FileStore) Finish(t Task, svg string, runErr error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t.UpdatedAt = time.Now().UTC()
	t.Progress = 100
	if runErr != nil {
		t.Status = "failed"
		t.Error = "模拟流程失败；请移除 [mock-fail] 后创建新任务"
	} else {
		t.Status = "succeeded"
		t.SVG = svg
		t.ResultURL = "/api/v1/tasks/" + t.ID + "/result?space_id=" + t.SpaceID
	}
	d := s.clone()
	d.Tasks[t.ID] = t
	return s.commit(d)
}
