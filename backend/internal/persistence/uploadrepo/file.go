package uploadrepo

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/uploads"

	"github.com/adcwb/ai-creative-studio/backend/internal/platform/nativefs"
)

type fileHeader struct {
	Format  int             `json:"format"`
	Session uploads.Session `json:"session"`
	Part    *uploads.Part   `json:"part,omitempty"`
}

var partName = regexp.MustCompile(`^[0-9]{20}\.part$`)

func (s *Store) spacePath(space string) string { return filepath.Join(s.dir, uploads.Digest(space)) }
func (s *Store) sessionPath(i Identity) string { return filepath.Join(s.spacePath(i.Space), i.ID) }
func (l *Locked) path() string {
	return l.store.sessionPath(Identity{l.Session.Space, l.Session.Project, l.Session.Actor, l.Session.ID})
}
func syncDir(dir string) error {
	f, e := os.Open(dir)
	if e != nil {
		return ErrStorage
	}
	defer f.Close()
	if nativefs.SyncFile(f) != nil {
		return ErrStorage
	}
	return nil
}
func atomicRecord(dir, name string, h fileHeader, data []byte) error {
	f, e := os.CreateTemp(dir, ".pending-")
	if e != nil {
		return ErrStorage
	}
	defer os.Remove(f.Name())
	b, e := json.Marshal(h)
	if e != nil || len(b) > 16382 {
		f.Close()
		return ErrStorage
	}
	b = append(b, '\n')
	if _, e = f.Write(b); e == nil {
		_, e = f.Write(data)
	}
	if e == nil {
		e = nativefs.SyncFile(f)
	}
	e = errors.Join(e, f.Close())
	if e != nil {
		return ErrStorage
	}
	if e = os.Link(f.Name(), filepath.Join(dir, name)); e != nil {
		return ErrStorage
	}
	return syncDir(dir)
}
func readHeader(path string) (fileHeader, *os.File, *bufio.Reader, error) {
	var h fileHeader
	f, e := nativefs.OpenFile(path, os.O_RDONLY|nativefs.NoFollow|nativefs.NonBlock, 0)
	if os.IsNotExist(e) {
		return h, nil, nil, uploads.ErrNotFound
	}
	if e != nil {
		return h, nil, nil, ErrStorage
	}
	fail := func() (fileHeader, *os.File, *bufio.Reader, error) { f.Close(); return h, nil, nil, ErrStorage }
	st, e := f.Stat()
	if e != nil || !st.Mode().IsRegular() {
		return fail()
	}
	r := bufio.NewReaderSize(f, 16384)
	line, e := r.ReadSlice('\n')
	if e != nil || json.Unmarshal(line, &h) != nil || h.Format != 1 || !uploads.ValidSession(h.Session) {
		return fail()
	}
	n := int64(len(line))
	if h.Part != nil {
		p := h.Part
		if p.Offset < 0 || filepath.Base(path) != fmt.Sprintf("%020d.part", p.Offset) || p.Offset%uploads.ChunkBytes != 0 || p.Size != min(uploads.ChunkBytes, h.Session.Size-p.Offset) || p.Size < 1 || h.Session.Offset != p.Offset+p.Size || h.Session.State != "receiving" || !uploads.SHA.MatchString(p.SHA256) {
			return fail()
		}
		n += p.Size
	}
	if st.Size() != n {
		return fail()
	}
	return h, f, r, nil
}
func sessionRecord(path string) (uploads.Session, error) {
	h, f, _, e := readHeader(path)
	if e != nil {
		return h.Session, e
	}
	f.Close()
	if h.Part != nil {
		return h.Session, ErrStorage
	}
	return h.Session, nil
}

