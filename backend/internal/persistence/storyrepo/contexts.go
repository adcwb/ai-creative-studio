package storyrepo

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/story"

	"github.com/adcwb/ai-creative-studio/backend/internal/platform/nativefs"
)

const contextSchema = `CREATE TABLE IF NOT EXISTS studio_story_contexts (
 space_id text NOT NULL,series_id text NOT NULL,episode_id text NOT NULL,id text NOT NULL,payload jsonb NOT NULL,
 PRIMARY KEY(space_id,series_id,episode_id,id),
 CHECK((payload->'snapshot'->>'space_id'=space_id AND payload->'snapshot'->>'series_id'=series_id AND payload->'snapshot'->>'episode_id'=episode_id AND payload->'snapshot'->>'id'=id) IS TRUE));`

type contextRecord struct {
	Format        int                   `json:"format"`
	Key           string                `json:"key_hash"`
	RequestSHA256 string                `json:"request_sha256"`
	Snapshot      story.ContextSnapshot `json:"snapshot"`
}
type ContextPage struct {
	Items []story.ContextSummary `json:"items"`
	Next  string                 `json:"next_cursor"`
}

func ContextID(a story.Actor, root, ep, key string) string {
	return story.TextHash("story-context-v1:" + a.Space + ":" + root + ":" + ep + ":" + a.User + ":" + key)[:32]
}
func validContext(r contextRecord) bool {
	return r.Format == 1 && story.Hash.MatchString(r.Key) && r.RequestSHA256 == story.Digest(r.Snapshot.Context.Selection) && story.ValidSnapshot(r.Snapshot)
}
func decodeContext(raw io.Reader) (contextRecord, error) {
	var r contextRecord
	d := json.NewDecoder(raw)
	d.DisallowUnknownFields()
	if d.Decode(&r) != nil || !validContext(r) {
		return r, ErrStorage
	}
	var tail any
	if d.Decode(&tail) != io.EOF {
		return r, ErrStorage
	}
	return r, nil
}
func (s *Store) contextDir(q Scope, ep string) string {
	return filepath.Join(s.dir, story.TextHash(q.Space), q.Series, ".contexts", ep)
}
func (s *Store) readContext(q Scope, ep, id string) (contextRecord, error) {
	p := s.contextDir(q, ep)
	for _, dir := range []string{filepath.Join(s.dir, story.TextHash(q.Space)), filepath.Join(s.dir, story.TextHash(q.Space), q.Series), filepath.Dir(p), p} {
		if e := realDir(dir); os.IsNotExist(e) {
			return contextRecord{}, story.ErrNotFound
		} else if e != nil {
			return contextRecord{}, ErrStorage
		}
	}
	f, e := nativefs.OpenFile(filepath.Join(p, id+".json"), os.O_RDONLY|nativefs.NoFollow|nativefs.NonBlock, 0)
	if os.IsNotExist(e) {
		return contextRecord{}, story.ErrNotFound
	}
	if e != nil {
		return contextRecord{}, ErrStorage
	}
	defer f.Close()
	st, e := f.Stat()
	if e != nil || !st.Mode().IsRegular() {
		return contextRecord{}, ErrStorage
	}
	r, e := decodeContext(f)
	if e != nil || r.Snapshot.Space != q.Space || r.Snapshot.SeriesID != q.Series || r.Snapshot.EpisodeID != ep || r.Snapshot.ID != id {
		return contextRecord{}, ErrStorage
	}
	return r, nil
}
func (s *Store) appendContext(q Scope, r contextRecord) error {
	if !validContext(r) || r.Snapshot.Space != q.Space || r.Snapshot.SeriesID != q.Series {
		return ErrStorage
	}
	p := s.dir
	for _, part := range []string{story.TextHash(q.Space), q.Series, ".contexts", r.Snapshot.EpisodeID} {
		p = filepath.Join(p, part)
		if e := os.Mkdir(p, 0700); e != nil && !os.IsExist(e) {
			return ErrStorage
		}
		if realDir(p) != nil || syncDir(filepath.Dir(p)) != nil {
			return ErrStorage
		}
	}
	f, e := os.CreateTemp(p, ".pending-")
	if e != nil {
		return ErrStorage
	}
	defer os.Remove(f.Name())
	e = json.NewEncoder(f).Encode(r)
	if e == nil {
		e = nativefs.SyncFile(f)
	}
	e = errors.Join(e, f.Close())
	if e != nil {
		return ErrStorage
	}
	if os.Link(f.Name(), filepath.Join(p, r.Snapshot.ID+".json")) != nil {
		return ErrStorage
	}
	return syncDir(p)
}
func scanContext(row rowScanner, q Scope, ep, id string) (contextRecord, error) {
	var b []byte
	if e := row.Scan(&b); errors.Is(e, sql.ErrNoRows) {
		return contextRecord{}, story.ErrNotFound
	} else if e != nil {
		return contextRecord{}, ErrStorage
	}
	r, e := decodeContext(bytes.NewReader(b))
	if e != nil || r.Snapshot.Space != q.Space || r.Snapshot.SeriesID != q.Series || r.Snapshot.EpisodeID != ep || r.Snapshot.ID != id {
		return contextRecord{}, ErrStorage
	}
	return r, nil
}
func (s *Store) GetContext(ctx context.Context, q Scope, ep, id string) (story.ContextSnapshot, error) {
	if !validScope(q) || !story.ID.MatchString(ep) || !story.ID.MatchString(id) {
		return story.ContextSnapshot{}, story.ErrNotFound
	}
	var r contextRecord
	var e error
	if s.db != nil {
		r, e = scanContext(s.db.QueryRowContext(ctx, `SELECT payload FROM studio_story_contexts WHERE space_id=$1 AND series_id=$2 AND episode_id=$3 AND id=$4`, q.Space, q.Series, ep, id), q, ep, id)
	} else {
		e = s.withFile(ctx, q, false, func() error { r, e = s.readContext(q, ep, id); return e })
	}
	return r.Snapshot, e
}
func (s *Store) ReplayContext(ctx context.Context, a story.Actor, root, ep, key string, in story.Selection) (story.ContextSnapshot, bool, error) {
	if !a.Writable {
		return story.ContextSnapshot{}, false, story.ErrDenied
	}
	normalized, e := story.NormalizeSelection(in)
	if e != nil {
		return story.ContextSnapshot{}, false, e
	}
	if !story.Scope.MatchString(a.Space) || !story.Scope.MatchString(a.User) || !story.ID.MatchString(root) || !story.ID.MatchString(ep) || !keyPattern.MatchString(key) {
		return story.ContextSnapshot{}, false, story.ErrInput
	}
	v, e := s.GetContext(ctx, Scope{a.Space, root}, ep, ContextID(a, root, ep, key))
	if errors.Is(e, story.ErrNotFound) {
		return v, false, nil
	}
	if e != nil {
		return v, false, e
	}
	if v.Actor != a.User || story.Digest(v.Context.Selection) != story.Digest(normalized) {
		return v, false, ErrKey
	}
	return v, true, nil
}
func (s *Store) SaveContext(ctx context.Context, a story.Actor, key string, v story.ContextSnapshot) (story.ContextSnapshot, error) {
	if !a.Writable {
		return v, story.ErrDenied
	}
	if !story.Scope.MatchString(a.User) || !keyPattern.MatchString(key) || v.Space != a.Space || v.Actor != a.User || v.ID != ContextID(a, v.SeriesID, v.EpisodeID, key) || !story.ValidSnapshot(v) {
		return v, story.ErrInput
	}
	q := Scope{v.Space, v.SeriesID}
	r := contextRecord{1, story.TextHash(a.User + ":" + key), story.Digest(v.Context.Selection), v}
	check := func(old contextRecord) (story.ContextSnapshot, error) {
		if old.Key != r.Key || old.RequestSHA256 != r.RequestSHA256 {
			return story.ContextSnapshot{}, ErrKey
		}
		return old.Snapshot, nil
	}
	if s.db != nil {
		tx, e := s.db.BeginTx(ctx, nil)
		if e != nil {
			return v, ErrStorage
		}
		defer tx.Rollback()
		if _, e = tx.ExecContext(ctx, `SET LOCAL synchronous_commit=on;SELECT pg_advisory_xact_lock_shared(728219117)`); e != nil {
			return v, ErrStorage
		}
		if _, e = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,728219140))`, q.Space+":"+q.Series); e != nil {
			return v, ErrStorage
		}
		old, e := scanContext(tx.QueryRowContext(ctx, `SELECT payload FROM studio_story_contexts WHERE space_id=$1 AND series_id=$2 AND episode_id=$3 AND id=$4`, q.Space, q.Series, v.EpisodeID, v.ID), q, v.EpisodeID, v.ID)
		if e == nil {
			return check(old)
		}
		if !errors.Is(e, story.ErrNotFound) {
			return v, e
		}
		if e = insertContext(ctx, tx, r); e != nil {
			return v, e
		}
		if tx.Commit() != nil {
			return v, ErrStorage
		}
		return v, nil
	}
	var out story.ContextSnapshot
	e := s.withFile(ctx, q, true, func() error {
		old, e := s.readContext(q, v.EpisodeID, v.ID)
		if e == nil {
			out, e = check(old)
			return e
		}
		if !errors.Is(e, story.ErrNotFound) {
			return e
		}
		if e = s.appendContext(q, r); e != nil {
			return e
		}
		out = v
		return nil
	})
	return out, e
}
func insertContext(ctx context.Context, tx *sql.Tx, r contextRecord) error {
	if !validContext(r) {
		return ErrStorage
	}
	b, e := json.Marshal(r)
	if e != nil {
		return ErrStorage
	}
	v := r.Snapshot
	_, e = tx.ExecContext(ctx, `INSERT INTO studio_story_contexts(space_id,series_id,episode_id,id,payload) VALUES($1,$2,$3,$4,$5)`, v.Space, v.SeriesID, v.EpisodeID, v.ID, b)
	return e
}

func (s *Store) ListContexts(ctx context.Context, q Scope, ep, after string, limit int) (ContextPage, error) {
	out := ContextPage{Items: []story.ContextSummary{}}
	if !validScope(q) || !story.ID.MatchString(ep) || (after != "" && !story.ID.MatchString(after)) || limit < 1 || limit > 100 {
		return out, story.ErrInput
	}
	if s.db != nil {
		rows, e := s.db.QueryContext(ctx, `SELECT id,payload->'snapshot'->>'script_version',payload->'snapshot'->>'created_at',jsonb_array_length(payload->'snapshot'->'context'->'included'),jsonb_array_length(payload->'snapshot'->'context'->'problems'),payload->'snapshot'->>'content_sha256' FROM studio_story_contexts WHERE space_id=$1 AND series_id=$2 AND episode_id=$3 AND id>$4 ORDER BY id LIMIT $5`, q.Space, q.Series, ep, after, limit+1)
		if e != nil {
			return out, ErrStorage
		}
		defer rows.Close()
		for rows.Next() {
			v := story.ContextSummary{EpisodeID: ep}
			var timestamp string
			if rows.Scan(&v.ID, &v.ScriptVersion, &timestamp, &v.Included, &v.Problems, &v.SHA256) != nil {
				return out, ErrStorage
			}
			v.CreatedAt, e = time.Parse(time.RFC3339Nano, timestamp)
			if e != nil || !story.ID.MatchString(v.ID) || !story.Hash.MatchString(v.SHA256) || v.ScriptVersion < 0 || v.Included < 0 || v.Problems < 0 {
				return out, ErrStorage
			}
			out.Items = append(out.Items, v)
		}
		if rows.Err() != nil {
			return out, ErrStorage
		}
	} else {
		e := s.withFile(ctx, q, false, func() error {
			dir := s.contextDir(q, ep)
			for _, p := range []string{filepath.Join(s.dir, story.TextHash(q.Space)), filepath.Join(s.dir, story.TextHash(q.Space), q.Series), filepath.Dir(dir), dir} {
				if e := realDir(p); os.IsNotExist(e) {
					return nil
				} else if e != nil {
					return ErrStorage
				}
			}
			files, e := os.ReadDir(dir)
			if e != nil {
				return ErrStorage
			}
			for _, f := range files {
				if e = ctx.Err(); e != nil {
					return e
				}
				if strings.HasPrefix(f.Name(), ".pending-") {
					continue
				}
				id := strings.TrimSuffix(f.Name(), ".json")
				if !story.ID.MatchString(id) || f.Name() != id+".json" || !f.Type().IsRegular() {
					return ErrStorage
				}
				if id <= after {
					continue
				}
				r, e := s.readContext(q, ep, id)
				if e != nil {
					return e
				}
				out.Items = append(out.Items, story.SummarizeContext(r.Snapshot))
				if len(out.Items) > limit {
					break
				}
			}
			return nil
		})
		if e != nil {
			return out, e
		}
	}
	if len(out.Items) > limit {
		out.Next = out.Items[limit-1].ID
		out.Items = out.Items[:limit]
	}
	return out, nil
}
