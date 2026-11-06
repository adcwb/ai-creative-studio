// Package revisionrepo stores an immutable branch advance and its original
// receipt in one record. Capturing or forking never changes live creative state.
package revisionrepo

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/revisions"
)

var ErrStorage = errors.New("revision storage unavailable")
var ErrKey = errors.New("revision idempotency conflict")
var ErrNotFound = errors.New("revision not found")
var scopePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,80}$`)
var branchPattern = regexp.MustCompile(`^(main|[a-f0-9]{32})$`)
var keyPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{8,128}$`)
var hashPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
var entryPattern = regexp.MustCompile(`^([0-9]{20})-([a-f0-9]{64})\.json$`)

const Schema = `CREATE TABLE IF NOT EXISTS studio_revision_commits (
 space_id text NOT NULL, project_id text NOT NULL, branch_id text NOT NULL, sequence bigint NOT NULL CHECK(sequence>0),key_hash text NOT NULL,request_digest text NOT NULL,revision_id text,payload jsonb NOT NULL,
 PRIMARY KEY(space_id,project_id,branch_id,sequence),UNIQUE(space_id,project_id,branch_id,key_hash),UNIQUE(space_id,project_id,revision_id),
 CHECK((payload->'branch'->>'space_id'=space_id AND payload->'branch'->>'project_id'=project_id AND payload->'branch'->>'id'=branch_id AND (payload->'branch'->>'version')::bigint=sequence AND payload->>'key_hash'=key_hash AND payload->>'request_digest'=request_digest) IS TRUE));
 CREATE INDEX IF NOT EXISTS studio_revision_heads ON studio_revision_commits(space_id,project_id,branch_id,sequence DESC);`

type Scope struct{ Space, Project string }
type Store struct {
	dir string
	db  *sql.DB
}
type Command struct {
	Operation, Branch, Name, SourceID, Label, Actor, Key, Digest string
	Expected                                                     int
	Head                                                         string
	ProjectVersion, MediaVersion                                 int
	Manifest                                                     revisions.Manifest
}
type Record struct {
	Format   int                 `json:"format"`
	Key      string              `json:"key_hash"`
	Digest   string              `json:"request_digest"`
	Branch   revisions.Branch    `json:"branch"`
	Revision *revisions.Revision `json:"revision,omitempty"`
}
type Summary struct {
	ID             string    `json:"id"`
	BranchID       string    `json:"branch_id"`
	ParentID       string    `json:"parent_id"`
	Label          string    `json:"label"`
	SHA256         string    `json:"content_sha256"`
	Actor          string    `json:"actor"`
	ProjectVersion int       `json:"project_version"`
	MediaVersion   int       `json:"media_version"`
	CreatedAt      time.Time `json:"created_at"`
}

