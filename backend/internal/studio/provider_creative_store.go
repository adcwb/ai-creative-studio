package studio

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

type CreativeJobInput struct {
	ExpectedModel    string `json:"expected_model,omitempty"`
	ReferenceAssetID string `json:"reference_asset_id,omitempty"`
	Kind             string `json:"kind"`
	ExpectedVersion  int    `json:"expected_version"`
	ExpectedProvider string `json:"expected_provider,omitempty"`
	Prompt           string `json:"prompt,omitempty"`
	ShotID           string `json:"shot_id,omitempty"`
	Character        string `json:"character,omitempty"`
}
type CreativeJob struct {
	Model            string         `json:"model,omitempty"`
	ReferenceAssetID string         `json:"reference_asset_id,omitempty"`
	ID               string         `json:"id"`
	SpaceID          string         `json:"space_id"`
	ProjectID        string         `json:"project_id"`
	Kind             string         `json:"kind"`
	Provider         string         `json:"provider"`
	Status           string         `json:"status"`
	SourceVersion    int            `json:"source_version"`
	Prompt           string         `json:"prompt"`
	ShotID           string         `json:"shot_id"`
	Character        string         `json:"character"`
	UpstreamID       string         `json:"upstream_id,omitempty"`
	AssetID          string         `json:"asset_id,omitempty"`
	Analysis         *BriefAnalysis `json:"analysis,omitempty"`
	Error            string         `json:"error,omitempty"`
	ErrorCode        string         `json:"error_code,omitempty"`
	CreatedAt        time.Time      `json:"created_at"`
	UpdatedAt        time.Time      `json:"updated_at"`
	Source           ProjectInput   `json:"source"`
}
type creativeReceipt struct {
	Fingerprint string `json:"fingerprint"`
	JobID       string `json:"job_id"`
}
type creativeDisk struct {
	Version  int                        `json:"version"`
	Jobs     map[string]CreativeJob     `json:"jobs"`
	Receipts map[string]creativeReceipt `json:"receipts"`
}
type creativeStore struct {
	speechCaps, videoCaps Capabilities
	mu                    sync.Mutex
	path                  string
	data                  creativeDisk
	provider              CreativeProvider
	speech                CreativeProvider
	video                 CreativeProvider
	image                 ImageProvider
	caps                  Capabilities
	imageCaps             Capabilities
	cancels               map[string]context.CancelFunc
}