// An interrupted create can leave only a lock/temp file, without an accepted
// identity. Missing creation metadata alongside accepted chunks is corruption.
func creationRecord(path string) (uploads.Session, error) {
	u, e := sessionRecord(path)
	if !errors.Is(e, uploads.ErrNotFound) {
		return u, e
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		return u, ErrStorage
	}
	for _, entry := range entries {
		if entry.Name() != ".lock" && !strings.HasPrefix(entry.Name(), ".pending-") {
			return u, ErrStorage
		}
	}
	return u, uploads.ErrNotFound
}
func (s *Store) withFile(ctx context.Context, i Identity, create *uploads.Session, fn func(*Locked) error) error {
	dir := s.sessionPath(i)
	if create != nil {
		if e := os.MkdirAll(dir, 0700); e != nil {
			return ErrStorage
		}
	}
	st, e := os.Lstat(dir)
	if os.IsNotExist(e) {
		return uploads.ErrNotFound
	}
	if e != nil || !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return ErrStorage
	}
	f, e := nativefs.OpenFile(filepath.Join(dir, ".lock"), os.O_RDWR|os.O_CREATE|nativefs.NoFollow, 0600)
	if e != nil {
		return ErrStorage
	}
	defer f.Close()
	st, e = f.Stat()
	if e != nil || !st.Mode().IsRegular() {
		return ErrStorage
	}
	for {
		if e = ctx.Err(); e != nil {
			return e
		}
		e = nativefs.Flock(int(f.Fd()), nativefs.LOCK_EX|nativefs.LOCK_NB)
		if e == nil {
			break
		}
		if !errors.Is(e, nativefs.ErrWouldBlock) {
			return ErrStorage
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
	defer nativefs.Flock(int(f.Fd()), nativefs.LOCK_UN)
	u, e := sessionRecord(filepath.Join(dir, "session.json"))
	if errors.Is(e, uploads.ErrNotFound) && create != nil {
		if e = atomicRecord(dir, "session.json", fileHeader{Format: 1, Session: *create}, nil); e != nil {
			return e
		}
		u = *create
	} else if e != nil {
		return e
	}
	u, e = s.currentFile(i)
	if e != nil {
		return e
	}
	if create != nil && u.RequestSHA != create.RequestSHA {
		return uploads.ErrConflict
	}
	// Confirm directory publication, including a previously uncertain response.
	for _, d := range []string{dir, filepath.Dir(dir), s.dir} {
		if e = syncDir(d); e != nil {
			return e
		}
	}
	l := &Locked{Session: u, store: s, ctx: ctx}
	if u.State != "receiving" {
		l.purgeParts()
	}
	return fn(l)
}
func (l *Locked) writePart(p uploads.Part, b []byte) error {
	return atomicRecord(l.path(), fmt.Sprintf("%020d.part", p.Offset), fileHeader{1, l.Session, &p}, b)
}
func (l *Locked) readPart(offset int64) (uploads.Part, []byte, error) {
	if offset < 0 {
		return uploads.Part{}, nil, uploads.ErrInput
	}
	h, f, r, e := readHeader(filepath.Join(l.path(), fmt.Sprintf("%020d.part", offset)))
	if e != nil {
		return uploads.Part{}, nil, e
	}
	defer f.Close()
	if h.Part == nil || h.Part.Offset != offset || h.Session.ID != l.Session.ID || h.Session.RequestSHA != l.Session.RequestSHA {
		return uploads.Part{}, nil, ErrStorage
	}
	b, e := io.ReadAll(io.LimitReader(r, uploads.ChunkBytes+1))
	if e != nil || int64(len(b)) != h.Part.Size || uploads.BytesSHA(b) != h.Part.SHA256 {
		return uploads.Part{}, nil, ErrStorage
	}
	return *h.Part, b, nil
}
func (l *Locked) saveTerminal() error {
	return atomicRecord(l.path(), "closed.json", fileHeader{Format: 1, Session: l.Session}, nil)
}
func (l *Locked) purgeParts() {
	entries, e := os.ReadDir(l.path())
	if e != nil {
		return
	}
	for _, v := range entries {
		if partName.MatchString(v.Name()) {
			_ = os.Remove(filepath.Join(l.path(), v.Name()))
		}
	}
	_ = syncDir(l.path())
}
func (s *Store) fileIdentities(ctx context.Context, space string) ([]Identity, error) {
	entries, e := os.ReadDir(s.spacePath(space))
	if os.IsNotExist(e) {
		return nil, nil
	}
	if e != nil {
		return nil, ErrStorage
	}
	out := []Identity{}
	for _, v := range entries {
		if e = ctx.Err(); e != nil {
			return nil, e
		}
		if !v.IsDir() || !uploads.ID.MatchString(v.Name()) {
			return nil, ErrStorage
		}
		u, e := creationRecord(filepath.Join(s.spacePath(space), v.Name(), "session.json"))
		if errors.Is(e, uploads.ErrNotFound) {
			continue
		}
		if e != nil {
			return nil, e
		}
		out = append(out, Identity{u.Space, u.Project, u.Actor, u.ID})
	}
	return out, nil
}
func (s *Store) listFile(ctx context.Context, i Identity, after string, limit int) ([]uploads.Session, error) {
	ids, e := s.fileIdentities(ctx, i.Space)
	if e != nil {
		return nil, e
	}
	out := []uploads.Session{}
	for _, id := range ids {
		if id.ID <= after || id.Project != i.Project || id.Actor != i.Actor || id.Space != i.Space {
			continue
		}
		u, e := s.Get(ctx, id)
		if e != nil {
			return nil, e
		}
		if u.State == "receiving" {
			out = append(out, u)
		}
		if len(out) == limit {
			break
		}
	}
	return out, nil
}
func (s *Store) expiredFiles(ctx context.Context, before time.Time, limit int) ([]Identity, error) {
	spaces, e := os.ReadDir(s.dir)
	if e != nil {
		return nil, ErrStorage
	}
	out := []Identity{}
	for _, space := range spaces {
		if !space.IsDir() || !uploads.SHA.MatchString(space.Name()) {
			return nil, ErrStorage
		}
		entries, e := os.ReadDir(filepath.Join(s.dir, space.Name()))
		if e != nil {
			return nil, ErrStorage
		}
		for _, v := range entries {
			if e = ctx.Err(); e != nil {
				return nil, e
			}
			if !v.IsDir() || !uploads.ID.MatchString(v.Name()) {
				return nil, ErrStorage
			}
			u, e := creationRecord(filepath.Join(s.dir, space.Name(), v.Name(), "session.json"))
			if errors.Is(e, uploads.ErrNotFound) {
				continue
			}
			if e != nil {
				return nil, e
			}
			if uploads.Digest(u.Space) != space.Name() {
				return nil, ErrStorage
			}
			u, e = s.Get(ctx, Identity{u.Space, u.Project, u.Actor, u.ID})
			if e != nil {
				return nil, e
			}
			if u.State == "receiving" && u.ExpiresAt.Before(before) {
				out = append(out, Identity{u.Space, u.Project, u.Actor, u.ID})
			}
			if len(out) == limit {
				return out, nil
			}
		}
	}
	return out, nil
}

// Offline migration holds the application's exclusive source lock and uses
// this read-only path; it must not create locks, purge bytes or change source.
func (s *Store) currentFile(i Identity) (uploads.Session, error) {
	dir := s.sessionPath(i)
	u, e := sessionRecord(filepath.Join(dir, "session.json"))
	if e != nil {
		return u, e
	}
	if u.Offset != 0 || u.State != "receiving" || u.AssetID != "" {
		return u, ErrStorage
	}
	if u.ID != i.ID || u.Space != i.Space || u.Project != i.Project || u.Actor != i.Actor {
		return u, uploads.ErrNotFound
	}
	closed, e := sessionRecord(filepath.Join(dir, "closed.json"))
	if e == nil {
		if closed.ID != u.ID || closed.RequestSHA != u.RequestSHA || closed.Space != u.Space || closed.Actor != u.Actor || closed.Project != u.Project || !closed.CreatedAt.Equal(u.CreatedAt) || !closed.ExpiresAt.Equal(u.ExpiresAt) || closed.State == "receiving" {
			return u, ErrStorage
		}
		u = closed
	} else if !errors.Is(e, uploads.ErrNotFound) {
		return u, e
	} else {
		names, e := os.ReadDir(dir)
		if e != nil {
			return u, ErrStorage
		}
		last := ""
		for _, v := range names {
			if partName.MatchString(v.Name()) {
				last = v.Name()
			} else if v.Name() != "session.json" && v.Name() != ".lock" && !strings.HasPrefix(v.Name(), ".pending-") {
				return u, ErrStorage
			}
		}
		if last != "" {
			h, f, _, e := readHeader(filepath.Join(dir, last))
			if e != nil {
				return u, e
			}
			f.Close()
			if h.Part == nil || h.Session.ID != u.ID || h.Session.RequestSHA != u.RequestSHA || h.Session.Space != u.Space || h.Session.Actor != u.Actor || h.Session.Project != u.Project || !h.Session.CreatedAt.Equal(u.CreatedAt) || !h.Session.ExpiresAt.Equal(u.ExpiresAt) {
				return u, ErrStorage
			}
			u = h.Session
		}
	}
	return u, nil
}
