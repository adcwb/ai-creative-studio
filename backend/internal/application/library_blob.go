package application

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/library"
)

// Never expose a partially written immutable reference after process failure.
// Quota includes orphaned copies until a later explicit cleanup can prove them
// unreferenced. Unknown metadata outcomes must not trigger eager deletion.
func (m *MediaStore) writeLibraryObject(space string, ref library.Reference, data []byte) error {
	if !mediaFileID(ref.ID) || int64(len(data)) != ref.SizeBytes {
		return ErrBlobIntegrity
	}
	h := sha256.Sum256(data)
	if hex.EncodeToString(h[:]) != ref.SHA256 {
		return ErrBlobIntegrity
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failure != nil {
		return m.failure
	}
	path := filepath.Join(m.dir, ref.ID)
	existing, err := safeReadLocal(path, 4<<20)
	if err == nil {
		h := sha256.Sum256(existing)
		if hex.EncodeToString(h[:]) != ref.SHA256 {
			return ErrBlobIntegrity
		}
	} else {
		if !os.IsNotExist(err) {
			return err
		}
		if m.overQuota(int64(len(data))) {
			return errMediaLimit
		}
		if e := writePreparedLibraryCopy(path, data); e != nil {
			return e
		}
	}
	return m.storeBlob(space, "library", ref.ID)
}

// The caller holds the media write lock. Metadata is committed only after the
// file and its directory entry are durable; failed requests leave safe orphans.
func writePreparedLibraryCopy(path string, data []byte) error {
	dirName := filepath.Dir(path)
	f, err := os.CreateTemp(dirName, ".library-reference-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if err = errors.Join(err, f.Close()); err != nil {
		return err
	}
	if err = os.Rename(f.Name(), path); err != nil {
		return err
	}
	dir, err := os.Open(dirName)
	if err != nil {
		return err
	}
	return errors.Join(dir.Sync(), dir.Close())
}
