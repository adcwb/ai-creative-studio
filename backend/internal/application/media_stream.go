package application

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/adcwb/ai-creative-studio/backend/internal/platform/jobqueue"
)

// Match mediaDigest([]byte) from earlier releases without allocating the JSON
// base64 string. Existing upload idempotency keys remain valid after upgrading.
func mediaFileDigest(ctx context.Context, path string) (string, error) {
	f, err := openLocalBlob(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	_, _ = h.Write([]byte{'"'})
	e := base64.NewEncoder(base64.StdEncoding, h)
	_, err = io.CopyBuffer(e, contextBlobReader{ctx, f}, make([]byte, 64<<10))
	if err != nil {
		return "", err
	}
	if err = e.Close(); err != nil {
		return "", err
	}
	_, _ = h.Write([]byte{'"'})
	return hex.EncodeToString(h.Sum(nil)), nil
}

func (m *MediaStore) addAssetFile(ctx context.Context, project, space, key, name, role, character, shotID, source, path, mime string, authorize func() bool, leases ...*jobqueue.Job) (MediaState, error) {
	content, err := mediaFileDigest(ctx, path)
	if err != nil {
		return MediaState{}, err
	}
	digest := mediaDigest([]string{"upload", name, role, character, shotID, source, mime, content})
	k := mediaKey(space, project)
	m.mu.Lock()
	p := mediaState(m.data, k)
	replay, err := m.check(m.data, k, key, digest)
	closed := m.closed
	m.mu.Unlock()
	if err != nil || replay {
		return p, err
	}
	if closed {
		return p, errors.New("media store closed")
	}
	if err = ctx.Err(); err != nil {
		return p, err
	}
	kind, duration, err := validateMedia(path, mime)
	if err != nil {
		return p, err
	}
	if authorize != nil && !authorize() {
		return p, errRuntimeDenied
	}
	// Keep the storage identity across failed/unknown writes so multipart
	// transfer can resume. Different bodies sharing a key never share a path.
	id := mediaDigest([]string{"media-upload-id", space, project, key, digest})[:32]
	destination := filepath.Join(m.dir, id)
	if err = os.Link(path, destination); err != nil {
		if !errors.Is(err, os.ErrExist) {
			return p, err
		}
		old, e := mediaFileDigest(ctx, destination)
		if e != nil || old != content {
			return p, ErrBlobIntegrity
		}
	}
	if filepath.Clean(path) != destination {
		if err = os.Remove(path); err != nil {
			return p, err
		}
	}
	directory, e := os.Open(m.dir)
	if e != nil {
		return p, e
	}
	err = errors.Join(directory.Sync(), directory.Close())
	if err != nil {
		return p, err
	}
	// A failed commit may already be visible remotely or to another retry. Keep
	// the immutable local object; lifecycle cleanup may collect safe orphans.
	if err = m.storeBlobContext(ctx, space, project, id); err != nil {
		return p, err
	}
	if err = ctx.Err(); err != nil {
		return p, err
	}
	if authorize != nil && !authorize() {
		return p, errRuntimeDenied
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	d := m.clone()
	p = mediaState(d, k)
	if replay, err = m.check(d, k, key, digest); err != nil || replay {
		return p, err
	}
	if m.closed {
		return p, errors.New("media store closed")
	}
	if len(leases) > 0 && leases[0] != nil {
		if m.pg == nil {
			return p, errors.New("fenced media write requires postgres")
		}
		j := *leases[0]
		if j.TenantID != space {
			return p, ErrNotFound
		}
		m.pg.beforeCommit = func(ctx context.Context, tx *sql.Tx) error { return jobqueue.CheckLeaseTx(ctx, tx, j) }
		defer func() { m.pg.beforeCommit = nil }()
	}
	a := MediaAsset{ID: id, Kind: kind, MIMEType: mime, Name: name, Role: role, Character: character, ShotID: shotID, Duration: duration, Source: source, FileURL: mediaURL(space, project, "assets/"+id+"/file")}
	p.Assets = append(p.Assets, a)
	p.Version++
	d.Projects[k] = p
	d.Receipts[k+":"+key] = mediaReceipt{Digest: digest, AssetID: id}
	return m.commitState(d, k)
}
