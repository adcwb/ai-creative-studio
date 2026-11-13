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

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/governance"
	"github.com/adcwb/ai-creative-studio/backend/internal/modules/story"
	"github.com/adcwb/ai-creative-studio/backend/internal/platform/jobqueue"

	"github.com/adcwb/ai-creative-studio/backend/internal/platform/nativefs"
)

const checkSchema = `CREATE TABLE IF NOT EXISTS studio_story_checks (
 space_id text NOT NULL,series_id text NOT NULL,episode_id text NOT NULL,id text NOT NULL,state text NOT NULL,payload jsonb NOT NULL,
 PRIMARY KEY(space_id,series_id,episode_id,id),
 CHECK((payload->'check'->>'space_id'=space_id AND payload->'check'->>'series_id'=series_id AND payload->'check'->>'episode_id'=episode_id AND payload->'check'->>'id'=id AND payload->'check'->>'state'=state) IS TRUE));
 CREATE INDEX IF NOT EXISTS studio_story_checks_pending ON studio_story_checks(state) WHERE state IN ('queued','running');`

type CheckRecord struct {
	Format       int                         `json:"format"`
	Key          string                      `json:"key_hash"`
	Digest       string                      `json:"request_sha256"`
	Check        story.Check                 `json:"check"`
	Selection    governance.RuntimeSelection `json:"selection"`
	ResultSHA256 string                      `json:"result_sha256,omitempty"`
	TraceParent  string                      `json:"trace_parent,omitempty"`
}
type CheckPage struct {
	Items []story.CheckSummary `json:"items"`
	Next  string               `json:"next_cursor"`
}

