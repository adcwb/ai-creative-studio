package studio

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/adcwb/ai-creative-studio/backend/internal/jobqueue"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type MediaAsset struct {
	ID        string  `json:"id"`
	Kind      string  `json:"kind"`
	MIMEType  string  `json:"mime_type"`
	Name      string  `json:"name"`
	Role      string  `json:"role"`
	Character string  `json:"character"`
	ShotID    string  `json:"shot_id"`
	Duration  float64 `json:"duration"`
	Source    string  `json:"source"`
	FileURL   string  `json:"file_url"`
}
type MediaClip struct {
	PreserveSourceAudio bool          `json:"preserve_source_audio,omitempty"`
	SourceAudioVolume   float64       `json:"source_audio_volume,omitempty"`
	Transition          string        `json:"transition,omitempty"`
	TransitionDuration  float64       `json:"transition_duration,omitempty"`
	SubtitleCues        []SubtitleCue `json:"subtitle_cues,omitempty"`
	SubtitleSource      string        `json:"subtitle_source,omitempty"`
	SubtitleAlignmentID string        `json:"subtitle_alignment_id,omitempty"`
	ID                  string        `json:"id"`
	AssetID             string        `json:"asset_id"`
	Duration            float64       `json:"duration"`
	TrimStart           float64       `json:"trim_start"`
	Subtitle            string        `json:"subtitle"`
	AudioAssetID        string        `json:"audio_asset_id"`
	AudioVolume         float64       `json:"audio_volume"`
	// A normalized crop, applied before fitting to the output frame.
	CropX      float64 `json:"crop_x"`
	CropY      float64 `json:"crop_y"`
	CropWidth  float64 `json:"crop_width"`
	CropHeight float64 `json:"crop_height"`
}
type MediaAudioTrack struct {
	ID        string  `json:"id"`
	AssetID   string  `json:"asset_id"`
	Start     float64 `json:"start"`
	TrimStart float64 `json:"trim_start"`
	Duration  float64 `json:"duration"`
	Volume    float64 `json:"volume"`
}
type MediaVideoOverlay struct {
	ID                  string  `json:"id"`
	AssetID             string  `json:"asset_id"`
	Start               float64 `json:"start"`
	TrimStart           float64 `json:"trim_start"`
	Duration            float64 `json:"duration"`
	X                   float64 `json:"x"`
	Y                   float64 `json:"y"`
	Width               float64 `json:"width"`
	Height              float64 `json:"height"`
	Opacity             float64 `json:"opacity"`
	PreserveSourceAudio bool    `json:"preserve_source_audio"`
	SourceAudioVolume   float64 `json:"source_audio_volume"`
}
type MediaTimeline struct {
	VideoOverlays    []MediaVideoOverlay `json:"video_overlays,omitempty"`
	OutputResolution string              `json:"output_resolution,omitempty"`
	AspectRatio      string              `json:"aspect_ratio,omitempty"`
	SubtitleMode     string              `json:"subtitle_mode,omitempty"`
	AudioTracks      []MediaAudioTrack   `json:"audio_tracks,omitempty"`
	Clips            []MediaClip         `json:"clips"`
	MusicAssetID     string              `json:"music_asset_id"`
	MusicVolume      float64             `json:"music_volume"`
}
type MediaExport struct {
	TraceParent    string        `json:"trace_parent,omitempty"`
	SubtitleVTTURL string        `json:"subtitle_vtt_url"`
	ID             string        `json:"id"`
	Status         string        `json:"status"`
	FileURL        string        `json:"file_url"`
	SubtitleURL    string        `json:"subtitle_url"`
	Error          string        `json:"error"`
	Duration       float64       `json:"duration"`
	Timeline       MediaTimeline `json:"timeline"`
}
type MediaState struct {
	Version  int           `json:"version"`
	Assets   []MediaAsset  `json:"assets"`
	Timeline MediaTimeline `json:"timeline"`
	Exports  []MediaExport `json:"exports"`
}
type mediaReceipt struct {
	AssetID  string `json:"asset_id,omitempty"`
	Digest   string `json:"digest"`
	ExportID string `json:"export_id"`
}
type mediaDisk struct {
	Version  int                     `json:"version"`
	Projects map[string]MediaState   `json:"projects"`
	Receipts map[string]mediaReceipt `json:"receipts"`
}
type MediaStore struct {
	pg        *postgresScope
	blobs     *managedBlobs
	alignment *AlignmentStore
	failure   error
	mu        stateMutex
	dir       string
	data      mediaDisk
	cancels   map[string]context.CancelFunc
	wg        sync.WaitGroup
}

