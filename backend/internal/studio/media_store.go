package studio

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
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
	ID           string  `json:"id"`
	AssetID      string  `json:"asset_id"`
	Duration     float64 `json:"duration"`
	TrimStart    float64 `json:"trim_start"`
	Subtitle     string  `json:"subtitle"`
	AudioAssetID string  `json:"audio_asset_id"`
	AudioVolume  float64 `json:"audio_volume"`
	// A normalized crop, applied before fitting to the output frame.
	CropX      float64 `json:"crop_x"`
	CropY      float64 `json:"crop_y"`
	CropWidth  float64 `json:"crop_width"`
	CropHeight float64 `json:"crop_height"`
}
type MediaTimeline struct {
	Clips        []MediaClip `json:"clips"`
	MusicAssetID string      `json:"music_asset_id"`
	MusicVolume  float64     `json:"music_volume"`
}
type MediaExport struct {
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
	Digest   string `json:"digest"`
	ExportID string `json:"export_id"`
}
type mediaDisk struct {
	Version  int                     `json:"version"`
	Projects map[string]MediaState   `json:"projects"`
	Receipts map[string]mediaReceipt `json:"receipts"`
}
type MediaStore struct {
	failure error
	mu      sync.Mutex
	dir     string
	data    mediaDisk
	cancels map[string]context.CancelFunc
	wg      sync.WaitGroup
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
func openMediaStore(dir string) (*MediaStore, error) {
	m := &MediaStore{dir: filepath.Join(dir, "media"), data: mediaDisk{Version: 1, Projects: map[string]MediaState{}, Receipts: map[string]mediaReceipt{}}, cancels: map[string]context.CancelFunc{}}
	if err := os.MkdirAll(m.dir, 0700); err != nil {
		return nil, err
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
	return m, nil
}
func (m *MediaStore) close() {
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
func (m *MediaStore) AddAsset(project, space, key, name, role, character, shotID, source string, data []byte, mime string) (MediaState, error) {
	digest := mediaDigest([]string{"upload", name, role, character, shotID, source, mime, mediaDigest(data)})
	k := mediaKey(space, project)
	m.mu.Lock()
	defer m.mu.Unlock()
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
	a := MediaAsset{ID: id, Kind: kind, MIMEType: mime, Name: name, Role: role, Character: character, ShotID: shotID, Duration: duration, Source: source, FileURL: mediaURL(space, project, "assets/"+id+"/file")}
	p.Assets = append(p.Assets, a)
	p.Version++
	d.Projects[k] = p
	d.Receipts[k+":"+key] = mediaReceipt{Digest: digest}
	if err = m.commit(d); err != nil {
		os.Remove(path)
	}
	return p, err
}
func validateTimeline(p MediaState, t MediaTimeline) error {
	if len(t.Clips) < 1 || len(t.Clips) > 30 || t.MusicVolume < 0 || t.MusicVolume > 1 {
		return mediaInvalid("时间轴需包含 1–30 镜，音量须为 0–1")
	}
	duration := 0.0
	ids := map[string]bool{}
	for _, c := range t.Clips {
		a, ok := mediaAsset(p, c.AssetID)
		if !ok || (a.Kind != "image" && a.Kind != "video") || !shotIDPattern.MatchString(c.ID) || ids[c.ID] || c.Duration < 0.5 || c.Duration > 30 || c.TrimStart < 0 || len([]rune(c.Subtitle)) > 300 || c.AudioVolume < 0 || c.AudioVolume > 1 {
			return mediaInvalid("镜头素材、时长、字幕或音量无效")
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
	if duration > 180 {
		return mediaInvalid("成片最长 180 秒")
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
			return filepath.Join(m.dir, a.ID), a.MIMEType, nil
		}
	} else {
		for _, e := range p.Exports {
			if e.ID == id && e.Status == "succeeded" {
				if subtitles {
					return filepath.Join(m.dir, e.ID+".srt"), "application/x-subrip", nil
				}
				return filepath.Join(m.dir, e.ID+".mp4"), "video/mp4", nil
			}
		}
	}
	return "", "", fmt.Errorf("media not found")
}

// Includes export and scratch files; reserve room before accepting new work.
func (m *MediaStore) overQuota(extra int64) bool {
	total := extra
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
