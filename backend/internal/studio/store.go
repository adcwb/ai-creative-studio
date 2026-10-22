package studio

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	Create(string, string, Input, ...Expectation) (Task, bool, error)
	Retry(string, string, string, Input, ...Expectation) (Task, bool, error)
	Get(string, string) (Task, error)
	List(string) []Task
	Assets(string) []Asset
	SaveAsset(string, string) (Asset, error)
	ReadResult(string, string) (GenerationResult, error)
	Capabilities() Capabilities
	Next() (Task, bool, error)
	Finish(Task, GenerationResult, error) error
}
type FileStore struct {
	mu           sync.Mutex
	path         string
	data         snapshot
	lock         *os.File
	results      *os.Root
	capabilities Capabilities
}

func OpenStore(dir string, options ...Capabilities) (*FileStore, error) {
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
	s.capabilities = mockCapabilities()
	if len(options) > 0 {
		s.capabilities = options[0]
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		s.Close()
		return nil, err
	}
	if err = root.MkdirAll("results", 0700); err == nil {
		s.results, err = root.OpenRoot("results")
	}
	root.Close()
	if err != nil {
		s.Close()
		return nil, err
	}
	b, err := os.ReadFile(s.path)
	if err == nil {
		// Existing files must supply the complete snapshot envelope. Decoding
		// over the new-store defaults could silently accept a missing map and
		// discard durable task identities and idempotency keys on the commit.
		var restored snapshot
		err = json.Unmarshal(b, &restored)
		s.data = restored
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
		if t.Provider == "" {
			t.Provider = "mock-svg"
		}
		if t.Status == "succeeded" && t.Provider == "mock-svg" && t.ResultMIME == "" {
			t.ResultMIME = "image/svg+xml"
		}
		if t.Status == "running" {
			if t.Provider == "mock-svg" {
				t.Status = "queued"
				t.Progress = 0
			} else {
				t.Status = "failed"
				t.Progress = 100
				t.ErrorCode, t.Error = "execution_interrupted", providerMessage("execution_interrupted")
			}
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
			if a.ResultMIME == "" {
				a.ResultMIME = t.ResultMIME
			}
		}
		if a.ResultMIME == "" && (a.Provider == "mock-svg" || a.Provider == "") {
			a.ResultMIME = "image/svg+xml"
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
func (s *FileStore) Close() error {
	var resultErr error
	if s.results != nil {
		resultErr = s.results.Close()
	}
	return errors.Join(resultErr, s.lock.Close())
}

func (s *FileStore) Capabilities() Capabilities { return s.capabilities }
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
func (s *FileStore) Create(space, key string, in Input, expectation ...Expectation) (Task, bool, error) {
	var err error
	in, err = Normalize(in)
	if err != nil {
		return Task{}, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.createLocked(space, key, in, "", expectation...)
}

func (s *FileStore) Retry(space, parentID, key string, in Input, expectation ...Expectation) (Task, bool, error) {
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
	return s.createLocked(space, key, in, parentID, expectation...)
}

// createLocked keeps a retry's parent check and idempotency decision in the
// same critical section as the durable write. The original task is unchanged.
func (s *FileStore) createLocked(space, key string, in Input, retryOf string, expectation ...Expectation) (Task, bool, error) {
	for _, t := range s.data.Tasks {
		if t.SpaceID == space && t.Key == key {
			storedInput, err := Normalize(t.Input)
			if err != nil || storedInput != in || t.RetryOf != retryOf {
				return Task{}, false, ErrConflict
			}
			return t, false, nil
		}
	}
	if !s.capabilities.Ready {
		return Task{}, false, ErrProviderNotReady
	}
	if len(expectation) > 0 && (expectation[0].Provider != s.capabilities.Provider || expectation[0].Model != s.capabilities.Model) {
		return Task{}, false, ErrProviderChanged
	}
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return Task{}, false, err
	}
	id := hex.EncodeToString(raw)
	now := time.Now().UTC()
	t := Task{ID: id, SpaceID: space, Input: in, Status: "queued", Provider: s.capabilities.Provider, Model: s.capabilities.Model, RetryOf: retryOf, CreatedAt: now, UpdatedAt: now, Key: key}
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
	a := Asset{ID: id, SpaceID: space, TaskID: id, Prompt: t.Prompt, Kind: t.Kind, Ratio: t.Ratio, Style: t.Style, Provider: t.Provider, Model: t.Model, ResultURL: t.ResultURL, ResultMIME: t.ResultMIME, ResultWidth: t.ResultWidth, ResultHeight: t.ResultHeight, CreatedAt: time.Now().UTC()}
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
func (s *FileStore) Finish(t Task, result GenerationResult, runErr error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.data.Tasks[t.ID]
	if !ok || current.SpaceID != t.SpaceID {
		return ErrNotFound
	}
	if current.Status != "running" && current.Status != "queued" {
		return ErrConflict
	}
	t = current
	if runErr == nil {
		switch t.Provider {
		case "mock-svg":
			width, height := mockDimensions(t.Ratio)
			if result.MIMEType != "image/svg+xml" || len(result.Data) == 0 || result.Width != width || result.Height != height {
				runErr = providerFailure("invalid_provider_response")
			}
		case "openai-images":
			_, width, height := imageSize(t.Ratio)
			if t.Model != OpenAIModel || result.MIMEType != "image/png" || result.Width != width || result.Height != height {
				runErr = providerFailure("invalid_provider_response")
			} else {
				runErr = validatePNG(result.Data, width, height)
			}
		default:
			runErr = providerFailure("provider_unavailable")
		}
	}
	t.UpdatedAt = time.Now().UTC()
	t.Progress = 100
	if runErr != nil {
		t.Status = "failed"
		t.ErrorCode, t.Error = safeFailure(runErr, t.Provider)
	} else {
		if t.Provider == "openai-images" {
			name, err := resultName(t.ID)
			if err != nil {
				return err
			}
			if err := s.writePNG(name, result.Data); err != nil {
				return err
			}
			t.ResultPath = name
		} else {
			t.SVG = string(result.Data)
		}
		t.Status = "succeeded"
		t.ResultMIME, t.ResultWidth, t.ResultHeight = result.MIMEType, result.Width, result.Height
		t.ResultURL = "/api/v1/tasks/" + t.ID + "/result?space_id=" + t.SpaceID
	}
	d := s.clone()
	d.Tasks[t.ID] = t
	return s.commit(d)
}

func resultName(id string) (string, error) {
	if len(id) != 32 {
		return "", errors.New("invalid stored result identity")
	}
	if _, err := hex.DecodeString(id); err != nil {
		return "", errors.New("invalid stored result identity")
	}
	return id + ".png", nil
}

func (s *FileStore) writePNG(name string, data []byte) error {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return err
	}
	temporary := ".result-" + hex.EncodeToString(raw)
	f, err := s.results.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer s.results.Remove(temporary)
	if _, err = f.Write(data); err != nil {
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
	if err = s.results.Rename(temporary, name); err != nil {
		return err
	}
	dir, err := s.results.Open(".")
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func (s *FileStore) ReadResult(space, id string) (GenerationResult, error) {
	t, err := s.Get(space, id)
	if err != nil {
		return GenerationResult{}, err
	}
	if t.Status != "succeeded" {
		return GenerationResult{}, ErrConflict
	}
	if t.Provider == "mock-svg" && t.ResultMIME == "image/svg+xml" {
		return GenerationResult{Data: []byte(t.SVG), MIMEType: "image/svg+xml", Width: t.ResultWidth, Height: t.ResultHeight}, nil
	}
	name, err := resultName(id)
	if err != nil || t.ID != id || t.ResultPath != name || t.Provider != "openai-images" || t.ResultMIME != "image/png" {
		return GenerationResult{}, errors.New("invalid stored result metadata")
	}
	f, err := s.results.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return GenerationResult{}, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxPNGBytes {
		return GenerationResult{}, errors.New("invalid stored result file")
	}
	data, err := io.ReadAll(io.LimitReader(f, maxPNGBytes+1))
	if err != nil {
		return GenerationResult{}, err
	}
	_, width, height := imageSize(t.Ratio)
	if t.ResultWidth != width || t.ResultHeight != height || validatePNG(data, width, height) != nil {
		return GenerationResult{}, errors.New("invalid stored PNG")
	}
	return GenerationResult{Data: data, MIMEType: "image/png", Width: width, Height: height}, nil
}
