package application

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/adcwb/ai-creative-studio/backend/internal/platform/jobqueue"
	"github.com/adcwb/ai-creative-studio/backend/internal/platform/tracing"
)

type CreativeJobInput struct {
	ReferenceAssetIDs      []string `json:"reference_asset_ids,omitempty"`
	FirstFrameAssetID      string   `json:"first_frame_asset_id,omitempty"`
	LastFrameAssetID       string   `json:"last_frame_asset_id,omitempty"`
	MotionReferenceAssetID string   `json:"motion_reference_asset_id,omitempty"`

	ModelRequest
	consentActor          string
	AvatarID              string `json:"avatar_id,omitempty"`
	VoiceConsentReference string `json:"voice_consent_reference,omitempty"`
	VoiceConfirmed        bool   `json:"voice_confirmed,omitempty"`
	AudioAssetID          string `json:"audio_asset_id,omitempty"`
	ComparisonAssetID     string `json:"comparison_asset_id,omitempty"`
	MaskAssetID           string `json:"mask_asset_id,omitempty"`
	ExpectedModel         string `json:"expected_model,omitempty"`
	ReferenceAssetID      string `json:"reference_asset_id,omitempty"`
	Kind                  string `json:"kind"`
	ExpectedVersion       int    `json:"expected_version"`
	ExpectedProvider      string `json:"expected_provider,omitempty"`
	Prompt                string `json:"prompt,omitempty"`
	ShotID                string `json:"shot_id,omitempty"`
	Character             string `json:"character,omitempty"`
}
type CreativeJob struct {
	ReferenceAssetIDs      []string `json:"reference_asset_ids,omitempty"`
	FirstFrameAssetID      string   `json:"first_frame_asset_id,omitempty"`
	LastFrameAssetID       string   `json:"last_frame_asset_id,omitempty"`
	MotionReferenceAssetID string   `json:"motion_reference_asset_id,omitempty"`

	VoiceConsentBy        string `json:"voice_consent_by,omitempty"`
	AvatarID              string `json:"avatar_id,omitempty"`
	VoiceConsentReference string `json:"voice_consent_reference,omitempty"`
	VoiceConfirmed        bool   `json:"voice_confirmed,omitempty"`
	AIGenerated           bool   `json:"ai_generated,omitempty"`
	TraceParent           string `json:"trace_parent,omitempty"`
	lease                 *jobqueue.Job
	RuntimeSelection      *RuntimeSelection `json:"runtime_selection,omitempty"`
	AudioAssetID          string            `json:"audio_asset_id,omitempty"`
	ComparisonAssetID     string            `json:"comparison_asset_id,omitempty"`
	MaskAssetID           string            `json:"mask_asset_id,omitempty"`
	Model                 string            `json:"model,omitempty"`
	ReferenceAssetID      string            `json:"reference_asset_id,omitempty"`
	ID                    string            `json:"id"`
	SpaceID               string            `json:"space_id"`
	ProjectID             string            `json:"project_id"`
	Kind                  string            `json:"kind"`
	Provider              string            `json:"provider"`
	Status                string            `json:"status"`
	SourceVersion         int               `json:"source_version"`
	Prompt                string            `json:"prompt"`
	ShotID                string            `json:"shot_id"`
	Character             string            `json:"character"`
	UpstreamID            string            `json:"upstream_id,omitempty"`
	AssetID               string            `json:"asset_id,omitempty"`
	Analysis              *BriefAnalysis    `json:"analysis,omitempty"`
	Error                 string            `json:"error,omitempty"`
	ErrorCode             string            `json:"error_code,omitempty"`
	CreatedAt             time.Time         `json:"created_at"`
	UpdatedAt             time.Time         `json:"updated_at"`
	Source                ProjectInput      `json:"source"`
}
type creativeReceipt struct {
	AvatarID    string `json:"avatar_id,omitempty"`
	Fingerprint string `json:"fingerprint"`
	JobID       string `json:"job_id"`
}
type creativeDisk struct {
	DigitalHumans map[string]DigitalHuman    `json:"digital_humans,omitempty"`
	Version       int                        `json:"version"`
	Jobs          map[string]CreativeJob     `json:"jobs"`
	Receipts      map[string]creativeReceipt `json:"receipts"`
}
type creativeStore struct {
	avatarPhoto            PhotoAvatarProvider
	avatarPhotoCaps        Capabilities
	avatarLicenseConfirmed bool
	pg                     *postgresScope
	soundEffect            MusicProvider
	soundEffectCaps        Capabilities
	lipSync                LipSyncProvider
	lipSyncCaps            Capabilities
	music                  MusicProvider
	musicCaps              Capabilities
	speechCaps, videoCaps  Capabilities
	mu                     stateMutex
	path                   string
	data                   creativeDisk
	provider               CreativeProvider
	speech                 CreativeProvider
	video                  CreativeProvider
	image                  ImageProvider
	caps                   Capabilities
	imageCaps              Capabilities
	cancels                map[string]context.CancelFunc
}