func Summarize(r revisions.Revision) Summary {
	return Summary{r.ID, r.BranchID, r.ParentID, r.Label, r.SHA256, r.Actor, r.ProjectVersion, r.MediaVersion, r.CreatedAt}
}
func Hash(v string) string            { h := sha256.Sum256([]byte(v)); return hex.EncodeToString(h[:]) }
func ForkID(actor, key string) string { return Hash("revision-fork:" + actor + ":" + key)[:32] }
func validScope(q Scope) bool {
	return scopePattern.MatchString(q.Space) && scopePattern.MatchString(q.Project)
}
func validRecord(r Record) bool {
	b := r.Branch
	if r.Format != 1 || !hashPattern.MatchString(r.Key) || !hashPattern.MatchString(r.Digest) || !validScope(Scope{b.SpaceID, b.ProjectID}) || !branchPattern.MatchString(b.ID) || b.Version < 1 || b.CreatedAt.IsZero() || b.UpdatedAt.Before(b.CreatedAt) || !label(b.Name) {
		return false
	}
	if r.Revision == nil {
		return b.Version == 1 && b.ID != "main" && validRevisionID(b.HeadRevisionID)
	}
	v := r.Revision
	if b.Version == 1 && (b.ID != "main" || v.ParentID != "") || b.Version > 1 && v.ParentID == "" {
		return false
	}
	sha, e := revisions.Digest(v.Manifest)
	return e == nil && sha == v.SHA256 && v.SpaceID == b.SpaceID && v.ProjectID == b.ProjectID && v.BranchID == b.ID && v.ID == revisionID(b.ID, b.Version, r.Key) && b.HeadRevisionID == v.ID && v.CreatedAt.Equal(b.UpdatedAt) && v.ProjectVersion > 0 && v.MediaVersion > 0 && scopePattern.MatchString(v.Actor) && label(v.Label) && ((v.ParentID == "") == (v.Manifest.ParentSHA256 == "")) && (v.ParentID == "" || validRevisionID(v.ParentID))
}
func label(v string) bool {
	for _, r := range v {
		if unicode.IsControl(r) {
			return false
		}
	}
	return v == strings.TrimSpace(v) && utf8.ValidString(v) && utf8.RuneCountInString(v) > 0 && utf8.RuneCountInString(v) <= 80
}
func revisionID(branch string, sequence int, key string) string {
	return fmt.Sprintf("%s_%020d_%s", branch, sequence, key)
}
func parseRevisionID(id string) (branch, file string, ok bool) {
	p := strings.Split(id, "_")
	if len(p) != 3 || !branchPattern.MatchString(p[0]) || len(p[1]) != 20 || !hashPattern.MatchString(p[2]) {
		return "", "", false
	}
	file = p[1] + "-" + p[2] + ".json"
	return p[0], file, entryPattern.MatchString(file)
}
func validRevisionID(id string) bool { _, _, ok := parseRevisionID(id); return ok }
func commandValid(q Scope, c Command) bool {
	return validScope(q) && branchPattern.MatchString(c.Branch) && scopePattern.MatchString(c.Actor) && keyPattern.MatchString(c.Key) && hashPattern.MatchString(c.Digest) && c.Expected >= 0 && (c.Head == "" || validRevisionID(c.Head)) && (c.Operation == "capture" || c.Operation == "fork")
}

// reader methods are always bound to one authorized project and space.
type reader interface {
	head(string) (revisions.Branch, error)
	get(string) (revisions.Revision, error)
	replay(string, string) (Record, error)
}

func mutate(q Scope, c Command, r reader) (Record, bool, error) {
	if !commandValid(q, c) {
		return Record{}, false, revisions.ErrInput
	}
	key := Hash(c.Actor + ":" + c.Key)
	if old, e := r.replay(c.Branch, key); e == nil {
		if old.Digest != c.Digest {
			return Record{}, false, ErrKey
		}
		return old, true, nil
	} else if !errors.Is(e, ErrNotFound) {
		return Record{}, false, e
	}
	now := time.Now().UTC()
	b, e := r.head(c.Branch)
	if errors.Is(e, ErrNotFound) {
		if c.Expected != 0 || c.Head != "" {
			return Record{}, false, revisions.ErrConflict
		}
		name := "主线"
		if c.Operation == "fork" {
			name = strings.TrimSpace(c.Name)
		} else if c.Branch != "main" {
			return Record{}, false, ErrNotFound
		}
		if !label(name) {
			return Record{}, false, revisions.ErrInput
		}
		b = revisions.Branch{ID: c.Branch, SpaceID: q.Space, ProjectID: q.Project, Name: name, CreatedAt: now}
	} else if e != nil {
		return Record{}, false, e
	} else if c.Operation == "fork" || b.Version != c.Expected || b.HeadRevisionID != c.Head {
		return Record{}, false, revisions.ErrConflict
	}
	out := Record{Format: 1, Key: key, Digest: c.Digest}
	if c.Operation == "fork" {
		if c.Branch != ForkID(c.Actor, c.Key) {
			return Record{}, false, revisions.ErrInput
		}
		source, e := r.get(c.SourceID)
		if e != nil {
			return Record{}, false, e
		}
		b.HeadRevisionID = source.ID
		b.Version = 1
		b.UpdatedAt = now
		out.Branch = b
	} else {
		m := c.Manifest
		parent := ""
		if b.HeadRevisionID != "" {
			v, e := r.get(b.HeadRevisionID)
			if e != nil {
				return Record{}, false, e
			}
			parent = v.ID
			m.ParentSHA256 = v.SHA256
		} else {
			m.ParentSHA256 = ""
		}
		sha, e := revisions.Digest(m)
		if e != nil || !label(strings.TrimSpace(c.Label)) || c.ProjectVersion < 1 || c.MediaVersion < 1 {
			return Record{}, false, revisions.ErrInput
		}
		v := revisions.Revision{ID: revisionID(c.Branch, b.Version+1, key), BranchID: c.Branch, Label: strings.TrimSpace(c.Label), Actor: c.Actor, SpaceID: q.Space, ProjectID: q.Project, ParentID: parent, Manifest: m, SHA256: sha, CreatedAt: now, ProjectVersion: c.ProjectVersion, MediaVersion: c.MediaVersion}
		b.Version++
		b.HeadRevisionID = v.ID
		b.UpdatedAt = now
		out.Branch = b
		out.Revision = &v
	}
	if !validRecord(out) {
		return Record{}, false, ErrStorage
	}
	return out, false, nil
}

