package application

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
	"syscall"
	"time"
)

type snapshot struct {
	ImageResumeReceipts   map[string]string               `json:"image_resume_receipts,omitempty"`
	History               map[string][]ProjectRevision    `json:"history,omitempty"`
	StoryboardGenerations map[string]StoryboardGeneration `json:"storyboard_generations"`
	Version               int                             `json:"version"`
	Tasks                 map[string]Task                 `json:"tasks"`
	Assets                map[string]Asset                `json:"assets"`
	Projects              map[string]Project              `json:"projects"`
	ScriptGenerations     map[string]ScriptGeneration     `json:"script_generations"`
	ProjectReceipts       map[string]projectReceipt       `json:"project_receipts"`
}

// Repository is the boundary for a future transactional PostgreSQL implementation.
type Repository interface {
	ProjectRepository
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
	pg           *postgresScope
	blobs        *managedBlobs
	execution    *ExecutionGovernor
	runtime      *ModelRuntime
	budget       *BudgetLedger
	exchange     ExchangeProvider
	creative     *creativeStore
	media        *MediaStore
	mu           stateMutex
	path         string
	data         snapshot
	lock         *os.File
	results      *os.Root
	capabilities Capabilities
}

func OpenStore(dir string, options ...Capabilities) (*FileStore, error) {
	if _, err := os.Lstat(filepath.Join(dir, ".postgres-cache")); err == nil {
		return nil, errors.New("PostgreSQL cache cannot be opened as file storage; restore to a new directory")
	} else if !os.IsNotExist(err) {
		return nil, err
	}
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
	s := &FileStore{path: filepath.Join(dir, "state.json"), lock: lock, data: emptySnapshot()}
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
	if (s.data.Version != 1 && s.data.Version != 2 && s.data.Version != 3) || s.data.Tasks == nil || s.data.Assets == nil {
		s.Close()
		return nil, fmt.Errorf("unsupported or invalid state")
	}
	if s.data.Version == 1 {
		// Only a complete legacy image snapshot can be upgraded. A v1 file
		// carrying newer records must not have those records silently reset.
		if s.data.Projects != nil || s.data.ScriptGenerations != nil || s.data.ProjectReceipts != nil || s.data.StoryboardGenerations != nil {
			s.Close()
			return nil, fmt.Errorf("invalid legacy state with project records")
		}
		s.data.Version = 2
		s.data.Projects = map[string]Project{}
		s.data.ScriptGenerations = map[string]ScriptGeneration{}
		s.data.ProjectReceipts = map[string]projectReceipt{}
	}
	if s.data.Projects == nil || s.data.ScriptGenerations == nil || s.data.ProjectReceipts == nil {
		s.Close()
		return nil, fmt.Errorf("incomplete project state")
	}
	if s.data.Version == 2 {
		if s.data.StoryboardGenerations != nil {
			s.Close()
			return nil, fmt.Errorf("invalid legacy state with storyboard records")
		}
		for id, p := range s.data.Projects {
			if p.Storyboard != nil || p.LatestStoryboardGenerationID != "" || p.LatestStoryboardGeneration != nil || p.ScriptVersion != 0 {
				s.Close()
				return nil, fmt.Errorf("invalid legacy storyboard project")
			}
			if p.Script != nil {
				p.ScriptVersion = 1
			}
			s.data.Projects[id] = p
		}
		s.data.StoryboardGenerations = map[string]StoryboardGeneration{}
		s.data.Version = 3
	}
	if s.data.StoryboardGenerations == nil {
		s.Close()
		return nil, fmt.Errorf("incomplete storyboard state")
	}
	for _, p := range s.data.Projects {
		if (p.Script == nil && p.ScriptVersion != 0) || (p.Script != nil && p.ScriptVersion < 1) {
			s.Close()
			return nil, fmt.Errorf("invalid script revision")
		}
		if p.Storyboard != nil {
			if p.Storyboard.SourceScriptVersion < 1 || p.Storyboard.SourceScriptVersion > p.ScriptVersion {
				s.Close()
				return nil, fmt.Errorf("invalid storyboard source revision")
			}
			if _, err := NormalizeStoryboardShots(p.Storyboard.Shots); err != nil {
				s.Close()
				return nil, fmt.Errorf("invalid saved storyboard")
			}
		}
	}
	for id, g := range s.data.StoryboardGenerations {
		p, ok := s.data.Projects[g.ProjectID]
		if !ok || g.ID != id || p.SpaceID != g.SpaceID || g.SourceScript == nil || g.SourceScriptVersion < 1 || g.SourceScriptVersion > p.ScriptVersion || !validCreativeGenerationProvider(g.Provider, "mock-storyboard") {
			s.Close()
			return nil, fmt.Errorf("invalid storyboard generation")
		}
		if _, err := NormalizeScript(*g.SourceScript); err != nil {
			s.Close()
			return nil, fmt.Errorf("invalid storyboard source script")
		}
		if g.Status != "queued" && g.Status != "running" && g.Status != "succeeded" && g.Status != "failed" {
			s.Close()
			return nil, fmt.Errorf("invalid storyboard status")
		}
		if g.Status == "queued" || g.Status == "running" {
			if p.LatestStoryboardGenerationID != id || p.Version != g.BaseVersion || p.ScriptVersion != g.SourceScriptVersion {
				s.Close()
				return nil, fmt.Errorf("invalid active storyboard linkage")
			}
			if sg, ok := s.data.ScriptGenerations[p.LatestGenerationID]; ok && (sg.Status == "queued" || sg.Status == "running") {
				s.Close()
				return nil, fmt.Errorf("multiple active project generations")
			}
		}
		if g.Status == "running" {
			g.Status = "queued"
			g.Progress = 0
			if g.Provider != "mock-storyboard" {
				g.Status = "failed"
				g.Progress = 100
				g.ErrorCode, g.Error = creativeFailure(providerFailure("execution_interrupted"))
			}
			g.UpdatedAt = time.Now().UTC()
			s.data.StoryboardGenerations[id] = g
		}
	}
	for _, p := range s.data.Projects {
		if p.LatestStoryboardGenerationID != "" {
			g, ok := s.data.StoryboardGenerations[p.LatestStoryboardGenerationID]
			if !ok || g.ProjectID != p.ID || g.SpaceID != p.SpaceID {
				s.Close()
				return nil, fmt.Errorf("invalid storyboard projection linkage")
			}
		}
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
			if t.Provider == "mock-svg" || (t.Provider == "jimeng-images" && t.UpstreamID != "") {
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
	for id, generation := range s.data.ScriptGenerations {
		if generation.Status == "running" {
			generation.Status, generation.Progress = "queued", 0
			if generation.Provider != "mock-script" {
				generation.Status = "failed"
				generation.Progress = 100
				generation.ErrorCode, generation.Error = creativeFailure(providerFailure("execution_interrupted"))
			}
			generation.UpdatedAt = time.Now().UTC()
			s.data.ScriptGenerations[id] = generation
			if project, ok := s.data.Projects[generation.ProjectID]; ok && project.SpaceID == generation.SpaceID && project.LatestGenerationID == id {
				project.UpdatedAt = generation.UpdatedAt
				s.data.Projects[project.ID] = project
			}
		}
	}
	if err = s.commit(s.data); err != nil {
		s.Close()
		return nil, err
	}
	s.creative, err = openCreativeStore(filepath.Dir(s.path))
	if err != nil {
		s.Close()
		return nil, err
	}
	s.media, err = openMediaStore(filepath.Dir(s.path))
	if err != nil {
		s.Close()
		return nil, err
	}
	s.budget, err = OpenBudgetLedger(filepath.Dir(s.path))
	if err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}
func (s *FileStore) Close() error {
	if s.media != nil {
		s.media.close()
	}
	var resultErr error
	if s.blobs != nil {
		if closer, ok := s.blobs.store.(io.Closer); ok {
			resultErr = closer.Close()
		}
	}
	if s.results != nil {
		resultErr = errors.Join(resultErr, s.results.Close())
	}
	if s.lock != nil {
		return errors.Join(resultErr, s.lock.Close())
	}
	return resultErr
}

func (s *FileStore) Capabilities() Capabilities { return s.capabilities }
func emptySnapshot() snapshot {
	return snapshot{Version: 3, StoryboardGenerations: map[string]StoryboardGeneration{}, Tasks: map[string]Task{}, Assets: map[string]Asset{}, Projects: map[string]Project{}, ScriptGenerations: map[string]ScriptGeneration{}, ProjectReceipts: map[string]projectReceipt{}}
}
func (s *FileStore) clone() snapshot {
	d := emptySnapshot()
	for k, v := range s.data.StoryboardGenerations {
		d.StoryboardGenerations[k] = cloneStoryboardGeneration(v)
	}
	for k, v := range s.data.Tasks {
		d.Tasks[k] = v
	}
	for k, v := range s.data.Assets {
		d.Assets[k] = v
	}
	for k, v := range s.data.Projects {
		d.Projects[k] = cloneProject(v)
	}
	for k, v := range s.data.ScriptGenerations {
		d.ScriptGenerations[k] = v
	}
	for k, v := range s.data.ProjectReceipts {
		d.ProjectReceipts[k] = v
	}
	d.History = cloneHistory(s.data.History)
	d.ImageResumeReceipts = map[string]string{}
	for k, v := range s.data.ImageResumeReceipts {
		d.ImageResumeReceipts[k] = v
	}
	return d
}
func (s *FileStore) commit(d snapshot) error {
	if err := s.captureHistory(&d); err != nil {
		return err
	}
	if s.pg != nil {
		if err := s.enqueueNewPostgres(d); err != nil {
			return err
		}
		if err := s.pg.save(d); err != nil {
			return err
		}
		s.data = d
		return nil
	}
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
	traceParent := in.traceParent
	in.traceParent = ""
	selection, selectedCaps := in.selection, in.selectedCaps
	in.selection, in.selectedCaps = nil, Capabilities{}
	for _, t := range s.data.Tasks {
		if t.SpaceID == space && t.Key == key {
			storedInput, err := Normalize(t.Input)
			if err != nil || storedInput != in || t.RetryOf != retryOf {
				return Task{}, false, ErrConflict
			}
			return t, false, nil
		}
	}
	caps := s.capabilities
	if selection != nil {
		caps = selectedCaps
	}
	if !caps.Ready {
		return Task{}, false, ErrProviderNotReady
	}
	if len(expectation) > 0 && (expectation[0].Provider != caps.Provider || expectation[0].Model != caps.Model) {
		return Task{}, false, ErrProviderChanged
	}
	// Bound both queue pressure and costly real attempts, including failures.
	if caps.Mode == "real" {
		active, recent := 0, 0
		since := time.Now().Add(-time.Hour)
		for _, task := range s.data.Tasks {
			if task.Status == "queued" || task.Status == "running" {
				active++
			}
			if task.Provider != "mock-svg" && task.CreatedAt.After(since) {
				recent++
			}
		}
		if active >= 4 || recent >= 20 {
			return Task{}, false, ErrProviderNotReady
		}
	}
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return Task{}, false, err
	}
	id := hex.EncodeToString(raw)
	now := time.Now().UTC()
	t := Task{TraceParent: traceParent, RuntimeSelection: cloneRuntimeSelection(selection), ID: id, SpaceID: space, Input: in, Status: "queued", Provider: caps.Provider, Model: caps.Model, RetryOf: retryOf, CreatedAt: now, UpdatedAt: now, Key: key}
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
	return s.finishLocked(t, result, runErr)
}
func (s *FileStore) finishLocked(t Task, result GenerationResult, runErr error) error {
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
			if !configToken.MatchString(t.Model) || result.MIMEType != "image/png" || result.Width != width || result.Height != height {
				runErr = providerFailure("invalid_provider_response")
			} else {
				runErr = validatePNG(result.Data, width, height)
			}
		case "gemini-images", "gemini-interactions-images", "ark-images", "jimeng-images", "minimax-images":
			if result.MIMEType != "image/png" {
				runErr = providerFailure("invalid_provider_response")
			} else {
				runErr = validateLargePNG(result.Data, result.Width, result.Height)
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
		if realImageProvider(t.Provider) {
			name, err := resultName(t.ID)
			if err != nil {
				return err
			}
			if err := s.writePNG(name, result.Data); err != nil {
				return err
			}
			if s.blobs != nil {
				ctx, cancel := blobContext()
				err := s.blobs.put(ctx, blobKey(t.SpaceID, "results", name), result.Data)
				cancel()
				if err != nil {
					return err
				}
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
	if err != nil || t.ID != id || t.ResultPath != name || !realImageProvider(t.Provider) || t.ResultMIME != "image/png" {
		return GenerationResult{}, errors.New("invalid stored result metadata")
	}
	if s.blobs != nil {
		ctx, cancel := blobContext()
		defer cancel()
		if _, err = s.blobs.materialize(ctx, blobKey(space, "results", name), filepath.Join(filepath.Dir(s.path), "results", name)); err != nil {
			return GenerationResult{}, err
		}
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
	width, height := t.ResultWidth, t.ResultHeight
	validation := validateLargePNG(data, width, height)
	if t.Provider == "openai-images" {
		_, expectedWidth, expectedHeight := imageSize(t.Ratio)
		if width != expectedWidth || height != expectedHeight {
			validation = providerFailure("invalid_provider_response")
		} else {
			validation = validatePNG(data, width, height)
		}
	}
	if validation != nil {
		return GenerationResult{}, errors.New("invalid stored PNG")
	}
	return GenerationResult{Data: data, MIMEType: "image/png", Width: width, Height: height}, nil
}
