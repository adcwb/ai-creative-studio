package seriesrepo

import (
	"context"
	"database/sql"
	"errors"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/series"
)

type EpisodeContext struct {
	Episode series.Node `json:"episode"`
	Season  series.Node `json:"season"`
	Series  series.Node `json:"series"`
}

// Context reads all three directory versions from one consistent snapshot.
func (s *Store) Context(ctx context.Context, space, id string) (EpisodeContext, error) {
	if !series.ScopeID.MatchString(space) || !series.ID.MatchString(id) {
		return EpisodeContext{}, series.ErrNotFound
	}
	var out EpisodeContext
	read := func(r reader) error {
		var err error
		out.Episode, err = r.get("episode", id)
		if err != nil {
			return err
		}
		if out.Episode.State != "ready" {
			return series.ErrNotFound
		}
		out.Season, err = r.get("season", out.Episode.Parent)
		if errors.Is(err, series.ErrNotFound) {
			return ErrStorage
		}
		if err != nil {
			return err
		}
		out.Series, err = r.get("series", out.Episode.SeriesID)
		if errors.Is(err, series.ErrNotFound) {
			return ErrStorage
		}
		if err != nil {
			return err
		}
		if out.Season.SeriesID != out.Series.ID {
			return ErrStorage
		}
		return nil
	}
	if s.db != nil {
		tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
		if err != nil {
			return out, ErrStorage
		}
		defer tx.Rollback()
		if err = read(sqlAccess{ctx, space, tx}); err != nil {
			return EpisodeContext{}, err
		}
		if err = tx.Commit(); err != nil {
			return EpisodeContext{}, ErrStorage
		}
		return out, nil
	}
	err := s.withFile(ctx, space, func(f *fileAccess) error { return read(f) })
	return out, err
}
