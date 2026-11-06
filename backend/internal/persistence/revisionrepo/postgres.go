package revisionrepo

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/revisions"
)

type rowQuerier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}
type sqlReader struct {
	db  rowQuerier
	ctx context.Context
	q   Scope
}

func scan(row *sql.Row) (Record, error) {
	var b []byte
	e := row.Scan(&b)
	if errors.Is(e, sql.ErrNoRows) {
		return Record{}, ErrNotFound
	}
	if e != nil {
		return Record{}, ErrStorage
	}
	return decode(b)
}
func (r *sqlReader) check(v Record, e error) (Record, error) {
	if e == nil && (v.Branch.SpaceID != r.q.Space || v.Branch.ProjectID != r.q.Project) {
		return Record{}, ErrStorage
	}
	return v, e
}
func (r *sqlReader) head(id string) (revisions.Branch, error) {
	v, e := scan(r.db.QueryRowContext(r.ctx, `SELECT payload FROM studio_revision_commits WHERE space_id=$1 AND project_id=$2 AND branch_id=$3 ORDER BY sequence DESC LIMIT 1`, r.q.Space, r.q.Project, id))
	v, e = r.check(v, e)
	return v.Branch, e
}
func (r *sqlReader) get(id string) (revisions.Revision, error) {
	v, e := scan(r.db.QueryRowContext(r.ctx, `SELECT payload FROM studio_revision_commits WHERE space_id=$1 AND project_id=$2 AND revision_id=$3`, r.q.Space, r.q.Project, id))
	v, e = r.check(v, e)
	if e != nil {
		return revisions.Revision{}, e
	}
	if v.Revision == nil || v.Revision.ID != id {
		return revisions.Revision{}, ErrStorage
	}
	return *v.Revision, nil
}
func (r *sqlReader) replay(branch, key string) (Record, error) {
	v, e := scan(r.db.QueryRowContext(r.ctx, `SELECT payload FROM studio_revision_commits WHERE space_id=$1 AND project_id=$2 AND branch_id=$3 AND key_hash=$4`, r.q.Space, r.q.Project, branch, key))
	return r.check(v, e)
}
func saveSQL(ctx context.Context, tx *sql.Tx, r Record) error {
	if !validRecord(r) {
		return ErrStorage
	}
	raw, e := encode(r)
	if e != nil {
		return e
	}
	var rid any
	if r.Revision != nil {
		rid = r.Revision.ID
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO studio_revision_commits(space_id,project_id,branch_id,sequence,key_hash,request_digest,revision_id,payload) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, r.Branch.SpaceID, r.Branch.ProjectID, r.Branch.ID, r.Branch.Version, r.Key, r.Digest, rid, raw)
	return e
}
func (s *Store) mutateSQL(ctx context.Context, tx *sql.Tx, q Scope, c Command) (Record, error) {
	if !commandValid(q, c) {
		return Record{}, revisions.ErrInput
	}
	own := tx == nil
	var e error
	if own {
		tx, e = s.db.BeginTx(ctx, nil)
		if e != nil {
			return Record{}, ErrStorage
		}
		defer tx.Rollback()
	}
	if _, e = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "revision:"+q.Space+":"+q.Project+":"+c.Branch); e != nil {
		return Record{}, ErrStorage
	}
	r, replay, e := mutate(q, c, &sqlReader{tx, ctx, q})
	if e != nil {
		return Record{}, e
	}
	if !replay {
		if e = saveSQL(ctx, tx, r); e != nil {
			return Record{}, e
		}
	}
	if own {
		e = tx.Commit()
	}
	return r, e
}
func (s *Store) branchesSQL(ctx context.Context, q Scope, after string, limit int) (BranchPage, error) {
	out := BranchPage{Items: []revisions.Branch{}}
	rows, e := s.db.QueryContext(ctx, `SELECT payload->'branch' FROM (SELECT DISTINCT ON(branch_id) branch_id,payload FROM studio_revision_commits WHERE space_id=$1 AND project_id=$2 AND branch_id>$3 ORDER BY branch_id,sequence DESC) heads ORDER BY branch_id LIMIT $4`, q.Space, q.Project, after, limit+1)
	if e != nil {
		return out, ErrStorage
	}
	defer rows.Close()
	for rows.Next() {
		var raw []byte
		var b revisions.Branch
		if rows.Scan(&raw) != nil || decodeValue(raw, &b) != nil || b.SpaceID != q.Space || b.ProjectID != q.Project || b.Version < 1 {
			return out, ErrStorage
		}
		if len(out.Items) == limit {
			out.Next = out.Items[len(out.Items)-1].ID
			break
		}
		out.Items = append(out.Items, b)
	}
	return out, rows.Err()
}
func (s *Store) historySQL(ctx context.Context, q Scope, branch, before string, limit int) (RevisionPage, error) {
	out := RevisionPage{Items: []Summary{}}
	var number int64
	if before != "" {
		n, e := strconv.ParseInt(strings.SplitN(before, "-", 2)[0], 10, 64)
		if e != nil || n < 1 {
			return out, revisions.ErrInput
		}
		number = n
	}
	// Metadata only: neither the full manifest nor asset/context bodies are sent
	// from PostgreSQL into list responses or repeated UI polling.
	rows, e := s.db.QueryContext(ctx, `SELECT sequence,key_hash,payload->'revision'->>'id',payload->'revision'->>'branch_id',COALESCE(payload->'revision'->>'parent_id',''),payload->'revision'->>'label',payload->'revision'->>'content_sha256',payload->'revision'->>'actor',(payload->'revision'->>'project_version')::integer,(payload->'revision'->>'media_version')::integer,(payload->'revision'->>'created_at')::timestamptz FROM studio_revision_commits WHERE space_id=$1 AND project_id=$2 AND branch_id=$3 AND revision_id IS NOT NULL AND ($4::bigint=0 OR sequence<$4) ORDER BY sequence DESC LIMIT $5`, q.Space, q.Project, branch, number, limit+1)
	if e != nil {
		return out, ErrStorage
	}
	defer rows.Close()
	last := ""
	for rows.Next() {
		var n int
		var key string
		var v Summary
		if rows.Scan(&n, &key, &v.ID, &v.BranchID, &v.ParentID, &v.Label, &v.SHA256, &v.Actor, &v.ProjectVersion, &v.MediaVersion, &v.CreatedAt) != nil || v.ID != revisionID(branch, n, key) {
			return out, ErrStorage
		}
		if len(out.Items) == limit {
			out.Next = last
			break
		}
		out.Items = append(out.Items, v)
		_, last, _ = parseRevisionID(v.ID)
	}
	return out, rows.Err()
}
