// Package storyrepo appends one immutable authored version and its receipt per
// transaction. It has no tenant-wide body or history snapshot.
package storyrepo

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/story"
)

var ErrStorage = errors.New("story storage unavailable")
var ErrKey = errors.New("story idempotency conflict")

const Schema = checkSchema + decisionSchema + contextSchema + `CREATE TABLE IF NOT EXISTS studio_story_versions (
 space_id text NOT NULL,series_id text NOT NULL,id text NOT NULL,number bigint NOT NULL CHECK(number>0),version_id text NOT NULL,key_hash text NOT NULL,request_digest text NOT NULL,payload jsonb NOT NULL,
 PRIMARY KEY(space_id,series_id,id,number),UNIQUE(space_id,series_id,id,version_id),UNIQUE(space_id,series_id,id,key_hash),
 CHECK((payload->'version'->>'space_id'=space_id AND payload->'version'->>'series_id'=series_id AND payload->'version'->>'id'=id AND (payload->'version'->>'number')::bigint=number AND payload->'version'->>'version_id'=version_id AND payload->>'key_hash'=key_hash AND payload->>'request_digest'=request_digest) IS TRUE));
 CREATE TABLE IF NOT EXISTS studio_story_heads (
 space_id text NOT NULL,series_id text NOT NULL,id text NOT NULL,number bigint NOT NULL,version_id text NOT NULL,kind text NOT NULL,status text NOT NULL,title text NOT NULL,required boolean NOT NULL,sha256 text NOT NULL,
 PRIMARY KEY(space_id,series_id,id),FOREIGN KEY(space_id,series_id,id,number) REFERENCES studio_story_versions(space_id,series_id,id,number));
 CREATE INDEX IF NOT EXISTS studio_story_list ON studio_story_heads(space_id,series_id,kind,status,id);`

type Store struct {
	dir string
	db  *sql.DB
}
type Scope struct{ Space, Series string }
type Query struct {
	Kind, Status, After string
	Limit               int
}
type Page struct {
	Items []story.Summary `json:"items"`
	Next  string          `json:"next_cursor"`
}
type HistoryPage struct {
	Items []story.Version `json:"items"`
	Next  string          `json:"next_cursor"`
}
type record struct {
	Adoption *DecisionRecord `json:"adoption,omitempty"`
	Format   int             `json:"format"`
	Key      string          `json:"key_hash"`
	Digest   string          `json:"request_digest"`
	Version  story.Version   `json:"version"`
}

var keyPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{8,128}$`)

var namePattern = regexp.MustCompile(`^([0-9]{20})-([a-f0-9]{64})\.json$`)

func BibleID(series string) string { return story.TextHash("story-bible-v1:" + series)[:32] }
func validScope(q Scope) bool {
	return story.Scope.MatchString(q.Space) && story.ID.MatchString(q.Series)
}
func (s *Store) path(q Scope, id string) string {
	return filepath.Join(s.dir, story.TextHash(q.Space), q.Series, id)
}
func realDir(p string) error {
	st, e := os.Lstat(p)
	if e != nil {
		return e
	}
	if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return ErrStorage
	}
	return nil
}
func Open(dir string, db *sql.DB) (*Store, error) {
	if db == nil {
		if e := os.MkdirAll(dir, 0700); e != nil {
			return nil, e
		}
		if e := realDir(dir); e != nil {
			return nil, e
		}
	}
	return &Store{dir, db}, nil
}
func validRecord(r record) bool {
	return (r.Adoption == nil || (validDecision(*r.Adoption) && r.Adoption.Decision.Action == "adopt" && r.Adoption.Decision.TargetRecordID == r.Version.ID && r.Adoption.Decision.VersionID == r.Version.VersionID && r.Adoption.Decision.Actor == r.Version.Actor)) && r.Format == 1 && story.Hash.MatchString(r.Key) && story.Hash.MatchString(r.Digest) && story.Valid(r.Version) && (r.Version.Kind != "cast" || r.Version.ID == story.CastID(r.Version.SeriesID, r.Version.Body.Cast.EpisodeID)) && ((r.Version.Kind == "bible") == (r.Version.ID == BibleID(r.Version.SeriesID)))
}
func decode(raw io.Reader) (record, error) {
	var r record
	d := json.NewDecoder(raw)
	d.DisallowUnknownFields()
	if d.Decode(&r) != nil || !validRecord(r) {
		return record{}, ErrStorage
	}
	var tail any
	if d.Decode(&tail) != io.EOF {
		return record{}, ErrStorage
	}
	return r, nil
}
func read(p string) (record, error) {
	if realDir(filepath.Dir(p)) != nil {
		return record{}, ErrStorage
	}
	f, e := os.OpenFile(p, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if e != nil {
		return record{}, ErrStorage
	}
	defer f.Close()
	st, e := f.Stat()
	if e != nil || !st.Mode().IsRegular() {
		return record{}, ErrStorage
	}
	r, e := decode(f)
	if e == nil && filepath.Base(p) != name(r) {
		e = ErrStorage
	}
	return r, e
}
func name(r record) string { return fmt.Sprintf("%020d-%s.json", r.Version.Number, r.Key) }
func syncDir(p string) error {
	f, e := os.Open(p)
	if e != nil {
		return e
	}
	return errors.Join(f.Sync(), f.Close())
}
func (s *Store) makeDirs(q Scope, id string) error {
	p := s.dir
	for _, part := range []string{story.TextHash(q.Space), q.Series, id} {
		p = filepath.Join(p, part)
		if e := os.Mkdir(p, 0700); e != nil && !os.IsExist(e) {
			return ErrStorage
		}
		if realDir(p) != nil {
			return ErrStorage
		}
		if syncDir(filepath.Dir(p)) != nil {
			return ErrStorage
		}
	}
	return nil
}
func (s *Store) appendFile(q Scope, r record) error {
	if !validRecord(r) || r.Version.Space != q.Space || r.Version.SeriesID != q.Series {
		return ErrStorage
	}
	if e := s.makeDirs(q, r.Version.ID); e != nil {
		return e
	}
	dir := s.path(q, r.Version.ID)
	f, e := os.CreateTemp(dir, ".pending-")
	if e != nil {
		return ErrStorage
	}
	defer os.Remove(f.Name())
	e = json.NewEncoder(f).Encode(r)
	if e == nil {
		e = f.Sync()
	}
	e = errors.Join(e, f.Close())
	if e != nil {
		return ErrStorage
	}
	if e = os.Link(f.Name(), filepath.Join(dir, name(r))); e != nil {
		return ErrStorage
	}
	return syncDir(dir)
}
func (s *Store) withFile(ctx context.Context, q Scope, write bool, fn func() error) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	if realDir(s.dir) != nil {
		return ErrStorage
	}
	f, e := os.OpenFile(filepath.Join(s.dir, ".lock-"+story.TextHash(q.Space+":"+q.Series)), os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if e != nil {
		return ErrStorage
	}
	defer f.Close()
	st, e := f.Stat()
	if e != nil || !st.Mode().IsRegular() {
		return ErrStorage
	}
	mode := syscall.LOCK_SH
	if write {
		mode = syscall.LOCK_EX
	}
	for {
		if e = ctx.Err(); e != nil {
			return e
		}
		e = syscall.Flock(int(f.Fd()), mode|syscall.LOCK_NB)
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
	return fn()
}
func (s *Store) entries(q Scope, id string) ([]string, error) {
	for _, p := range []string{filepath.Join(s.dir, story.TextHash(q.Space)), filepath.Join(s.dir, story.TextHash(q.Space), q.Series), s.path(q, id)} {
		if e := realDir(p); os.IsNotExist(e) {
			return []string{}, nil
		} else if e != nil {
			return nil, ErrStorage
		}
	}
	list, e := os.ReadDir(s.path(q, id))
	if e != nil {
		return nil, ErrStorage
	}
	out := []string{}
	for _, v := range list {
		if strings.HasPrefix(v.Name(), ".pending-") {
			continue
		}
		if v.IsDir() || v.Type()&os.ModeSymlink != 0 || !namePattern.MatchString(v.Name()) {
			return nil, ErrStorage
		}
		out = append(out, v.Name())
	}
	sort.Strings(out)
	for i, n := range out {
		number, e := strconv.ParseInt(n[:20], 10, 64)
		if e != nil || number != int64(i)+1 {
			return nil, ErrStorage
		}
	}
	return out, nil
}
func (s *Store) fileCurrent(q Scope, id string) (story.Version, error) {
	list, e := s.entries(q, id)
	if e != nil {
		return story.Version{}, e
	}
	if len(list) == 0 {
		return story.Version{}, story.ErrNotFound
	}
	r, e := read(filepath.Join(s.path(q, id), list[len(list)-1]))
	if e != nil || r.Version.ID != id || r.Version.Space != q.Space || r.Version.SeriesID != q.Series {
		return story.Version{}, ErrStorage
	}
	return r.Version, nil
}
func apply(a story.Actor, q Scope, id, key string, in story.Input, old *story.Version) (record, error) {
	if old == nil && in.ExpectedVersion != 0 || old != nil && (old.Number != in.ExpectedVersion || old.Kind != in.Kind) {
		return record{}, story.ErrConflict
	}
	n := int64(1)
	if old != nil {
		n = old.Number + 1
	}
	if n > story.MaxVersion {
		return record{}, story.ErrInput
	}
	r := record{Format: 1, Key: story.TextHash(a.User + ":" + key), Digest: story.Digest(in)}
	r.Version = story.Version{Format: 1, ID: id, Space: q.Space, SeriesID: q.Series, VersionID: story.TextHash(a.Space + ":" + q.Series + ":" + id + ":" + r.Key)[:32], Number: n, Title: in.Title, Kind: in.Kind, Status: in.Status, Required: in.Required, Body: in.Body, SHA256: story.ContentDigest(in), Actor: a.User, CreatedAt: time.Now().UTC()}
	if !validRecord(r) {
		return record{}, ErrStorage
	}
	return r, nil
}
func (s *Store) Save(ctx context.Context, a story.Actor, series, id, key string, in story.Input) (story.Version, error) {
	q := Scope{a.Space, series}
	if !a.Writable {
		return story.Version{}, story.ErrDenied
	}
	if !validScope(q) || !story.Scope.MatchString(a.User) || !story.ID.MatchString(id) || !regexp.MustCompile(`^[A-Za-z0-9_-]{8,128}$`).MatchString(key) {
		return story.Version{}, story.ErrInput
	}
	in, e := story.Normalize(in)
	if e != nil {
		return story.Version{}, e
	}
	if in.Kind == "cast" && id != story.CastID(series, in.Body.Cast.EpisodeID) || in.Kind == "bible" && id != BibleID(series) {
		return story.Version{}, story.ErrInput
	}
	if s.db != nil {
		return s.saveSQL(ctx, a, q, id, key, in)
	}
	var out story.Version
	e = s.withFile(ctx, q, true, func() error {
		list, e := s.entries(q, id)
		if e != nil {
			return e
		}
		hash := story.TextHash(a.User + ":" + key)
		for _, n := range list {
			if strings.HasSuffix(n, "-"+hash+".json") {
				r, e := read(filepath.Join(s.path(q, id), n))
				if e != nil {
					return e
				}
				if r.Version.Space != q.Space || r.Version.SeriesID != q.Series || r.Version.ID != id {
					return ErrStorage
				}
				if r.Digest != story.Digest(in) {
					return ErrKey
				}
				out = r.Version
				return nil
			}
		}
		var old *story.Version
		if len(list) > 0 {
			v, e := s.fileCurrent(q, id)
			if e != nil {
				return e
			}
			old = &v
		}
		r, e := apply(a, q, id, key, in, old)
		if e != nil {
			return e
		}
		if e = s.appendFile(q, r); e != nil {
			return e
		}
		out = r.Version
		return nil
	})
	return out, e
}
func (s *Store) Get(ctx context.Context, q Scope, id, version string) (story.Version, error) {
	if !validScope(q) || !story.ID.MatchString(id) || version != "" && !story.ID.MatchString(version) {
		return story.Version{}, story.ErrNotFound
	}
	if s.db != nil {
		return s.getSQL(ctx, q, id, version)
	}
	var out story.Version
	e := s.withFile(ctx, q, false, func() error {
		if version == "" {
			var e error
			out, e = s.fileCurrent(q, id)
			return e
		}
		list, e := s.entries(q, id)
		if e != nil {
			return e
		}
		for _, n := range list {
			r, e := read(filepath.Join(s.path(q, id), n))
			if e != nil {
				return e
			}
			if r.Version.ID != id || r.Version.Space != q.Space || r.Version.SeriesID != q.Series {
				return ErrStorage
			}
			if r.Version.VersionID == version {
				out = r.Version
				return nil
			}
		}
		return story.ErrNotFound
	})
	return out, e
}
func validQuery(q Query) bool {
	return q.Limit > 0 && q.Limit <= 100 && (q.After == "" || story.ID.MatchString(q.After)) && (q.Kind == "" || q.Kind == "bible" || q.Kind == "fact" || q.Kind == "event" || q.Kind == "foreshadow" || q.Kind == "cast") && (q.Status == "" || q.Status == "active" || q.Status == "archived")
}
func (s *Store) ids(q Scope) ([]string, error) {
	p := filepath.Join(s.dir, story.TextHash(q.Space), q.Series)
	if e := realDir(filepath.Dir(p)); os.IsNotExist(e) {
		return []string{}, nil
	} else if e != nil {
		return nil, ErrStorage
	}
	if e := realDir(p); os.IsNotExist(e) {
		return []string{}, nil
	} else if e != nil {
		return nil, ErrStorage
	}
	entries, e := os.ReadDir(p)
	if e != nil {
		return nil, ErrStorage
	}
	out := []string{}
	for _, v := range entries {
		if v.Name() == ".contexts" || v.Name() == ".checks" || v.Name() == ".decisions" {
			if !v.IsDir() || v.Type()&os.ModeSymlink != 0 {
				return nil, ErrStorage
			}
			continue
		}
		if !v.IsDir() || !story.ID.MatchString(v.Name()) || v.Type()&os.ModeSymlink != 0 {
			return nil, ErrStorage
		}
		out = append(out, v.Name())
	}
	sort.Strings(out)
	return out, nil
}
func (s *Store) List(ctx context.Context, q Scope, query Query) (Page, error) {
	out := Page{Items: []story.Summary{}}
	if !validScope(q) || !validQuery(query) {
		return out, story.ErrInput
	}
	if s.db != nil {
		return s.listSQL(ctx, q, query)
	}
	e := s.withFile(ctx, q, false, func() error {
		ids, e := s.ids(q)
		if e != nil {
			return e
		}
		for _, id := range ids {
			if id <= query.After {
				continue
			}
			v, e := s.fileCurrent(q, id)
			if errors.Is(e, story.ErrNotFound) {
				continue
			}
			if e != nil {
				return e
			}
			if query.Kind != "" && v.Kind != query.Kind || query.Status != "" && v.Status != query.Status {
				continue
			}
			out.Items = append(out.Items, story.Summarize(v))
			if len(out.Items) > query.Limit {
				out.Next = out.Items[query.Limit-1].ID
				out.Items = out.Items[:query.Limit]
				break
			}
		}
		return nil
	})
	return out, e
}

// Snapshot pins current versions under one series lock/read transaction. A
// context caller can compare its digest after reading directory/project inputs.
func (s *Store) Snapshot(ctx context.Context, q Scope) ([]story.Version, error) {
	if !validScope(q) {
		return nil, story.ErrInput
	}
	if s.db != nil {
		return s.snapshotSQL(ctx, q)
	}
	out := []story.Version{}
	e := s.withFile(ctx, q, false, func() error {
		ids, e := s.ids(q)
		if e != nil {
			return e
		}
		for _, id := range ids {
			v, e := s.fileCurrent(q, id)
			if errors.Is(e, story.ErrNotFound) {
				continue
			}
			if e != nil {
				return e
			}
			out = append(out, v)
		}
		return nil
	})
	return out, e
}
func (s *Store) History(ctx context.Context, q Scope, id string, before int64, limit int) (HistoryPage, error) {
	out := HistoryPage{Items: []story.Version{}}
	if !validScope(q) || !story.ID.MatchString(id) || before < 0 || limit < 1 || limit > 100 {
		return out, story.ErrInput
	}
	if s.db != nil {
		return s.historySQL(ctx, q, id, before, limit)
	}
	e := s.withFile(ctx, q, false, func() error {
		list, e := s.entries(q, id)
		if e != nil {
			return e
		}
		if len(list) == 0 {
			return story.ErrNotFound
		}
		for i := len(list) - 1; i >= 0; i-- {
			n, _ := strconv.ParseInt(list[i][:20], 10, 64)
			if before > 0 && n >= before {
				continue
			}
			r, e := read(filepath.Join(s.path(q, id), list[i]))
			if e != nil || r.Version.Space != q.Space || r.Version.SeriesID != q.Series || r.Version.ID != id {
				return ErrStorage
			}
			out.Items = append(out.Items, r.Version)
			if len(out.Items) > limit {
				out.Next = strconv.FormatInt(out.Items[limit-1].Number, 10)
				out.Items = out.Items[:limit]
				break
			}
		}
		return nil
	})
	return out, e
}
func decodeBytes(b []byte) (record, error) { return decode(bytes.NewReader(b)) }
