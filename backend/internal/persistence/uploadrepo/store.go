// Package uploadrepo stores each transfer and each bounded chunk separately.
// PostgreSQL chunk bytes are temporary staging data, removed atomically when
// the session completes/cancels. File chunks use one atomic header+bytes record.
package uploadrepo

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"os"
	"sync"
	"time"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/uploads"
)

var ErrStorage = errors.New("upload storage unavailable")

const Schema = `CREATE TABLE IF NOT EXISTS studio_upload_sessions (
 space_id text NOT NULL, project_id text NOT NULL, actor_id text NOT NULL, id text NOT NULL,
 state text NOT NULL CHECK(state IN ('receiving','completed','cancelled')), expires_at timestamptz NOT NULL,
 payload jsonb NOT NULL CHECK((payload->>'space_id'=space_id AND payload->>'project_id'=project_id AND payload->>'actor_id'=actor_id AND payload->>'id'=id AND payload->>'state'=state) IS TRUE), PRIMARY KEY(space_id,id));
 CREATE INDEX IF NOT EXISTS studio_upload_list ON studio_upload_sessions(space_id,project_id,actor_id,state,id);
 CREATE INDEX IF NOT EXISTS studio_upload_expiry ON studio_upload_sessions(expires_at) WHERE state='receiving';
 CREATE TABLE IF NOT EXISTS studio_upload_parts (
 space_id text NOT NULL, session_id text NOT NULL, byte_offset bigint NOT NULL CHECK(byte_offset>=0),
 size bigint NOT NULL CHECK(size>0 AND size<=8388608), sha256 text NOT NULL CHECK(sha256 ~ '^[a-f0-9]{64}$'),
 data bytea NOT NULL CHECK(octet_length(data)=size), PRIMARY KEY(space_id,session_id,byte_offset),
 FOREIGN KEY(space_id,session_id) REFERENCES studio_upload_sessions(space_id,id));`

type Store struct {
	maintenanceMu sync.Mutex
	cancel        context.CancelFunc
	done          chan struct{}
	dir           string
	db            *sql.DB
}
type Identity struct{ Space, Project, Actor, ID string }
type Locked struct {
	Session uploads.Session
	store   *Store
	conn    *sql.Conn
	tx      *sql.Tx
	ctx     context.Context
}

