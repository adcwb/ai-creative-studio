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
	"sync"

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
	wg        sync.WaitGroup
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