var errMediaInput = errors.New("invalid media input")

func mediaInvalid(message string) error { return fmt.Errorf("%w: %s", errMediaInput, message) }

var errMediaVersion = errors.New("media version conflict")
var errMediaKey = errors.New("media idempotency conflict")
var errMediaLimit = errors.New("media resource limit")

func mediaID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}
func mediaKey(space, project string) string { return space + ":" + project }
func mediaURL(space, project, path string) string {
	return "/api/v1/projects/" + project + "/media/" + path + "?space_id=" + space
}
func openMediaStore(dir string, database ...*sql.DB) (*MediaStore, error) {
	m := &MediaStore{dir: filepath.Join(dir, "media"), data: mediaDisk{Version: 1, Projects: map[string]MediaState{}, Receipts: map[string]mediaReceipt{}}, cancels: map[string]context.CancelFunc{}}
	if err := os.MkdirAll(m.dir, 0700); err != nil {
		return nil, err
	}
	if len(database) > 0 {
		var err error
		m.pg, err = attachPostgres(database[0], "media", &m.data, &m.mu)
		if err == nil {
			m.alignment, err = openAlignmentStore(m.dir, database[0])
		}
		return m, err
	}
	b, err := os.ReadFile(filepath.Join(m.dir, "state.json"))
	if err == nil {
		var d mediaDisk
		if json.Unmarshal(b, &d) != nil || d.Version != 1 || d.Projects == nil || d.Receipts == nil {
			return nil, errors.New("invalid media state; refusing reset")
		}
		m.data = d
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	for k, p := range m.data.Projects {
		if p.Version < 1 || p.Assets == nil || p.Exports == nil || p.Timeline.Clips == nil {
			return nil, errors.New("invalid media project; refusing reset")
		}
		for _, a := range p.Assets {
			if !mediaFileID(a.ID) {
				return nil, errors.New("invalid media asset identity")
			}
		}
		for i := range p.Exports {
			if !mediaFileID(p.Exports[i].ID) {
				return nil, errors.New("invalid media export identity")
			}
			if p.Exports[i].Status == "running" || p.Exports[i].Status == "queued" {
				p.Exports[i].Status = "failed"
				p.Exports[i].Error = "本地合成执行中断；可新建导出，不涉及模型调用"
			}
		}
		m.data.Projects[k] = p
	}
	if err = m.commit(m.data); err != nil {
		return nil, err
	}
	m.alignment, err = openAlignmentStore(m.dir)
	if err != nil {
		return nil, err
	}
	return m, nil
}
func (m *MediaStore) close() {
	if m.alignment != nil {
		m.alignment.close()
	}
	m.mu.Lock()
	for _, cancel := range m.cancels {
		cancel()
	}
	m.mu.Unlock()
	m.wg.Wait()
}
func (m *MediaStore) clone() mediaDisk {
	b, _ := json.Marshal(m.data)
	var d mediaDisk
	_ = json.Unmarshal(b, &d)
	return d
}
func (m *MediaStore) commit(d mediaDisk) (resultErr error) {
	if m.pg != nil {
		if err := m.pg.save(d); err != nil {
			return err
		}
		m.data = d
		return nil
	}
	defer func() {
		if resultErr != nil {
			m.failure = resultErr
		}
	}()
	b, err := json.Marshal(d)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(m.dir, ".state-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(b); err == nil {
		err = f.Sync()
	}
	err = errors.Join(err, f.Close())
	if err == nil {
		err = os.Rename(f.Name(), filepath.Join(m.dir, "state.json"))
	}
	if err == nil {
		var committed mediaDisk
		_ = json.Unmarshal(b, &committed)
		m.data = committed
	}
	return err
}
func mediaState(d mediaDisk, k string) MediaState {
	p, ok := d.Projects[k]
	if !ok {
		return MediaState{Version: 1, Assets: []MediaAsset{}, Exports: []MediaExport{}, Timeline: MediaTimeline{Clips: []MediaClip{}, MusicVolume: 0.2}}
	}
	return p
}
func (m *MediaStore) Get(project, space string) MediaState {
	m.mu.Lock()
	defer m.mu.Unlock()
	return mediaState(m.clone(), mediaKey(space, project))
}
func mediaDigest(v any) string {
	b, _ := json.Marshal(v)
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}
func (m *MediaStore) check(d mediaDisk, k, key, digest string) (bool, error) {
	if m.failure != nil {
		return false, m.failure
	}
	if r, ok := d.Receipts[k+":"+key]; ok {
		if r.Digest != digest {
			return false, errMediaKey
		}
		return true, nil
	}
	return false, nil
}
func mediaAsset(p MediaState, id string) (MediaAsset, bool) {
	for _, a := range p.Assets {
		if a.ID == id {
			return a, true
		}
	}
	return MediaAsset{}, false
}
func (m *MediaStore) AddAsset(project, space, key, name, role, character, shotID, source string, data []byte, mime string, leases ...*jobqueue.Job) (MediaState, error) {
	digest := mediaDigest([]string{"upload", name, role, character, shotID, source, mime, mediaDigest(data)})
	k := mediaKey(space, project)
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(leases) > 0 && leases[0] != nil {
		if m.pg == nil {
			return MediaState{}, errors.New("fenced media write requires postgres")
		}
		j := *leases[0]
		if j.TenantID != space {
			return MediaState{}, ErrNotFound
		}
		m.pg.beforeCommit = func(ctx context.Context, tx *sql.Tx) error { return jobqueue.CheckLeaseTx(ctx, tx, j) }
		defer func() { m.pg.beforeCommit = nil }()
	}
	d := m.clone()
	p := mediaState(d, k)
	if replay, err := m.check(d, k, key, digest); err != nil || replay {
		return p, err
	}
	if len(p.Assets) >= 120 || len(data) > 32<<20 || m.overQuota(int64(len(data))) {
		return p, errMediaLimit
	}
	id := mediaID()
	path := filepath.Join(m.dir, id)
	if err := os.WriteFile(path, data, 0600); err != nil {
		return p, err
	}
	kind, duration, err := validateMedia(path, mime)
	if err != nil {
		os.Remove(path)
		return p, err
	}
	if err = m.storeBlob(space, project, id); err != nil {
		os.Remove(path)
		return p, err
	}
	a := MediaAsset{ID: id, Kind: kind, MIMEType: mime, Name: name, Role: role, Character: character, ShotID: shotID, Duration: duration, Source: source, FileURL: mediaURL(space, project, "assets/"+id+"/file")}
	p.Assets = append(p.Assets, a)
	p.Version++
	d.Projects[k] = p
	d.Receipts[k+":"+key] = mediaReceipt{Digest: digest, AssetID: id}
	if err = m.commit(d); err != nil {
		os.Remove(path)
	}
	return p, err
}
func validateTimeline(p MediaState, t MediaTimeline) error {
	if _, _, _, err := outputSpec(t); err != nil {
		return err
	}
	if t.SubtitleMode != "" && t.SubtitleMode != "soft" && t.SubtitleMode != "burn" && t.SubtitleMode != "none" {
		return mediaInvalid("字幕模式无效")
	}
	if len(t.AudioTracks) > 8 {
		return mediaInvalid("附加音轨最多8条")
	}
	if len(t.Clips) < 1 || len(t.Clips) > 30 || t.MusicVolume < 0 || t.MusicVolume > 1 {
		return mediaInvalid("时间轴需包含 1–30 镜，音量须为 0–1")
	}
	duration := 0.0
	ids := map[string]bool{}
	for i, c := range t.Clips {
		for _, v := range []float64{c.Duration, c.TrimStart, c.AudioVolume, c.SourceAudioVolume, c.TransitionDuration, c.CropX, c.CropY, c.CropWidth, c.CropHeight, t.MusicVolume} {
			if math.IsNaN(v) || math.IsInf(v, 0) {
				return mediaInvalid("时间轴数值必须有限")
			}
		}
		if c.SourceAudioVolume < 0 || c.SourceAudioVolume > 1 {
			return mediaInvalid("原音音量须为0–1")
		}
		if c.Transition != "" && c.Transition != "fade" && c.Transition != "crossfade" {
			return mediaInvalid("转场类型无效")
		}
		if c.Transition == "fade" && (c.TransitionDuration < .1 || c.TransitionDuration > 2 || c.TransitionDuration > c.Duration/2) {
			return mediaInvalid("淡入淡出时长须为0.1–2秒且不超过镜头一半")
		}
		if c.Transition == "crossfade" && (i+1 == len(t.Clips) || c.TransitionDuration < .1 || c.TransitionDuration > 2 || c.TransitionDuration > c.Duration/2 || c.TransitionDuration > t.Clips[i+1].Duration/2) {
			return mediaInvalid("交叉溶解需下一镜，时长0.1–2秒且不超过相邻镜头各自一半")
		}
		if c.Transition == "" && c.TransitionDuration != 0 {
			return mediaInvalid("未选择转场")
		}
		a, ok := mediaAsset(p, c.AssetID)
		if !ok || (a.Kind != "image" && a.Kind != "video") || !shotIDPattern.MatchString(c.ID) || ids[c.ID] || c.Duration < 0.5 || c.Duration > 30 || c.TrimStart < 0 || len([]rune(c.Subtitle)) > 300 || c.AudioVolume < 0 || c.AudioVolume > 1 {
			return mediaInvalid("镜头素材、时长、字幕或音量无效")
		}
		if err := validateSubtitleCues(c.SubtitleCues, c.Duration); err != nil {
			return err
		}
		ids[c.ID] = true
		duration += c.Duration
		if a.Kind == "video" && c.TrimStart+c.Duration > a.Duration+0.05 {
			return mediaInvalid("镜头超过视频素材长度")
		}
		if c.AudioAssetID != "" {
			audio, ok := mediaAsset(p, c.AudioAssetID)
			if !ok || audio.Kind != "audio" {
				return mediaInvalid("配音素材无效")
			}
		}
		if c.CropWidth != 0 || c.CropHeight != 0 {
			if c.CropWidth < 0.1 || c.CropHeight < 0.1 || c.CropX < 0 || c.CropY < 0 || c.CropX+c.CropWidth > 1 || c.CropY+c.CropHeight > 1 {
				return mediaInvalid("裁剪区域无效")
			}
		}
	}
	if err := validateComposite(p, t); err != nil {
		return err
	}
	duration = timelineDuration(t)
	_, _, maxDuration, _ := outputSpec(t)
	if duration > maxDuration {
		return mediaInvalid(fmt.Sprintf("所选清晰度成片最长 %.0f 秒", maxDuration))
	}
	trackIDs := map[string]bool{}
	for _, track := range t.AudioTracks {
		for _, v := range []float64{track.Start, track.TrimStart, track.Duration, track.Volume} {
			if math.IsNaN(v) || math.IsInf(v, 0) {
				return mediaInvalid("音轨数值必须有限")
			}
		}
		a, ok := mediaAsset(p, track.AssetID)
		if !ok || a.Kind != "audio" || !shotIDPattern.MatchString(track.ID) || trackIDs[track.ID] || track.Start < 0 || track.TrimStart < 0 || track.Duration < .1 || track.Start+track.Duration > duration+.001 || track.TrimStart+track.Duration > a.Duration+.05 || track.Volume < 0 || track.Volume > 1 {
			return mediaInvalid("附加音轨素材、时间或音量无效")
		}
		trackIDs[track.ID] = true
	}
	if t.MusicAssetID != "" {
		a, ok := mediaAsset(p, t.MusicAssetID)
		if !ok || a.Kind != "audio" {
			return mediaInvalid("音乐素材无效")
		}
	}
	return nil
}
func (m *MediaStore) SaveTimeline(project, space, key string, version int, t MediaTimeline) (MediaState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d := m.clone()
	k := mediaKey(space, project)
	p := mediaState(d, k)
	digest := mediaDigest(struct {
		Op       string
		Version  int
		Timeline MediaTimeline
	}{"timeline", version, t})
	if replay, err := m.check(d, k, key, digest); err != nil || replay {
		return p, err
	}
	if p.Version != version {
		return p, errMediaVersion
	}
	if err := validateTimeline(p, t); err != nil {
		return p, err
	}
	if err := m.validateAlignmentSources(project, space, t); err != nil {
		return p, err
	}
	p.Timeline = t
	p.Version++
	d.Projects[k] = p
	d.Receipts[k+":"+key] = mediaReceipt{Digest: digest}
	return p, m.commit(d)
}
func (m *MediaStore) file(project, space, id string, export, subtitles bool) (string, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p := mediaState(m.data, mediaKey(space, project))
	if !export {
		if a, ok := mediaAsset(p, id); ok {
			path, err := m.localBlob(space, project, a.ID)
			return path, a.MIMEType, err
		}
	} else {
		for _, e := range p.Exports {
			if e.ID == id && e.Status == "succeeded" {
				if subtitles {
					path, err := m.localBlob(space, project, e.ID+".srt")
					return path, "application/x-subrip", err
				}
				path, err := m.localBlob(space, project, e.ID+".mp4")
				return path, "video/mp4", err
			}
		}
	}
	return "", "", ErrNotFound
}

// Includes export and scratch files; reserve room before accepting new work.
func (m *MediaStore) overQuota(extra int64) bool {
	total := extra
	// Conservatively reserve encoder scratch/output space for accepted exports as well as new work.
	for _, p := range m.data.Projects {
		for _, e := range p.Exports {
			if e.Status == "running" || e.Status == "queued" {
				total += 256 << 20
			}
		}
	}
	err := filepath.WalkDir(m.dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		total += info.Size()
		if total > 2<<30 {
			return errMediaLimit
		}
		return nil
	})
	return err != nil || total > 2<<30
}

