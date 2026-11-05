package storyrepo

import (
	"context"
	"errors"
	"sort"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/story"
)

// Visit holds one series head view and decodes one body at a time. Callbacks
// must not reenter this repository or perform network I/O.
func (s *Store) Visit(ctx context.Context, q Scope, fn func(story.Version) error) error {
	if !validScope(q) {
		return story.ErrInput
	}
	if s.db != nil {
		rows, e := s.db.QueryContext(ctx, headSelect+` ORDER BY CASE WHEN h.kind='bible' THEN 0 WHEN h.required THEN 1 ELSE 2 END,v.id`, q.Space, q.Series)
		if e != nil {
			return ErrStorage
		}
		defer rows.Close()
		for rows.Next() {
			r, e := scan(rows, q, "")
			if e != nil {
				return e
			}
			if e = fn(r.Version); e != nil {
				return e
			}
		}
		if rows.Err() != nil {
			return ErrStorage
		}
		return nil
	}
	return s.withFile(ctx, q, false, func() error {
		ids, e := s.ids(q)
		if e != nil {
			return e
		}
		heads := []story.Summary{}
		for _, id := range ids {
			if e = ctx.Err(); e != nil {
				return e
			}
			v, e := s.fileCurrent(q, id)
			if errors.Is(e, story.ErrNotFound) {
				continue
			}
			if e != nil {
				return e
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
		for _, h := range heads {
			if e = ctx.Err(); e != nil {
				return e
			}
			v, e := s.fileCurrent(q, h.ID)
			if e != nil {
				return e
			}
			if e = fn(v); e != nil {
				return e
			}
		}
		return nil
	})
}
