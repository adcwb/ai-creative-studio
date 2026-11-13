package revisionrepo

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/revisions"

	"github.com/adcwb/ai-creative-studio/backend/internal/platform/nativefs"
)

func Open(dir string, db *sql.DB) (*Store, error) {
	if db == nil {
		if e := os.MkdirAll(dir, 0700); e != nil {
			return nil, e
		}
		if e := directory(dir); e != nil {
			return nil, e
		}
	}
	return &Store{dir, db}, nil
}
func directory(p string) error {
	st, e := os.Lstat(p)
	if e != nil {
		return e
	}
	if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return ErrStorage
	}
	return nil
}
func syncDir(p string) error {
	f, e := os.Open(p)
	if e != nil {
		return e
	}
	return errors.Join(nativefs.SyncFile(f), f.Close())
}
func decode(raw []byte) (Record, error) {
	var r Record
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&r) != nil || !validRecord(r) {
		return Record{}, ErrStorage
	}
	var tail any
	if d.Decode(&tail) != io.EOF {
		return Record{}, ErrStorage
	}
	return r, nil
}
func read(p string) (Record, error) {
	f, e := nativefs.OpenFile(p, os.O_RDONLY|nativefs.NoFollow|nativefs.NonBlock, 0)
	if os.IsNotExist(e) {
		return Record{}, ErrNotFound
	}
	if e != nil {
		return Record{}, ErrStorage
	}
	defer f.Close()
	st, e := f.Stat()
	if e != nil || !st.Mode().IsRegular() {
		return Record{}, ErrStorage
	}
	raw, e := io.ReadAll(f)
	if e != nil {
		return Record{}, ErrStorage
	}
	return decode(raw)
}

type files struct {
	s    *Store
	q    Scope
	ctx  context.Context
	base string
}