func Open(dir string, db *sql.DB) (*Store, error) {
	if db == nil {
		if e := os.MkdirAll(dir, 0700); e != nil {
			return nil, e
		}
	}
	return &Store{dir: dir, db: db}, nil
}
func valid(i Identity) bool {
	return i.Space != "" && i.Project != "" && i.Actor != "" && len(i.Space) <= 128 && len(i.Project) <= 128 && len(i.Actor) <= 128 && uploads.ID.MatchString(i.ID)
}
func (s *Store) with(ctx context.Context, i Identity, create *uploads.Session, fn func(*Locked) error) error {
	if !valid(i) {
		return uploads.ErrNotFound
	}
	if s.db == nil {
		return s.withFile(ctx, i, create, fn)
	}
	// The advisory session lock serializes this transfer without holding an
	// open SQL transaction across media probing/blob I/O/another domain commit.
	// Waiting contenders return their connection to the pool between attempts.
	var conn *sql.Conn
	for {
		var e error
		conn, e = s.db.Conn(ctx)
		if e != nil {
			return ErrStorage
		}
		var acquired bool
		e = conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock(hashtextextended($1,728219132))`, i.Space+":"+i.ID).Scan(&acquired)
		if e != nil {
			_ = conn.Raw(func(any) error { return driver.ErrBadConn })
			conn.Close()
			return ErrStorage
		}
		if acquired {
			break
		}
		conn.Close()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, e := conn.ExecContext(cleanup, `SELECT pg_advisory_unlock(hashtextextended($1,728219132))`, i.Space+":"+i.ID); e != nil {
			_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		}
		conn.Close()
	}()
	l := &Locked{store: s, conn: conn, ctx: ctx}
	read := func() error {
		var b []byte
		var q interface {
			QueryRowContext(context.Context, string, ...any) *sql.Row
		} = conn
		if l.tx != nil {
			q = l.tx
		}
		e := q.QueryRowContext(ctx, `SELECT payload FROM studio_upload_sessions WHERE space_id=$1 AND id=$2 AND project_id=$3 AND actor_id=$4`, i.Space, i.ID, i.Project, i.Actor).Scan(&b)
		if errors.Is(e, sql.ErrNoRows) {
			return uploads.ErrNotFound
		}
		if e != nil || json.Unmarshal(b, &l.Session) != nil || !uploads.ValidSession(l.Session) {
			return ErrStorage
		}
		if create != nil && l.Session.RequestSHA != create.RequestSHA {
			return uploads.ErrConflict
		}
		return nil
	}
	if create != nil {
		if e := l.transaction(func() error {
			b, _ := json.Marshal(create)
			if _, e := l.tx.ExecContext(ctx, `INSERT INTO studio_upload_sessions(space_id,project_id,actor_id,id,state,expires_at,payload) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT DO NOTHING`, i.Space, i.Project, i.Actor, i.ID, create.State, create.ExpiresAt, b); e != nil {
				return ErrStorage
			}
			return read()
		}); e != nil {
			return e
		}
	} else if e := read(); e != nil {
		return e
	}
	return fn(l)
}

func (l *Locked) transaction(fn func() error) error {
	tx, e := l.conn.BeginTx(l.ctx, nil)
	if e != nil {
		return ErrStorage
	}
	defer tx.Rollback()
	l.tx = tx
	defer func() { l.tx = nil }()
	if _, e = tx.ExecContext(l.ctx, `SET LOCAL synchronous_commit=on; SELECT pg_advisory_xact_lock_shared(728219117)`); e != nil {
		return ErrStorage
	}
	if e = fn(); e != nil {
		return e
	}
	if e = tx.Commit(); e != nil {
		return ErrStorage
	}
	return nil
}

func (s *Store) Create(ctx context.Context, u uploads.Session) (uploads.Session, error) {
	if !uploads.ValidSession(u) || u.Offset != 0 || u.State != "receiving" {
		return u, uploads.ErrInput
	}
	var out uploads.Session
	e := s.with(ctx, Identity{u.Space, u.Project, u.Actor, u.ID}, &u, func(l *Locked) error { out = l.Session; return nil })
	return out, e
}
func (s *Store) With(ctx context.Context, i Identity, fn func(*Locked) error) error {
	return s.with(ctx, i, nil, fn)
}
func (s *Store) Get(ctx context.Context, i Identity) (uploads.Session, error) {
	var out uploads.Session
	e := s.With(ctx, i, func(l *Locked) error { out = l.Session; return nil })
	return out, e
}
func (l *Locked) save() error {
	if !uploads.ValidSession(l.Session) {
		return uploads.ErrInput
	}
	if l.conn == nil && l.tx == nil {
		return l.saveTerminal()
	}
	b, _ := json.Marshal(l.Session)
	_, e := l.tx.ExecContext(l.ctx, `UPDATE studio_upload_sessions SET state=$3,expires_at=$4,payload=$5 WHERE space_id=$1 AND id=$2`, l.Session.Space, l.Session.ID, l.Session.State, l.Session.ExpiresAt, b)
	if e != nil {
		return ErrStorage
	}
	return nil
}
func (l *Locked) Part(offset int64) (uploads.Part, []byte, error) {
	if l.conn == nil && l.tx == nil {
		return l.readPart(offset)
	}
	var p uploads.Part
	var b []byte
	p.Offset = offset
	var q interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
	} = l.conn
	if l.tx != nil {
		q = l.tx
	}
	e := q.QueryRowContext(l.ctx, `SELECT size,sha256,data FROM studio_upload_parts WHERE space_id=$1 AND session_id=$2 AND byte_offset=$3`, l.Session.Space, l.Session.ID, offset).Scan(&p.Size, &p.SHA256, &b)
	if errors.Is(e, sql.ErrNoRows) {
		return p, nil, uploads.ErrNotFound
	}
	if e != nil || int64(len(b)) != p.Size || uploads.BytesSHA(b) != p.SHA256 {
		return p, nil, ErrStorage
	}
	return p, b, nil
}
func (l *Locked) Put(offset int64, b []byte, sha string) error {
	p, e := l.Session.CheckPart(offset, b, sha)
	if e != nil {
		return e
	}
	if offset < l.Session.Offset {
		old, _, e := l.Part(offset)
		if e != nil {
			return e
		}
		if old != p {
			return uploads.ErrConflict
		}
		return nil
	}
	l.Session.Offset += p.Size
	if l.conn == nil && l.tx == nil {
		return l.writePart(p, b)
	}
	return l.transaction(func() error {
		if _, e = l.tx.ExecContext(l.ctx, `INSERT INTO studio_upload_parts(space_id,session_id,byte_offset,size,sha256,data) VALUES($1,$2,$3,$4,$5,$6)`, l.Session.Space, l.Session.ID, offset, p.Size, p.SHA256, b); e != nil {
			return ErrStorage
		}
		return l.save()
	})
}
func (l *Locked) Close(assetID string) error {
	state := "cancelled"
	if assetID != "" {
		state = "completed"
	}
	if l.Session.State != "receiving" {
		if l.Session.State == state && l.Session.AssetID == assetID {
			return nil
		}
		return uploads.ErrClosed
	}
	if assetID != "" && (l.Session.Offset != l.Session.Size || !uploads.ID.MatchString(assetID)) {
		return uploads.ErrInput
	}
	l.Session.State = state
	l.Session.AssetID = assetID
	if l.conn != nil {
		return l.transaction(func() error {
			if e := l.save(); e != nil {
				return e
			}
			if _, e := l.tx.ExecContext(l.ctx, `DELETE FROM studio_upload_parts WHERE space_id=$1 AND session_id=$2`, l.Session.Space, l.Session.ID); e != nil {
				return ErrStorage
			}
			return nil
		})
	}
	if e := l.save(); e != nil {
		return e
	}
	l.purgeParts()
	return nil
}
func (s *Store) List(ctx context.Context, scope Identity, after string, limit int) ([]uploads.Session, error) {
	if scope.Space == "" || scope.Project == "" || scope.Actor == "" || limit < 1 || limit > 100 || (after != "" && !uploads.ID.MatchString(after)) {
		return nil, uploads.ErrInput
	}
	if s.db == nil {
		return s.listFile(ctx, scope, after, limit)
	}
	rows, e := s.db.QueryContext(ctx, `SELECT payload FROM studio_upload_sessions WHERE space_id=$1 AND project_id=$2 AND actor_id=$3 AND state='receiving' AND id>$4 ORDER BY id LIMIT $5`, scope.Space, scope.Project, scope.Actor, after, limit)
	if e != nil {
		return nil, ErrStorage
	}
	defer rows.Close()
	out := []uploads.Session{}
	for rows.Next() {
		var b []byte
		var u uploads.Session
		if rows.Scan(&b) != nil || json.Unmarshal(b, &u) != nil || !uploads.ValidSession(u) {
			return nil, ErrStorage
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// Expire cleans only temporary receiving sessions. Completed project assets and
// their receipts are retained. Callers paginate bounded maintenance batches.
func (s *Store) Expire(ctx context.Context, before time.Time, limit int) (int, error) {
	if limit < 1 || limit > 100 {
		return 0, uploads.ErrInput
	}
	var ids []Identity
	if s.db != nil {
		rows, e := s.db.QueryContext(ctx, `SELECT space_id,project_id,actor_id,id FROM studio_upload_sessions WHERE state='receiving' AND expires_at<$1 ORDER BY expires_at,id LIMIT $2`, before, limit)
		if e != nil {
			return 0, ErrStorage
		}
		for rows.Next() {
			var i Identity
			if rows.Scan(&i.Space, &i.Project, &i.Actor, &i.ID) != nil {
				rows.Close()
				return 0, ErrStorage
			}
			ids = append(ids, i)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return 0, e
		}
	} else {
		var e error
		ids, e = s.expiredFiles(ctx, before, limit)
		if e != nil {
			return 0, e
		}
	}
	n := 0
	for _, i := range ids {
		e := s.With(ctx, i, func(l *Locked) error {
			if l.Session.State == "receiving" && l.Session.ExpiresAt.Before(before) {
				if e := l.Close(""); e != nil {
					return e
				}
				n++
			}
			return nil
		})
		if e != nil {
			return n, e
		}
	}
	return n, nil
}
