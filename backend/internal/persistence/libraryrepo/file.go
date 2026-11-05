package libraryrepo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/library"
)

// An immutable record commits a head, an optional new version and its receipt
// together. Current and history queries read only the selected asset records;
// the space's cumulative JSON size is never an admission condition.
type fileRecord struct {
	Format     int    `json:"format"`
	Sequence   int64  `json:"sequence"`
	NewVersion bool   `json:"new_version"`
	Key        string `json:"key,omitempty"`
	Digest     string `json:"digest,omitempty"`
	Result     Result `json:"result"`
}

var fileRecordPattern = regexp.MustCompile(`^[0-9]{20}-[vm]-[A-Za-z0-9_-]{1,80}-[a-f0-9]{64}\.json$`)

func keyHash(key string) string                { h := sha256.Sum256([]byte(key)); return hex.EncodeToString(h[:]) }
func (s *Store) fileSpace(space string) string { return filepath.Join(s.dir, space+".entities") }
func recordFilename(r fileRecord) string {
	kind := "m"
	if r.NewVersion {
		kind = "v"
	}
	return fmt.Sprintf("%020d-%s-%s-%s.json", r.Sequence, kind, r.Result.Version.ID, keyHash(r.Key))
}

func (s *Store) withFileLock(ctx context.Context, space string, fn func() error) error {
	if !library.IDPattern.MatchString(space) {
		return library.ErrInput
	}
	f, err := os.OpenFile(filepath.Join(s.dir, ".lock-"+space), os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return ErrStorage
	}
	defer f.Close()
	if st, err := f.Stat(); err != nil || !st.Mode().IsRegular() {
		return ErrStorage
	}
	for {
		if err = ctx.Err(); err != nil {
			return err
		}
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			return ErrStorage
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	if err = s.ensureFileSpace(space); err != nil {
		return err
	}
	// Confirm a previously uncertain upgrade's directory publication before
	// acknowledging any subsequent mutation or its accepted-key replay.
	if err = syncDirectory(s.dir); err != nil {
		return err
	}
	return fn()
}

func syncDirectory(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return ErrStorage
	}
	if errors.Join(f.Sync(), f.Close()) != nil {
		return ErrStorage
	}
	return nil
}

func writeFileRecord(dir string, r fileRecord) error {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return ErrStorage
	}
	data, err := json.Marshal(r)
	// One validated card/receipt is bounded, independently of the library size.
	if err != nil || len(data) > 2<<20 {
		return ErrStorage
	}
	f, err := os.CreateTemp(dir, ".pending-")
	if err != nil {
		return ErrStorage
	}
	defer os.Remove(f.Name())
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	err = errors.Join(err, f.Close())
	if err != nil {
		return ErrStorage
	}
	// Never overwrite an immutable transaction, even after an uncertain result.
	if err = os.Link(f.Name(), filepath.Join(dir, recordFilename(r))); err != nil {
		return ErrStorage
	}
	if err = syncDirectory(dir); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(dir))
}

func (s *Store) fileEntities(space string) ([]string, error) {
	entries, err := os.ReadDir(s.fileSpace(space))
	if err != nil {
		return nil, ErrStorage
	}
	ids := []string{}
	for _, e := range entries {
		if e.Name() == ".format" {
			continue
		}
		if !e.IsDir() || !library.IDPattern.MatchString(e.Name()) {
			return nil, ErrStorage
		}
		ids = append(ids, e.Name())
	}
	return ids, nil
}

func (s *Store) fileEntries(space, asset string) ([]string, error) {
	if !library.IDPattern.MatchString(asset) {
		return nil, ErrNotFound
	}
	path := filepath.Join(s.fileSpace(space), asset)
	st, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil || !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return nil, ErrStorage
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, ErrStorage
	}
	names := []string{}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".pending-") {
			continue
		}
		if !e.Type().IsRegular() || !fileRecordPattern.MatchString(e.Name()) {
			return nil, ErrStorage
		}
		names = append(names, e.Name())
	}
	return names, nil // os.ReadDir returns lexical sequence order.
}

func validFileResult(space, id string, r Result) bool {
	a, v := r.Asset, r.Version
	if !library.ValidAuthorization(a.UseAuthorization) {
		return false
	}
	if a.ID != id || a.SpaceID != space || v.AssetID != id || v.SpaceID != space || a.HeadVersionID != v.ID || !library.IDPattern.MatchString(v.ID) || v.Number < 1 || (a.Status != "active" && a.Status != "archived") {
		return false
	}
	if _, err := library.Normalize(a.Kind, v.Card); err != nil {
		return false
	}
	if library.ValidateContent(v.Card, v.References) != nil || library.ValidateReferences(v.References) != nil {
		return false
	}
	h, err := library.Digest(struct {
		Card       library.Card
		References []library.Reference
	}{v.Card, v.References})
	return err == nil && h == v.SHA256
}