func openCreativeStore(dir string) (*creativeStore, error) {
	c := &creativeStore{path: filepath.Join(dir, "creative.json"), data: creativeDisk{Version: 1, Jobs: map[string]CreativeJob{}, Receipts: map[string]creativeReceipt{}}, cancels: map[string]context.CancelFunc{}}
	_, c.caps = ConfigureCreativeProvider("mock-text", "")
	c.imageCaps = Capabilities{Provider: "local-synthetic", Mode: "mock", Ready: true, Message: "合成色卡图片，不是AI生成。"}
	b, err := os.ReadFile(c.path)
	if err == nil {
		var d creativeDisk
		if json.Unmarshal(b, &d) != nil || d.Version != 1 || d.Jobs == nil || d.Receipts == nil {
			return nil, errors.New("invalid creative state")
		}
		c.data = d
		for id, j := range d.Jobs {
			if j.ID != id || j.ProjectID == "" || j.SpaceID == "" {
				return nil, errors.New("invalid creative job")
			}
			if j.Status == "running" {
				if j.Kind == "video" && j.UpstreamID != "" {
					j.Status = "queued"
				} else {
					j.Status = "failed"
					j.ErrorCode, j.Error = creativeFailure(providerFailure("execution_interrupted"))
				}
				j.UpdatedAt = time.Now().UTC()
				c.data.Jobs[id] = j
			}
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	if err = c.saveLocked(); err != nil {
		return nil, err
	}
	return c, nil
}
func (c *creativeStore) saveLocked() error {
	b, err := json.Marshal(c.data)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(c.path), ".creative-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err = f.Write(b); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(name, c.path)
}

// Configuration is set once, before workers/HTTP start; it never probes remote services.
func ConfigureCreative(s *FileStore, p CreativeProvider, caps Capabilities) error {
	if s.creative == nil {
		return errors.New("creative store unavailable")
	}
	s.creative.provider = p
	s.creative.caps = caps
	return nil
}
func SetCreativeSpeech(s *FileStore, p CreativeProvider, caps ...Capabilities) {
	s.creative.speech = p
	if len(caps) > 0 {
		s.creative.speechCaps = caps[0]
	}
}
func SetCreativeVideo(s *FileStore, p CreativeProvider, caps ...Capabilities) {
	s.creative.video = p
	if len(caps) > 0 {
		s.creative.videoCaps = caps[0]
	}
}
func SetCreativeImage(s *FileStore, p ImageProvider, caps Capabilities) {
	if caps.Provider != "mock-svg" {
		s.creative.image = p
		s.creative.imageCaps = caps
	}
}
func (s *FileStore) creativeGenerationProvider(mock string) string {
	if s.creative == nil || s.creative.caps.Provider == "mock-text" {
		return mock
	}
	return s.creative.caps.Provider
}
func (s *FileStore) guardCreativeLocked(expected, expectedModel string) error {
	if s.creative == nil {
		return nil
	}
	caps := s.creative.caps
	if !caps.Ready {
		return ErrProviderNotReady
	}
	if expected != "" && expected != caps.Provider {
		return ErrProviderChanged
	}
	if caps.Mode == "real" && (expected != caps.Provider || caps.Model == "" || expectedModel != caps.Model) {
		return ErrProviderChanged
	}
	// Per-space/project activity is already locked by callers. Bound real generation history
	// globally as a conservative local cost safeguard, including failed attempts.
	if caps.Mode == "real" {
		count := 0
		since := time.Now().Add(-time.Hour)
		for _, g := range s.data.ScriptGenerations {
			if g.Provider != "mock-script" && g.CreatedAt.After(since) {
				count++
			}
		}
		for _, g := range s.data.StoryboardGenerations {
			if g.Provider != "mock-storyboard" && g.CreatedAt.After(since) {
				count++
			}
		}
		if count >= 20 {
			return ErrProviderNotReady
		}
	}
	return nil
}
func (c *creativeStore) mediaCaps() map[string]Capabilities {
	capFor := func(p CreativeProvider) Capabilities {
		if p == nil {
			return Capabilities{Provider: "unavailable", Mode: "unavailable", Ready: false, Message: "尚未配置此类官方供应商；可上传本地素材。"}
		}
		return Capabilities{Provider: creativeProviderName(p), Mode: "real", Ready: true, Message: "真实协议未验证效果及费用；提交可能计费，失败不自动重试。"}
	}
	speech, video := c.speechCaps, c.videoCaps
	if speech.Provider == "" {
		speech = capFor(c.speech)
	}
	if video.Provider == "" {
		video = capFor(c.video)
	}
	return map[string]Capabilities{"image": c.imageCaps, "speech": speech, "video": video}
}
func (s *FileStore) createCreativeJob(space, id, key string, in CreativeJobInput) (CreativeJob, bool, error) {
	if in.ExpectedVersion < 1 {
		return CreativeJob{}, false, ErrInvalidProjectInput
	}
	if in.Kind != "analysis" && in.Kind != "image" && in.Kind != "speech" && in.Kind != "video" {
		return CreativeJob{}, false, ErrInvalidProjectInput
	}
	if in.Kind != "analysis" {
		var err error
		in.Prompt, err = normalizeProjectText(in.Prompt, "创作提示", 3, 2000)
		if err != nil {
			return CreativeJob{}, false, err
		}
	}
	if in.ShotID != "" && !shotIDPattern.MatchString(in.ShotID) {
		return CreativeJob{}, false, ErrInvalidProjectInput
	}
	if len([]rune(in.Character)) > 40 {
		return CreativeJob{}, false, ErrInvalidProjectInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p, err := s.projectLocked(space, id)
	if err != nil {
		return CreativeJob{}, false, err
	}
	c := s.creative
	c.mu.Lock()
	defer c.mu.Unlock()
	fingerprint, _ := projectFingerprint("creative", id, in)
	rk := projectReceiptKey(space, key)
	if r, ok := c.data.Receipts[rk]; ok {
		if r.Fingerprint != fingerprint {
			return CreativeJob{}, false, ErrIdempotencyConflict
		}
		return c.data.Jobs[r.JobID], false, nil
	}
	if p.Version != in.ExpectedVersion {
		return CreativeJob{}, false, ErrVersionConflict
	}
	if in.ShotID != "" {
		found := false
		if p.Storyboard != nil {
			for _, shot := range p.Storyboard.Shots {
				found = found || shot.ID == in.ShotID
			}
		}
		if !found {
			return CreativeJob{}, false, ErrInvalidProjectInput
		}
	}
	if in.Character != "" {
		found := false
		if p.Script != nil {
			for _, char := range p.Script.Characters {
				found = found || char.Name == in.Character
			}
		}
		if !found {
			return CreativeJob{}, false, ErrInvalidProjectInput
		}
	}
	caps := c.caps
	if in.Kind != "analysis" {
		caps = c.mediaCaps()[in.Kind]
	}
	if !caps.Ready {
		return CreativeJob{}, false, ErrProviderNotReady
	}
	if in.ReferenceAssetID != "" {
		if in.Kind != "image" && in.Kind != "video" {
			return CreativeJob{}, false, ErrInvalidProjectInput
		}
		_, supportsImage := c.image.(ReferenceImageProvider)
		_, supportsVideo := c.video.(referenceVideoProvider)
		if (in.Kind == "image" && !supportsImage) || (in.Kind == "video" && !supportsVideo) {
			return CreativeJob{}, false, projectInputError("当前图像适配器不支持参考图；不会忽略参考素材生成")
		}
		path, mime, e := s.media.file(id, space, in.ReferenceAssetID, false, false)
		if e != nil || path == "" || (mime != "image/png" && mime != "image/jpeg") {
			return CreativeJob{}, false, ErrInvalidProjectInput
		}
	}

	if (in.ExpectedProvider != "" && in.ExpectedProvider != caps.Provider) || (caps.Mode == "real" && (in.ExpectedProvider != caps.Provider || caps.Model == "" || in.ExpectedModel != caps.Model)) {
		return CreativeJob{}, false, ErrProviderChanged
	}
	recent := 0
	active := 0
	for _, j := range c.data.Jobs {
		if j.Status == "queued" || j.Status == "running" {
			active++
			if j.ProjectID == id && j.SpaceID == space {
				return CreativeJob{}, false, ErrGenerationActive
			}
		}
		if j.CreatedAt.After(time.Now().Add(-time.Hour)) {
			recent++
		}
	}
	if len(c.data.Jobs) >= 500 || len(c.data.Receipts) >= 1000 || recent >= 20 || active >= 4 {
		return CreativeJob{}, false, ErrProviderNotReady
	}
	jid, err := newProjectID()
	if err != nil {
		return CreativeJob{}, false, err
	}
	now := time.Now().UTC()
	job := CreativeJob{ReferenceAssetID: in.ReferenceAssetID, ID: jid, SpaceID: space, ProjectID: id, Kind: in.Kind, Provider: caps.Provider, Model: caps.Model, Status: "queued", SourceVersion: p.Version, Prompt: in.Prompt, ShotID: in.ShotID, Character: in.Character, Source: ProjectInput{Title: p.Title, Brief: p.Brief}, CreatedAt: now, UpdatedAt: now}
	c.data.Jobs[jid] = job
	c.data.Receipts[rk] = creativeReceipt{Fingerprint: fingerprint, JobID: jid}
	if err = c.saveLocked(); err != nil {
		delete(c.data.Jobs, jid)
		delete(c.data.Receipts, rk)
		return CreativeJob{}, false, err
	}
	return job, true, nil
}
func (s *FileStore) listCreativeJobs(space, id string) ([]CreativeJob, error) {
	if _, err := s.GetProject(space, id); err != nil {
		return nil, err
	}
	c := s.creative
	c.mu.Lock()
	defer c.mu.Unlock()
	jobs := []CreativeJob{}
	for _, j := range c.data.Jobs {
		if j.SpaceID == space && j.ProjectID == id {
			jobs = append(jobs, j)
		}
	}
	sort.Slice(jobs, func(i, j int) bool { return jobs[i].CreatedAt.After(jobs[j].CreatedAt) })
	return jobs, nil
}
func (s *FileStore) cancelCreativeJob(space, id, jid, key string) (CreativeJob, error) {
	if _, err := s.GetProject(space, id); err != nil {
		return CreativeJob{}, err
	}
	c := s.creative
	c.mu.Lock()
	defer c.mu.Unlock()
	job, ok := c.data.Jobs[jid]
	if !ok || job.SpaceID != space || job.ProjectID != id {
		return CreativeJob{}, ErrNotFound
	}
	fingerprint, _ := projectFingerprint("cancel-creative", jid, id)
	rk := projectReceiptKey(space, key)
	if r, ok := c.data.Receipts[rk]; ok {
		if r.Fingerprint != fingerprint {
			return CreativeJob{}, ErrIdempotencyConflict
		}
		return job, nil
	}
	if len(c.data.Receipts) >= 1000 {
		return CreativeJob{}, ErrProviderNotReady
	}
	if job.Status == "succeeded" || job.Status == "failed" {
		return CreativeJob{}, ErrConflict
	}
	old := job
	job.Status = "cancelled"
	job.UpdatedAt = time.Now().UTC()
	job.Error = "已停止本地处理；不能撤销已发送的供应商请求，费用可能已产生。"
	c.data.Jobs[jid] = job
	c.data.Receipts[rk] = creativeReceipt{Fingerprint: fingerprint, JobID: jid}
	if err := c.saveLocked(); err != nil {
		c.data.Jobs[jid] = old
		delete(c.data.Receipts, rk)
		return CreativeJob{}, err
	}
	if cancel := c.cancels[jid]; cancel != nil {
		cancel()
	}
	return job, nil
}
func (c *creativeStore) updateJob(job CreativeJob) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	old := c.data.Jobs[job.ID]
	if old.Status == "cancelled" {
		return context.Canceled
	}
	job.UpdatedAt = time.Now().UTC()
	c.data.Jobs[job.ID] = job
	if err := c.saveLocked(); err != nil {
		c.data.Jobs[job.ID] = old
		return err
	}
	return nil
}
func syntheticCreativePNG() []byte {
	im := image.NewRGBA(image.Rect(0, 0, 640, 360))
	for y := 0; y < 360; y++ {
		for x := 0; x < 640; x++ {
			im.SetRGBA(x, y, color.RGBA{R: uint8(40 + x/4), G: uint8(35 + y/3), B: 140, A: 255})
		}
	}
	var b bytes.Buffer
	_ = png.Encode(&b, im)
	return b.Bytes()
}
func RunCreativeWorker(ctx context.Context, s *FileStore) error {
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	c := s.creative
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
		}
		c.mu.Lock()
		var job CreativeJob
		for _, j := range c.data.Jobs {
			if j.Status == "queued" && (job.ID == "" || j.CreatedAt.Before(job.CreatedAt)) {
				job = j
			}
		}
		if job.ID == "" {
			c.mu.Unlock()
			continue
		}
		old := job
		job.Status = "running"
		job.UpdatedAt = time.Now().UTC()
		c.data.Jobs[job.ID] = job
		if err := c.saveLocked(); err != nil {
			c.data.Jobs[job.ID] = old
			c.mu.Unlock()
			return err
		}
		runCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
		c.cancels[job.ID] = cancel
		c.mu.Unlock()
		err := s.executeCreativeJob(runCtx, &job)
		cancel()
		c.mu.Lock()
		cancelled := c.data.Jobs[job.ID].Status == "cancelled"
		c.mu.Unlock()
		if ctx.Err() != nil {
			c.mu.Lock()
			delete(c.cancels, job.ID)
			c.mu.Unlock()
			return nil
		}
		if cancelled {
			c.mu.Lock()
			delete(c.cancels, job.ID)
			c.mu.Unlock()
			continue
		}
		if err != nil {
			job.Status = "failed"
			job.ErrorCode, job.Error = creativeFailure(err)
		} else {
			job.Status = "succeeded"
		}
		err = c.updateJob(job)
		c.mu.Lock()
		delete(c.cancels, job.ID)
		c.mu.Unlock()
		if err != nil && !errors.Is(err, context.Canceled) {
			return err
		}
	}
}
func (s *FileStore) executeCreativeJob(ctx context.Context, job *CreativeJob) error {
	c := s.creative
	var data []byte
	var mime, role string
	var err error
	if job.Kind == "analysis" {
		if !c.caps.Ready || job.Provider != c.caps.Provider || (job.Provider != "mock-text" && (job.Model == "" || job.Model != c.caps.Model || c.provider == nil)) {
			return providerFailure("provider_unavailable")
		}
		var a BriefAnalysis
		if c.caps.Provider == "mock-text" {
			a = localBriefAnalysis(job.Source)
		} else {
			a, err = c.provider.Analyze(ctx, job.Source)
		}
		a.SourceVersion = job.SourceVersion
		job.Analysis = &a
		return err
	}
	caps := c.mediaCaps()[job.Kind]
	if job.Provider != caps.Provider || !caps.Ready || (caps.Mode == "real" && (job.Model == "" || job.Model != caps.Model)) {
		return providerFailure("provider_unavailable")
	}
	switch job.Kind {
	case "image":
		mime = "image/png"
		role = "shot"
		if job.Character != "" {
			role = "reference"
		}
		if c.image == nil {
			data = syntheticCreativePNG()
		} else {
			var result GenerationResult
			input := Input{Kind: "general", Prompt: job.Prompt, Ratio: "16:9", Style: "natural"}
			if job.ReferenceAssetID != "" {
				adapter, ok := c.image.(ReferenceImageProvider)
				if !ok {
					return providerFailure("unsupported_capability")
				}
				path, refMIME, e := s.media.file(job.ProjectID, job.SpaceID, job.ReferenceAssetID, false, false)
				if e != nil {
					return e
				}
				ref, e := os.ReadFile(path)
				if e != nil || len(ref) > 16<<20 {
					return providerFailure("invalid_provider_response")
				}
				result, err = adapter.GenerateWithReference(ctx, input, ref, refMIME)
			} else {
				result, err = c.image.Generate(ctx, input)
			}
			data = result.Data
		}
	case "speech":
		mime = "audio/wav"
		role = "voice"
		data, err = c.speech.Speech(ctx, job.Prompt)
	case "video":
		mime = "video/mp4"
		role = "shot"
		var v VideoJob
		if job.UpstreamID == "" {
			if job.ReferenceAssetID != "" {
				adapter, ok := c.video.(referenceVideoProvider)
				if !ok {
					return providerFailure("unsupported_capability")
				}
				path, refMIME, e := s.media.file(job.ProjectID, job.SpaceID, job.ReferenceAssetID, false, false)
				if e != nil {
					return e
				}
				ref, e := os.ReadFile(path)
				if e != nil || len(ref) > 10<<20 {
					return providerFailure("invalid_provider_response")
				}
				v, err = adapter.SubmitVideoWithImage(ctx, job.Prompt, ref, refMIME)
			} else {
				v, err = c.video.SubmitVideo(ctx, job.Prompt)
			}
			if err != nil {
				return err
			}
			job.UpstreamID = v.ID
			if err = c.updateJob(*job); err != nil {
				return err
			}
		}
		for {
			v, err = c.video.PollVideo(ctx, job.UpstreamID)
			if err != nil {
				return err
			}
			if v.Status == "failed" {
				return providerFailure("provider_rejected")
			}
			if v.Status == "completed" {
				break
			}
			timer := time.NewTimer(2 * time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}
		data, err = c.video.DownloadVideo(ctx, job.UpstreamID)
	}
	if err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	// Asset ingest has its own persistent idempotency receipt. Restart cannot
	// publish duplicate assets even if ingestion succeeded before job completion.
	_, err = s.media.AddAsset(job.ProjectID, job.SpaceID, "creative-"+job.ID, "创作素材 · "+job.Kind, role, job.Character, job.ShotID, job.Provider, data, mime)
	if err != nil {
		return err
	}
	asset, err := s.media.AssetForReceipt(job.ProjectID, job.SpaceID, "creative-"+job.ID)
	if err != nil {
		return err
	}
	job.AssetID = asset.ID
	return nil
}

// Resume only reads a known asynchronous upstream job. It can never issue a
// generation POST, including after a crash between acknowledgment and polling.
func (s *FileStore) resumeCreativeJob(space, id, jid, key string) (CreativeJob, error) {
	if _, err := s.GetProject(space, id); err != nil {
		return CreativeJob{}, err
	}
	c := s.creative
	c.mu.Lock()
	defer c.mu.Unlock()
	job, ok := c.data.Jobs[jid]
	if !ok || job.SpaceID != space || job.ProjectID != id {
		return CreativeJob{}, ErrNotFound
	}
	fingerprint, _ := projectFingerprint("resume-creative", jid, id)
	rk := projectReceiptKey(space, key)
	if r, ok := c.data.Receipts[rk]; ok {
		if r.Fingerprint != fingerprint {
			return CreativeJob{}, ErrIdempotencyConflict
		}
		return job, nil
	}
	if _, running := c.cancels[jid]; running {
		return CreativeJob{}, ErrGenerationActive
	}
	if job.Kind != "video" || job.UpstreamID == "" || (job.Status != "failed" && job.Status != "cancelled") {
		return CreativeJob{}, ErrConflict
	}
	caps := c.mediaCaps()["video"]
	if !caps.Ready {
		return CreativeJob{}, ErrProviderNotReady
	}
	if caps.Provider != job.Provider || job.Model == "" || caps.Model != job.Model {
		return CreativeJob{}, ErrProviderChanged
	}
	for _, active := range c.data.Jobs {
		if active.ProjectID == id && active.SpaceID == space && (active.Status == "queued" || active.Status == "running") {
			return CreativeJob{}, ErrGenerationActive
		}
	}
	if len(c.data.Receipts) >= 1000 {
		return CreativeJob{}, ErrProviderNotReady
	}
	old := job
	job.Status = "queued"
	job.Error = ""
	job.ErrorCode = ""
	job.UpdatedAt = time.Now().UTC()
	c.data.Jobs[jid] = job
	c.data.Receipts[rk] = creativeReceipt{Fingerprint: fingerprint, JobID: jid}
	if err := c.saveLocked(); err != nil {
		c.data.Jobs[jid] = old
		delete(c.data.Receipts, rk)
		return CreativeJob{}, err
	}
	return job, nil
}

type referenceVideoProvider interface {
	SubmitVideoWithImage(context.Context, string, []byte, string) (VideoJob, error)
}

func (s *FileStore) creativeGenerationModel() string {
	if s.creative == nil {
		return ""
	}
	return s.creative.caps.Model
}
