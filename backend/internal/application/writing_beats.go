package application

import (
	"errors"
	"net/http"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/story"
	"github.com/adcwb/ai-creative-studio/backend/internal/persistence/storyrepo"
)

type writingBeatsView struct {
	ID        string         `json:"id"`
	SeriesID  string         `json:"series_id"`
	SeasonID  string         `json:"season_id"`
	EpisodeID string         `json:"episode_id,omitempty"`
	Current   *story.Version `json:"current,omitempty"`
}

func registerWritingBeatsRoutes(mux *http.ServeMux, s *FileStore) {
	base := "/api/v1/seasons/{id}/writing-beats"
	handler := func(w http.ResponseWriter, r *http.Request) {
		var space, key string
		var ok bool
		if r.Method == http.MethodPut {
			space, key, ok = projectWriteScope(w, r)
		} else {
			space, ok = spaceID(w, r)
		}
		if !ok {
			return
		}
		season, e := s.series.Get(r.Context(), space, "season", r.PathValue("id"))
		if e != nil {
			seriesError(w, e)
			return
		}
		ep := r.URL.Query().Get("episode_id")
		if ep != "" {
			node, e := s.series.Get(r.Context(), space, "episode", ep)
			if e != nil {
				seriesError(w, e)
				return
			}
			if node.Parent != season.ID {
				storyError(w, story.ErrInput)
				return
			}
		}
		id := story.OutlineID(season.SeriesID, season.ID, ep)
		if r.Method == http.MethodPut {
			var in story.Input
			if !decodeProject(w, r, &in) {
				return
			}
			if in.Kind != "outline" || in.Body.Outline == nil || in.Body.Outline.SeasonID != season.ID || in.Body.Outline.EpisodeID != ep {
				storyError(w, story.ErrInput)
				return
			}
			a := seriesActorFor(r, space)
			v, e := s.SaveStory(r.Context(), story.Actor{Space: space, User: a.User, Writable: a.Writable}, season.SeriesID, id, key, in)
			if e != nil {
				storyError(w, e)
				return
			}
			respond(w, 200, v)
			return
		}
		out := writingBeatsView{ID: id, SeriesID: season.SeriesID, SeasonID: season.ID, EpisodeID: ep}
		v, e := s.StoryVersion(r.Context(), storyrepo.Scope{Space: space, Series: season.SeriesID}, id, "")
		if e != nil && !errors.Is(e, story.ErrNotFound) {
			storyError(w, e)
			return
		}
		if e == nil {
			out.Current = &v
		}
		respond(w, 200, out)
	}
	mux.HandleFunc("GET "+base, handler)
	mux.HandleFunc("PUT "+base, handler)
}
