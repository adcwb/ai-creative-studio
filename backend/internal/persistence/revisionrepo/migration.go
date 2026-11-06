package revisionrepo

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

func encode(r Record) ([]byte, error)     { return json.Marshal(r) }
func decodeValue(raw []byte, v any) error { return json.Unmarshal(raw, v) }
func eachFile(ctx context.Context, dir string, fn func(Record) error) error {
	if e := directory(dir); os.IsNotExist(e) {
		return nil
	} else if e != nil {
		return ErrStorage
	}
	return filepath.WalkDir(dir, func(p string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if e = ctx.Err(); e != nil {
			return e
		}
		if d.Type()&os.ModeSymlink != 0 {
			return ErrStorage
		}
		if p == dir {
			return nil
		}
		rel, e := filepath.Rel(dir, p)
		if e != nil {
			return e
		}
		parts := strings.Split(rel, string(filepath.Separator))
		if d.IsDir() {
			if len(parts) > 3 || len(parts) == 1 && !hashPattern.MatchString(parts[0]) || len(parts) == 2 && !scopePattern.MatchString(parts[1]) || len(parts) == 3 && !branchPattern.MatchString(parts[2]) {
				return ErrStorage
			}
			return nil
		}
		if len(parts) == 1 && strings.HasPrefix(d.Name(), ".lock-") {
			st, e := d.Info()
			if e != nil || !st.Mode().IsRegular() {
				return ErrStorage
			}
			return nil
		}
		if len(parts) == 4 && strings.HasPrefix(d.Name(), ".pending-") {
			return nil
		}
		if len(parts) != 4 || !entryPattern.MatchString(d.Name()) {
			return ErrStorage
		}
		r, e := read(p)
		if e != nil || Hash(r.Branch.SpaceID) != parts[0] || r.Branch.ProjectID != parts[1] || r.Branch.ID != parts[2] || recordName(r) != parts[3] {
			return ErrStorage
		}
		return fn(r)
	})
}
func CopyValidation(ctx context.Context, src, dst string) error {
	s, e := Open(dst, nil)
	if e != nil {
		return e
	}
	return eachFile(ctx, src, func(r Record) error {
		return (&files{s, Scope{r.Branch.SpaceID, r.Branch.ProjectID}, ctx, ""}).appendAt(r)
	})
}
func (f *files) appendAt(r Record) error {
	f.base = filepath.Join(f.s.dir, Hash(f.q.Space), f.q.Project)
	return f.append(r)
}
func ImportFiles(ctx context.Context, tx *sql.Tx, dir string) (int, error) {
	var total int
	if e := tx.QueryRowContext(ctx, `SELECT count(*) FROM studio_revision_commits`).Scan(&total); e != nil || total != 0 {
		return 0, ErrStorage
	}
	e := eachFile(ctx, dir, func(r Record) error {
		if e := saveSQL(ctx, tx, r); e != nil {
			return e
		}
		total++
		return nil
	})
	if e == nil {
		e = validateSQL(ctx, tx)
	}
	return total, e
}
func validateSQL(ctx context.Context, tx *sql.Tx) error {
	var bad int
	e := tx.QueryRowContext(ctx, `SELECT count(*) FROM studio_revision_commits r WHERE (r.sequence>1 AND NOT EXISTS(SELECT 1 FROM studio_revision_commits p WHERE p.space_id=r.space_id AND p.project_id=r.project_id AND p.branch_id=r.branch_id AND p.sequence=r.sequence-1)) OR NOT EXISTS(SELECT 1 FROM studio_revision_commits v WHERE v.space_id=r.space_id AND v.project_id=r.project_id AND v.revision_id=r.payload->'branch'->>'head_revision_id') OR (r.revision_id IS NOT NULL AND COALESCE(r.payload->'revision'->>'parent_id','')<>'' AND NOT EXISTS(SELECT 1 FROM studio_revision_commits p WHERE p.space_id=r.space_id AND p.project_id=r.project_id AND p.revision_id=r.payload->'revision'->>'parent_id' AND p.payload->'revision'->>'content_sha256'=r.payload->'revision'->'manifest'->>'parent_sha256'))`).Scan(&bad)
	if e != nil || bad != 0 {
		return ErrStorage
	}
	return nil
}
func ExportFiles(ctx context.Context, tx *sql.Tx, dir string) (int, error) {
	if list, e := os.ReadDir(dir); e != nil && !os.IsNotExist(e) || len(list) > 0 {
		return 0, ErrStorage
	}
	if e := validateSQL(ctx, tx); e != nil {
		return 0, e
	}
	s, e := Open(dir, nil)
	if e != nil {
		return 0, e
	}
	rows, e := tx.QueryContext(ctx, `SELECT payload FROM studio_revision_commits ORDER BY space_id,project_id,branch_id,sequence`)
	if e != nil {
		return 0, e
	}
	defer rows.Close()
	total := 0
	for rows.Next() {
		var raw []byte
		if rows.Scan(&raw) != nil {
			return 0, ErrStorage
		}
		r, e := decode(raw)
		if e != nil {
			return 0, e
		}
		if e = (&files{s, Scope{r.Branch.SpaceID, r.Branch.ProjectID}, ctx, ""}).appendAt(r); e != nil {
			return 0, e
		}
		total++
	}
	return total, rows.Err()
}