func (s *Store) readFileRecord(space, asset, name string) (fileRecord, error) {
	path := filepath.Join(s.fileSpace(space), asset, name)
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return fileRecord{}, ErrStorage
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() || st.Size() > 2<<20 {
		return fileRecord{}, ErrStorage
	}
	var r fileRecord
	dec := json.NewDecoder(io.LimitReader(f, (2<<20)+1))
	if dec.Decode(&r) != nil || dec.Decode(new(any)) != io.EOF || r.Format != 2 || r.Sequence < 1 || recordFilename(r) != name || !validFileResult(space, asset, r.Result) {
		return fileRecord{}, ErrStorage
	}
	return r, nil
}

func (s *Store) fileCurrent(space, asset string) (Result, error) {
	names, err := s.fileEntries(space, asset)
	if err != nil {
		return Result{}, err
	}
	if len(names) == 0 {
		return Result{}, ErrNotFound
	}
	r, err := s.readFileRecord(space, asset, names[len(names)-1])
	return r.Result, err
}

func (s *Store) fileReceipt(ctx context.Context, space, key string) (receipt, bool, error) {
	ids, err := s.fileEntities(space)
	if err != nil {
		return receipt{}, false, err
	}
	suffix := "-" + keyHash(key) + ".json"
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return receipt{}, false, err
		}
		names, err := s.fileEntries(space, id)
		if err != nil {
			return receipt{}, false, err
		}
		for _, name := range names {
			if !strings.HasSuffix(name, suffix) {
				continue
			}
			r, err := s.readFileRecord(space, id, name)
			if err != nil || r.Key != key {
				return receipt{}, false, ErrStorage
			}
			// A prior writer may have linked this record but failed its directory
			// sync. Replay confirms durability instead of just trusting visibility.
			if err = syncDirectory(filepath.Join(s.fileSpace(space), id)); err != nil {
				return receipt{}, false, err
			}
			if err = syncDirectory(s.fileSpace(space)); err != nil {
				return receipt{}, false, err
			}
			return receipt{r.Digest, r.Result}, true, nil
		}
	}
	return receipt{}, false, nil
}

func (s *Store) fileVersion(space, asset, version string) (library.Version, error) {
	names, err := s.fileEntries(space, asset)
	if err != nil {
		return library.Version{}, err
	}
	for _, name := range names {
		// IDs may contain hyphens. A prefix match would confuse v1 with
		// v1-other and return the wrong immutable source version.
		if name[21] == 'v' && name[23:len(name)-70] == version {
			r, err := s.readFileRecord(space, asset, name)
			return r.Result.Version, err
		}
	}
	return library.Version{}, ErrNotFound
}

func (s *Store) fileVersions(ctx context.Context, space, asset string, before, limit int) ([]library.Version, error) {
	names, err := s.fileEntries(space, asset)
	if err != nil {
		return nil, err
	}
	out := []library.Version{}
	for i := len(names) - 1; i >= 0 && len(out) < limit; i-- {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !strings.HasPrefix(names[i][20:], "-v-") {
			continue
		}
		r, err := s.readFileRecord(space, asset, names[i])
		if err != nil {
			return nil, err
		}
		if before == 0 || r.Result.Version.Number < before {
			out = append(out, r.Result.Version)
		}
	}
	return out, nil
}

func (s *Store) fileList(ctx context.Context, space string, q Query) ([]library.Asset, error) {
	ids, err := s.fileEntities(space)
	if err != nil {
		return nil, err
	}
	out := []library.Asset{}
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if id <= q.After {
			continue
		}
		r, err := s.fileCurrent(space, id)
		if err != nil {
			return nil, err
		}
		a := r.Asset
		a.UseAuthorization = nil
		if (q.Kind == "" || a.Kind == q.Kind) && (q.Status == "" || a.Status == q.Status) {
			out = append(out, a)
		}
		if len(out) == q.Limit {
			break
		}
	}
	return out, nil
}

