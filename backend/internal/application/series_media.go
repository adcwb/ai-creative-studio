package application

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/media"
	"github.com/adcwb/ai-creative-studio/backend/internal/modules/series"
)

type initialMediaProfile struct{ Resolution, Aspect string }

func (s *FileStore) configureSeriesMedia() {
	s.media.seriesProfile = func(space, project string) (initialMediaProfile, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		n, err := s.series.ByProject(ctx, space, project)
		if errors.Is(err, series.ErrNotFound) {
			return initialMediaProfile{}, nil
		}
		if err != nil {
			return initialMediaProfile{}, err
		}
		return initialMediaProfile{n.Profile.Resolution, n.Profile.Aspect}, nil
	}
	s.media.seriesSnapshot = func(space, project string) (*media.SeriesEpisodeSnapshot, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		c, err := s.series.Context(ctx, space, project)
		if errors.Is(err, series.ErrNotFound) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		return &media.SeriesEpisodeSnapshot{SeriesID: c.Series.ID, SeasonID: c.Season.ID, EpisodeID: c.Episode.ID, SeasonOrdinal: c.Season.Ordinal, Ordinal: c.Episode.Ordinal, ProjectLocalEpisode: 1}, nil
	}
}

// Defaults are applied only to newly persisted media, never an existing cut.
// A lookup error belongs to this operation and must not permanently poison the
// media store after a transient directory outage.
func (m *MediaStore) initializeSeriesProfile(k string, p *MediaState) error {
	if m.seriesProfile == nil {
		return nil
	}
	space, project, ok := strings.Cut(k, ":")
	if !ok {
		return nil
	}
	profile, err := m.seriesProfile(space, project)
	if err != nil {
		return err
	}
	if p.Timeline.OutputResolution == "" {
		p.Timeline.OutputResolution = profile.Resolution
	}
	if p.Timeline.AspectRatio == "" {
		p.Timeline.AspectRatio = profile.Aspect
	}
	return nil
}
func (m *MediaStore) GetChecked(project, space string) (MediaState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failure != nil {
		return MediaState{}, m.failure
	}
	if m.pg != nil && m.pg.err != nil {
		return MediaState{}, m.pg.err
	}
	p := mediaState(m.clone(), mediaKey(space, project))
	if _, exists := m.data.Projects[mediaKey(space, project)]; !exists {
		if err := m.initializeSeriesProfile(mediaKey(space, project), &p); err != nil {
			return MediaState{}, err
		}
	}
	for i := range p.Exports {
		if p.Exports[i].RenderVersion == 1 && p.Exports[i].Status == "running" {
			p.Exports[i].RenderProgress = m.renderProgress(space, project, p.Exports[i].ID)
		}
	}
	return p, nil
}
func (m *MediaStore) requireLocalEpisode(space, project string, episode int) error {
	if episode <= 1 || m.seriesSnapshot == nil {
		return nil
	}
	snapshot, err := m.seriesSnapshot(space, project)
	if err != nil {
		return err
	}
	if snapshot != nil {
		return mediaInvalid("系列每集使用独立项目，请从系列目录打开其他剧集")
	}
	return nil
}
