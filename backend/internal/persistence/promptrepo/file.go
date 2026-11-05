package promptrepo

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/adcwb/ai-creative-studio/backend/internal/modules/prompts"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
)

var recordPattern = regexp.MustCompile(`^[0-9]{20}-[vm]-[a-f0-9]{32}-[a-f0-9]{64}\.json$`)

// Each committed file is one atomic metadata/version/receipt transaction.
// No tenant-wide body/history snapshot is loaded, rewritten or capped.
func (s *Store) initDir() error                    { return os.MkdirAll(s.dir, 0700) }
func (s *Store) spaceDir(space string) string      { return filepath.Join(s.dir, prompts.TextSHA(space)) }
func (s *Store) entityDir(space, id string) string { return filepath.Join(s.spaceDir(space), id) }
func (s *Store) withLock(space string, write bool, fn func() error) error {
	dir := s.spaceDir(space)
	if e := os.MkdirAll(dir, 0700); e != nil {
		return ErrStorage
	}
	f, e := os.OpenFile(filepath.Join(dir, ".lock"), os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if e != nil {
		return ErrStorage
	}
	defer f.Close()
	mode := syscall.LOCK_SH
	if write {
		mode = syscall.LOCK_EX
	}
	if syscall.Flock(int(f.Fd()), mode) != nil {
		return ErrStorage
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	root, e := os.Open(s.dir)
	if e != nil {
		return ErrStorage
	}
	e = errors.Join(root.Sync(), root.Close())
	if e != nil {
		return ErrStorage
	}
	return fn()
}
func (s *Store) entities(space string) ([]string, error) {
	entries, e := os.ReadDir(s.spaceDir(space))
	if e != nil {
		return nil, ErrStorage
	}
	out := []string{}
	for _, v := range entries {
		if v.IsDir() && prompts.ID.MatchString(v.Name()) {
			out = append(out, v.Name())
		}
	}
	return out, nil
}
func (s *Store) entityEntries(space, id string) ([]string, error) {
	if !prompts.ID.MatchString(id) {
		return nil, prompts.ErrNotFound
	}
	entries, e := os.ReadDir(s.entityDir(space, id))
	if os.IsNotExist(e) {
		return nil, nil
	}
	if e != nil {
		return nil, ErrStorage
	}
	out := []string{}
	for _, v := range entries {
		n := v.Name()
		if strings.HasPrefix(n, ".") {
			continue
		}
		if v.Type()&os.ModeSymlink != 0 || v.IsDir() || !recordPattern.MatchString(n) {
			return nil, ErrStorage
		}
		out = append(out, n)
	}
	sort.Strings(out)
	return out, nil
}
func (s *Store) readRecord(space, id, name string) (record, error) {
	root, e := os.OpenRoot(s.dir)
	if e != nil {
		return record{}, ErrStorage
	}
	defer root.Close()
	f, e := root.Open(filepath.Join(prompts.TextSHA(space), id, name))
	if e != nil {
		return record{}, ErrStorage
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil || !info.Mode().IsRegular() || info.Size() > 2<<20 {
		return record{}, ErrStorage
	}
	var r record
	decoder := json.NewDecoder(io.LimitReader(f, (2<<20)+1))
	e = decoder.Decode(&r)
	if e != nil || r.Format != 1 || name != recordName(r) || r.Result.Template.RowVersion < 1 || r.Result.Version.Number < 1 || r.Result.Template.OwnerID == "" || !prompts.ID.MatchString(r.Result.Version.ID) || (r.Result.Template.Status != "active" && r.Result.Template.Status != "archived") || r.Result.Template.ID != id || r.Result.Template.SpaceID != space || r.Result.Version.TemplateID != id || r.Result.Version.SpaceID != space || r.Result.Template.HeadVersionID != r.Result.Version.ID || r.Result.Version.SHA256 != prompts.Digest(r.Result.Version.Recipe) {
		return record{}, ErrStorage
	}
	if _, e = prompts.NormalizeMetadata(r.Result.Template.Metadata); e != nil {
		return record{}, ErrStorage
	}
	if _, e = prompts.NormalizeRecipe(r.Result.Version.Recipe); e != nil {
		return record{}, ErrStorage
	}
	return r, nil
}
func (s *Store) currentFile(space, id string) (*prompts.Result, error) {
	entries, e := s.entityEntries(space, id)
	if e != nil {
		return nil, e
	}
	if len(entries) == 0 {
		return nil, prompts.ErrNotFound
	}
	r, e := s.readRecord(space, id, entries[len(entries)-1])
	if e != nil {
		return nil, e
	}
	return &r.Result, nil
}
func (s *Store) receiptFile(space, id, hash string) (*record, error) {
	entries, e := s.entityEntries(space, id)
	if e != nil {
		return nil, e
	}
	for _, name := range entries {
		if strings.HasSuffix(name, "-"+hash+".json") {
			r, e := s.readRecord(space, id, name)
			return &r, e
		}
	}
	return nil, nil
}
func atomicFile(dir, name string, value any) error {
	if e := os.MkdirAll(dir, 0700); e != nil {
		return ErrStorage
	}
	b, e := json.Marshal(value)
	if e != nil || len(b) > 2<<20 {
		return ErrStorage
	}
	f, e := os.CreateTemp(dir, ".pending-")
	if e != nil {
		return ErrStorage
	}
	defer os.Remove(f.Name())
	_, e = f.Write(b)
	if e == nil {
		e = f.Sync()
	}
	e = errors.Join(e, f.Close())
	if e != nil {
		return ErrStorage
	}
	if e = os.Rename(f.Name(), filepath.Join(dir, name)); e != nil {
		return ErrStorage
	}
	d, e := os.Open(dir)
	if e != nil {
		return ErrStorage
	}
	e = errors.Join(d.Sync(), d.Close())
	if e != nil {
		return ErrStorage
	}
	parent, e := os.Open(filepath.Dir(dir))
	if e != nil {
		return ErrStorage
	}
	e = errors.Join(parent.Sync(), parent.Close())
	if e != nil {
		return ErrStorage
	}
	return nil
}
func recordName(r record) string {
	kind, version := "m", strings.Repeat("0", 32)
	if r.NewVersion {
		kind, version = "v", r.Result.Version.ID
	}
	return fmt.Sprintf("%020d-%s-%s-%s.json", r.Result.Template.RowVersion, kind, version, r.KeyHash)
}
func (s *Store) writeRecord(r record) error {
	return atomicFile(s.entityDir(r.Result.Template.SpaceID, r.Result.Template.ID), recordName(r), r)
}
func (s *Store) favoritePath(a prompts.Actor, id string) (string, string) {
	return filepath.Join(s.spaceDir(a.Space), "favorites", prompts.TextSHA(a.User)), id + ".json"
}

type favorite struct {
	Space    string `json:"space_id"`
	Actor    string `json:"actor_id"`
	Template string `json:"template_id"`
	Enabled  bool   `json:"enabled"`
}

func (s *Store) favoriteFile(a prompts.Actor, id string) bool {
	dir, name := s.favoritePath(a, id)
	f, e := os.OpenFile(filepath.Join(dir, name), os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if e != nil {
		return false
	}
	defer f.Close()
	var v favorite
	return json.NewDecoder(io.LimitReader(f, 4096)).Decode(&v) == nil && v.Space == a.Space && v.Actor == a.User && v.Template == id && v.Enabled
}
func (s *Store) writeFavorite(a prompts.Actor, id string, enabled bool) error {
	dir, name := s.favoritePath(a, id)
	return atomicFile(dir, name, favorite{a.Space, a.User, id, enabled})
}
