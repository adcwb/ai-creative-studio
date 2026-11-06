package storyrepo

import (
	"context"
	"database/sql"
	"errors"
	"sort"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/story"
)

type headsQuery interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func queryHeads(ctx context.Context, db headsQuery, q Scope) ([]story.Summary, error) {
	rows, e := db.QueryContext(ctx, `SELECT id,series_id,version_id,number,title,kind,status,required,sha256 FROM studio_story_heads WHERE space_id=$1 AND series_id=$2 ORDER BY CASE WHEN kind='bible' THEN 0 WHEN required THEN 1 ELSE 2 END,id`, q.Space, q.Series)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	heads := []story.Summary{}
	for rows.Next() {
		var h story.Summary
		if rows.Scan(&h.ID, &h.SeriesID, &h.VersionID, &h.Number, &h.Title, &h.Kind, &h.Status, &h.Required, &h.SHA256) != nil || h.SeriesID != q.Series || !story.ID.MatchString(h.ID) || !story.ID.MatchString(h.VersionID) || !story.Hash.MatchString(h.SHA256) || h.Number < 1 {
			return nil, ErrStorage
		}
		heads = append(heads, h)
	}
	if rows.Err() != nil {
		return nil, ErrStorage
	}
	return heads, nil
}
func (s *Store) fileSummaries(q Scope) ([]story.Summary, error) {
	ids, e := s.ids(q)
	if e != nil {
		return nil, e
	}
	heads := []story.Summary{}
	for _, id := range ids {
		v, e := s.fileCurrent(q, id)
		if errors.Is(e, story.ErrNotFound) {
			continue
		}
		if e != nil {
			return nil, e
		}
		heads = append(heads, story.Summarize(v))
	}
	sort.Slice(heads, func(i, j int) bool {
		a, b := heads[i], heads[j]
		if (a.Kind == "bible") != (b.Kind == "bible") {
			return a.Kind == "bible"
		}
		if a.Required != b.Required {
			return a.Required
		}
		return a.ID < b.ID
	})
	return heads, nil
}
func (s *Store) HeadDigest(ctx context.Context, q Scope) (string, error) {
	if !validScope(q) {
		return "", story.ErrInput
	}
	if s.db != nil {
		return sqlHeads(ctx, s.db, q)
	}
	var digest string
	var e error
	e = s.withFile(ctx, q, false, func() error { digest, e = s.fileHeads(q); return e })
	return digest, e
}

// WithHeads protects a short local authored commit. Never call this around
// provider I/O or reenter this repository in the callback.
func (s *Store) WithHeads(ctx context.Context, q Scope, expected string, fn func() error) error {
	if s.db != nil || !validScope(q) || !story.Hash.MatchString(expected) {
		return story.ErrInput
	}
	return s.withFile(ctx, q, false, func() error {
		digest, e := s.fileHeads(q)
		if e != nil {
			return e
		}
		if digest != expected {
			return story.ErrConflict
		}
		return fn()
	})
}
func GuardHeadsTx(ctx context.Context, tx *sql.Tx, q Scope, expected string) error {
	if tx == nil || !validScope(q) || !story.Hash.MatchString(expected) {
		return story.ErrInput
	}
	if _, e := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,728219140))`, q.Space+":"+q.Series); e != nil {
		return e
	}
	digest, e := sqlHeads(ctx, tx, q)
	if e != nil {
		return e
	}
	if digest != expected {
		return story.ErrConflict
	}
	return nil
}

func sqlHeads(ctx context.Context, db headsQuery, q Scope) (string, error) {
	heads, e := queryHeads(ctx, db, q)
	if e != nil {
		return "", e
	}
	return story.Digest(heads), nil
}
func (s *Store) fileHeads(q Scope) (string, error) {
	heads, e := s.fileSummaries(q)
	if e != nil {
		return "", e
	}
	return story.Digest(heads), nil
}
func (s *Store) HeadSummaries(ctx context.Context, q Scope) ([]story.Summary, error) {
	if !validScope(q) {
		return nil, story.ErrInput
	}
	if s.db != nil {
		return queryHeads(ctx, s.db, q)
	}
	var heads []story.Summary
	var e error
	e = s.withFile(ctx, q, false, func() error { heads, e = s.fileSummaries(q); return e })
	return heads, e
}
