package application

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/adcwb/ai-creative-studio/backend/internal/platform/jobqueue"
)

type mediaReceipt struct {
	SocialSecret string `json:"social_secret,omitempty"`
	AssetID      string `json:"asset_id,omitempty"`
	Digest       string `json:"digest"`
	ExportID     string `json:"export_id"`
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
	attempts  map[string]string
	jobs      *localJobs
	closed    bool
}

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
				if p.Exports[i].RenderVersion == 1 {
					p.Exports[i].Status = "queued"
					p.Exports[i].Error = ""
				} else {
					p.Exports[i].Status = "failed"
					p.Exports[i].Error = "本地合成执行中断；可新建导出，不涉及模型调用"
				}
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
	m.closed = true
	for _, cancel := range m.cancels {
		cancel()
	}
	m.mu.Unlock()
	m.jobs.close()
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
		// Admission/receipts must survive a directory-entry flush as well as the
		// file flush before an accepted export can be handed to a worker.
		err = syncMultipartDir(m.dir)
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
	p := mediaState(m.clone(), mediaKey(space, project))
	for i := range p.Exports {
		if p.Exports[i].RenderVersion == 1 && p.Exports[i].Status == "running" {
			p.Exports[i].RenderProgress = m.renderProgress(space, project, p.Exports[i].ID)
		}
	}
	return p
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

func (m *MediaStore) AddAsset(project, space, key, name, role, character, shotID, source string, data []byte, mime string, leases ...*jobqueue.Job) (MediaState, error) {
	if len(data) > 32<<20 {
		return MediaState{}, errMediaLimit
	}
	f, err := os.CreateTemp(m.dir, ".asset-input-")
	if err != nil {
		return MediaState{}, err
	}
	defer os.Remove(f.Name())
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	err = errors.Join(err, f.Close())
	if err != nil {
		return MediaState{}, err
	}
	return m.addAssetFile(context.Background(), project, space, key, name, role, character, shotID, source, f.Name(), mime, nil, leases...)
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
	if t.EpisodeNumber <= 1 {
		p.Timeline = t
	} else {
		if p.EpisodeTimelines == nil {
			p.EpisodeTimelines = map[int]MediaTimeline{}
		}
		p.EpisodeTimelines[t.EpisodeNumber] = t
	}
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
