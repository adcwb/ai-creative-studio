package storyrepo

import (
	"context"
	"errors"
	"path/filepath"
	"strings"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/story"
)

// Replay is read-only. Callers verify current membership/root before consulting
// it, then skip mutable content preconditions only for this exact old request.
func (s *Store) Replay(ctx context.Context, a story.Actor, root, id, key string, in story.Input) (story.Version, bool, error) {
	q := Scope{a.Space, root}
	if !a.Writable {
		return story.Version{}, false, story.ErrDenied
	}
	if !validScope(q) || !story.ID.MatchString(id) || !story.Scope.MatchString(a.User) {
		return story.Version{}, false, story.ErrInput
	}
	hash := story.TextHash(a.User + ":" + key)
	var found *record
	if s.db != nil {
		r, e := scan(s.db.QueryRowContext(ctx, `SELECT payload FROM studio_story_versions WHERE space_id=$1 AND series_id=$2 AND id=$3 AND key_hash=$4`, q.Space, q.Series, id, hash), q, id)
		if e == nil {
			found = &r
		} else if !errors.Is(e, story.ErrNotFound) {
			return story.Version{}, false, e
		}
	} else {
		e := s.withFile(ctx, q, false, func() error {
			list, e := s.entries(q, id)
			if e != nil {
				return e
			}
			for _, n := range list {
				if strings.HasSuffix(n, "-"+hash+".json") {
					r, e := read(filepath.Join(s.path(q, id), n))
					if e != nil || r.Version.Space != q.Space || r.Version.SeriesID != q.Series || r.Version.ID != id {
						return ErrStorage
					}
					found = &r
					break
				}
			}
			return nil
		})
		if e != nil {
			return story.Version{}, false, e
		}
	}
	if found == nil {
		return story.Version{}, false, nil
	}
	if found.Digest != story.Digest(in) {
		return story.Version{}, false, ErrKey
	}
	return found.Version, true, nil
}