func openCreativeStore(dir string, database ...*sql.DB) (*creativeStore, error) {
	c := &creativeStore{path: filepath.Join(dir, "creative.json"), data: creativeDisk{Version: 1, Jobs: map[string]CreativeJob{}, Receipts: map[string]creativeReceipt{}}, cancels: map[string]context.CancelFunc{}}
	_, c.caps = ConfigureCreativeProvider("mock-text", "")
	c.imageCaps = Capabilities{Provider: "local-synthetic", Mode: "mock", Ready: true, Message: "合成色卡图片，不是AI生成。"}
	c.musicCaps = Capabilities{Provider: "unavailable", Mode: "unavailable", Message: "未配置音乐生成。"}
	c.lipSyncCaps = Capabilities{Provider: "unavailable", Mode: "unavailable", Message: "未配置口型同步模型。"}
	c.soundEffectCaps = Capabilities{Provider: "unavailable", Mode: "unavailable", Message: "未配置音效生成模型。"}
	if len(database) > 0 {
		var err error
		c.pg, err = attachPostgres(database[0], "creative", &c.data, &c.mu)
		return c, err
	}
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
				if (j.Kind == "video" || (j.Kind == "image" && j.Provider == "jimeng-images")) && j.UpstreamID != "" {
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
	if c.pg != nil {
		return c.pg.save(c.data)
	}
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
func (s *FileStore) guardCreativeLocked(expected, expectedModel string, selected ...Capabilities) error {
	if s.creative == nil {
		return nil
	}
	caps := s.creative.caps
	if len(selected) > 0 {
		caps = selected[0]
	}
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
	vision := c.caps
	if _, ok := c.provider.(VisionProvider); !ok || c.provider == nil {
		vision = Capabilities{Provider: "unavailable", Mode: "unavailable", Ready: false, Message: "请配置支持图像输入的文本模型；不会以本地模拟识别图片。"}
	}
	return map[string]Capabilities{"image": c.imageCaps, "speech": speech, "video": video, "vision": vision, "consistency": vision, "music": c.musicCaps, "lip_sync": c.lipSyncCaps, "sound_effect": c.soundEffectCaps, "digital_human_photo": c.avatarCaps("photo"), "digital_human_video": c.avatarCaps("video")}
}
func (s *FileStore) createCreativeJob(space, id, key string, in CreativeJobInput) (CreativeJob, bool, error) {
	if in.ExpectedVersion < 1 {
		return CreativeJob{}, false, ErrInvalidProjectInput
	}
	if in.Kind != "analysis" && in.Kind != "image" && in.Kind != "speech" && in.Kind != "video" && in.Kind != "vision" && in.Kind != "consistency" && in.Kind != "music" && in.Kind != "lip_sync" && in.Kind != "sound_effect" && in.Kind != "digital_human" {
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
		return cloneCreativeJob(c.data.Jobs[r.JobID]), false, nil
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
	if in.selection != nil {
		caps = in.selectedCaps
	}
	if in.Kind == "digital_human" {
		// This release only runs operator-installed avatar engines. Do not allow
		// a generic video model selection to bypass the avatar capability gate.
		if in.selection != nil || !in.VoiceConfirmed {
			return CreativeJob{}, false, ErrInvalidProjectInput
		}
		consent, e := normalizeProjectText(in.VoiceConsentReference, "声音授权依据", 3, 300)
		if e != nil {
			return CreativeJob{}, false, e
		}
		in.VoiceConsentReference = consent
		avatar, e := c.avatarLocked(space, id, in.AvatarID)
		if e != nil {
			return CreativeJob{}, false, e
		}
		if in.ReferenceAssetID != "" && in.ReferenceAssetID != avatar.ReferenceAssetID {
			return CreativeJob{}, false, ErrInvalidProjectInput
		}
		in.ReferenceAssetID = avatar.ReferenceAssetID
		caps = c.avatarCaps(avatar.Route)
		media := s.media.Get(id, space)
		reference, found := mediaAsset(media, avatar.ReferenceAssetID)
		if !found {
			return CreativeJob{}, false, ErrInvalidProjectInput
		}
		audio, found := mediaAsset(media, in.AudioAssetID)
		if !found || audio.Kind != "audio" {
			return CreativeJob{}, false, projectInputError("请选择本项目已授权的配音")
		}
		if e = s.validateAvatarMedia(id, space, reference, false); e != nil {
			return CreativeJob{}, false, e
		}
		if e = s.validateAvatarMedia(id, space, audio, true); e != nil {
			return CreativeJob{}, false, e
		}
		if avatar.Route == "video" && audio.Duration > reference.Duration+0.1 {
			return CreativeJob{}, false, projectInputError("配音不能长于参考视频，请裁剪配音或更换视频")
		}
	} else if in.AvatarID != "" || in.VoiceConfirmed || in.VoiceConsentReference != "" {
		return CreativeJob{}, false, ErrInvalidProjectInput
	}
	if err := s.validateCreativeControls(id, space, in, caps); err != nil {
		return CreativeJob{}, false, err
	}
	if !caps.Ready {
		return CreativeJob{}, false, ErrProviderNotReady
	}
	if (in.Kind == "vision" || in.Kind == "consistency") && in.ReferenceAssetID == "" {
		return CreativeJob{}, false, projectInputError("图像理解需要选择项目图片")
	}
	if in.ReferenceAssetID != "" && in.Kind != "digital_human" {
		if in.Kind != "image" && in.Kind != "video" && in.Kind != "vision" && in.Kind != "consistency" && in.Kind != "lip_sync" {
			return CreativeJob{}, false, ErrInvalidProjectInput
		}
		if p, ok := c.image.(*JimengImageProvider); ok && in.selection == nil && in.Kind == "image" && p.bridge == nil {
			return CreativeJob{}, false, projectInputError("即梦参考图需要显式配置受控对象桥接，默认未启用")
		}
		_, supportsImage := c.image.(ReferenceImageProvider)
		_, supportsVideo := c.video.(referenceVideoProvider)
		if in.selection != nil {
			supportsImage = s.runtime.imageReferenceSupported(*in.selection, caps.Provider, caps.Model)
			supportsVideo = caps.Provider == "gemini-video" || caps.Provider == "ark-video" || caps.Provider == "minimax-video"
		}
		if (in.Kind == "image" && !supportsImage) || (in.Kind == "video" && !supportsVideo) {
			return CreativeJob{}, false, projectInputError("当前图像适配器不支持参考图；不会忽略参考素材生成")
		}
		path, mime, e := s.media.file(id, space, in.ReferenceAssetID, false, false)
		_, supportsVideoEdit := c.video.(editVideoProvider)
		if in.selection != nil {
			supportsVideoEdit = caps.Provider == "gemini-video"
		}
		if e != nil || path == "" || (mime != "image/png" && mime != "image/jpeg" && !((in.Kind == "video" && supportsVideoEdit || in.Kind == "lip_sync") && mime == "video/mp4")) {
			return CreativeJob{}, false, ErrInvalidProjectInput
		}
	}

	if in.Kind == "lip_sync" {
		_, videoMIME, e := s.media.file(id, space, in.ReferenceAssetID, false, false)
		if e != nil || videoMIME != "video/mp4" {
			return CreativeJob{}, false, ErrInvalidProjectInput
		}
		_, audioMIME, e := s.media.file(id, space, in.AudioAssetID, false, false)
		if e != nil || (audioMIME != "audio/wav" && audioMIME != "audio/mpeg") {
			return CreativeJob{}, false, ErrInvalidProjectInput
		}
	} else if in.AudioAssetID != "" && in.Kind != "digital_human" {
		return CreativeJob{}, false, ErrInvalidProjectInput
	}

	if in.Kind == "consistency" {
		if in.ComparisonAssetID == "" || in.ComparisonAssetID == in.ReferenceAssetID {
			return CreativeJob{}, false, projectInputError("请选择不同的参考图与镜头图")
		}
		path, mime, e := s.media.file(id, space, in.ComparisonAssetID, false, false)
		if e != nil || path == "" || (mime != "image/png" && mime != "image/jpeg") {
			return CreativeJob{}, false, ErrInvalidProjectInput
		}
	} else if in.ComparisonAssetID != "" {
		return CreativeJob{}, false, ErrInvalidProjectInput
	}

	if in.MaskAssetID != "" {
		if in.Kind != "image" || in.ReferenceAssetID == "" {
			return CreativeJob{}, false, ErrInvalidProjectInput
		}
		_, supportsMask := c.image.(MaskImageProvider)
		if in.selection != nil {
			supportsMask = caps.Provider == "openai-images" || caps.Provider == "gateway-images"
		}
		if !supportsMask {
			return CreativeJob{}, false, projectInputError("当前供应商不支持局部蒙版编辑")
		}
		path, mime, e := s.media.file(id, space, in.MaskAssetID, false, false)
		if e != nil || mime != "image/png" {
			return CreativeJob{}, false, ErrInvalidProjectInput
		}
		refPath, refMIME, e := s.media.file(id, space, in.ReferenceAssetID, false, false)
		if e != nil {
			return CreativeJob{}, false, ErrInvalidProjectInput
		}
		mask, e := os.ReadFile(path)
		if e != nil {
			return CreativeJob{}, false, e
		}
		ref, e := os.ReadFile(refPath)
		if e != nil {
			return CreativeJob{}, false, e
		}
		if validateEditMask(ref, refMIME, mask) != nil {
			return CreativeJob{}, false, projectInputError("蒙版须与参考图同尺寸，PNG透明区域编辑、不透明区域保留，最大4MiB")
		}
	}

	if (in.ExpectedProvider != "" && in.ExpectedProvider != caps.Provider) || (caps.Mode == "real" && in.selection == nil && (in.ExpectedProvider != caps.Provider || caps.Model == "" || in.ExpectedModel != caps.Model)) || (in.ExpectedModel != "" && in.ExpectedModel != caps.Model) {
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
	job := CreativeJob{ReferenceAssetIDs: append([]string(nil), in.ReferenceAssetIDs...), FirstFrameAssetID: in.FirstFrameAssetID, LastFrameAssetID: in.LastFrameAssetID, MotionReferenceAssetID: in.MotionReferenceAssetID, TraceParent: in.traceParent, RuntimeSelection: cloneRuntimeSelection(in.selection), AudioAssetID: in.AudioAssetID, ComparisonAssetID: in.ComparisonAssetID, MaskAssetID: in.MaskAssetID, ReferenceAssetID: in.ReferenceAssetID, ID: jid, SpaceID: space, ProjectID: id, Kind: in.Kind, Provider: caps.Provider, Model: caps.Model, Status: "queued", SourceVersion: p.Version, Prompt: in.Prompt, ShotID: in.ShotID, Character: in.Character, Source: ProjectInput{Title: p.Title, Brief: p.Brief}, CreatedAt: now, UpdatedAt: now}
	job.AvatarID, job.VoiceConsentReference, job.VoiceConfirmed, job.AIGenerated = in.AvatarID, in.VoiceConsentReference, in.VoiceConfirmed, in.Kind == "digital_human"
	if in.Kind == "digital_human" {
		job.VoiceConsentBy = in.consentActor
	}
	c.data.Jobs[jid] = job
	c.data.Receipts[rk] = creativeReceipt{Fingerprint: fingerprint, JobID: jid}
	if c.pg != nil {
		payload, _ := json.Marshal(map[string]string{"task_id": jid, "project_id": id})
		c.pg.beforeCommit = func(ctx context.Context, tx *sql.Tx) error {
			if job.RuntimeSelection != nil {
				sel := job.RuntimeSelection
				if err := ReservePostgresTx(ctx, tx, "creative:"+job.ID, space, job.Provider, job.Model, sel.Currency, sel.MaximumMinor); err != nil {
					return err
				}
			}
			return jobqueue.EnqueueTx(ctx, tx, jobqueue.Job{TenantID: space, ID: jid, TraceParent: job.TraceParent, Kind: "creative", IdempotencyKey: key, Payload: payload})
		}
		defer func() { c.pg.beforeCommit = nil }()
	}
	if err = c.saveLocked(); err != nil {
		delete(c.data.Jobs, jid)
		delete(c.data.Receipts, rk)
		return CreativeJob{}, false, err
	}
	return cloneCreativeJob(job), true, nil
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
			j.ReferenceAssetIDs = append([]string(nil), j.ReferenceAssetIDs...)
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
		return cloneCreativeJob(job), nil
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
	if c.pg != nil && old.Status != "cancelled" {
		c.pg.beforeCommit = func(ctx context.Context, tx *sql.Tx) error {
			return CancelQueuedPostgresTx(ctx, tx, space, jid, "creative")
		}
		defer func() { c.pg.beforeCommit = nil }()
	}
	if err := c.saveLocked(); err != nil {
		c.data.Jobs[jid] = old
		delete(c.data.Receipts, rk)
		return CreativeJob{}, err
	}
	if cancel := c.cancels[jid]; cancel != nil {
		cancel()
	}
	return cloneCreativeJob(job), nil
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
	if job.lease != nil && c.pg != nil {
		state := "succeeded"
		if job.Status != "succeeded" {
			state = "failed"
		}
		c.pg.beforeCommit = func(ctx context.Context, tx *sql.Tx) error {
			return jobqueue.FinishTx(ctx, tx, *job.lease, state, json.RawMessage(`{}`))
		}
		defer func() { c.pg.beforeCommit = nil }()
	}
	if err := c.saveLocked(); err != nil {
		c.data.Jobs[job.ID] = old
		return err
	}
	return nil
}

// Persist an acknowledged upstream identity even when local cancellation won
// the race. Keep the cancelled status and stop; a later resume only queries it.
func (c *creativeStore) recordCreativeUpstream(job *CreativeJob, id string) error {
	if id == "" {
		return providerFailure("invalid_provider_response")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	old, ok := c.data.Jobs[job.ID]
	if !ok {
		return ErrNotFound
	}
	if old.Provider != job.Provider || old.Model != job.Model || (old.Status != "running" && old.Status != "cancelled" && old.Status != "queued") || (old.UpstreamID != "" && old.UpstreamID != id) {
		return ErrConflict
	}
	updated := old
	updated.UpstreamID = id
	updated.UpdatedAt = time.Now().UTC()
	c.data.Jobs[job.ID] = updated
	if job.lease != nil && c.pg != nil {
		c.pg.beforeCommit = func(ctx context.Context, tx *sql.Tx) error { return jobqueue.CheckLeaseTx(ctx, tx, *job.lease) }
		defer func() { c.pg.beforeCommit = nil }()
	}
	if err := c.saveLocked(); err != nil {
		c.data.Jobs[job.ID] = old
		return err
	}
	job.UpstreamID = id
	if old.Status == "cancelled" {
		return context.Canceled
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
		runCtx, cancel := context.WithTimeout(tracing.WithParent(ctx, job.TraceParent), 10*time.Minute)
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
	if job.RuntimeSelection != nil {
		request, err := s.creativeExecutionRequest(*job)
		if err != nil {
			return err
		}
		return s.executeManaged(ctx, request, func(ctx context.Context, p RuntimeProviders) error { return s.executeCreativeJobWith(ctx, job, &p) })
	}
	return s.executeCreativeJobWith(ctx, job, nil)
}
func (s *FileStore) executeCreativeJobWith(ctx context.Context, job *CreativeJob, managed *RuntimeProviders) error {
	if job.Kind == "digital_human" {
		return s.executeDigitalHuman(ctx, job)
	}
	c := s.creative
	textProvider, imageProvider, speechProvider, videoProvider, musicProvider := c.provider, c.image, c.speech, c.video, c.music
	caps := c.mediaCaps()[job.Kind]
	textCaps := c.caps
	if managed != nil {
		textProvider, imageProvider, speechProvider, videoProvider, musicProvider = managed.Creative, managed.Image, managed.Creative, managed.Creative, managed.Music
		caps = Capabilities{Ready: true, Mode: "real", Provider: job.Provider, Model: job.Model}
		textCaps = caps
	}
	var data []byte
	var mime, role string
	var err error
	if job.Kind == "analysis" {
		if !textCaps.Ready || job.Provider != textCaps.Provider || (job.Provider != "mock-text" && (job.Model == "" || job.Model != textCaps.Model || textProvider == nil)) {
			return providerFailure("provider_unavailable")
		}
		var a BriefAnalysis
		if textCaps.Provider == "mock-text" {
			a = localBriefAnalysis(job.Source)
		} else {
			a, err = textProvider.Analyze(ctx, job.Source)
		}
		a.SourceVersion = job.SourceVersion
		job.Analysis = &a
		if err == nil && managed != nil {
			raw, _ := json.Marshal(a)
			err = s.execution.ReviewResult(ctx, ExecutionRequest{ID: "creative:" + job.ID, Selection: job.RuntimeSelection, Tenant: job.SpaceID, Provider: job.Provider, Model: job.Model, Capability: "text", Text: string(raw)})
		}
		return err
	}
	if job.Provider != caps.Provider || !caps.Ready || (caps.Mode == "real" && (job.Model == "" || job.Model != caps.Model)) {
		return providerFailure("provider_unavailable")
	}
	switch job.Kind {
	case "vision", "consistency":
		adapter, ok := textProvider.(VisionProvider)
		if !ok {
			return providerFailure("unsupported_capability")
		}
		path, refMIME, e := s.media.file(job.ProjectID, job.SpaceID, job.ReferenceAssetID, false, false)
		if e != nil {
			return e
		}
		ref, e := os.ReadFile(path)
		if e != nil || len(ref) > 4<<20 {
			return providerFailure("provider_rejected")
		}
		prompt := job.Prompt
		if job.Kind == "consistency" {
			otherPath, _, e := s.media.file(job.ProjectID, job.SpaceID, job.ComparisonAssetID, false, false)
			if e != nil {
				return e
			}
			other, e := os.ReadFile(otherPath)
			if e != nil {
				return e
			}
			ref, e = consistencyContactSheet(ref, other)
			if e != nil {
				return e
			}
			refMIME = "image/png"
			prompt = "Compare creative character continuity: LEFT is the reference, RIGHT is the shot. Report visible differences in costume, hair, palette and props and actionable corrections; describe uncertainty, never identify a person or guarantee identity. " + shortScriptText(job.Prompt, 1000)
		}
		analysis, e := adapter.DescribeImage(ctx, prompt, ref, refMIME)
		if e != nil {
			return e
		}
		analysis.SourceVersion = job.SourceVersion
		job.Analysis = &analysis
		if managed != nil {
			raw, _ := json.Marshal(analysis)
			if e = s.execution.ReviewResult(ctx, ExecutionRequest{ID: "creative:" + job.ID, Selection: job.RuntimeSelection, Tenant: job.SpaceID, Provider: job.Provider, Model: job.Model, Capability: "text", Text: string(raw)}); e != nil {
				return e
			}
		}
		return nil
	case "image":
		mime = "image/png"
		role = "shot"
		if job.Character != "" {
			role = "reference"
		}
		if imageProvider == nil {
			data = syntheticCreativePNG()
		} else {
			var result GenerationResult
			input := Input{Kind: "general", Prompt: job.Prompt, Ratio: "16:9", Style: "natural"}
			if len(job.ReferenceAssetIDs) > 0 {
				adapter, ok := imageProvider.(multiReferenceImageProvider)
				if !ok {
					return providerFailure("unsupported_capability")
				}
				refs, e := s.media.loadCreativeReferences(job.ProjectID, job.SpaceID, CreativeJobInput{ReferenceAssetIDs: job.ReferenceAssetIDs})
				if e != nil {
					return e
				}
				result, err = adapter.GenerateWithReferences(ctx, input, refs)
			} else if job.ReferenceAssetID != "" {
				adapter, ok := imageProvider.(ReferenceImageProvider)
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
				if job.MaskAssetID != "" {
					maskAdapter, ok := imageProvider.(MaskImageProvider)
					if !ok {
						return providerFailure("unsupported_capability")
					}
					maskPath, maskMIME, e := s.media.file(job.ProjectID, job.SpaceID, job.MaskAssetID, false, false)
					if e != nil || maskMIME != "image/png" {
						return ErrInvalidProjectInput
					}
					mask, e := os.ReadFile(maskPath)
					if e != nil {
						return e
					}
					result, err = maskAdapter.GenerateWithMask(ctx, input, ref, refMIME, mask)
				} else if async, ok := imageProvider.(asyncReferenceImageProvider); ok {
					if job.UpstreamID == "" {
						ack, e := async.SubmitImageWithReference(ctx, input, ref, refMIME)
						if e != nil {
							return e
						}
						if ack.ID == "" {
							return providerFailure("invalid_provider_response")
						}
						if e = c.recordCreativeUpstream(job, ack.ID); e != nil {
							return e
						}
					}
					result, err = awaitAsyncImage(ctx, async, job.UpstreamID, 2*time.Second)
				} else {
					result, err = adapter.GenerateWithReference(ctx, input, ref, refMIME)
				}
			} else {
				if async, ok := imageProvider.(AsyncImageProvider); ok {
					if job.UpstreamID == "" {
						ack, e := async.SubmitImage(ctx, input)
						if e != nil {
							return e
						}
						if ack.ID == "" {
							return providerFailure("invalid_provider_response")
						}
						if e = c.recordCreativeUpstream(job, ack.ID); e != nil {
							return e
						}
					}
					result, err = awaitAsyncImage(ctx, async, job.UpstreamID, 2*time.Second)
				} else if job.UpstreamID != "" {
					return providerFailure("provider_unavailable")
				} else {
					result, err = imageProvider.Generate(ctx, input)
				}
			}
			data = result.Data
		}
	case "lip_sync":
		if c.lipSync == nil {
			return providerFailure("provider_unavailable")
		}
		videoPath, _, e := s.media.file(job.ProjectID, job.SpaceID, job.ReferenceAssetID, false, false)
		if e != nil {
			return e
		}
		audioPath, audioMIME, e := s.media.file(job.ProjectID, job.SpaceID, job.AudioAssetID, false, false)
		if e != nil {
			return e
		}
		video, e := os.ReadFile(videoPath)
		if e != nil {
			return e
		}
		audio, e := os.ReadFile(audioPath)
		if e != nil {
			return e
		}
		mime = "video/mp4"
		role = "shot"
		data, err = c.lipSync.Sync(ctx, video, audio, audioMIME)
	case "sound_effect":
		if c.soundEffect == nil {
			return providerFailure("provider_unavailable")
		}
		mime = "audio/wav"
		role = "effect"
		data, err = c.soundEffect.Music(ctx, job.Prompt)
	case "music":
		mime = "audio/mpeg"
		role = "music"
		if musicProvider == nil {
			return providerFailure("unsupported_capability")
		}
		data, err = musicProvider.Music(ctx, job.Prompt)
	case "speech":
		mime = "audio/wav"
		role = "voice"
		data, err = speechProvider.Speech(ctx, job.Prompt)
	case "video":
		mime = "video/mp4"
		role = "shot"
		var v VideoJob
		if job.UpstreamID == "" {
			if len(job.ReferenceAssetIDs) > 0 || job.FirstFrameAssetID != "" || job.LastFrameAssetID != "" || job.MotionReferenceAssetID != "" {
				adapter, ok := videoProvider.(controlledVideoProvider)
				if !ok {
					return providerFailure("unsupported_capability")
				}
				refs, e := s.media.loadCreativeReferences(job.ProjectID, job.SpaceID, CreativeJobInput{ReferenceAssetIDs: job.ReferenceAssetIDs, FirstFrameAssetID: job.FirstFrameAssetID, LastFrameAssetID: job.LastFrameAssetID, MotionReferenceAssetID: job.MotionReferenceAssetID})
				if e != nil {
					return e
				}
				v, err = adapter.SubmitControlledVideo(ctx, job.Prompt, refs)
			} else if job.ReferenceAssetID != "" {
				adapter, ok := videoProvider.(referenceVideoProvider)
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
				if refMIME == "video/mp4" {
					editor, ok := videoProvider.(editVideoProvider)
					if !ok {
						return providerFailure("unsupported_capability")
					}
					v, err = editor.SubmitVideoWithVideo(ctx, job.Prompt, ref, refMIME)
				} else {
					v, err = adapter.SubmitVideoWithImage(ctx, job.Prompt, ref, refMIME)
				}
			} else {
				v, err = videoProvider.SubmitVideo(ctx, job.Prompt)
			}
			if err != nil {
				return err
			}
			if err = c.recordCreativeUpstream(job, v.ID); err != nil {
				return err
			}
		}
		for {
			v, err = videoProvider.PollVideo(ctx, job.UpstreamID)
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
		data, err = videoProvider.DownloadVideo(ctx, job.UpstreamID)
	}
	if err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if managed != nil {
		if err = s.execution.ReviewResult(ctx, ExecutionRequest{ID: "creative:" + job.ID, Selection: job.RuntimeSelection, Tenant: job.SpaceID, Provider: job.Provider, Model: job.Model, Capability: job.Kind, HasMedia: true, Media: []ExecutionMedia{{Data: data, MIME: mime}}}); err != nil {
			return err
		}
	}
	// Asset ingest has its own persistent idempotency receipt. Restart cannot
	// publish duplicate assets even if ingestion succeeded before job completion.
	_, err = s.media.AddAsset(job.ProjectID, job.SpaceID, "creative-"+job.ID, "创作素材 · "+job.Kind, role, job.Character, job.ShotID, job.Provider, data, mime, job.lease)
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
		return cloneCreativeJob(job), nil
	}
	if _, running := c.cancels[jid]; running {
		return CreativeJob{}, ErrGenerationActive
	}
	if (job.Kind != "video" && !(job.Kind == "image" && job.Provider == "jimeng-images")) || job.UpstreamID == "" || (job.Status != "failed" && job.Status != "cancelled") {
		return CreativeJob{}, ErrConflict
	}
	if c.pg != nil {
		if (job.Kind != "video" && !(job.Kind == "image" && job.Provider == "jimeng-images")) || job.RuntimeSelection == nil {
			return CreativeJob{}, ErrExecutionGovernance
		}
		request := ExecutionRequest{ID: "creative:" + job.ID, Tenant: job.SpaceID, Provider: job.Provider, Model: job.Model, Capability: job.RuntimeSelection.Capability, Selection: job.RuntimeSelection, UpstreamID: job.UpstreamID}
		if err := s.execution.RecoveryReady(context.Background(), s.runtime, request); err != nil {
			return CreativeJob{}, err
		}
	} else {
		caps := c.mediaCaps()[job.Kind]
		if job.Kind == "image" {
			if _, ok := c.image.(AsyncImageProvider); !ok {
				return CreativeJob{}, ErrProviderNotReady
			}
		}
		if !caps.Ready {
			return CreativeJob{}, ErrProviderNotReady
		}
		if caps.Provider != job.Provider || job.Model == "" || caps.Model != job.Model {
			return CreativeJob{}, ErrProviderChanged
		}
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
	if c.pg != nil {
		payload, _ := json.Marshal(map[string]string{"task_id": jid, "project_id": id})
		c.pg.beforeCommit = func(ctx context.Context, tx *sql.Tx) error {
			return jobqueue.ResumeTx(ctx, tx, jobqueue.Job{TenantID: space, ID: jid, Kind: "creative_resume", IdempotencyKey: key, Payload: payload})
		}
		defer func() { c.pg.beforeCommit = nil }()
	}
	if err := c.saveLocked(); err != nil {
		c.data.Jobs[jid] = old
		delete(c.data.Receipts, rk)
		return CreativeJob{}, err
	}
	return cloneCreativeJob(job), nil
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

type editVideoProvider interface {
	SubmitVideoWithVideo(context.Context, string, []byte, string) (VideoJob, error)
}

type asyncReferenceImageProvider interface {
	AsyncImageProvider
	SubmitImageWithReference(context.Context, Input, []byte, string) (AsyncImageJob, error)
}

func cloneCreativeJob(job CreativeJob) CreativeJob {
	job.ReferenceAssetIDs = append([]string(nil), job.ReferenceAssetIDs...)
	return job
}
