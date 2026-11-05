package seriesrepo

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/adcwb/ai-creative-studio/backend/internal/modules/series"
	"strconv"
	"strings"
)

const Schema = `CREATE TABLE IF NOT EXISTS studio_series_nodes (
 space_id text NOT NULL,kind text NOT NULL CHECK(kind IN ('series','season','episode')),id text NOT NULL,
 parent_id text,parent_kind text GENERATED ALWAYS AS (CASE kind WHEN 'season' THEN 'series' WHEN 'episode' THEN 'season' ELSE NULL END) STORED,
 series_id text NOT NULL,ordinal bigint NOT NULL CHECK(ordinal>=0),row_version bigint NOT NULL CHECK(row_version>0),status text NOT NULL,state text NOT NULL CHECK(state IN ('ready','preparing')),project_id text,payload jsonb NOT NULL,
 PRIMARY KEY(space_id,kind,id),UNIQUE(space_id,kind,id,series_id),UNIQUE(space_id,project_id),
 UNIQUE(space_id,kind,parent_id,ordinal) DEFERRABLE INITIALLY DEFERRED,
 FOREIGN KEY(space_id,parent_kind,parent_id,series_id) REFERENCES studio_series_nodes(space_id,kind,id,series_id) DEFERRABLE INITIALLY DEFERRED,
 CHECK((payload->>'space_id'=space_id AND payload->>'kind'=kind AND payload->>'id'=id AND payload->>'series_id'=series_id AND COALESCE((payload->>'ordinal')::bigint,0)=ordinal AND NULLIF(payload->>'parent_id','') IS NOT DISTINCT FROM parent_id AND NULLIF(payload->>'project_id','') IS NOT DISTINCT FROM project_id) IS TRUE),
 CHECK(((payload->>'row_version')::bigint=row_version AND payload->>'state'=state AND payload->>'status'=status) IS TRUE),
 CHECK((kind='series' AND parent_id IS NULL AND series_id=id AND ordinal=0 AND project_id IS NULL) OR (kind='season' AND parent_id=series_id AND ordinal>0 AND project_id IS NULL) OR (kind='episode' AND parent_id IS NOT NULL AND ordinal>0 AND project_id IS NOT NULL)));
CREATE INDEX IF NOT EXISTS studio_series_directory ON studio_series_nodes(space_id,kind,parent_id,state,ordinal,id);
CREATE TABLE IF NOT EXISTS studio_series_receipts (
 space_id text NOT NULL,key_hash text NOT NULL,digest text NOT NULL,kind text NOT NULL,id text NOT NULL,
 PRIMARY KEY(space_id,key_hash),FOREIGN KEY(space_id,kind,id) REFERENCES studio_series_nodes(space_id,kind,id));`

type sqlAccess struct {
	ctx   context.Context
	space string
	tx    *sql.Tx
}

