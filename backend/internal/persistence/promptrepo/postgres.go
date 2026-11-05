package promptrepo

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/adcwb/ai-creative-studio/backend/internal/modules/prompts"
	"strconv"
	"strings"
)

const Schema = `CREATE TABLE IF NOT EXISTS studio_prompt_templates (
 space_id text NOT NULL,id text NOT NULL,owner_id text NOT NULL,visibility text NOT NULL CHECK(visibility IN ('private','organization')),status text NOT NULL CHECK(status IN ('active','archived')),row_version bigint NOT NULL CHECK(row_version>0),head_version_id text NOT NULL,payload jsonb NOT NULL,
 PRIMARY KEY(space_id,id),CHECK((payload->>'space_id'=space_id AND payload->>'id'=id AND payload->>'owner_id'=owner_id AND payload->>'visibility'=visibility AND payload->>'status'=status AND payload->>'head_version_id'=head_version_id AND (payload->>'row_version')::bigint=row_version) IS TRUE));
CREATE TABLE IF NOT EXISTS studio_prompt_versions (
 space_id text NOT NULL,template_id text NOT NULL,id text NOT NULL,number bigint NOT NULL CHECK(number>0),payload jsonb NOT NULL,PRIMARY KEY(space_id,template_id,id),UNIQUE(space_id,template_id,number),FOREIGN KEY(space_id,template_id) REFERENCES studio_prompt_templates(space_id,id),CHECK((payload->>'space_id'=space_id AND payload->>'template_id'=template_id AND payload->>'id'=id AND (payload->>'number')::bigint=number) IS TRUE));
CREATE TABLE IF NOT EXISTS studio_prompt_receipts (
 space_id text NOT NULL,template_id text NOT NULL,key_hash text NOT NULL,digest text NOT NULL,payload jsonb NOT NULL,PRIMARY KEY(space_id,template_id,key_hash),FOREIGN KEY(space_id,template_id) REFERENCES studio_prompt_templates(space_id,id));
CREATE TABLE IF NOT EXISTS studio_prompt_favorites (
 space_id text NOT NULL,actor_id text NOT NULL,template_id text NOT NULL,enabled boolean NOT NULL,PRIMARY KEY(space_id,actor_id,template_id),FOREIGN KEY(space_id,template_id) REFERENCES studio_prompt_templates(space_id,id));
CREATE INDEX IF NOT EXISTS studio_prompt_visible_list ON studio_prompt_templates(space_id,visibility,status,id);
CREATE INDEX IF NOT EXISTS studio_prompt_owner_list ON studio_prompt_templates(space_id,owner_id,status,id);`
const readSQL = `SELECT t.payload,v.payload,COALESCE(f.enabled,false) FROM studio_prompt_templates t JOIN studio_prompt_versions v ON v.space_id=t.space_id AND v.template_id=t.id AND v.id=t.head_version_id LEFT JOIN studio_prompt_favorites f ON f.space_id=t.space_id AND f.template_id=t.id AND f.actor_id=$2 WHERE t.space_id=$1 AND (t.owner_id=$2 OR t.visibility='organization')`

