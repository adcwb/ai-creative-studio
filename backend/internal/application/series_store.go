package application

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/series"
	"github.com/adcwb/ai-creative-studio/backend/internal/persistence/migrationrepo"
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
	s.migrations, err = migrationrepo.Open(filepath.Join(dir, "episode-migrations"), db)
	if err != nil {
		return err
	}
	// File handoffs are accepted intents. Recover after core state is loaded and
	// before serving HTTP; this creates metadata only and never dispatches a model.
	s.mu.Lock()
	defer s.mu.Unlock()
	// Check the loaded core before any preparing callback can commit a snapshot.
	for _, p := range s.data.Projects {
		if p.StructureVersion != 0 && p.StructureVersion != 2 {
			return projectInputError("项目结构版本不受支持，请使用兼容版本的编辑器")
		}
	}
	if err = s.series.RecoverPreparing(context.Background(), func(n series.Node) error { return s.ensureEpisodeProjectLocked(n) }); err != nil {
		return err
	}
	s.configureSeriesMedia()
	for _, p := range s.data.Projects {
		if err = s.readySeriesProject(p.SpaceID, p); err != nil {
			if errors.Is(err, ErrNotFound) && s.validPendingMigration(context.Background(), p) {
				continue
			}
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
	d.Projects[n.ProjectID] = Project{ID: n.ProjectID, StructureVersion: 2, SeriesEpisodeID: n.ID, Kind: "drama", SpaceID: n.Space, Title: n.Title, Brief: n.Summary, Version: 1, CreatedAt: n.CreatedAt, UpdatedAt: n.CreatedAt}
	return s.commit(d)
}

// CreateSeriesEpisode persists one independently managed episode and Project.
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
	if p.StructureVersion != 0 && p.StructureVersion != 2 {
		return projectInputError("项目结构版本不受支持，请使用兼容版本的编辑器")
	}
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

func singleEpisodeScript(script *Script) error {
	for i := range script.Scenes {
		if script.Scenes[i].EpisodeNumber > 1 {
			return projectInputError("系列每集使用独立项目，请从系列目录打开其他剧集")
		}
		script.Scenes[i].EpisodeNumber = 1
	}
	return nil
}
func generationProjectInput(g ScriptGeneration) ProjectInput {
	return ProjectInput{Title: g.SourceTitle, Brief: g.SourceBrief, SingleEpisode: g.SourceEpisodeID != "", TargetDurationSeconds: g.TargetDurationSeconds}
}
