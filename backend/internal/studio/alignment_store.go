package studio

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/adcwb/ai-creative-studio/backend/internal/jobqueue"
	"github.com/adcwb/ai-creative-studio/backend/internal/tracing"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"
)

type AlignmentInput struct {
	ModelRequest
	ClipID           string `json:"clip_id"`
	AudioAssetID     string `json:"audio_asset_id,omitempty"`
	Mode             string `json:"mode"`
	Text             string `json:"text,omitempty"`
	ExpectedVersion  int    `json:"expected_version"`
	ExpectedProvider string `json:"expected_provider,omitempty"`
	ExpectedModel    string `json:"expected_model,omitempty"`
}
type AlignmentTask struct {
	TraceParent      string            `json:"trace_parent,omitempty"`
	RuntimeSelection *RuntimeSelection `json:"runtime_selection,omitempty"`
	ID               string            `json:"id"`
	ProjectID        string            `json:"project_id"`
	SpaceID          string            `json:"space_id"`
	ClipID           string            `json:"clip_id"`
	AudioAssetID     string            `json:"audio_asset_id"`
	AudioMIME        string            `json:"audio_mime"`
	AudioDuration    float64           `json:"audio_duration"`
	Mode             string            `json:"mode"`
	Provider         string            `json:"provider"`
	Model            string            `json:"model,omitempty"`
	Status           string            `json:"status"`
	SourceVersion    int               `json:"source_version"`
	Duration         float64           `json:"duration"`
	TrimStart        float64           `json:"trim_start"`
	Text             string            `json:"text"`
	Cues             []SubtitleCue     `json:"cues"`
	Warnings         []string          `json:"warnings"`
	TimingBasis      string            `json:"timing_basis"`
	Error            string            `json:"error,omitempty"`
	ErrorCode        string            `json:"error_code,omitempty"`
	CreatedAt        time.Time         `json:"created_at"`
	UpdatedAt        time.Time         `json:"updated_at"`
}
type alignmentReceipt struct {
	Digest string `json:"digest"`
	TaskID string `json:"task_id"`
}
type alignmentDisk struct {
	Version  int                         `json:"version"`
	Tasks    map[string]AlignmentTask    `json:"tasks"`
	Receipts map[string]alignmentReceipt `json:"receipts"`
}
type AlignmentStore struct {
	pg         *postgresScope
	execution  *ExecutionGovernor
	runtime    *ModelRuntime
	forced     AlignmentProvider
	forcedCaps Capabilities
	mu         stateMutex
	path       string
	data       alignmentDisk
	provider   AlignmentProvider
	caps       Capabilities
	cancels    map[string]context.CancelFunc
	wg         sync.WaitGroup
	failure    error
	closed     bool
}