func scanResult(row interface{ Scan(...any) error }) (prompts.Result, error) {
	var out prompts.Result
	var t, v []byte
	if e := row.Scan(&t, &v, &out.Favorite); errors.Is(e, sql.ErrNoRows) {
		return out, prompts.ErrNotFound
	} else if e != nil {
		return out, ErrStorage
	}
	if json.Unmarshal(t, &out.Template) != nil || json.Unmarshal(v, &out.Version) != nil {
		return out, ErrStorage
	}
	return out, nil
}
func (s *Store) getSQL(ctx context.Context, a prompts.Actor, id string) (prompts.Result, error) {
	return scanResult(s.db.QueryRowContext(ctx, readSQL+` AND t.id=$3`, a.Space, a.User, id))
}
func (s *Store) versionSQL(ctx context.Context, a prompts.Actor, id, version string) (prompts.Version, error) {
	var raw []byte
	var out prompts.Version
	e := s.db.QueryRowContext(ctx, `SELECT v.payload FROM studio_prompt_versions v JOIN studio_prompt_templates t ON t.space_id=v.space_id AND t.id=v.template_id WHERE v.space_id=$1 AND (t.owner_id=$2 OR t.visibility='organization') AND t.id=$3 AND v.id=$4`, a.Space, a.User, id, version).Scan(&raw)
	if errors.Is(e, sql.ErrNoRows) {
		return out, prompts.ErrNotFound
	}
	if e != nil || json.Unmarshal(raw, &out) != nil {
		return out, ErrStorage
	}
	return out, nil
}
func (s *Store) mutateSQL(ctx context.Context, a prompts.Actor, id, key string, c Command) (prompts.Result, error) {
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return prompts.Result{}, ErrStorage
	}
	defer tx.Rollback()
	if _, e = tx.ExecContext(ctx, `SET LOCAL synchronous_commit=on`); e != nil {
		return prompts.Result{}, ErrStorage
	}
	if _, e = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock_shared(728219117)`); e != nil {
		return prompts.Result{}, ErrStorage
	}
	if _, e = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,728219121))`, a.Space+":"+id); e != nil {
		return prompts.Result{}, ErrStorage
	}
	read := readSQL
	if managingArchive(a, c) {
		read = strings.Replace(read, "(t.owner_id=$2 OR t.visibility='organization')", "true", 1)
	}
	current, e := scanResult(tx.QueryRowContext(ctx, read+` AND t.id=$3`, a.Space, a.User, id))
	var old *prompts.Result
	if e == nil {
		old = &current
	} else if !errors.Is(e, prompts.ErrNotFound) {
		return prompts.Result{}, e
	}
	// A hidden template must never become a create target or replay oracle.
	if old == nil {
		var exists bool
		if e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM studio_prompt_templates WHERE space_id=$1 AND id=$2)`, a.Space, id).Scan(&exists); e != nil {
			return prompts.Result{}, ErrStorage
		}
		if exists {
			return prompts.Result{}, prompts.ErrNotFound
		}
	}
	hash := prompts.TextSHA(a.User + "\x00" + c.Operation + "\x00" + key)
	var raw []byte
	var digest string
	e = tx.QueryRowContext(ctx, `SELECT digest,payload FROM studio_prompt_receipts WHERE space_id=$1 AND template_id=$2 AND key_hash=$3`, a.Space, id, hash).Scan(&digest, &raw)
	if e == nil {
		if digest != commandDigest(c) {
			return prompts.Result{}, ErrKey
		}
		var r record
		if json.Unmarshal(raw, &r) != nil {
			return prompts.Result{}, ErrStorage
		}
		return r.Result, nil
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return prompts.Result{}, ErrStorage
	}
	r, e := apply(a, id, key, c, old)
	if e != nil {
		return prompts.Result{}, e
	}
	if e = saveSQL(ctx, tx, r); e != nil {
		return prompts.Result{}, e
	}
	if tx.Commit() != nil {
		return prompts.Result{}, ErrStorage
	}
	return r.Result, nil
}
func saveSQL(ctx context.Context, tx *sql.Tx, r record) error {
	t := r.Result.Template
	tb, _ := json.Marshal(t)
	v := r.Result.Version
	vb, _ := json.Marshal(v)
	b, _ := json.Marshal(r)
	_, e := tx.ExecContext(ctx, `INSERT INTO studio_prompt_templates(space_id,id,owner_id,visibility,status,row_version,head_version_id,payload) VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT(space_id,id) DO UPDATE SET visibility=EXCLUDED.visibility,status=EXCLUDED.status,row_version=EXCLUDED.row_version,head_version_id=EXCLUDED.head_version_id,payload=EXCLUDED.payload`, t.SpaceID, t.ID, t.OwnerID, t.Visibility, t.Status, t.RowVersion, t.HeadVersionID, tb)
	if e != nil {
		return ErrStorage
	}
	if r.NewVersion {
		if _, e = tx.ExecContext(ctx, `INSERT INTO studio_prompt_versions(space_id,template_id,id,number,payload) VALUES($1,$2,$3,$4,$5)`, v.SpaceID, v.TemplateID, v.ID, v.Number, vb); e != nil {
			return ErrStorage
		}
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO studio_prompt_receipts(space_id,template_id,key_hash,digest,payload) VALUES($1,$2,$3,$4,$5)`, t.SpaceID, t.ID, r.KeyHash, r.Digest, b)
	if e != nil {
		return ErrStorage
	}
	return nil
}
func (s *Store) listSQL(ctx context.Context, a prompts.Actor, q Query) ([]prompts.Result, error) {
	rows, e := s.db.QueryContext(ctx, readSQL+` AND t.status=$3 AND t.id>$4 AND ($5='' OR t.owner_id=$2 AND $5='mine' OR t.visibility='organization' AND $5='organization') AND ($6='' OR t.payload->>'category'=$6) AND ($7='' OR (t.payload->'tags') ? $7) AND (NOT $8 OR COALESCE(f.enabled,false)) AND ($9='' OR strpos(lower((t.payload->>'name')||E'\n'||(t.payload->>'description')||E'\n'||(v.payload->>'body')),lower($9))>0) ORDER BY t.id LIMIT $10`, a.Space, a.User, q.Status, q.After, q.Source, q.Category, q.Tag, q.Favorite, q.Text, q.Limit)
	if e != nil {
		return nil, ErrStorage
	}
	defer rows.Close()
	out := []prompts.Result{}
	for rows.Next() {
		r, e := scanResult(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, r)
	}
	if rows.Err() != nil {
		return nil, ErrStorage
	}
	return out, nil
}
func (s *Store) versionsSQL(ctx context.Context, a prompts.Actor, id, after string, limit int) ([]prompts.Version, string, error) {
	n := int64(9223372036854775807)
	var e error
	if after != "" {
		n, e = strconv.ParseInt(after, 10, 64)
		if e != nil || n < 1 {
			return nil, "", prompts.ErrInput
		}
	}
	rows, e := s.db.QueryContext(ctx, `SELECT v.payload FROM studio_prompt_versions v JOIN studio_prompt_templates t ON t.space_id=v.space_id AND t.id=v.template_id WHERE v.space_id=$1 AND (t.owner_id=$2 OR t.visibility='organization') AND v.template_id=$3 AND v.number<$4 ORDER BY v.number DESC LIMIT $5`, a.Space, a.User, id, n, limit)
	if e != nil {
		return nil, "", ErrStorage
	}
	defer rows.Close()
	out := []prompts.Version{}
	for rows.Next() {
		var b []byte
		var v prompts.Version
		if rows.Scan(&b) != nil || json.Unmarshal(b, &v) != nil {
			return nil, "", ErrStorage
		}
		out = append(out, v)
	}
	if rows.Err() != nil {
		return nil, "", ErrStorage
	}
	next := ""
	if len(out) == limit {
		next = strconv.FormatInt(out[len(out)-1].Number, 10)
	}
	return out, next, nil
}
func (s *Store) favoriteSQL(ctx context.Context, a prompts.Actor, id string, enabled bool) error {
	// Visibility and preference write share a single SQL statement (no stale read grant).
	result, e := s.db.ExecContext(ctx, `INSERT INTO studio_prompt_favorites(space_id,actor_id,template_id,enabled) SELECT space_id,$2,id,$4 FROM studio_prompt_templates WHERE space_id=$1 AND id=$3 AND (owner_id=$2 OR visibility='organization') ON CONFLICT(space_id,actor_id,template_id) DO UPDATE SET enabled=EXCLUDED.enabled`, a.Space, a.User, id, enabled)
	if e != nil {
		return ErrStorage
	}
	n, e := result.RowsAffected()
	if e != nil {
		return ErrStorage
	}
	if n != 1 {
		return prompts.ErrNotFound
	}
	return nil
}