func (s *Store) fileMutate(ctx context.Context, space, key, digest, expected string, result Result, archive bool, authorization *authorizationMutation) (Result, error) {
	if key == "" || len(key) > 1024 {
		return Result{}, library.ErrInput
	}
	if r, ok, err := s.fileReceipt(ctx, space, key); err != nil {
		return Result{}, err
	} else if ok {
		if r.Digest != digest {
			return Result{}, ErrKey
		}
		return r.Result, nil
	}
	old, err := s.fileCurrent(space, result.Asset.ID)
	exists := err == nil
	if err != nil && !errors.Is(err, ErrNotFound) {
		return Result{}, err
	}
	metadata := archive || authorization != nil
	if authorization != nil {
		if !exists {
			return Result{}, ErrNotFound
		}
		result, err = s.authorizationResult(ctx, old, *authorization, func(id string) error { _, e := s.fileVersion(space, old.Asset.ID, id); return e })
		if err != nil {
			return Result{}, err
		}
	} else if exists {
		result.Asset.UseAuthorization = old.Asset.UseAuthorization
	}
	if authorization == nil {
		if err = validateMutation(old.Asset, exists, expected, result, archive); err != nil {
			return Result{}, err
		}
	}
	if !metadata {
		// Preserve the legacy file format's globally unique version identities,
		// including the identity expected by offline round-trip inventories.
		ids, e := s.fileEntities(space)
		if e != nil {
			return Result{}, e
		}
		for _, id := range ids {
			if err = ctx.Err(); err != nil {
				return Result{}, err
			}
			if _, err = s.fileVersion(space, id, result.Version.ID); err == nil {
				return Result{}, library.ErrConflict
			} else if !errors.Is(err, ErrNotFound) {
				return Result{}, err
			}
		}
		n := 1
		if exists {
			n = old.Version.Number + 1
		}
		if result.Version.Number != n {
			return Result{}, library.ErrConflict
		}
	}
	names, err := s.fileEntries(space, result.Asset.ID)
	if err != nil {
		return Result{}, err
	}
	seq := int64(1)
	if len(names) > 0 {
		last, err := s.readFileRecord(space, result.Asset.ID, names[len(names)-1])
		if err != nil {
			return Result{}, err
		}
		seq = last.Sequence + 1
	}
	if seq < 1 {
		return Result{}, ErrStorage
	}
	r := fileRecord{Format: 2, Sequence: seq, NewVersion: !metadata, Key: key, Digest: digest, Result: result}
	if !validFileResult(space, result.Asset.ID, result) {
		return Result{}, library.ErrInput
	}
	if err = ctx.Err(); err != nil {
		return Result{}, err
	}
	err = writeFileRecord(filepath.Join(s.fileSpace(space), result.Asset.ID), r)
	return result, err
}

// Legacy snapshots are upgraded under the same inter-process space lock. A
// private complete staging tree is fsynced before its single rename publishes
// it. The old snapshot is retained for recovery but never consulted afterwards.
func (s *Store) ensureFileSpace(space string) error {
	path := s.fileSpace(space)
	if st, err := os.Lstat(path); err == nil {
		if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
			return ErrStorage
		}
		f, err := os.OpenFile(filepath.Join(path, ".format"), os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
		if err != nil {
			return ErrStorage
		}
		defer f.Close()
		if st, err := f.Stat(); err != nil || !st.Mode().IsRegular() || st.Size() != 2 {
			return ErrStorage
		}
		b, err := io.ReadAll(io.LimitReader(f, 16))
		if err != nil || string(b) != "2\n" {
			return ErrStorage
		}
		return nil
	} else if !os.IsNotExist(err) {
		return ErrStorage
	}
	d, err := s.readLegacy(space)
	if err != nil {
		return err
	}
	return s.publishFileSpace(space, d)
}

func (s *Store) publishFileSpace(space string, d disk) error {
	stage, err := os.MkdirTemp(s.dir, ".library-upgrade-")
	if err != nil {
		return ErrStorage
	}
	defer os.RemoveAll(stage)
	for id, a := range d.Assets {
		versions := []library.Version{}
		for _, v := range d.Versions {
			if v.AssetID == id {
				versions = append(versions, v)
			}
		}
		sort.Slice(versions, func(i, j int) bool { return versions[i].Number < versions[j].Number })
		seq := int64(0)
		appendRecord := func(r fileRecord) error {
			seq++
			r.Format = 2
			r.Sequence = seq
			return writeFileRecord(filepath.Join(stage, id), r)
		}
		for _, v := range versions {
			x := a
			x.HeadVersionID = v.ID
			x.Name = v.Card.Name
			x.Status = "active"
			x.UpdatedAt = v.CreatedAt
			if err = appendRecord(fileRecord{NewVersion: true, Result: Result{x, v}}); err != nil {
				return err
			}
		}
		keys := []string{}
		for key, r := range d.Receipts {
			if r.Result.Asset.ID == id {
				keys = append(keys, key)
			}
		}
		sort.Strings(keys)
		for _, key := range keys {
			r := d.Receipts[key]
			if err = appendRecord(fileRecord{Key: key, Digest: r.Digest, Result: r.Result}); err != nil {
				return err
			}
		}
		if err = appendRecord(fileRecord{Result: Result{a, d.Versions[a.HeadVersionID]}}); err != nil {
			return err
		}
	}
	if err = os.WriteFile(filepath.Join(stage, ".format"), []byte("2\n"), 0600); err != nil {
		return ErrStorage
	}
	f, err := os.OpenFile(filepath.Join(stage, ".format"), os.O_RDWR, 0)
	if err != nil {
		return ErrStorage
	}
	err = errors.Join(f.Sync(), f.Close())
	if err != nil {
		return ErrStorage
	}
	if err = syncDirectory(stage); err != nil {
		return err
	}
	if err = os.Rename(stage, s.fileSpace(space)); err != nil {
		return ErrStorage
	}
	return syncDirectory(s.dir)
}