func scanNode(row interface{ Scan(...any) error }) (series.Node, error) {
	var n series.Node
	var b []byte
	e := row.Scan(&b)
	if errors.Is(e, sql.ErrNoRows) {
		return n, series.ErrNotFound
	}
	if e != nil || json.Unmarshal(b, &n) != nil || !series.Valid(n) {
		return n, ErrStorage
	}
	return n, nil
}
func (f sqlAccess) get(kind, id string) (series.Node, error) {
	return scanNode(f.tx.QueryRowContext(f.ctx, `SELECT payload FROM studio_series_nodes WHERE space_id=$1 AND kind=$2 AND id=$3`, f.space, kind, id))
}
func (f sqlAccess) children(kind, parent string) ([]series.Node, error) {
	rows, e := f.tx.QueryContext(f.ctx, `SELECT payload FROM studio_series_nodes WHERE space_id=$1 AND kind=$2 AND ($3='' OR parent_id=$3) ORDER BY ordinal,id`, f.space, kind, parent)
	if e != nil {
		return nil, ErrStorage
	}
	defer rows.Close()
	out := []series.Node{}
	for rows.Next() {
		n, e := scanNode(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, n)
	}
	return out, rows.Err()
}
func saveSQL(ctx context.Context, tx *sql.Tx, n series.Node) error {
	if !series.Valid(n) {
		return ErrStorage
	}
	b, e := json.Marshal(n)
	if e != nil {
		return ErrStorage
	}
	var parent, project any
	if n.Parent != "" {
		parent = n.Parent
	}
	if n.ProjectID != "" {
		project = n.ProjectID
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO studio_series_nodes(space_id,kind,id,parent_id,series_id,ordinal,row_version,status,state,project_id,payload) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) ON CONFLICT(space_id,kind,id) DO UPDATE SET ordinal=EXCLUDED.ordinal,row_version=EXCLUDED.row_version,status=EXCLUDED.status,state=EXCLUDED.state,payload=EXCLUDED.payload`, n.Space, n.Kind, n.ID, parent, n.SeriesID, n.Ordinal, n.Version, n.Status, n.State, project, b)
	if e != nil {
		return ErrStorage
	}
	return nil
}
func (s *Store) MutateTx(ctx context.Context, tx *sql.Tx, a series.Actor, key string, c series.Command) (series.Node, error) {
	if !validActor(a) || !a.Writable {
		return series.Node{}, series.ErrDenied
	}
	if len(key) < 8 || len(key) > 128 {
		return series.Node{}, series.ErrInput
	}
	if _, e := tx.ExecContext(ctx, `SET LOCAL synchronous_commit=on; SELECT pg_advisory_xact_lock_shared(728219117)`); e != nil {
		return series.Node{}, ErrStorage
	}
	if _, e := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,728219128))`, a.Space); e != nil {
		return series.Node{}, ErrStorage
	}
	hash, digest := keyHash(a, c, key), series.Digest(c)
	var savedDigest, kind, id string
	e := tx.QueryRowContext(ctx, `SELECT digest,kind,id FROM studio_series_receipts WHERE space_id=$1 AND key_hash=$2`, a.Space, hash).Scan(&savedDigest, &kind, &id)
	r := sqlAccess{ctx, a.Space, tx}
	if e == nil {
		if savedDigest != digest {
			return series.Node{}, ErrKey
		}
		return r.get(kind, id)
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return series.Node{}, ErrStorage
	}
	m, e := apply(a, c, r, false)
	if e != nil {
		return series.Node{}, e
	}
	for _, n := range m.Rows {
		if e = saveSQL(ctx, tx, n); e != nil {
			return series.Node{}, e
		}
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO studio_series_receipts(space_id,key_hash,digest,kind,id) VALUES($1,$2,$3,$4,$5)`, a.Space, hash, digest, m.Result.Kind, m.Result.ID); e != nil {
		return series.Node{}, ErrStorage
	}
	return m.Result, nil
}
func (s *Store) Mutate(ctx context.Context, a series.Actor, key string, c series.Command, project func(series.Node) error) (series.Node, error) {
	if s.db == nil {
		return s.mutateFile(ctx, a, key, c, project)
	}
	// Episode + Project must share the caller's core transaction.
	if c.Operation == "create" && c.Kind == "episode" {
		return series.Node{}, ErrStorage
	}
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return series.Node{}, ErrStorage
	}
	defer tx.Rollback()
	n, e := s.MutateTx(ctx, tx, a, key, c)
	if e != nil {
		return n, e
	}
	if e = tx.Commit(); e != nil {
		return series.Node{}, ErrStorage
	}
	return n, nil
}
func cursor(n series.Node) string { return fmt.Sprintf("%016d:%s", n.Ordinal, n.ID) }
func parseCursor(s string) (int64, string, error) {
	if s == "" {
		return -1, "", nil
	}
	p := strings.Split(s, ":")
	if len(p) != 2 || len(p[0]) != 16 || !series.ID.MatchString(p[1]) {
		return 0, "", series.ErrInput
	}
	v, e := strconv.ParseInt(p[0], 10, 64)
	if e != nil || v < 0 || v > series.MaxOrdinal {
		return 0, "", series.ErrInput
	}
	return v, p[1], nil
}
func (s *Store) listSQL(ctx context.Context, space string, q Query) (Page, error) {
	ordinal, id, e := parseCursor(q.After)
	if e != nil {
		return Page{}, e
	}
	rows, e := s.db.QueryContext(ctx, `SELECT payload FROM studio_series_nodes WHERE space_id=$1 AND kind=$2 AND ($3='' OR parent_id=$3) AND state='ready' AND ($4='' OR status=$4) AND (ordinal,id)>($5,$6) ORDER BY ordinal,id LIMIT $7`, space, q.Kind, q.Parent, q.Status, ordinal, id, q.Limit+1)
	if e != nil {
		return Page{}, ErrStorage
	}
	defer rows.Close()
	out := Page{Items: []series.Node{}}
	for rows.Next() {
		n, e := scanNode(rows)
		if e != nil {
			return Page{}, e
		}
		out.Items = append(out.Items, n)
	}
	if e = rows.Err(); e != nil {
		return Page{}, ErrStorage
	}
	if len(out.Items) > q.Limit {
		out.Next = cursor(out.Items[q.Limit-1])
		out.Items = out.Items[:q.Limit]
	}
	return out, nil
}
