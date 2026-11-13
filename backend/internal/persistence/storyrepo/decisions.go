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
	"time"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/story"

	"github.com/adcwb/ai-creative-studio/backend/internal/platform/nativefs"
)

const decisionSchema = `CREATE TABLE IF NOT EXISTS studio_story_decisions (
 space_id text NOT NULL,series_id text NOT NULL,episode_id text NOT NULL,check_id text NOT NULL,finding_id text NOT NULL,payload jsonb NOT NULL,
 PRIMARY KEY(space_id,series_id,episode_id,check_id,finding_id),
 FOREIGN KEY(space_id,series_id,episode_id,check_id) REFERENCES studio_story_checks(space_id,series_id,episode_id,id),
 CHECK((payload->'decision'->>'episode_id'=episode_id AND payload->'decision'->>'check_id'=check_id AND payload->'decision'->>'finding_id'=finding_id) IS TRUE));`

type DecisionRecord struct {
	Format   int            `json:"format"`
	Key      string         `json:"key_hash"`
	Digest   string         `json:"request_sha256"`
	SHA256   string         `json:"content_sha256"`
	Decision story.Decision `json:"decision"`
}

func validDecision(r DecisionRecord) bool {
	copy := r
	copy.SHA256 = ""
	return r.SHA256 == story.Digest(copy) && r.Format == 1 && story.Hash.MatchString(r.Key) && story.Hash.MatchString(r.Digest) && story.ValidDecision(r.Decision)
}
func decodeDecision(raw io.Reader) (DecisionRecord, error) {
	var r DecisionRecord
	d := json.NewDecoder(raw)
	d.DisallowUnknownFields()
	if d.Decode(&r) != nil || !validDecision(r) {
		return r, ErrStorage
	}
	var tail any
	if d.Decode(&tail) != io.EOF {
		return r, ErrStorage
	}
	return r, nil
}
func finding(c story.Check, id string) (story.Finding, error) {
	if c.State != "succeeded" || c.Result == nil {
		return story.Finding{}, story.ErrInput
	}
	for _, f := range c.Result.Findings {
		if f.ID == id {
			return f, nil
		}
	}
	return story.Finding{}, story.ErrNotFound
}
func (s *Store) decisionPath(q Scope, ep, check, finding string) string {
	return filepath.Join(s.dir, story.TextHash(q.Space), q.Series, ".decisions", ep, check, finding+".json")
}
func (s *Store) readDecisionFile(q Scope, ep, check, id string) (DecisionRecord, error) {
	p := s.decisionPath(q, ep, check, id)
	dir := s.dir
	for _, part := range []string{story.TextHash(q.Space), q.Series, ".decisions", ep, check} {
		dir = filepath.Join(dir, part)
		if e := realDir(dir); os.IsNotExist(e) {
			return DecisionRecord{}, story.ErrNotFound
		} else if e != nil {
			return DecisionRecord{}, ErrStorage
		}
	}
	f, e := nativefs.OpenFile(p, os.O_RDONLY|nativefs.NoFollow|nativefs.NonBlock, 0)
	if os.IsNotExist(e) {
		return DecisionRecord{}, story.ErrNotFound
	}
	if e != nil {
		return DecisionRecord{}, ErrStorage
	}
	defer f.Close()
	st, e := f.Stat()
	if e != nil || !st.Mode().IsRegular() {
		return DecisionRecord{}, ErrStorage
	}
	r, e := decodeDecision(f)
	if e != nil || r.Decision.EpisodeID != ep || r.Decision.CheckID != check || r.Decision.FindingID != id || r.Decision.Action == "adopt" {
		return r, ErrStorage
	}
	return r, nil
}
func (s *Store) appendDecisionFile(q Scope, ep string, r DecisionRecord) error {
	if !validDecision(r) || r.Decision.Action == "adopt" {
		return ErrStorage
	}
	p := s.dir
	for _, part := range []string{story.TextHash(q.Space), q.Series, ".decisions", ep, r.Decision.CheckID} {
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
	if e = os.Link(f.Name(), s.decisionPath(q, ep, r.Decision.CheckID, r.Decision.FindingID)); e != nil {
		return ErrStorage
	}
	return syncDir(p)
}

// Accepted decisions live in their immutable formal version, so there is no
// crash window with a formal edit but no decision. Only the known target's
// history is inspected; no full series history scan or mutable mirror is used.
func (s *Store) fileDecision(q Scope, ep string, c story.Check, f story.Finding) (DecisionRecord, error) {
	target := story.AdoptionTarget(c.ID, f)
	names, e := s.entries(q, target)
	if e != nil {
		return DecisionRecord{}, e
	}
	var accepted *DecisionRecord
	for _, name := range names {
		r, e := read(filepath.Join(s.path(q, target), name))
		if e != nil {
			return DecisionRecord{}, e
		}
		if r.Adoption != nil && r.Adoption.Decision.CheckID == c.ID && r.Adoption.Decision.FindingID == f.ID {
			if accepted != nil {
				return DecisionRecord{}, ErrStorage
			}
			copy := *r.Adoption
			accepted = &copy
		}
	}
	rejected, e := s.readDecisionFile(q, ep, c.ID, f.ID)
	if accepted != nil {
		if !errors.Is(e, story.ErrNotFound) {
			return DecisionRecord{}, ErrStorage
		}
		return *accepted, nil
	}
	return rejected, e
}
func scanDecision(row rowScanner) (DecisionRecord, error) {
	var b []byte
	if e := row.Scan(&b); errors.Is(e, sql.ErrNoRows) {
		return DecisionRecord{}, story.ErrNotFound
	} else if e != nil {
		return DecisionRecord{}, ErrStorage
	}
	return decodeDecision(bytes.NewReader(b))
}
func insertDecision(ctx context.Context, tx *sql.Tx, q Scope, ep string, r DecisionRecord) error {
	if !validDecision(r) {
		return ErrStorage
	}
	b, e := json.Marshal(r)
	if e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO studio_story_decisions(space_id,series_id,episode_id,check_id,finding_id,payload) VALUES($1,$2,$3,$4,$5,$6)`, q.Space, q.Series, ep, r.Decision.CheckID, r.Decision.FindingID, b)
	return e
}
func (s *Store) GetDecision(ctx context.Context, q Scope, ep, check, id string) (story.Decision, error) {
	if !validScope(q) || !story.ID.MatchString(ep) || !story.ID.MatchString(check) || !story.ID.MatchString(id) {
		return story.Decision{}, story.ErrNotFound
	}
	if s.db != nil {
		r, e := scanDecision(s.db.QueryRowContext(ctx, `SELECT payload FROM studio_story_decisions WHERE space_id=$1 AND series_id=$2 AND episode_id=$3 AND check_id=$4 AND finding_id=$5`, q.Space, q.Series, ep, check, id))
		if e == nil && (r.Decision.CheckID != check || r.Decision.FindingID != id) {
			e = ErrStorage
		}
		return r.Decision, e
	}
	var out story.Decision
	e := s.withFile(ctx, q, false, func() error {
		c, e := s.readCheck(q, ep, check)
		if e != nil {
			return e
		}
		f, e := finding(c.Check, id)
		if e != nil {
			return e
		}
		r, e := s.fileDecision(q, ep, c.Check, f)
		if e != nil {
			return e
		}
		out = r.Decision
		return nil
	})
	return out, e
}
func (s *Store) Decide(ctx context.Context, a story.Actor, q Scope, ep, check, id, key string, in story.DecisionInput, expectedHeads string, guard func(context.Context, *sql.Tx) (func(), error)) (story.Decision, error) {
	var out story.Decision
	if !a.Writable || a.Space != q.Space {
		return out, story.ErrDenied
	}
	if !validScope(q) || !story.ID.MatchString(ep) || !story.ID.MatchString(check) || !story.ID.MatchString(id) || !story.Scope.MatchString(a.User) || !keyPattern.MatchString(key) {
		return out, story.ErrInput
	}
	in, e := story.NormalizeDecision(in)
	if e != nil {
		return out, e
	}
	hash := story.TextHash(a.User + ":" + key)
	digest := story.Digest(in)
	replay := func(r DecisionRecord) (story.Decision, error) {
		if r.Key != hash {
			return story.Decision{}, story.ErrConflict
		}
		if r.Digest != digest {
			return story.Decision{}, ErrKey
		}
		return r.Decision, nil
	}
	makeDecision := func(c story.Check, f story.Finding, old *story.Version) (DecisionRecord, *record, error) {
		d := story.Decision{ID: story.DecisionID(check, id), CheckID: check, FindingID: id, EpisodeID: ep, Action: in.Action, Reason: in.Reason, ReviewSHA256: in.ReviewSHA256, ContextID: c.Input.ContextID, Actor: a.User, CreatedAt: time.Now().UTC()}
		r := DecisionRecord{Format: 1, Key: hash, Digest: digest, Decision: d}
		if in.Action != "adopt" {
			r.SHA256 = decisionDigest(r)
			return r, nil, nil
		}
		target := story.AdoptionTarget(check, f)
		if f.TargetRecordID == "" && old != nil {
			return r, nil, story.ErrConflict
		}
		if f.TargetRecordID != "" && old == nil {
			return r, nil, story.ErrNotFound
		}
		if in.Proposal == nil || in.Proposal.Kind == "bible" && target != BibleID(q.Series) {
			return r, nil, story.ErrInput
		}
		v, e := apply(a, q, target, "adoption:"+check+":"+id+":"+key, *in.Proposal, old)
		if e != nil {
			return r, nil, e
		}
		r.Decision.TargetRecordID = target
		r.Decision.VersionID = v.Version.VersionID
		r.SHA256 = decisionDigest(r)
		v.Adoption = &r
		if !validRecord(v) {
			return r, nil, ErrStorage
		}
		return r, &v, nil
	}
	if s.db != nil {
		tx, e := s.db.BeginTx(ctx, nil)
		if e != nil {
			return out, e
		}
		defer tx.Rollback()
		if e = lockCheckTX(ctx, tx, q); e != nil {
			return out, e
		}
		previous, e := scanDecision(tx.QueryRowContext(ctx, `SELECT payload FROM studio_story_decisions WHERE space_id=$1 AND series_id=$2 AND episode_id=$3 AND check_id=$4 AND finding_id=$5`, q.Space, q.Series, ep, check, id))
		if e == nil {
			return replay(previous)
		}
		if !errors.Is(e, story.ErrNotFound) {
			return out, e
		}
		c, e := scanCheck(tx.QueryRowContext(ctx, `SELECT payload FROM studio_story_checks WHERE space_id=$1 AND series_id=$2 AND episode_id=$3 AND id=$4`, q.Space, q.Series, ep, check), q, ep, check)
		if e != nil {
			return out, e
		}
		f, e := finding(c.Check, id)
		if e != nil {
			return out, e
		}
		var old *story.Version
		if in.Action == "adopt" {
			heads, e := sqlHeads(ctx, tx, q)
			if e != nil {
				return out, e
			}
			if heads != expectedHeads {
				return out, story.ErrConflict
			}
			if guard != nil {
				release, e := guard(ctx, tx)
				if e != nil {
					return out, e
				}
				if release != nil {
					defer release()
				}
			}
			v, e := scan(tx.QueryRowContext(ctx, headSelect+` AND v.id=$3`, q.Space, q.Series, story.AdoptionTarget(check, f)), q, story.AdoptionTarget(check, f))
			if e == nil {
				old = &v.Version
			} else if !errors.Is(e, story.ErrNotFound) {
				return out, e
			}
		}
		decision, version, e := makeDecision(c.Check, f, old)
		if e != nil {
			return out, e
		}
		if version != nil {
			if e = saveSQL(ctx, tx, *version); e != nil {
				return out, e
			}
		}
		if e = insertDecision(ctx, tx, q, ep, decision); e != nil {
			return out, e
		}
		return decision.Decision, tx.Commit()
	}
	e = s.withFile(ctx, q, true, func() error {
		c, e := s.readCheck(q, ep, check)
		if e != nil {
			return e
		}
		f, e := finding(c.Check, id)
		if e != nil {
			return e
		}
		previous, e := s.fileDecision(q, ep, c.Check, f)
		if e == nil {
			out, e = replay(previous)
			return e
		}
		if !errors.Is(e, story.ErrNotFound) {
			return e
		}
		var old *story.Version
		if in.Action == "adopt" {
			heads, e := s.fileHeads(q)
			if e != nil {
				return e
			}
			if heads != expectedHeads {
				return story.ErrConflict
			}
			if guard != nil {
				release, e := guard(ctx, nil)
				if e != nil {
					return e
				}
				if release != nil {
					defer release()
				}
			}
			v, e := s.fileCurrent(q, story.AdoptionTarget(check, f))
			if e == nil {
				old = &v
			} else if !errors.Is(e, story.ErrNotFound) {
				return e
			}
		}
		decision, version, e := makeDecision(c.Check, f, old)
		if e != nil {
			return e
		}
		if version != nil {
			e = s.appendFile(q, *version)
		} else {
			e = s.appendDecisionFile(q, ep, decision)
		}
		if e != nil {
			return e
		}
		out = decision.Decision
		return nil
	})
	return out, e
}

func (s *Store) ReplayDecision(ctx context.Context, a story.Actor, q Scope, ep, check, id, key string, in story.DecisionInput) (story.Decision, bool, error) {
	if !a.Writable || a.Space != q.Space {
		return story.Decision{}, false, story.ErrDenied
	}
	if !validScope(q) || !story.ID.MatchString(ep) || !story.ID.MatchString(check) || !story.ID.MatchString(id) || !story.Scope.MatchString(a.User) || !keyPattern.MatchString(key) {
		return story.Decision{}, false, story.ErrInput
	}
	normalized, e := story.NormalizeDecision(in)
	if e != nil {
		return story.Decision{}, false, e
	}
	var r DecisionRecord
	if s.db != nil {
		r, e = scanDecision(s.db.QueryRowContext(ctx, `SELECT payload FROM studio_story_decisions WHERE space_id=$1 AND series_id=$2 AND episode_id=$3 AND check_id=$4 AND finding_id=$5`, q.Space, q.Series, ep, check, id))
	} else {
		e = s.withFile(ctx, q, false, func() error {
			c, e := s.readCheck(q, ep, check)
			if e != nil {
				return e
			}
			f, e := finding(c.Check, id)
			if e != nil {
				return e
			}
			r, e = s.fileDecision(q, ep, c.Check, f)
			return e
		})
	}
	if errors.Is(e, story.ErrNotFound) {
		return story.Decision{}, false, nil
	}
	if e != nil {
		return story.Decision{}, false, e
	}
	if r.Key != story.TextHash(a.User+":"+key) {
		return story.Decision{}, false, story.ErrConflict
	}
	if r.Digest != story.Digest(normalized) {
		return story.Decision{}, false, ErrKey
	}
	return r.Decision, true, nil
}

func decisionDigest(r DecisionRecord) string { r.SHA256 = ""; return story.Digest(r) }
