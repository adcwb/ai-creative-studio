package revisionrepo

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/revisions"
)

func sameCopyRecord(a, b Record) bool {
	hash := func(v Record) string {
		raw, _ := json.Marshal(v)
		d, e := revisions.Digest(revisions.Manifest{Brief: raw, Script: json.RawMessage("null"), Storyboard: json.RawMessage("null"), Timeline: json.RawMessage("null"), Poster: json.RawMessage("null"), References: []revisions.Ref{}})
		if e != nil {
			return ""
		}
		return d
	}
	x := hash(a)
	return x != "" && x == hash(b)
}

// SnapshotScope reads the complete validated journal under its existing scope
// lock. This API is internal; callers must authorize the owning work first.
func (s *Store) SnapshotScope(ctx context.Context, q Scope) ([]Record, error) {
	if !validScope(q) {
		return nil, revisions.ErrInput
	}
	out := []Record{}
	if s.db != nil {
		rows, e := s.db.QueryContext(ctx, `SELECT payload FROM studio_revision_commits WHERE space_id=$1 AND project_id=$2 ORDER BY branch_id,sequence`, q.Space, q.Project)
		if e != nil {
			return nil, e
		}
		defer rows.Close()
		for rows.Next() {
			var b []byte
			if e = rows.Scan(&b); e != nil {
				return nil, e
			}
			r, e := decode(b)
			if e != nil || r.Branch.SpaceID != q.Space || r.Branch.ProjectID != q.Project {
				return nil, ErrStorage
			}
			out = append(out, r)
		}
		return out, rows.Err()
	}
	e := s.withFile(ctx, q, false, func(f *files) error {
		if e := directory(filepath.Dir(f.base)); os.IsNotExist(e) {
			return nil
		} else if e != nil {
			return e
		}
		if e := directory(f.base); os.IsNotExist(e) {
			return nil
		} else if e != nil {
			return e
		}
		dirs, e := os.ReadDir(f.base)
		if e != nil {
			return e
		}
		for _, d := range dirs {
			if !d.IsDir() || d.Type()&os.ModeSymlink != 0 || !branchPattern.MatchString(d.Name()) {
				return ErrStorage
			}
			names, e := f.entries(d.Name())
			if e != nil {
				return e
			}
			for _, n := range names {
				r, e := f.load(d.Name(), n)
				if e != nil {
					return e
				}
				out = append(out, r)
			}
		}
		return nil
	})
	return out, e
}

// CloneRecords changes scope, every journal identity, and manifest digests.
// Parentage and fork heads remain equivalent; old mutation receipts are never
// credentials for the new work. A staged target may import an identical prefix
// again after a crash, but an existing different record is always rejected.
func CloneRecords(records []Record, q Scope, copyID string, rewrite func(revisions.Manifest) (revisions.Manifest, error)) ([]Record, error) {
	if !validScope(q) || !scopePattern.MatchString(copyID) {
		return nil, revisions.ErrInput
	}
	b, _ := json.Marshal(records)
	var out []Record
	if json.Unmarshal(b, &out) != nil {
		return nil, ErrStorage
	}
	ids := map[string]string{}
	branch := func(id string) string {
		if id == "main" {
			return id
		}
		return Hash(copyID + ":branch:" + id)[:32]
	}
	byID := map[string]*revisions.Revision{}
	for i := range out {
		r := &out[i]
		if !validRecord(*r) {
			return nil, ErrStorage
		}
		r.Key = Hash(copyID + ":" + r.Key)
		r.Digest = Hash(copyID + ":" + r.Digest)
		r.Branch.ID = branch(r.Branch.ID)
		r.Branch.SpaceID = q.Space
		r.Branch.ProjectID = q.Project
		if r.Revision != nil {
			old := r.Revision.ID
			next := revisionID(r.Branch.ID, r.Branch.Version, r.Key)
			ids[old] = next
			byID[old] = r.Revision
		}
	}
	visiting := map[string]bool{}
	done := map[string]bool{}
	var transform func(string) error
	transform = func(old string) error {
		if old == "" || done[old] {
			return nil
		}
		v, ok := byID[old]
		if !ok || visiting[old] {
			return ErrStorage
		}
		visiting[old] = true
		parent := v.ParentID
		if e := transform(parent); e != nil {
			return e
		}
		m, e := rewrite(v.Manifest)
		if e != nil {
			return e
		}
		m.ParentSHA256 = ""
		if parent != "" {
			m.ParentSHA256 = byID[parent].SHA256
		}
		sha, e := revisions.Digest(m)
		if e != nil {
			return e
		}
		v.Manifest = m
		v.SHA256 = sha
		v.ID = ids[old]
		v.ParentID = ids[parent]
		v.RestoredFromID = ids[v.RestoredFromID]
		v.BranchID = branch(v.BranchID)
		v.SpaceID = q.Space
		v.ProjectID = q.Project
		done[old] = true
		delete(visiting, old)
		return nil
	}
	for old := range byID {
		if e := transform(old); e != nil {
			return nil, e
		}
	}
	for i := range out {
		r := &out[i]
		r.Branch.HeadRevisionID = ids[r.Branch.HeadRevisionID]
		if !validRecord(*r) {
			return nil, ErrStorage
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i].Branch, out[j].Branch
		if a.ID != b.ID {
			return a.ID < b.ID
		}
		return a.Version < b.Version
	})
	return out, nil
}
func (s *Store) ImportCopy(ctx context.Context, q Scope, records []Record) error {
	for _, r := range records {
		if !validRecord(r) || r.Branch.SpaceID != q.Space || r.Branch.ProjectID != q.Project {
			return ErrStorage
		}
	}
	if s.db != nil {
		tx, e := s.db.BeginTx(ctx, nil)
		if e != nil {
			return e
		}
		defer tx.Rollback()
		if _, e = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "revision-copy:"+q.Space+":"+q.Project); e != nil {
			return e
		}
		for _, r := range records {
			old, e := scan(tx.QueryRowContext(ctx, `SELECT payload FROM studio_revision_commits WHERE space_id=$1 AND project_id=$2 AND branch_id=$3 AND sequence=$4`, q.Space, q.Project, r.Branch.ID, r.Branch.Version))
			if e == nil {
				if !sameCopyRecord(old, r) {
					return ErrStorage
				}
				continue
			}
			if !errors.Is(e, ErrNotFound) {
				return e
			}
			if e = saveSQL(ctx, tx, r); e != nil {
				return e
			}
		}
		return tx.Commit()
	}
	return s.withFile(ctx, q, true, func(f *files) error {
		for _, r := range records {
			old, e := f.load(r.Branch.ID, recordName(r))
			if e == nil {
				if !sameCopyRecord(old, r) {
					return ErrStorage
				}
				continue
			}
			if !errors.Is(e, ErrNotFound) {
				return e
			}
			if e = f.append(r); e != nil {
				return e
			}
		}
		return nil
	})
}
