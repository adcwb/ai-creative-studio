// Package migrationrepo stores each episode's plan and recovery checkpoints as
// one entity. No task from a different source/space can supply its checkpoint.
package migrationrepo

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"syscall"
	"time"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/series"
	m "github.com/adcwb/ai-creative-studio/backend/internal/modules/seriesmigration"
)

var ErrStorage = errors.New("episode migration storage unavailable")

const Schema = `CREATE TABLE IF NOT EXISTS studio_episode_migrations (
 space_id text NOT NULL,id text NOT NULL,source_project_id text NOT NULL,row_version bigint NOT NULL CHECK(row_version>0),state text NOT NULL,payload jsonb NOT NULL,
 PRIMARY KEY(space_id,id),CHECK((payload->>'space_id'=space_id AND payload->>'id'=id AND payload->>'source_project_id'=source_project_id AND (payload->>'row_version')::bigint=row_version AND payload->>'state'=state) IS TRUE));
 CREATE INDEX IF NOT EXISTS studio_episode_migration_list ON studio_episode_migrations(space_id,source_project_id,id);`

type Store struct {
	dir string
	db  *sql.DB
}
type Identity struct{ Space, Project, ID string }
type Locked struct {
	Job   m.Job
	store *Store
	id    Identity
	ctx   context.Context
	conn  *sql.Conn
}

