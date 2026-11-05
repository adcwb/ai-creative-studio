package application

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"sort"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/story"
)

type storyEpisodeOption struct {
	ID            string `json:"id"`
	SeasonID      string `json:"season_id"`
	SeasonOrdinal int64  `json:"season_ordinal"`
	Ordinal       int64  `json:"ordinal"`
	Title         string `json:"title"`
}
type storyEpisodeCursor struct{ Space, Root, Digest, After string }

func registerStoryEpisodeOptions(mux *http.ServeMux, s *FileStore) {
	mux.HandleFunc("GET /api/v1/series/{id}/story-episodes", func(w http.ResponseWriter, r *http.Request) {
		space, ok := spaceID(w, r)
		if !ok {
			return
		}
		root := r.PathValue("id")
		limit, e := storyLimit(r)
		if e != nil {
			storyError(w, e)
			return
		}
		dir, e := s.series.Directory(r.Context(), space, root)
		if e != nil {
			storyError(w, e)
			return
		}
		digest := story.Digest(dir)
		after := ""
		if raw := r.URL.Query().Get("cursor"); raw != "" {
			if len(raw) > 1024 {
				storyError(w, story.ErrInput)
				return
			}
			b, e := base64.RawURLEncoding.DecodeString(raw)
			var c storyEpisodeCursor
			if e != nil || json.Unmarshal(b, &c) != nil || c.Space != space || c.Root != root {
				storyError(w, story.ErrInput)
				return
			}
			if c.Digest != digest {
				storyError(w, story.ErrConflict)
				return
			}
			after = c.After
		}
		seasons := map[string]int64{}
		for _, n := range dir.Seasons {
			seasons[n.ID] = n.Ordinal
		}
		sort.Slice(dir.Episodes, func(i, j int) bool {
			a, b := dir.Episodes[i], dir.Episodes[j]
			if seasons[a.Parent] != seasons[b.Parent] {
				return seasons[a.Parent] < seasons[b.Parent]
			}
			if a.Ordinal != b.Ordinal {
				return a.Ordinal < b.Ordinal
			}
			return a.ID < b.ID
		})
		start := 0
		if after != "" {
			found := false
			for i, n := range dir.Episodes {
				if n.ID == after {
					start = i + 1
					found = true
					break
				}
			}
			if !found {
				storyError(w, story.ErrInput)
				return
			}
		}
		end := start + limit
		if end > len(dir.Episodes) {
			end = len(dir.Episodes)
		}
		entries, e := s.seriesEntries(dir.Episodes[start:end])
		if e != nil {
			storyError(w, e)
			return
		}
		out := []storyEpisodeOption{}
		for _, n := range entries {
			out = append(out, storyEpisodeOption{n.ID, n.Parent, seasons[n.Parent], n.Ordinal, n.Title})
		}
		next := ""
		if end < len(dir.Episodes) {
			b, _ := json.Marshal(storyEpisodeCursor{space, root, digest, dir.Episodes[end-1].ID})
			next = base64.RawURLEncoding.EncodeToString(b)
		}
		respond(w, 200, map[string]any{"items": out, "next_cursor": next})
	})
}