func (s *Store) Mutate(ctx context.Context, tx *sql.Tx, q Scope, c Command) (Record, error) {
	if s.db != nil {
		return s.mutateSQL(ctx, tx, q, c)
	}
	var out Record
	e := s.withFile(ctx, q, true, func(f *files) error {
		r, replay, e := mutate(q, c, f)
		if e != nil {
			return e
		}
		out = r
		if replay {
			return nil
		}
		return f.append(r)
	})
	return out, e
}
func (s *Store) Get(ctx context.Context, q Scope, id string) (revisions.Revision, error) {
	if !validScope(q) || !validRevisionID(id) {
		return revisions.Revision{}, ErrNotFound
	}
	if s.db != nil {
		return (&sqlReader{s.db, ctx, q}).get(id)
	}
	var out revisions.Revision
	e := s.withFile(ctx, q, false, func(f *files) error { var e error; out, e = f.get(id); return e })
	return out, e
}
func (s *Store) Branch(ctx context.Context, q Scope, id string) (revisions.Branch, error) {
	if !validScope(q) || !branchPattern.MatchString(id) {
		return revisions.Branch{}, ErrNotFound
	}
	if s.db != nil {
		return (&sqlReader{s.db, ctx, q}).head(id)
	}
	var out revisions.Branch
	e := s.withFile(ctx, q, false, func(f *files) error { var e error; out, e = f.head(id); return e })
	return out, e
}

type BranchPage struct {
	Items []revisions.Branch `json:"items"`
	Next  string             `json:"next_cursor"`
}
type RevisionPage struct {
	Items []Summary `json:"items"`
	Next  string    `json:"next_cursor"`
}

// Replay is deliberately available before checking current project/media CAS.
// The caller must already have checked current project access.
func (s *Store) Replay(ctx context.Context, q Scope, branch, actor, key, digest string) (Record, bool, error) {
	if !validScope(q) || !branchPattern.MatchString(branch) || !scopePattern.MatchString(actor) || !keyPattern.MatchString(key) || !hashPattern.MatchString(digest) {
		return Record{}, false, revisions.ErrInput
	}
	var r Record
	var e error
	if s.db != nil {
		r, e = (&sqlReader{s.db, ctx, q}).replay(branch, Hash(actor+":"+key))
	} else {
		e = s.withFile(ctx, q, false, func(f *files) error { r, e = f.replay(branch, Hash(actor+":"+key)); return e })
	}
	if errors.Is(e, ErrNotFound) {
		return Record{}, false, nil
	}
	if e != nil {
		return Record{}, false, e
	}
	if r.Digest != digest {
		return Record{}, false, ErrKey
	}
	return r, true, nil
}
