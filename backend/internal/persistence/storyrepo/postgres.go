package storyrepo

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/story"
)

type rowScanner interface{ Scan(...any) error }

func scan(row rowScanner, q Scope, id string) (record, error) {
	var b []byte
	if e := row.Scan(&b); errors.Is(e, sql.ErrNoRows) {
		return record{}, story.ErrNotFound
	} else if e != nil {
		return record{}, ErrStorage
	}
	r, e := decodeBytes(b)
	if e != nil || r.Version.Space != q.Space || r.Version.SeriesID != q.Series || id != "" && r.Version.ID != id {
		return record{}, ErrStorage
	}
	return r, nil
}

const headSelect = `SELECT v.payload FROM studio_story_versions v JOIN studio_story_heads h ON h.space_id=v.space_id AND h.series_id=v.series_id AND h.id=v.id AND h.number=v.number WHERE v.space_id=$1 AND v.series_id=$2`

func (s *Store) getSQL(ctx context.Context, q Scope, id, version string) (story.Version, error) {
	var r record
	var e error
	if version == "" {
		r, e = scan(s.db.QueryRowContext(ctx, headSelect+` AND v.id=$3`, q.Space, q.Series, id), q, id)
	} else {
		r, e = scan(s.db.QueryRowContext(ctx, `SELECT payload FROM studio_story_versions WHERE space_id=$1 AND series_id=$2 AND id=$3 AND version_id=$4`, q.Space, q.Series, id, version), q, id)
	}
	return r.Version, e
}
func saveSQL(ctx context.Context, tx *sql.Tx, r record) error {
	if !validRecord(r) {
		return ErrStorage
	}
	v := r.Version
	b, e := json.Marshal(r)
	if e != nil {
		return ErrStorage
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO studio_story_versions(space_id,series_id,id,number,version_id,key_hash,request_digest,payload) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, v.Space, v.SeriesID, v.ID, v.Number, v.VersionID, r.Key, r.Digest, b); e != nil {
		return ErrStorage
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO studio_story_heads(space_id,series_id,id,number,version_id,kind,status,title,required,sha256) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) ON CONFLICT(space_id,series_id,id) DO UPDATE SET number=EXCLUDED.number,version_id=EXCLUDED.version_id,kind=EXCLUDED.kind,status=EXCLUDED.status,title=EXCLUDED.title,required=EXCLUDED.required,sha256=EXCLUDED.sha256`, v.Space, v.SeriesID, v.ID, v.Number, v.VersionID, v.Kind, v.Status, v.Title, v.Required, v.SHA256)
	if e != nil {
		return ErrStorage
	}
	return nil
}
func (s *Store) saveSQL(ctx context.Context, a story.Actor, q Scope, id, key string, in story.Input) (story.Version, error) {
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return story.Version{}, ErrStorage
	}
	defer tx.Rollback()
	if _, e = tx.ExecContext(ctx, `SET LOCAL synchronous_commit=on;SELECT pg_advisory_xact_lock_shared(728219117)`); e != nil {
		return story.Version{}, ErrStorage
	}
	if _, e = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,728219140))`, q.Space+":"+q.Series); e != nil {
		return story.Version{}, ErrStorage
	}
	hash := story.TextHash(a.User + ":" + key)
	replay, e := scan(tx.QueryRowContext(ctx, `SELECT payload FROM studio_story_versions WHERE space_id=$1 AND series_id=$2 AND id=$3 AND key_hash=$4`, q.Space, q.Series, id, hash), q, id)
	if e == nil {
		if replay.Digest != story.Digest(in) {
			return story.Version{}, ErrKey
		}
		return replay.Version, nil
	}
	if !errors.Is(e, story.ErrNotFound) {
		return story.Version{}, e
	}
	current, e := scan(tx.QueryRowContext(ctx, headSelect+` AND v.id=$3`, q.Space, q.Series, id), q, id)
	var old *story.Version
	if e == nil {
		old = &current.Version
	} else if !errors.Is(e, story.ErrNotFound) {
		return story.Version{}, e
	}
	r, e := apply(a, q, id, key, in, old)
	if e != nil {
		return story.Version{}, e
	}
	if e = saveSQL(ctx, tx, r); e != nil {
		return story.Version{}, e
	}
	if e = tx.Commit(); e != nil {
		return story.Version{}, ErrStorage
	}
	return r.Version, nil
}
func (s *Store) listSQL(ctx context.Context, q Scope, in Query) (Page, error) {
	out := Page{Items: []story.Summary{}}
	rows, e := s.db.QueryContext(ctx, `SELECT id,series_id,version_id,number,title,kind,status,required,sha256 FROM studio_story_heads WHERE space_id=$1 AND series_id=$2 AND id>$3 AND ($4='' OR kind=$4) AND ($5='' OR status=$5) ORDER BY id LIMIT $6`, q.Space, q.Series, in.After, in.Kind, in.Status, in.Limit+1)
	if e != nil {
		return out, ErrStorage
	}
	defer rows.Close()
	for rows.Next() {
		var v story.Summary
		if rows.Scan(&v.ID, &v.SeriesID, &v.VersionID, &v.Number, &v.Title, &v.Kind, &v.Status, &v.Required, &v.SHA256) != nil || !story.ID.MatchString(v.ID) || v.SeriesID != q.Series || !story.ID.MatchString(v.VersionID) || v.Number < 1 || !story.Hash.MatchString(v.SHA256) {
			return out, ErrStorage
		}
		out.Items = append(out.Items, v)
	}
	if rows.Err() != nil {
		return out, ErrStorage
	}
	if len(out.Items) > in.Limit {
		out.Next = out.Items[in.Limit-1].ID
		out.Items = out.Items[:in.Limit]
	}
	return out, nil
}
func (s *Store) snapshotSQL(ctx context.Context, q Scope) ([]story.Version, error) {
	out := []story.Version{}
	rows, e := s.db.QueryContext(ctx, headSelect+` ORDER BY v.id`, q.Space, q.Series)
	if e != nil {
		return nil, ErrStorage
	}
	defer rows.Close()
	for rows.Next() {
		r, e := scan(rows, q, "")
		if e != nil {
			return nil, e
		}
		out = append(out, r.Version)
	}
	if rows.Err() != nil {
		return nil, ErrStorage
	}
	return out, nil
}
func (s *Store) historySQL(ctx context.Context, q Scope, id string, before int64, limit int) (HistoryPage, error) {
	out := HistoryPage{Items: []story.Version{}}
	if _, e := s.getSQL(ctx, q, id, ""); e != nil {
		return out, e
	}
	rows, e := s.db.QueryContext(ctx, `SELECT payload FROM studio_story_versions WHERE space_id=$1 AND series_id=$2 AND id=$3 AND ($4::bigint=0 OR number<$4) ORDER BY number DESC LIMIT $5`, q.Space, q.Series, id, before, limit+1)
	if e != nil {
		return out, ErrStorage
	}
	defer rows.Close()
	for rows.Next() {
		r, e := scan(rows, q, id)
		if e != nil {
			return out, e
		}
		out.Items = append(out.Items, r.Version)
	}
	if rows.Err() != nil {
		return out, ErrStorage
	}
	if len(out.Items) > limit {
		out.Next = strconv.FormatInt(out.Items[limit-1].Number, 10)
		out.Items = out.Items[:limit]
	}
	return out, nil
}