func (m *MediaStore) Available() error { m.mu.Lock(); defer m.mu.Unlock(); return m.failure }

func mediaFileID(id string) bool {
	b, err := hex.DecodeString(id)
	return err == nil && len(b) == 16 && id == strings.ToLower(id)
}

// AssetForReceipt resolves the immutable ingestion result, never a best match by
// source or shot. Legacy receipts without an identity fail closed.
func (m *MediaStore) AssetForReceipt(project, space, key string) (MediaAsset, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failure != nil {
		return MediaAsset{}, m.failure
	}
	k := mediaKey(space, project)
	receipt, ok := m.data.Receipts[k+":"+key]
	if !ok || receipt.AssetID == "" {
		return MediaAsset{}, errors.New("media asset receipt unavailable")
	}
	asset, ok := mediaAsset(mediaState(m.data, k), receipt.AssetID)
	if !ok {
		return MediaAsset{}, errors.New("media asset receipt target missing")
	}
	return asset, nil
}

// Output presets bound both dimensions and encoder work. Missing fields preserve legacy exports.
func outputSpec(t MediaTimeline) (int, int, float64, error) {
	w, h, limit := 640, 360, 180.0
	switch t.OutputResolution {
	case "", "360p":
	case "720p":
		w, h, limit = 1280, 720, 120
	case "1080p":
		w, h, limit = 1920, 1080, 60
	default:
		return 0, 0, 0, mediaInvalid("不支持的输出清晰度")
	}
	switch t.AspectRatio {
	case "", "16:9":
	case "9:16":
		w, h = h, w
	case "1:1":
		w = h
	default:
		return 0, 0, 0, mediaInvalid("不支持的画面比例")
	}
	return w, h, limit, nil
}