func (s *Store) withFile(ctx context.Context, q Scope, write bool, fn func(*files) error) error {
	if !validScope(q) {
		return revisions.ErrInput
	}
	if e := ctx.Err(); e != nil {
		return e
	}
	if directory(s.dir) != nil {
		return ErrStorage
	}
	f, e := nativefs.OpenFile(filepath.Join(s.dir, ".lock-"+Hash(q.Space+":"+q.Project)), os.O_CREATE|os.O_RDWR|nativefs.NoFollow, 0600)
	if e != nil {
		return ErrStorage
	}
	defer f.Close()
	st, e := f.Stat()
	if e != nil || !st.Mode().IsRegular() {
		return ErrStorage
	}
	mode := nativefs.LOCK_SH
	if write {
		mode = nativefs.LOCK_EX
	}
	for {
		if e = ctx.Err(); e != nil {
			return e
		}
		e = nativefs.Flock(int(f.Fd()), mode|nativefs.LOCK_NB)
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
	return fn(&files{s, q, ctx, filepath.Join(s.dir, Hash(q.Space), q.Project)})
}
func (f *files) dirs(branch string, create bool) error {
	p := f.s.dir
	for _, part := range []string{Hash(f.q.Space), f.q.Project, branch} {
		p = filepath.Join(p, part)
		if create {
			if e := os.Mkdir(p, 0700); e != nil && !os.IsExist(e) {
				return ErrStorage
			}
		}
		if e := directory(p); os.IsNotExist(e) {
			return ErrNotFound
		} else if e != nil {
			return ErrStorage
		}
		if create {
			if e := syncDir(filepath.Dir(p)); e != nil {
				return ErrStorage
			}
		}
	}
	return nil
}
func (f *files) entries(branch string) ([]string, error) {
	if !branchPattern.MatchString(branch) {
		return nil, revisions.ErrInput
	}
	if e := f.dirs(branch, false); e != nil {
		return nil, e
	}
	list, e := os.ReadDir(filepath.Join(f.base, branch))
	if e != nil {
		return nil, ErrStorage
	}
	out := []string{}
	for _, v := range list {
		if strings.HasPrefix(v.Name(), ".pending-") && v.Type()&os.ModeSymlink == 0 {
			continue
		}
		if v.IsDir() || v.Type()&os.ModeSymlink != 0 || !entryPattern.MatchString(v.Name()) {
			return nil, ErrStorage
		}
		out = append(out, v.Name())
	}
	sort.Strings(out)
	for i, name := range out {
		n, e := strconv.ParseInt(strings.SplitN(name, "-", 2)[0], 10, 64)
		if e != nil || n != int64(i+1) {
			return nil, ErrStorage
		}
	}
	return out, nil
}
func recordName(r Record) string {
	v := revisionID(r.Branch.ID, r.Branch.Version, r.Key)
	_, name, _ := parseRevisionID(v)
	return name
}
func (f *files) load(branch, name string) (Record, error) {
	if e := f.dirs(branch, false); e != nil {
		return Record{}, e
	}
	r, e := read(filepath.Join(f.base, branch, name))
	if e != nil {
		return Record{}, e
	}
	if r.Branch.SpaceID != f.q.Space || r.Branch.ProjectID != f.q.Project || r.Branch.ID != branch || recordName(r) != name {
		return Record{}, ErrStorage
	}
	return r, nil
}
func (f *files) head(branch string) (revisions.Branch, error) {
	list, e := f.entries(branch)
	if e != nil {
		return revisions.Branch{}, e
	}
	if len(list) == 0 {
		return revisions.Branch{}, ErrNotFound
	}
	r, e := f.load(branch, list[len(list)-1])
	return r.Branch, e
}
func (f *files) get(id string) (revisions.Revision, error) {
	branch, name, ok := parseRevisionID(id)
	if !ok {
		return revisions.Revision{}, ErrNotFound
	}
	r, e := f.load(branch, name)
	if e != nil {
		return revisions.Revision{}, e
	}
	if r.Revision == nil || r.Revision.ID != id {
		return revisions.Revision{}, ErrNotFound
	}
	return *r.Revision, nil
}
func (f *files) replay(branch, key string) (Record, error) {
	list, e := f.entries(branch)
	if e != nil {
		return Record{}, e
	}
	for _, name := range list {
		if strings.HasSuffix(name, "-"+key+".json") {
			return f.load(branch, name)
		}
	}
	return Record{}, ErrNotFound
}
func (f *files) append(r Record) error {
	if !validRecord(r) || r.Branch.SpaceID != f.q.Space || r.Branch.ProjectID != f.q.Project {
		return ErrStorage
	}
	if e := f.dirs(r.Branch.ID, true); e != nil {
		return e
	}
	dir := filepath.Join(f.base, r.Branch.ID)
	tmp, e := os.CreateTemp(dir, ".pending-")
	if e != nil {
		return ErrStorage
	}
	defer os.Remove(tmp.Name())
	e = json.NewEncoder(tmp).Encode(r)
	if e == nil {
		e = nativefs.SyncFile(tmp)
	}
	e = errors.Join(e, tmp.Close())
	if e != nil {
		return ErrStorage
	}
	// Linking is the only visibility/commit point. Revision, branch and receipt
	// cannot appear separately; a crash before it leaves an ignored private temp.
	if e = os.Link(tmp.Name(), filepath.Join(dir, recordName(r))); e != nil {
		return ErrStorage
	}
	return syncDir(dir)
}
func pageValid(q Scope, after string, limit int) bool {
	return validScope(q) && limit > 0 && limit <= 100 && (after == "" || branchPattern.MatchString(after))
}
func (s *Store) Branches(ctx context.Context, q Scope, after string, limit int) (BranchPage, error) {
	out := BranchPage{Items: []revisions.Branch{}}
	if !pageValid(q, after, limit) {
		return out, revisions.ErrInput
	}
	if s.db != nil {
		return s.branchesSQL(ctx, q, after, limit)
	}
	e := s.withFile(ctx, q, false, func(f *files) error {
		// Validate the hashed-space and project directories before reading children.
		for _, p := range []string{filepath.Dir(f.base), f.base} {
			if e := directory(p); os.IsNotExist(e) {
				return nil
			} else if e != nil {
				return ErrStorage
			}
		}
		list, e := os.ReadDir(f.base)
		if e != nil {
			return ErrStorage
		}
		for _, v := range list {
			if !v.IsDir() || !branchPattern.MatchString(v.Name()) || v.Type()&os.ModeSymlink != 0 {
				return ErrStorage
			}
			if v.Name() <= after {
				continue
			}
			b, e := f.head(v.Name())
			if errors.Is(e, ErrNotFound) {
				continue
			}
			if e != nil {
				return e
			}
			if len(out.Items) == limit {
				out.Next = out.Items[len(out.Items)-1].ID
				break
			}
			out.Items = append(out.Items, b)
		}
		return nil
	})
	return out, e
}
func (s *Store) History(ctx context.Context, q Scope, branch, before string, limit int) (RevisionPage, error) {
	out := RevisionPage{Items: []Summary{}}
	if !validScope(q) || !branchPattern.MatchString(branch) || limit < 1 || limit > 100 || (before != "" && !entryPattern.MatchString(before)) {
		return out, revisions.ErrInput
	}
	if s.db != nil {
		return s.historySQL(ctx, q, branch, before, limit)
	}
	e := s.withFile(ctx, q, false, func(f *files) error {
		list, e := f.entries(branch)
		if errors.Is(e, ErrNotFound) {
			return nil
		}
		if e != nil {
			return e
		}
		for i := len(list) - 1; i >= 0; i-- {
			if before != "" && list[i] >= before {
				continue
			}
			r, e := f.load(branch, list[i])
			if e != nil {
				return e
			}
			if r.Revision == nil {
				continue
			}
			if len(out.Items) == limit {
				out.Next = list[i+1]
				break
			}
			out.Items = append(out.Items, Summarize(*r.Revision))
		}
		return nil
	})
	return out, e
}