func estimateAlignmentCapabilities() Capabilities {
	return Capabilities{Provider: "local-text-estimate", Mode: "mock", Ready: true, Message: "按文本估算：仅按句子字数分配时长，不识别音频、停顿或说话人；必须试听并人工校准。"}
}
func openAlignmentStore(dir string, database ...*sql.DB) (*AlignmentStore, error) {
	a := &AlignmentStore{path: filepath.Join(dir, "alignments.json"), data: alignmentDisk{Version: 1, Tasks: map[string]AlignmentTask{}, Receipts: map[string]alignmentReceipt{}}, cancels: map[string]context.CancelFunc{}, caps: Capabilities{Provider: "unavailable", Mode: "unavailable", Message: "未配置ASR；可按文本估算并人工校准。"}}
	a.forcedCaps = Capabilities{Provider: "unavailable", Mode: "unavailable", Message: "未配置强制对齐模型。"}
	if len(database) > 0 {
		var err error
		a.pg, err = attachPostgres(database[0], "alignment", &a.data, &a.mu)
		return a, err
	}
	b, err := os.ReadFile(a.path)
	if err == nil {
		var d alignmentDisk
		if json.Unmarshal(b, &d) != nil || d.Version != 1 || d.Tasks == nil || d.Receipts == nil {
			return nil, errors.New("invalid alignment state; refusing reset")
		}
		a.data = d
		for id, t := range d.Tasks {
			if t.ID != id || !mediaFileID(id) || t.SourceVersion < 1 || t.ProjectID == "" || t.SpaceID == "" || t.Cues == nil || t.Warnings == nil || (t.Mode != "estimate" && t.Mode != "asr" && t.Mode != "forced") {
				return nil, errors.New("invalid alignment task")
			}
			if t.Status != "queued" && t.Status != "running" && t.Status != "succeeded" && t.Status != "failed" && t.Status != "cancelled" {
				return nil, errors.New("invalid alignment status")
			}
			if t.Status == "succeeded" {
				if len(t.Cues) == 0 || validateSubtitleCues(t.Cues, t.Duration) != nil {
					return nil, errors.New("invalid persisted alignment result")
				}
			}
			if t.Status == "queued" || t.Status == "running" {
				t.Status = "failed"
				t.ErrorCode = "execution_interrupted"
				t.Error = "字幕对齐执行中断，未自动重试；真实ASR费用可能不确定，请确认后再新建任务。"
				t.UpdatedAt = time.Now().UTC()
				a.data.Tasks[id] = t
			}
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	if err = a.commitLocked(a.data); err != nil {
		return nil, err
	}
	return a, nil
}
func (a *AlignmentStore) cloneLocked() alignmentDisk {
	b, _ := json.Marshal(a.data)
	var d alignmentDisk
	_ = json.Unmarshal(b, &d)
	return d
}
func (a *AlignmentStore) commitLocked(d alignmentDisk) (err error) {
	if a.pg != nil {
		if err := a.pg.save(d); err != nil {
			return err
		}
		a.data = d
		return nil
	}
	defer func() {
		if err != nil {
			a.failure = err
		}
	}()
	b, err := json.Marshal(d)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(a.path), ".alignment-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(b); err == nil {
		err = f.Sync()
	}
	err = errors.Join(err, f.Close())
	if err != nil {
		return err
	}
	if err = os.Rename(f.Name(), a.path); err != nil {
		return err
	}
	var committed alignmentDisk
	_ = json.Unmarshal(b, &committed)
	a.data = committed
	return nil
}
func SetAlignmentProvider(s *FileStore, p AlignmentProvider, caps Capabilities) {
	a := s.media.alignment
	a.mu.Lock()
	defer a.mu.Unlock()
	a.provider = p
	a.caps = caps
}
func (a *AlignmentStore) capabilities() map[string]Capabilities {
	a.mu.Lock()
	defer a.mu.Unlock()
	return map[string]Capabilities{"estimate": estimateAlignmentCapabilities(), "asr": a.caps, "forced": a.forcedCaps}
}
func (a *AlignmentStore) close() {
	a.mu.Lock()
	a.closed = true
	for _, cancel := range a.cancels {
		cancel()
	}
	a.mu.Unlock()
	a.wg.Wait()
}
func (m *MediaStore) CreateAlignment(project, space, key string, in AlignmentInput) (AlignmentTask, bool, error) {
	if in.ExpectedVersion < 1 || !shotIDPattern.MatchString(in.ClipID) || (in.Mode != "estimate" && in.Mode != "asr" && in.Mode != "forced") {
		return AlignmentTask{}, false, mediaInvalid("字幕对齐输入无效")
	}
	in.Text = strings.TrimSpace(in.Text)
	if len([]rune(in.Text)) > 2000 || (in.Mode != "asr" && in.Text == "") {
		return AlignmentTask{}, false, mediaInvalid("估算文本需1–2000字")
	}
	if in.Mode == "asr" && in.Text != "" {
		return AlignmentTask{}, false, mediaInvalid("ASR只识别已上传音频，不接收预填剧本文字")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failure != nil {
		return AlignmentTask{}, false, m.failure
	}
	p := mediaState(m.data, mediaKey(space, project))
	a := m.alignment
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.failure != nil {
		return AlignmentTask{}, false, a.failure
	}
	if a.closed {
		return AlignmentTask{}, false, errors.New("alignment store closed")
	}
	rk := mediaKey(space, project) + ":" + key
	digest := mediaDigest([]any{"alignment", in})
	if receipt, ok := a.data.Receipts[rk]; ok {
		if receipt.Digest != digest {
			return AlignmentTask{}, false, errMediaKey
		}
		return cloneAlignmentTask(a.data.Tasks[receipt.TaskID]), false, nil
	}
	if p.Version != in.ExpectedVersion {
		return AlignmentTask{}, false, errMediaVersion
	}
	var clip MediaClip
	for _, c := range p.Timeline.Clips {
		if c.ID == in.ClipID {
			clip = c
			break
		}
	}
	if clip.ID == "" {
		return AlignmentTask{}, false, mediaInvalid("请先保存包含此镜头的时间轴")
	}
	if in.AudioAssetID != "" && in.AudioAssetID != clip.AudioAssetID {
		return AlignmentTask{}, false, mediaInvalid("音频必须是当前镜头已保存的配音素材")
	}
	var audio MediaAsset
	if clip.AudioAssetID != "" {
		audio, _ = mediaAsset(p, clip.AudioAssetID)
	}
	if in.Mode != "estimate" && (in.AudioAssetID == "" || audio.ID == "" || audio.Kind != "audio") {
		return AlignmentTask{}, false, mediaInvalid("ASR需要绑定当前镜头的WAV/MP3配音素材")
	}
	caps := estimateAlignmentCapabilities()
	if in.Mode == "asr" {
		caps = a.caps
	}
	if in.Mode == "forced" {
		caps = a.forcedCaps
	}
	if in.selection != nil {
		caps = in.selectedCaps
	}
	if !caps.Ready || (in.selection == nil && ((in.Mode == "asr" && a.provider == nil) || (in.Mode == "forced" && a.forced == nil))) {
		return AlignmentTask{}, false, ErrProviderNotReady
	}
	if (in.ExpectedProvider != "" && in.ExpectedProvider != caps.Provider) || (in.Mode != "estimate" && in.selection == nil && (in.ExpectedProvider != caps.Provider || in.ExpectedModel == "" || in.ExpectedModel != caps.Model)) || (in.ExpectedModel != "" && in.ExpectedModel != caps.Model) {
		return AlignmentTask{}, false, ErrProviderChanged
	}
	recent, paid, active := 0, 0, 0
	for _, task := range a.data.Tasks {
		if task.CreatedAt.After(time.Now().Add(-time.Hour)) {
			recent++
			if task.Mode == "asr" {
				paid++
			}
		}
		if task.Status == "queued" || task.Status == "running" {
			active++
			if task.ProjectID == project && task.SpaceID == space {
				return AlignmentTask{}, false, ErrGenerationActive
			}
		}
	}
	if len(a.data.Tasks) >= 500 || len(a.data.Receipts) >= 1000 || recent >= 20 || active >= 2 || (in.Mode == "asr" && paid >= 10) {
		return AlignmentTask{}, false, errMediaLimit
	}
	if in.Mode == "estimate" {
		if _, err := estimateSubtitleCues(in.Text, clip.Duration); err != nil {
			return AlignmentTask{}, false, err
		}
	}
	now := time.Now().UTC()
	task := AlignmentTask{TraceParent: in.traceParent, RuntimeSelection: cloneRuntimeSelection(in.selection), ID: mediaID(), ProjectID: project, SpaceID: space, ClipID: clip.ID, AudioAssetID: clip.AudioAssetID, AudioMIME: audio.MIMEType, AudioDuration: audio.Duration, Mode: in.Mode, Provider: caps.Provider, Model: caps.Model, Status: "queued", SourceVersion: p.Version, Duration: clip.Duration, TrimStart: clip.TrimStart, Text: in.Text, Cues: []SubtitleCue{}, Warnings: []string{}, CreatedAt: now, UpdatedAt: now}
	d := a.cloneLocked()
	d.Tasks[task.ID] = task
	d.Receipts[rk] = alignmentReceipt{Digest: digest, TaskID: task.ID}
	if a.pg != nil {
		payload, _ := json.Marshal(map[string]string{"task_id": task.ID, "project_id": project})
		a.pg.beforeCommit = func(ctx context.Context, tx *sql.Tx) error {
			if task.RuntimeSelection != nil {
				sel := task.RuntimeSelection
				if err := ReservePostgresTx(ctx, tx, "alignment:"+task.ID, space, task.Provider, task.Model, sel.Currency, sel.MaximumMinor); err != nil {
					return err
				}
			}
			return jobqueue.EnqueueTx(ctx, tx, jobqueue.Job{TenantID: space, ID: task.ID, TraceParent: task.TraceParent, Kind: "alignment", IdempotencyKey: project + ":" + key, Payload: payload})
		}
		defer func() { a.pg.beforeCommit = nil }()
	}
	if err := a.commitLocked(d); err != nil {
		return AlignmentTask{}, false, err
	}
	if a.pg != nil {
		return task, true, nil
	}
	ctx, cancel := context.WithTimeout(tracing.WithParent(context.Background(), task.TraceParent), 3*time.Minute)
	a.cancels[task.ID] = cancel
	a.wg.Add(1)
	go func() { defer a.wg.Done(); defer cancel(); a.run(ctx, m, task.ID) }()
	return task, true, nil
}
func cloneAlignmentTask(t AlignmentTask) AlignmentTask {
	t.Cues = append([]SubtitleCue{}, t.Cues...)
	t.Warnings = append([]string{}, t.Warnings...)
	return t
}
func (a *AlignmentStore) list(project, space string) []AlignmentTask {
	a.mu.Lock()
	defer a.mu.Unlock()
	tasks := []AlignmentTask{}
	for _, t := range a.data.Tasks {
		if t.ProjectID == project && t.SpaceID == space {
			tasks = append(tasks, cloneAlignmentTask(t))
		}
	}
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].CreatedAt.After(tasks[j].CreatedAt) })
	return tasks
}
func (a *AlignmentStore) cancel(project, space, key, id string) (AlignmentTask, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.failure != nil {
		return AlignmentTask{}, a.failure
	}
	t, ok := a.data.Tasks[id]
	if !ok || t.ProjectID != project || t.SpaceID != space {
		return AlignmentTask{}, ErrNotFound
	}
	rk := mediaKey(space, project) + ":" + key
	digest := mediaDigest([]string{"cancel-alignment", id})
	if receipt, ok := a.data.Receipts[rk]; ok {
		if receipt.Digest != digest {
			return AlignmentTask{}, errMediaKey
		}
		return cloneAlignmentTask(t), nil
	}
	if len(a.data.Receipts) >= 1000 {
		return AlignmentTask{}, errMediaLimit
	}
	if t.Status == "succeeded" || t.Status == "failed" {
		return AlignmentTask{}, ErrConflict
	}
	t.Status = "cancelled"
	t.Error = "已停止本地对齐处理，不能撤销已发送的ASR请求或费用。"
	t.UpdatedAt = time.Now().UTC()
	d := a.cloneLocked()
	d.Tasks[id] = t
	d.Receipts[rk] = alignmentReceipt{Digest: digest, TaskID: id}
	if a.pg != nil {
		a.pg.beforeCommit = func(ctx context.Context, tx *sql.Tx) error {
			return CancelQueuedPostgresTx(ctx, tx, space, id, "alignment")
		}
		defer func() { a.pg.beforeCommit = nil }()
	}
	if err := a.commitLocked(d); err != nil {
		return AlignmentTask{}, err
	}
	if cancel := a.cancels[id]; cancel != nil {
		cancel()
	}
	return cloneAlignmentTask(t), nil
}
func (a *AlignmentStore) run(ctx context.Context, m *MediaStore, id string) {
	_ = a.runTask(ctx, m, id, nil)
}
func (a *AlignmentStore) runTask(ctx context.Context, m *MediaStore, id string, lease *jobqueue.Job) error {
	a.mu.Lock()
	task := a.data.Tasks[id]
	if task.Status != "queued" || a.closed {
		delete(a.cancels, id)
		a.mu.Unlock()
		return context.Canceled
	}
	task.Status = "running"
	task.UpdatedAt = time.Now().UTC()
	d := a.cloneLocked()
	d.Tasks[id] = task
	if lease != nil {
		a.pg.beforeCommit = func(ctx context.Context, tx *sql.Tx) error { return jobqueue.CheckLeaseTx(ctx, tx, *lease) }
	}
	initialErr := a.commitLocked(d)
	if a.pg != nil {
		a.pg.beforeCommit = nil
	}
	if initialErr != nil {
		delete(a.cancels, id)
		a.mu.Unlock()
		return initialErr
	}
	provider, caps := a.provider, a.caps
	if task.Mode == "forced" {
		provider, caps = a.forced, a.forcedCaps
	}
	a.mu.Unlock()
	var err error
	if task.Mode == "estimate" {
		task.Cues, err = estimateSubtitleCues(task.Text, task.Duration)
		task.TimingBasis = "text-estimate"
		task.Warnings = []string{"按文本估算，不识别音频；停顿、语速和说话人均未检测，请试听并人工调整。"}
	} else if task.RuntimeSelection == nil && (provider == nil || !caps.Ready || caps.Provider != task.Provider || caps.Model == "" || caps.Model != task.Model) {
		err = providerFailure("provider_unavailable")
	} else {
		var path, mime string
		path, mime, err = m.file(task.ProjectID, task.SpaceID, task.AudioAssetID, false, false)
		if err == nil {
			var data []byte
			var stat os.FileInfo
			stat, err = os.Stat(path)
			if err == nil && (stat.Size() <= 0 || stat.Size() > 24<<20) {
				err = mediaInvalid("ASR音频最多24MiB")
			}
			if err == nil {
				data, err = os.ReadFile(path)
			}
			if err == nil {
				var result AlignmentResult
				if task.RuntimeSelection != nil {
					err = a.execution.ExecuteRuntime(ctx, a.runtime, alignmentExecutionRequest(task, data, mime), func(ctx context.Context, p RuntimeProviders) error {
						if p.Alignment == nil {
							return ErrExecutionGovernance
						}
						var runErr error
						result, runErr = p.Alignment.Transcribe(ctx, data, mime, task.AudioDuration)
						if runErr == nil {
							raw, _ := json.Marshal(result)
							runErr = a.execution.ReviewResult(ctx, ExecutionRequest{ID: "alignment:" + task.ID, Selection: task.RuntimeSelection, Tenant: task.SpaceID, Provider: task.Provider, Model: task.Model, Capability: "text", Text: string(raw)})
						}
						return runErr
					})
				} else if task.Mode == "forced" {
					aligner, ok := provider.(ForcedAlignmentProvider)
					if !ok {
						err = providerFailure("unsupported_capability")
					} else {
						result, err = aligner.Align(ctx, data, mime, task.AudioDuration, task.Text)
					}
				} else {
					result, err = provider.Transcribe(ctx, data, mime, task.AudioDuration)
				}
				if err == nil {
					task.Cues = []SubtitleCue{}
					task.TimingBasis = result.TimingBasis
					clipped := false
					for _, cue := range result.Cues {
						if cue.Start >= task.Duration {
							clipped = true
							continue
						}
						if cue.End > task.Duration {
							cue.End = task.Duration
							clipped = true
						}
						task.Cues = append(task.Cues, cue)
					}
					if clipped {
						task.Warnings = append(task.Warnings, "已按镜头时长裁切超出范围的识别字幕；配音也会按此镜头时长结束。")
					}
					task.Warnings = append(task.Warnings, "模型对齐结果仍需试听校验；本项目尚未验证实际模型准确率及费用。")
					if len(task.Cues) == 0 {
						err = mediaInvalid("镜头时间范围内没有识别字幕")
					} else {
						err = validateSubtitleCues(task.Cues, task.Duration)
					}
				}
			}
		}
	}
	if err == nil {
		err = ctx.Err()
	}
	if lease != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	defer delete(a.cancels, id)
	if a.data.Tasks[id].Status == "cancelled" || a.closed {
		return context.Canceled
	}
	task.UpdatedAt = time.Now().UTC()
	if err != nil {
		task.Status = "failed"
		task.Cues = []SubtitleCue{}
		task.ErrorCode, task.Error = creativeFailure(err)
	} else {
		task.Status = "succeeded"
	}
	d = a.cloneLocked()
	d.Tasks[id] = task
	if lease != nil {
		state := "succeeded"
		if err != nil {
			state = "failed"
		}
		a.pg.beforeCommit = func(ctx context.Context, tx *sql.Tx) error {
			return jobqueue.FinishTx(ctx, tx, *lease, state, json.RawMessage(`{}`))
		}
		defer func() { a.pg.beforeCommit = nil }()
	}
	return a.commitLocked(d)
}

