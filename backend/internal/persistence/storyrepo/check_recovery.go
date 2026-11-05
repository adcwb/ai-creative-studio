package storyrepo

import (
	"context"
	"time"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/story"
)

func (s *Store) RecoverFileChecks(ctx context.Context) error {
	if s.db != nil {
		return nil
	}
	return eachCheckFile(ctx, s.dir, func(r CheckRecord) error {
		if r.Check.State == "queued" {
			q := Scope{r.Check.Space, r.Check.SeriesID}
			return s.withFile(ctx, q, true, func() error {
				current, e := s.readCheck(q, r.Check.EpisodeID, r.Check.ID)
				if e != nil {
					return e
				}
				if current.Check.State == "queued" {
					return s.ensureCheckIntent(current.Check)
				}
				return nil
			})
		}
		if r.Check.State != "running" {
			return s.removeCheckIntent(r.Check.ID)
		}
		q := Scope{r.Check.Space, r.Check.SeriesID}
		return s.withFile(ctx, q, true, func() error {
			current, e := s.readCheck(q, r.Check.EpisodeID, r.Check.ID)
			if e != nil {
				return e
			}
			if current.Check.State != "running" {
				return nil
			}
			current.Check.State = "unknown"
			current.Check.ErrorCode = "execution_outcome_unknown"
			current.Check.UpdatedAt = time.Now().UTC()
			if e = s.writeCheck(q, current, false); e != nil {
				return e
			}
			return s.removeCheckIntent(current.Check.ID)
		})
	})
}
func (s *Store) NextFileCheck(ctx context.Context) (CheckRecord, bool, error) {
	if s.db != nil {
		return CheckRecord{}, false, story.ErrDenied
	}
	return s.nextIntent(ctx)
}
