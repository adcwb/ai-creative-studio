package seriesrepo

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/adcwb/ai-creative-studio/backend/internal/modules/series"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type fileRef struct{ Kind, ID, File, SHA string }
type fileCommit struct {
	Format   int
	Space    string
	Sequence int64
	Receipt  *receipt
	Rows     []fileRef
}
type fileRow struct {
	Format   int
	Sequence int64
	Node     series.Node
}
type fileAccess struct {
	s           *Store
	ctx         context.Context
	space, base string
}

func directory(path string) error {
	st, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return ErrStorage
	}
	return nil
}
func privateDirectory(path string) error {
	if err := os.MkdirAll(path, 0700); err != nil {
		return ErrStorage
	}
	if directory(path) != nil {
		return ErrStorage
	}
	return nil
}
func (s *Store) initFile() error { return privateDirectory(s.dir) }
func syncDir(path string) error {
	f, e := os.Open(path)
	if e != nil {
		return ErrStorage
	}
	if errors.Join(f.Sync(), f.Close()) != nil {
		return ErrStorage
	}
	return nil
}
func read(path string) ([]byte, error) {
	f, e := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	st, e := f.Stat()
	if e != nil || !st.Mode().IsRegular() || st.Size() > 8<<20 {
		return nil, ErrStorage
	}
	b, e := io.ReadAll(io.LimitReader(f, (8<<20)+1))
	if e != nil || len(b) > 8<<20 {
		return nil, ErrStorage
	}
	return b, nil
}
func atomicJSON(path string, v any, replace bool) error {
	b, e := json.Marshal(v)
	if e != nil || len(b) > 8<<20 {
		return ErrStorage
	}
	dir := filepath.Dir(path)
	if e = os.MkdirAll(dir, 0700); e != nil {
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
	if replace {
		e = os.Rename(f.Name(), path)
	} else {
		e = os.Link(f.Name(), path)
	}
	if e != nil {
		return ErrStorage
	}
	return syncDir(dir)
}
func (s *Store) withFile(ctx context.Context, space string, fn func(*fileAccess) error) error {
	if !series.ScopeID.MatchString(space) {
		return series.ErrInput
	}
	hash := series.SHA(space)
	f, e := os.OpenFile(filepath.Join(s.dir, ".lock-"+hash), os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if e != nil {
		return ErrStorage
	}
	defer f.Close()
	if st, e := f.Stat(); e != nil || !st.Mode().IsRegular() {
		return ErrStorage
	}
	for {
		if e = ctx.Err(); e != nil {
			return e
		}
		e = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if e == nil {
			break
		}
		if !errors.Is(e, syscall.EWOULDBLOCK) {
			return ErrStorage
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	base := filepath.Join(s.dir, hash)
	if e = privateDirectory(base); e != nil {
		return e
	}
	if e = privateDirectory(filepath.Join(base, "commits")); e != nil {
		return ErrStorage
	}
	if e = privateDirectory(filepath.Join(base, "nodes")); e != nil {
		return ErrStorage
	}
	header := struct {
		Format int
		Space  string
	}{1, space}
	b, e := read(filepath.Join(base, ".space.json"))
	if os.IsNotExist(e) {
		entries, err := os.ReadDir(filepath.Join(base, "commits"))
		if err != nil {
			return ErrStorage
		}
		for _, entry := range entries {
			if strings.HasSuffix(entry.Name(), ".json") {
				return ErrStorage
			}
		}
		if e = atomicJSON(filepath.Join(base, ".space.json"), header, false); e != nil {
			return e
		}
	} else {
		var old struct {
			Format int
			Space  string
		}
		if e != nil || json.Unmarshal(b, &old) != nil || old != header {
			return ErrStorage
		}
	}
	if e = syncDir(s.dir); e != nil {
		return e
	}
	return fn(&fileAccess{s, ctx, space, base})
}
func (f *fileAccess) marker(id string) (*fileCommit, error) {
	if b, err := hex.DecodeString(id); err != nil || len(b) != 32 {
		return nil, ErrStorage
	}
	b, e := read(filepath.Join(f.base, "commits", id+".json"))
	if os.IsNotExist(e) {
		return nil, nil
	}
	var c fileCommit
	if e != nil || json.Unmarshal(b, &c) != nil || c.Format != 1 || c.Space != f.space || c.Sequence < 1 || len(c.Rows) == 0 {
		return nil, ErrStorage
	}
	return &c, nil
}
func (f *fileAccess) get(kind, id string) (series.Node, error) {
	if (kind != "series" && kind != "season" && kind != "episode") || !series.ID.MatchString(id) {
		return series.Node{}, series.ErrNotFound
	}
	dir := filepath.Join(f.base, "nodes", kind, id)
	for _, path := range []string{filepath.Dir(dir), dir} {
		if err := directory(path); os.IsNotExist(err) {
			return series.Node{}, series.ErrNotFound
		} else if err != nil {
			return series.Node{}, ErrStorage
		}
	}
	entries, e := os.ReadDir(dir)
	if os.IsNotExist(e) {
		return series.Node{}, series.ErrNotFound
	}
	if e != nil {
		return series.Node{}, ErrStorage
	}
	names := []string{}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".pending-") {
			continue
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			return series.Node{}, ErrStorage
		}
		names = append(names, entry.Name())
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	for _, name := range names {
		if e = f.ctx.Err(); e != nil {
			return series.Node{}, e
		}
		if len(name) != 20+1+64+5 || name[20] != '-' {
			return series.Node{}, ErrStorage
		}
		seq, e := strconv.ParseInt(name[:20], 10, 64)
		if e != nil || seq < 1 {
			return series.Node{}, ErrStorage
		}
		txid := name[21 : len(name)-5]
		c, e := f.marker(txid)
		if e != nil {
			return series.Node{}, e
		}
		if c == nil {
			continue
		}
		if c.Sequence != seq {
			return series.Node{}, ErrStorage
		}
		b, e := read(filepath.Join(dir, name))
		if e != nil {
			return series.Node{}, ErrStorage
		}
		found := false
		for _, r := range c.Rows {
			if r.Kind == kind && r.ID == id && r.File == name && r.SHA == series.SHA(string(b)) {
				found = true
				break
			}
		}
		var row fileRow
		if !found || json.Unmarshal(b, &row) != nil || row.Format != 1 || row.Sequence != seq || !series.Valid(row.Node) || row.Node.Space != f.space || row.Node.ID != id || row.Node.Kind != kind {
			return series.Node{}, ErrStorage
		}
		return row.Node, nil
	}
	return series.Node{}, series.ErrNotFound
}
func (f *fileAccess) children(kind, parent string) ([]series.Node, error) {
	if kind != "series" && kind != "season" && kind != "episode" {
		return nil, series.ErrInput
	}
	path := filepath.Join(f.base, "nodes", kind)
	if err := directory(path); os.IsNotExist(err) {
		return []series.Node{}, nil
	} else if err != nil {
		return nil, ErrStorage
	}
	entries, e := os.ReadDir(path)
	if os.IsNotExist(e) {
		return []series.Node{}, nil
	}
	if e != nil {
		return nil, ErrStorage
	}
	out := []series.Node{}
	for _, entry := range entries {
		if !entry.IsDir() || !series.ID.MatchString(entry.Name()) {
			return nil, ErrStorage
		}
		n, e := f.get(kind, entry.Name())
		if errors.Is(e, series.ErrNotFound) {
			continue
		}
		if e != nil {
			return nil, e
		}
		if parent == "" || n.Parent == parent {
			out = append(out, n)
		}
	}
	return out, nil
}
func (f *fileAccess) commit(txid string, rows []series.Node, r *receipt) error {
	var seq int64
	b, e := read(filepath.Join(f.base, ".sequence.json"))
	if e == nil {
		if json.Unmarshal(b, &seq) != nil || seq < 0 || seq == 1<<63-1 {
			return ErrStorage
		}
	} else if os.IsNotExist(e) {
		entries, err := os.ReadDir(filepath.Join(f.base, "commits"))
		if err != nil {
			return ErrStorage
		}
		for _, entry := range entries {
			if strings.HasSuffix(entry.Name(), ".json") {
				return ErrStorage
			}
		}
	} else {
		return ErrStorage
	}
	seq++
	if e = atomicJSON(filepath.Join(f.base, ".sequence.json"), seq, true); e != nil {
		return e
	}
	c := fileCommit{Format: 1, Space: f.space, Sequence: seq, Receipt: r}
	name := fmt.Sprintf("%020d-%s.json", seq, txid)
	for _, n := range rows {
		if !series.Valid(n) || n.Space != f.space {
			return ErrStorage
		}
		row := fileRow{1, seq, n}
		b, e := json.Marshal(row)
		if e != nil {
			return ErrStorage
		}
		dir := filepath.Join(f.base, "nodes", n.Kind, n.ID)
		if e = privateDirectory(filepath.Dir(dir)); e != nil {
			return e
		}
		if e = privateDirectory(dir); e != nil {
			return e
		}
		if e = atomicJSON(filepath.Join(dir, name), row, false); e != nil {
			return e
		}
		if e = syncDir(filepath.Dir(dir)); e != nil {
			return e
		}
		c.Rows = append(c.Rows, fileRef{n.Kind, n.ID, name, series.SHA(string(b))})
	}
	if e = syncDir(filepath.Join(f.base, "nodes")); e != nil {
		return e
	}
	if e = atomicJSON(filepath.Join(f.base, "commits", txid+".json"), c, false); e != nil {
		return e
	}
	return syncDir(f.base)
}
func randomTx() (string, error) {
	var b [32]byte
	if _, e := rand.Read(b[:]); e != nil {
		return "", ErrStorage
	}
	return hex.EncodeToString(b[:]), nil
}
func (s *Store) mutateFile(ctx context.Context, a series.Actor, key string, c series.Command, project func(series.Node) error) (series.Node, error) {
	if !validActor(a) || !a.Writable {
		return series.Node{}, series.ErrDenied
	}
	if len(key) < 8 || len(key) > 128 {
		return series.Node{}, series.ErrInput
	}
	var result series.Node
	e := s.withFile(ctx, a.Space, func(f *fileAccess) error {
		hash, digest := keyHash(a, c, key), series.Digest(c)
		prior, e := f.marker(hash)
		if e != nil {
			return e
		}
		if prior != nil {
			if prior.Receipt == nil || prior.Receipt.KeyHash != hash || prior.Receipt.Digest != digest {
				return ErrKey
			}
			result, e = f.get(prior.Receipt.Kind, prior.Receipt.ID)
			if e != nil {
				return e
			}
		} else {
			if c.Operation == "create" && c.Kind == "episode" && project == nil {
				return ErrStorage
			}
			m, e := apply(a, c, f, true)
			if e != nil {
				return e
			}
			r := receipt{a.User, hash, digest, m.Result.ID, m.Result.Kind}
			if e = f.commit(hash, m.Rows, &r); e != nil {
				return e
			}
			result = m.Result
		}
		if result.Kind == "episode" && result.State == "preparing" {
			if project == nil {
				return ErrStorage
			}
			if e = project(result); e != nil {
				return e
			}
			result.State = "ready"
			txid, err := randomTx()
			if err != nil {
				return err
			}
			if e = f.commit(txid, []series.Node{result}, nil); e != nil {
				return e
			}
		}
		return nil
	})
	return result, e
}

// RecoverPreparing is startup-only, after the core store and this repository are
// open but before HTTP. It completes accepted metadata intents, never models.
func (s *Store) RecoverPreparing(ctx context.Context, project func(series.Node) error) error {
	if s.db != nil {
		return nil
	}
	if project == nil {
		return ErrStorage
	}
	entries, e := os.ReadDir(s.dir)
	if e != nil {
		return e
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		b, e := read(filepath.Join(s.dir, entry.Name(), ".space.json"))
		if e != nil {
			return ErrStorage
		}
		var h struct {
			Format int
			Space  string
		}
		if json.Unmarshal(b, &h) != nil || h.Format != 1 || entry.Name() != series.SHA(h.Space) {
			return ErrStorage
		}
		if e = s.withFile(ctx, h.Space, func(f *fileAccess) error {
			nodes, e := f.children("episode", "")
			if e != nil {
				return e
			}
			for _, n := range nodes {
				if n.State == "preparing" {
					if e = project(n); e != nil {
						return e
					}
					n.State = "ready"
					txid, err := randomTx()
					if err != nil {
						return err
					}
					if e = f.commit(txid, []series.Node{n}, nil); e != nil {
						return e
					}
				}
			}
			return nil
		}); e != nil {
			return e
		}
	}
	return nil
}