// Called while media.mu is held. Alignment workers never hold alignment.mu
// while acquiring media.mu, keeping the lock order stable.
func (m *MediaStore) validateAlignmentSources(project, space string, t MediaTimeline) error {
	a := m.alignment
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, clip := range t.Clips {
		if clip.SubtitleSource != "" && clip.SubtitleSource != "manual" && clip.SubtitleSource != "estimated" && clip.SubtitleSource != "asr" && clip.SubtitleSource != "forced" {
			return mediaInvalid("字幕来源无效")
		}
		if clip.SubtitleAlignmentID == "" {
			if clip.SubtitleSource == "estimated" || clip.SubtitleSource == "asr" || clip.SubtitleSource == "forced" {
				return mediaInvalid("估算或ASR来源必须引用成功对齐任务")
			}
			continue
		}
		task, ok := a.data.Tasks[clip.SubtitleAlignmentID]
		if !ok || task.ProjectID != project || task.SpaceID != space || task.Status != "succeeded" || task.ClipID != clip.ID {
			return mediaInvalid("字幕来源任务无效")
		}
		if clip.SubtitleSource == "estimated" || clip.SubtitleSource == "asr" || clip.SubtitleSource == "forced" {
			mode := "estimate"
			if clip.SubtitleSource == "asr" || clip.SubtitleSource == "forced" {
				mode = clip.SubtitleSource
			}
			if task.Mode != mode || task.Duration != clip.Duration || task.TrimStart != clip.TrimStart || task.AudioAssetID != clip.AudioAssetID || !reflect.DeepEqual(task.Cues, clip.SubtitleCues) {
				return mediaInvalid("时间、音频或字幕已修改，请将字幕来源设为manual并人工确认")
			}
		}
	}
	return nil
}

// Polling must surface a failed durable write instead of reporting a task as
// running after its worker has stopped. Recovery requires reopening the store.
func (a *AlignmentStore) available() error { a.mu.Lock(); defer a.mu.Unlock(); return a.failure }