func Open(dir string, db *sql.DB) (*Store, error) {
	if db == nil {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return nil, err
		}
		if err := realDirectory(dir); err != nil {
			return nil, err
		}
	}
	return &Store{dir, db}, nil
}
func realDirectory(path string) error {
	st, e := os.Lstat(path)
	if e != nil {
		return e
	}
	if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return ErrStorage
	}
	return nil
}
func valid(i Identity) bool {
	return series.ScopeID.MatchString(i.Space) && series.ID.MatchString(i.Project) && series.ID.MatchString(i.ID)
}
func (s *Store) path(i Identity) string {
	return filepath.Join(s.dir, series.SHA(i.Space), i.ID+".json")
}
func readFile(path string) (m.Job, error) {
	if e := realDirectory(filepath.Dir(path)); os.IsNotExist(e) {
		return m.Job{}, m.ErrNotFound
	} else if e != nil {
		return m.Job{}, ErrStorage
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if os.IsNotExist(err) {
		return m.Job{}, m.ErrNotFound
	}
	if err != nil {
		return m.Job{}, ErrStorage
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		return m.Job{}, ErrStorage
	}
	return decodeJob(f)
}
func decodeJob(r io.Reader) (m.Job, error) {
	var j m.Job
	d := json.NewDecoder(r)
	d.DisallowUnknownFields()
	if d.Decode(&j) != nil || !m.ValidJob(j) {
		return m.Job{}, ErrStorage
	}
	var trailing any
	if d.Decode(&trailing) != io.EOF {
		return m.Job{}, ErrStorage
	}
	return j, nil
}
func syncDir(path string) error {
	f, e := os.Open(path)
	if e != nil {
		return e
	}
	return errors.Join(f.Sync(), f.Close())
}
func writeFile(path string, j m.Job) error {
	if !m.ValidJob(j) {
		return ErrStorage
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return ErrStorage
	}
	if realDirectory(dir) != nil || realDirectory(filepath.Dir(dir)) != nil {
		return ErrStorage
	}
	f, err := os.CreateTemp(dir, ".pending-")
	if err != nil {
		return ErrStorage
	}
	defer os.Remove(f.Name())
	err = json.NewEncoder(f).Encode(j)
	if err == nil {
		err = f.Sync()
	}
	err = errors.Join(err, f.Close())
	if err == nil {
		err = os.Rename(f.Name(), path)
	}
	if err == nil {
		err = syncDir(dir)
	}
	if err == nil {
		err = syncDir(filepath.Dir(dir))
	}
	return err
}
func (s *Store) Get(ctx context.Context, i Identity) (m.Job, error) {
	if !valid(i) {
		return m.Job{}, m.ErrNotFound
	}
	if err := ctx.Err(); err != nil {
		return m.Job{}, err
	}
	var j m.Job
	var err error
	if s.db == nil {
		j, err = readFile(s.path(i))
	} else {
		var b []byte
		err = s.db.QueryRowContext(ctx, `SELECT payload FROM studio_episode_migrations WHERE space_id=$1 AND id=$2`, i.Space, i.ID).Scan(&b)
		if errors.Is(err, sql.ErrNoRows) {
			return j, m.ErrNotFound
		}
		if err == nil {
			j, err = decodeJob(bytes.NewReader(b))
		}
	}
	if err != nil {
		return m.Job{}, err
	}
	if j.Space != i.Space || j.ID != i.ID || j.ProjectID != i.Project {
		return m.Job{}, m.ErrNotFound
	}
	return j, nil
}
func (l *Locked) Check() error {
	if err := l.ctx.Err(); err != nil {
		return err
	}
	if l.conn != nil {
		var one int
		if l.conn.QueryRowContext(l.ctx, "SELECT 1").Scan(&one) != nil {
			return ErrStorage
		}
	}
	return nil
}
func (s *Store) With(ctx context.Context, i Identity, create *m.Job, fn func(*Locked) error) error {
	if !valid(i) {
		return m.ErrNotFound
	}
	if create != nil && (!m.ValidJob(*create) || create.ID != i.ID || create.Space != i.Space || create.ProjectID != i.Project) {
		return m.ErrInput
	}
	l := &Locked{store: s, id: i, ctx: ctx}
	if s.db == nil {
		f, e := os.OpenFile(filepath.Join(s.dir, ".lock-"+series.SHA(i.Space+":"+i.ID)), os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
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
		j, e := readFile(s.path(i))
		if errors.Is(e, m.ErrNotFound) && create != nil {
			j = *create
			e = writeFile(s.path(i), j)
		}
		if e != nil {
			return e
		}
		if j.Space != i.Space || j.ID != i.ID || j.ProjectID != i.Project {
			if create != nil {
				return m.ErrConflict
			}
			return m.ErrNotFound
		}
		l.Job = j
	} else {
		for {
			c, e := s.db.Conn(ctx)
			if e != nil {
				return ErrStorage
			}
			var ok bool
			e = c.QueryRowContext(ctx, `SELECT pg_try_advisory_lock(hashtextextended($1,728219139))`, i.Space+":"+i.ID).Scan(&ok)
			if e != nil {
				c.Raw(func(any) error { return driver.ErrBadConn })
				c.Close()
				return ErrStorage
			}
			if ok {
				l.conn = c
				break
			}
			c.Close()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(10 * time.Millisecond):
			}
		}
		defer func() {
			cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if _, e := l.conn.ExecContext(cleanup, `SELECT pg_advisory_unlock(hashtextextended($1,728219139))`, i.Space+":"+i.ID); e != nil {
				l.conn.Raw(func(any) error { return driver.ErrBadConn })
			}
			l.conn.Close()
		}()
		if create != nil {
			if !m.ValidJob(*create) {
				return ErrStorage
			}
			b, e := json.Marshal(create)
			if e != nil {
				return e
			}
			tx, e := l.conn.BeginTx(ctx, nil)
			if e != nil {
				return ErrStorage
			}
			if _, e = tx.ExecContext(ctx, `SET LOCAL synchronous_commit=on;SELECT pg_advisory_xact_lock_shared(728219117)`); e == nil {
				_, e = tx.ExecContext(ctx, `INSERT INTO studio_episode_migrations(space_id,id,source_project_id,row_version,state,payload) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT DO NOTHING`, i.Space, i.ID, i.Project, create.Version, create.State, b)
			}
			if e != nil {
				tx.Rollback()
				return ErrStorage
			}
			if tx.Commit() != nil {
				return ErrStorage
			}
		}
		var b []byte
		e := l.conn.QueryRowContext(ctx, `SELECT payload FROM studio_episode_migrations WHERE space_id=$1 AND id=$2`, i.Space, i.ID).Scan(&b)
		if errors.Is(e, sql.ErrNoRows) {
			return m.ErrNotFound
		}
		if e != nil {
			return ErrStorage
		}
		l.Job, e = decodeJob(bytes.NewReader(b))
		if e != nil {
			return e
		}
	}
	if l.Job.Space != i.Space || l.Job.ID != i.ID || l.Job.ProjectID != i.Project {
		if create != nil {
			return m.ErrConflict
		}
		return m.ErrNotFound
	}
	if create != nil && (l.Job.Plan.Digest != create.Plan.Digest || l.Job.Actor != create.Actor) {
		return m.ErrConflict
	}
	return fn(l)
}
func (l *Locked) Save(next m.Job) error {
	if err := l.Check(); err != nil {
		return err
	}
	if next.ID != l.Job.ID || next.Space != l.Job.Space || next.ProjectID != l.Job.ProjectID || next.Actor != l.Job.Actor || next.Plan.Digest != l.Job.Plan.Digest || next.Version != l.Job.Version {
		return m.ErrConflict
	}
	next.Version++
	next.UpdatedAt = time.Now().UTC()
	if !m.ValidJob(next) {
		return ErrStorage
	}
	if l.conn == nil {
		if err := writeFile(l.store.path(l.id), next); err != nil {
			return err
		}
	} else {
		tx, e := l.conn.BeginTx(l.ctx, nil)
		if e != nil {
			return ErrStorage
		}
		defer tx.Rollback()
		if _, e = tx.ExecContext(l.ctx, `SET LOCAL synchronous_commit=on;SELECT pg_advisory_xact_lock_shared(728219117)`); e != nil {
			return ErrStorage
		}
		b, e := json.Marshal(next)
		if e != nil {
			return e
		}
		r, e := tx.ExecContext(l.ctx, `UPDATE studio_episode_migrations SET row_version=$4,state=$5,payload=$6 WHERE space_id=$1 AND id=$2 AND row_version=$3`, next.Space, next.ID, l.Job.Version, next.Version, next.State, b)
		if e != nil {
			return ErrStorage
		}
		n, e := r.RowsAffected()
		if e != nil || n != 1 {
			return m.ErrConflict
		}
		if tx.Commit() != nil {
			return ErrStorage
		}
	}
	l.Job = next
	return nil
}
func (s *Store) List(ctx context.Context, space, project, after string, limit int) ([]m.Job, string, error) {
	if !series.ScopeID.MatchString(space) || !series.ID.MatchString(project) || after != "" && !series.ID.MatchString(after) || limit < 1 || limit > 100 {
		return nil, "", m.ErrInput
	}
	out := []m.Job{}
	if s.db == nil {
		entries, e := os.ReadDir(filepath.Join(s.dir, series.SHA(space)))
		if os.IsNotExist(e) {
			return out, "", nil
		}
		if e != nil {
			return nil, "", ErrStorage
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
		for _, entry := range entries {
			if entry.IsDir() {
				return nil, "", ErrStorage
			}
			name := entry.Name()
			if len(name) != 37 || filepath.Ext(name) != ".json" {
				if len(name) >= 9 && name[:9] == ".pending-" {
					continue
				}
				return nil, "", ErrStorage
			}
			id := name[:32]
			if id <= after {
				continue
			}
			j, e := readFile(filepath.Join(s.dir, series.SHA(space), name))
			if e != nil || j.Space != space || j.ID != id {
				return nil, "", ErrStorage
			}
			if j.ProjectID == project {
				out = append(out, j)
				if len(out) > limit {
					break
				}
			}
		}
	} else {
		rows, e := s.db.QueryContext(ctx, `SELECT payload FROM studio_episode_migrations WHERE space_id=$1 AND source_project_id=$2 AND id>$3 ORDER BY id LIMIT $4`, space, project, after, limit+1)
		if e != nil {
			return nil, "", ErrStorage
		}
		defer rows.Close()
		for rows.Next() {
			var b []byte
			var j m.Job
			if rows.Scan(&b) != nil {
				return nil, "", ErrStorage
			}
			j, e = decodeJob(bytes.NewReader(b))
			if e != nil {
				return nil, "", e
			}
			out = append(out, j)
		}
		if rows.Err() != nil {
			return nil, "", ErrStorage
		}
	}
	next := ""
	if len(out) > limit {
		next = out[limit-1].ID
		out = out[:limit]
	}
	return out, next, nil
}