func CheckID(a story.Actor, root, ep, key string) string {
	return story.TextHash("story-check-v1:" + a.Space + ":" + root + ":" + ep + ":" + a.User + ":" + key)[:32]
}
func validCheck(r CheckRecord) bool {
	return ((r.Check.Result == nil && r.ResultSHA256 == "") || (r.Check.Result != nil && r.ResultSHA256 == story.Digest(r.Check.Result))) && r.Format == 1 && story.Hash.MatchString(r.Key) && r.Digest == story.Digest(r.Check.Input) && story.ValidCheck(r.Check) && story.ValidCheckSelection(r.Check, r.Selection)
}
func decodeCheck(raw io.Reader) (CheckRecord, error) {
	var r CheckRecord
	d := json.NewDecoder(raw)
	d.DisallowUnknownFields()
	if d.Decode(&r) != nil || !validCheck(r) {
		return r, ErrStorage
	}
	var tail any
	if d.Decode(&tail) != io.EOF {
		return r, ErrStorage
	}
	return r, nil
}
func (s *Store) checkDir(q Scope, ep string) string {
	return filepath.Join(s.dir, story.TextHash(q.Space), q.Series, ".checks", ep)
}
func (s *Store) readCheck(q Scope, ep, id string) (CheckRecord, error) {
	dir := s.checkDir(q, ep)
	for _, p := range []string{filepath.Join(s.dir, story.TextHash(q.Space)), filepath.Join(s.dir, story.TextHash(q.Space), q.Series), filepath.Dir(dir), dir} {
		if e := realDir(p); os.IsNotExist(e) {
			return CheckRecord{}, story.ErrNotFound
		} else if e != nil {
			return CheckRecord{}, ErrStorage
		}
	}
	f, e := nativefs.OpenFile(filepath.Join(dir, id+".json"), os.O_RDONLY|nativefs.NoFollow|nativefs.NonBlock, 0)
	if os.IsNotExist(e) {
		return CheckRecord{}, story.ErrNotFound
	}
	if e != nil {
		return CheckRecord{}, ErrStorage
	}
	defer f.Close()
	st, e := f.Stat()
	if e != nil || !st.Mode().IsRegular() {
		return CheckRecord{}, ErrStorage
	}
	r, e := decodeCheck(f)
	if e != nil || r.Check.Space != q.Space || r.Check.SeriesID != q.Series || r.Check.EpisodeID != ep || r.Check.ID != id {
		return r, ErrStorage
	}
	return r, nil
}
func (s *Store) writeCheck(q Scope, r CheckRecord, create bool) error {
	if !validCheck(r) || r.Check.Space != q.Space || r.Check.SeriesID != q.Series {
		return ErrStorage
	}
	p := s.dir
	for _, part := range []string{story.TextHash(q.Space), q.Series, ".checks", r.Check.EpisodeID} {
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
	target := filepath.Join(p, r.Check.ID+".json")
	if create {
		e = os.Link(f.Name(), target)
	} else {
		e = nativefs.Rename(f.Name(), target)
	}
	if e != nil {
		return ErrStorage
	}
	return syncDir(p)
}
func scanCheck(row rowScanner, q Scope, ep, id string) (CheckRecord, error) {
	var b []byte
	if e := row.Scan(&b); errors.Is(e, sql.ErrNoRows) {
		return CheckRecord{}, story.ErrNotFound
	} else if e != nil {
		return CheckRecord{}, ErrStorage
	}
	r, e := decodeCheck(bytes.NewReader(b))
	if e != nil || r.Check.Space != q.Space || r.Check.SeriesID != q.Series || r.Check.EpisodeID != ep || r.Check.ID != id {
		return r, ErrStorage
	}
	return r, nil
}
func (s *Store) GetCheck(ctx context.Context, q Scope, ep, id string) (CheckRecord, error) {
	if !validScope(q) || !story.ID.MatchString(ep) || !story.ID.MatchString(id) {
		return CheckRecord{}, story.ErrNotFound
	}
	if s.db != nil {
		return scanCheck(s.db.QueryRowContext(ctx, `SELECT payload FROM studio_story_checks WHERE space_id=$1 AND series_id=$2 AND episode_id=$3 AND id=$4`, q.Space, q.Series, ep, id), q, ep, id)
	}
	var r CheckRecord
	var e error
	e = s.withFile(ctx, q, false, func() error { r, e = s.readCheck(q, ep, id); return e })
	return r, e
}
func (s *Store) ReplayCheck(ctx context.Context, a story.Actor, root, ep, key string, in story.CheckInput) (CheckRecord, bool, error) {
	if !a.Writable {
		return CheckRecord{}, false, story.ErrDenied
	}
	if !validScope(Scope{a.Space, root}) || !story.ID.MatchString(ep) || !story.Scope.MatchString(a.User) || !keyPattern.MatchString(key) || !story.ValidCheckInput(in) {
		return CheckRecord{}, false, story.ErrInput
	}
	r, e := s.GetCheck(ctx, Scope{a.Space, root}, ep, CheckID(a, root, ep, key))
	if errors.Is(e, story.ErrNotFound) {
		return r, false, nil
	}
	if e != nil {
		return r, false, e
	}
	if r.Key != story.TextHash(a.User+":"+key) || r.Digest != story.Digest(in) {
		return r, false, ErrKey
	}
	if s.db == nil && r.Check.State == "queued" {
		q := Scope{a.Space, root}
		if e = s.withFile(ctx, q, true, func() error {
			current, e := s.readCheck(q, ep, r.Check.ID)
			if e != nil {
				return e
			}
			if current.Check.State == "queued" {
				return s.ensureCheckIntent(current.Check)
			}
			return nil
		}); e != nil {
			return r, false, e
		}
	}
	return r, true, nil
}
func insertCheck(ctx context.Context, tx *sql.Tx, r CheckRecord) error {
	if !validCheck(r) {
		return ErrStorage
	}
	b, e := json.Marshal(r)
	if e != nil {
		return e
	}
	c := r.Check
	_, e = tx.ExecContext(ctx, `INSERT INTO studio_story_checks(space_id,series_id,episode_id,id,state,payload) VALUES($1,$2,$3,$4,$5,$6)`, c.Space, c.SeriesID, c.EpisodeID, c.ID, c.State, b)
	return e
}
func (s *Store) CreateCheck(ctx context.Context, a story.Actor, key string, c story.Check, selection governance.RuntimeSelection, trace string) (story.Check, error) {
	if !a.Writable {
		return c, story.ErrDenied
	}
	if c.Space != a.Space || c.Actor != a.User || !keyPattern.MatchString(key) || c.ID != CheckID(a, c.SeriesID, c.EpisodeID, key) || c.State != "queued" {
		return c, story.ErrInput
	}
	r := CheckRecord{Format: 1, Key: story.TextHash(a.User + ":" + key), Digest: story.Digest(c.Input), Check: c, Selection: selection, TraceParent: trace}
	if !validCheck(r) {
		return c, story.ErrInput
	}
	q := Scope{c.Space, c.SeriesID}
	check := func(old CheckRecord) (story.Check, error) {
		if old.Key != r.Key || old.Digest != r.Digest {
			return story.Check{}, ErrKey
		}
		return old.Check, nil
	}
	if s.db != nil {
		tx, e := s.db.BeginTx(ctx, nil)
		if e != nil {
			return c, e
		}
		defer tx.Rollback()
		if e = lockCheckTX(ctx, tx, q); e != nil {
			return c, e
		}
		old, e := scanCheck(tx.QueryRowContext(ctx, `SELECT payload FROM studio_story_checks WHERE space_id=$1 AND series_id=$2 AND episode_id=$3 AND id=$4`, q.Space, q.Series, c.EpisodeID, c.ID), q, c.EpisodeID, c.ID)
		if e == nil {
			return check(old)
		}
		if !errors.Is(e, story.ErrNotFound) {
			return c, e
		}
		if e = insertCheck(ctx, tx, r); e != nil {
			return c, e
		}
		payload, _ := json.Marshal(CheckJob{c.SeriesID, c.EpisodeID, c.ID})
		if e = jobqueue.EnqueueTx(ctx, tx, jobqueue.Job{TenantID: c.Space, ID: c.ID, Kind: "story_check", IdempotencyKey: c.ID, Payload: payload, TraceParent: trace}); e != nil {
			return c, e
		}
		return c, tx.Commit()
	}
	var out story.Check
	e := s.withFile(ctx, q, true, func() error {
		old, e := s.readCheck(q, c.EpisodeID, c.ID)
		if e == nil {
			out, e = check(old)
			return e
		}
		if !errors.Is(e, story.ErrNotFound) {
			return e
		}
		if e = s.ensureCheckIntent(r.Check); e != nil {
			return e
		}
		if e = s.writeCheck(q, r, true); e != nil {
			return e
		}
		out = c
		return nil
	})
	return out, e
}
func lockCheckTX(ctx context.Context, tx *sql.Tx, q Scope) error {
	if _, e := tx.ExecContext(ctx, `SET LOCAL synchronous_commit=on;SELECT pg_advisory_xact_lock_shared(728219117)`); e != nil {
		return e
	}
	_, e := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,728219140))`, q.Space+":"+q.Series)
	return e
}

type CheckJob struct {
	Series  string `json:"series_id"`
	Episode string `json:"episode_id"`
	ID      string `json:"check_id"`
}

func (s *Store) TransitionCheck(ctx context.Context, q Scope, ep, id, from, to, code string, result *story.CheckResult, lease *jobqueue.Job) (story.Check, error) {
	var out story.Check
	if !validScope(q) || !story.ID.MatchString(ep) || !story.ID.MatchString(id) || !(from == "queued" && to == "running" || from == "running" && (to == "succeeded" || to == "failed" || to == "unknown")) {
		return out, story.ErrInput
	}
	apply := func(r CheckRecord) (CheckRecord, error) {
		if r.Check.State != from {
			return r, story.ErrConflict
		}
		r.Check.State = to
		r.Check.ErrorCode = code
		r.Check.Result = result
		r.ResultSHA256 = ""
		if result != nil {
			r.ResultSHA256 = story.Digest(result)
		}
		r.Check.UpdatedAt = time.Now().UTC()
		if !validCheck(r) {
			return r, story.ErrInput
		}
		return r, nil
	}
	if s.db != nil {
		if lease == nil || lease.TenantID != q.Space || lease.ID != id || lease.Kind != "story_check" {
			return out, story.ErrDenied
		}
		tx, e := s.db.BeginTx(ctx, nil)
		if e != nil {
			return out, e
		}
		defer tx.Rollback()
		if e = lockCheckTX(ctx, tx, q); e != nil {
			return out, e
		}
		if e = jobqueue.CheckLeaseTx(ctx, tx, *lease); e != nil {
			return out, e
		}
		r, e := scanCheck(tx.QueryRowContext(ctx, `SELECT payload FROM studio_story_checks WHERE space_id=$1 AND series_id=$2 AND episode_id=$3 AND id=$4 FOR UPDATE`, q.Space, q.Series, ep, id), q, ep, id)
		if e != nil {
			return out, e
		}
		r, e = apply(r)
		if e != nil {
			return out, e
		}
		raw, _ := json.Marshal(r)
		if _, e = tx.ExecContext(ctx, `UPDATE studio_story_checks SET state=$5,payload=$6 WHERE space_id=$1 AND series_id=$2 AND episode_id=$3 AND id=$4`, q.Space, q.Series, ep, id, to, raw); e != nil {
			return out, e
		}
		if to != "running" {
			if e = jobqueue.FinishTx(ctx, tx, *lease, to, json.RawMessage(`null`)); e != nil {
				return out, e
			}
		}
		return r.Check, tx.Commit()
	}
	e := s.withFile(ctx, q, true, func() error {
		r, e := s.readCheck(q, ep, id)
		if e != nil {
			return e
		}
		r, e = apply(r)
		if e != nil {
			return e
		}
		if e = s.writeCheck(q, r, false); e != nil {
			return e
		}
		out = r.Check
		if to != "running" {
			return s.removeCheckIntent(id)
		}
		return nil
	})
	return out, e
}
func (s *Store) ListChecks(ctx context.Context, q Scope, ep, after string, limit int) (CheckPage, error) {
	out := CheckPage{Items: []story.CheckSummary{}}
	if !validScope(q) || !story.ID.MatchString(ep) || (after != "" && !story.ID.MatchString(after)) || limit < 1 || limit > 100 {
		return out, story.ErrInput
	}
	if s.db != nil {
		rows, e := s.db.QueryContext(ctx, `SELECT id,payload->'check'->'input'->>'context_snapshot_id',state,payload->'check'->>'model',payload->'check'->>'created_at',COALESCE(payload->'check'->>'error_code','') FROM studio_story_checks WHERE space_id=$1 AND series_id=$2 AND episode_id=$3 AND id>$4 ORDER BY id LIMIT $5`, q.Space, q.Series, ep, after, limit+1)
		if e != nil {
			return out, e
		}
		defer rows.Close()
		for rows.Next() {
			v := story.CheckSummary{EpisodeID: ep}
			var at string
			if rows.Scan(&v.ID, &v.ContextID, &v.State, &v.Model, &at, &v.ErrorCode) != nil {
				return out, ErrStorage
			}
			v.CreatedAt, e = time.Parse(time.RFC3339Nano, at)
			if e != nil || !story.ID.MatchString(v.ID) || !story.ID.MatchString(v.ContextID) {
				return out, ErrStorage
			}
			out.Items = append(out.Items, v)
		}
		if rows.Err() != nil {
			return out, ErrStorage
		}
	} else {
		e := s.withFile(ctx, q, false, func() error {
			dir := s.checkDir(q, ep)
			for _, p := range []string{filepath.Join(s.dir, story.TextHash(q.Space)), filepath.Join(s.dir, story.TextHash(q.Space), q.Series), filepath.Dir(dir), dir} {
				if e := realDir(p); os.IsNotExist(e) {
					return nil
				} else if e != nil {
					return ErrStorage
				}
			}
			files, e := os.ReadDir(dir)
			if e != nil {
				return e
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
				r, e := s.readCheck(q, ep, id)
				if e != nil {
					return e
				}
				out.Items = append(out.Items, story.SummarizeCheck(r.Check))
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
