package application

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/series"
	"github.com/adcwb/ai-creative-studio/backend/internal/persistence/seriesrepo"
)

func (s *FileStore) openSeries(dir string) error {
	var err error
	var db *sql.DB
	if s.pg != nil {
		db = s.pg.db
	}
	s.series, err = seriesrepo.Open(filepath.Join(dir, "series"), db)
	if err != nil {
		return err
	}
	// File handoffs are accepted intents. Recover after core state is loaded and
	// before serving HTTP; this creates metadata only and never dispatches a model.
	s.mu.Lock()
	defer s.mu.Unlock()
	if err = s.series.RecoverPreparing(context.Background(), func(n series.Node) error { return s.ensureEpisodeProjectLocked(n) }); err != nil {
		return err
	}
	for _, p := range s.data.Projects {
		if err = s.readySeriesProject(p.SpaceID, p); err != nil {
			return err
		}
	}
	return nil
}

// ensureEpisodeProjectLocked participates in the caller's core transaction. An
// accepted replay must never replace an edited Project with its creation seed.
func (s *FileStore) ensureEpisodeProjectLocked(n series.Node) error {
	if n.Kind != "episode" || n.ProjectID != n.ID {
		return series.ErrInput
	}
	if p, ok := s.data.Projects[n.ProjectID]; ok {
		if p.SpaceID != n.Space || p.SeriesEpisodeID != n.ID || p.Kind != "drama" {
			return series.ErrConflict
		}
		if s.pg != nil {
			return s.commit(s.clone())
		}
		return nil
	}
	d := s.clone()
	d.Projects[n.ProjectID] = Project{ID: n.ProjectID, SeriesEpisodeID: n.ID, Kind: "drama", SpaceID: n.Space, Title: n.Title, Brief: n.Summary, Version: 1, CreatedAt: n.CreatedAt, UpdatedAt: n.CreatedAt}
	return s.commit(d)
}

// CreateSeriesEpisode is deliberately not registered as an HTTP route until
// local episode numbering, output snapshots and the C-end directory are ready.
// PostgreSQL publishes Project/node/counter/receipt in one core transaction;
// files publish the node only after its stable Project is durable.
func (s *FileStore) CreateSeriesEpisode(ctx context.Context, a series.Actor, key, parent string, in series.Input) (series.Node, Project, error) {
	if s.series == nil {
		return series.Node{}, Project{}, seriesrepo.ErrStorage
	}
	c := series.Command{Operation: "create", Kind: "episode", ID: series.NewID(a, "create", key), Parent: parent, Input: in}
	s.mu.Lock()
	defer s.mu.Unlock()
	var n series.Node
	var err error
	if s.pg != nil {
		if s.pg.err != nil {
			return n, Project{}, s.pg.err
		}
		n, err = s.series.MutateTx(ctx, s.pg.tx, a, key, c)
		if err == nil {
			err = s.ensureEpisodeProjectLocked(n)
		}
	} else {
		n, err = s.series.Mutate(ctx, a, key, c, s.ensureEpisodeProjectLocked)
	}
	if err != nil {
		return series.Node{}, Project{}, err
	}
	p, ok := s.data.Projects[n.ProjectID]
	if !ok || p.SpaceID != a.Space || p.SeriesEpisodeID != n.ID {
		return series.Node{}, Project{}, seriesrepo.ErrStorage
	}
	return n, s.projectViewLocked(p), nil
}

func (s *FileStore) readySeriesProject(space string, p Project) error {
	if p.SeriesEpisodeID == "" {
		return nil
	}
	if s.series == nil {
		return seriesrepo.ErrStorage
	}
	n, err := s.series.Get(context.Background(), space, "episode", p.SeriesEpisodeID)
	if errors.Is(err, series.ErrNotFound) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if n.ProjectID != p.ID {
		return seriesrepo.ErrStorage
	}
	return nil
}
