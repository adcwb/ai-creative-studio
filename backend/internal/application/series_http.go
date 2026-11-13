package application

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/series"
	"github.com/adcwb/ai-creative-studio/backend/internal/persistence/seriesrepo"
)

type seriesEntry struct {
	series.Node
	ProjectVersion int                `json:"project_version,omitempty"`
	Production     *episodeProduction `json:"production,omitempty"`
}

func (s *FileStore) seriesEntries(nodes []series.Node) ([]seriesEntry, error) {
	out := make([]seriesEntry, 0, len(nodes))
	episodes := false
	for _, n := range nodes {
		if n.Kind == "episode" {
			episodes = true
		}
	}
	if episodes {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.pg != nil && s.pg.err != nil {
			return nil, s.pg.err
		}
	}
	for _, n := range nodes {
		item := seriesEntry{Node: n}
		if n.Kind == "episode" {
			p, ok := s.data.Projects[n.ProjectID]
			if !ok || p.SpaceID != n.Space || p.SeriesEpisodeID != n.ID {
				return nil, seriesrepo.ErrStorage
			}
			item.Title, item.Summary, item.ProjectVersion = p.Title, p.Brief, p.Version
		}
		out = append(out, item)
	}
	return out, nil
}
func seriesActorFor(r *http.Request, space string) series.Actor {
	a := promptActor(r, space)
	return series.Actor{Space: a.Space, User: a.User, Writable: a.Writable}
}
func seriesError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, series.ErrInput):
		problem(w, 400, "invalid_series", "系列名称、简介、输出设置或编号无效，请检查后重试")
	case errors.Is(err, series.ErrNotFound), errors.Is(err, ErrNotFound):
		problem(w, 404, "not_found", "系列、季或剧集不存在")
	case errors.Is(err, series.ErrDenied):
		problem(w, 403, "role_denied", "当前权限不能修改此系列")
	case errors.Is(err, series.ErrConflict):
		problem(w, 409, "series_version_conflict", "目录已更新或已归档，请保留草稿并重新读取")
	case errors.Is(err, seriesrepo.ErrKey):
		problem(w, 409, "idempotency_conflict", "同一请求键不能用于不同内容")
	default:
		problem(w, 503, "series_storage_error", "保存结果未确认，请保留草稿并使用原请求重试")
	}
}

type seriesCursor struct {
	Space, Kind, Parent, Status, After string
	ParentVersion                      int64
}

func registerSeriesRoutes(mux *http.ServeMux, s *FileStore) {
	if s.series == nil {
		return
	}
	registerSeasonDeliveryRoutes(mux, s)
	list := func(kind, parentParam, parentKind string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			space, ok := spaceID(w, r)
			if !ok {
				return
			}
			q := seriesrepo.Query{Kind: kind, Parent: r.PathValue(parentParam), Status: r.URL.Query().Get("status"), Limit: 20}
			if kind == "episode" && q.Status != "" {
				valid := false
				for _, v := range []string{"planned", "writing", "storyboard", "production", "review", "released"} {
					if q.Status == v {
						valid = true
					}
				}
				if !valid {
					seriesError(w, series.ErrInput)
					return
				}
			}
			if q.Status != "" && q.Status != "active" && q.Status != "archived" && kind != "episode" {
				seriesError(w, series.ErrInput)
				return
			}
			var parentVersion int64
			if parentKind != "" {
				n, err := s.series.Get(r.Context(), space, parentKind, q.Parent)
				if err != nil {
					seriesError(w, err)
					return
				}
				parentVersion = n.Version
			}
			if raw := r.URL.Query().Get("limit"); raw != "" {
				v, err := strconv.Atoi(raw)
				if err != nil {
					seriesError(w, series.ErrInput)
					return
				}
				q.Limit = v
			}
			if raw := r.URL.Query().Get("cursor"); raw != "" {
				if len(raw) > 1024 {
					seriesError(w, series.ErrInput)
					return
				}
				b, err := base64.RawURLEncoding.DecodeString(raw)
				var c seriesCursor
				if err != nil || json.Unmarshal(b, &c) != nil || c.Space != space || c.Kind != kind || c.Parent != q.Parent || c.Status != q.Status {
					seriesError(w, series.ErrInput)
					return
				}
				if c.ParentVersion != parentVersion {
					seriesError(w, series.ErrConflict)
					return
				}
				q.After = c.After
			}
			page, err := s.series.List(r.Context(), space, q)
			if err != nil {
				seriesError(w, err)
				return
			}
			if parentKind != "" {
				n, err := s.series.Get(r.Context(), space, parentKind, q.Parent)
				if err != nil {
					seriesError(w, err)
					return
				}
				if n.Version != parentVersion {
					seriesError(w, series.ErrConflict)
					return
				}
			}
			items, err := s.seriesEntries(page.Items)
			if err != nil {
				seriesError(w, err)
				return
			}
			if kind == "episode" && r.URL.Query().Get("include_production") == "true" {
				for i := range items {
					progress, err := s.episodeProduction(space, items[i].ProjectID)
					if err != nil {
						seriesError(w, err)
						return
					}
					items[i].Production = &progress
				}
			}
			next := ""
			if page.Next != "" {
				b, _ := json.Marshal(seriesCursor{space, kind, q.Parent, q.Status, page.Next, parentVersion})
				next = base64.RawURLEncoding.EncodeToString(b)
			}
			respond(w, 200, map[string]any{"items": items, "next_cursor": next})
		}
	}
	get := func(kind string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			space, ok := spaceID(w, r)
			if !ok {
				return
			}
			if kind == "episode" {
				c, err := s.series.Context(r.Context(), space, r.PathValue("id"))
				if err != nil {
					seriesError(w, err)
					return
				}
				entries, err := s.seriesEntries([]series.Node{c.Episode})
				if err != nil {
					seriesError(w, err)
					return
				}
				respond(w, 200, map[string]any{"episode": entries[0], "season": c.Season, "series": c.Series})
				return
			}
			n, err := s.series.Get(r.Context(), space, kind, r.PathValue("id"))
			if err != nil {
				seriesError(w, err)
				return
			}
			respond(w, 200, n)
		}
	}
	create := func(kind, parentParam string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			space, key, ok := projectWriteScope(w, r)
			if !ok {
				return
			}
			var in series.Input
			if !decodeProject(w, r, &in) {
				return
			}
			a := seriesActorFor(r, space)
			var n series.Node
			var err error
			if kind == "episode" {
				n, _, err = s.CreateSeriesEpisode(r.Context(), a, key, r.PathValue(parentParam), in)
			} else {
				c := series.Command{Operation: "create", Kind: kind, ID: series.NewID(a, "create", key), Parent: r.PathValue(parentParam), Input: in}
				n, err = s.series.Mutate(r.Context(), a, key, c, nil)
			}
			if err != nil {
				seriesError(w, err)
				return
			}
			items, err := s.seriesEntries([]series.Node{n})
			if err != nil {
				seriesError(w, err)
				return
			}
			respond(w, 201, items[0])
		}
	}
	update := func(kind string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			space, key, ok := projectWriteScope(w, r)
			if !ok {
				return
			}
			var body struct {
				Title    string         `json:"title"`
				Summary  string         `json:"summary"`
				Audience string         `json:"audience,omitempty"`
				Format   string         `json:"format,omitempty"`
				Profile  series.Profile `json:"profile"`
				Expected int64          `json:"expected_version"`
				Status   string         `json:"status"`
			}
			if kind == "episode" {
				var in struct {
					Profile  series.Profile `json:"profile"`
					Expected int64          `json:"expected_version"`
					Status   string         `json:"status"`
				}
				if !decodeProject(w, r, &in) {
					return
				}
				body.Profile, body.Expected, body.Status = in.Profile, in.Expected, in.Status
			} else if kind == "season" {
				var in struct {
					Title    string         `json:"title"`
					Summary  string         `json:"summary"`
					Profile  series.Profile `json:"profile"`
					Expected int64          `json:"expected_version"`
					Status   string         `json:"status"`
				}
				if !decodeProject(w, r, &in) {
					return
				}
				body.Title, body.Summary, body.Profile, body.Expected, body.Status = in.Title, in.Summary, in.Profile, in.Expected, in.Status
			} else if !decodeProject(w, r, &body) {
				return
			}
			c := series.Command{Operation: "update", Kind: kind, ID: r.PathValue("id"), Expected: body.Expected, Status: body.Status, Input: series.Input{Title: body.Title, Summary: body.Summary, Audience: body.Audience, Format: body.Format, Profile: body.Profile}}
			n, err := s.series.Mutate(r.Context(), seriesActorFor(r, space), key, c, nil)
			if err != nil {
				seriesError(w, err)
				return
			}
			items, err := s.seriesEntries([]series.Node{n})
			if err != nil {
				seriesError(w, err)
				return
			}
			respond(w, 200, items[0])
		}
	}
	move := func(kind, parentKind string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			space, key, ok := projectWriteScope(w, r)
			if !ok {
				return
			}
			var in struct {
				ID             string `json:"id"`
				Ordinal        int64  `json:"ordinal"`
				Expected       int64  `json:"expected_version"`
				ExpectedParent int64  `json:"expected_parent_version"`
				Mode           string `json:"mode,omitempty"`
			}
			if !decodeProject(w, r, &in) {
				return
			}
			n, err := s.series.Get(r.Context(), space, kind, in.ID)
			if err != nil {
				seriesError(w, err)
				return
			}
			if n.Parent != r.PathValue("id") {
				seriesError(w, series.ErrNotFound)
				return
			}
			c := series.Command{Operation: "move", Kind: kind, ID: in.ID, Ordinal: in.Ordinal, Expected: in.Expected, ExpectedParent: in.ExpectedParent, Mode: in.Mode}
			n, err = s.series.Mutate(r.Context(), seriesActorFor(r, space), key, c, nil)
			if err != nil {
				seriesError(w, err)
				return
			}
			items, err := s.seriesEntries([]series.Node{n})
			if err != nil {
				seriesError(w, err)
				return
			}
			respond(w, 200, items[0])
		}
	}
	mux.HandleFunc("GET /api/v1/series", list("series", "", ""))
	mux.HandleFunc("POST /api/v1/series", create("series", ""))
	mux.HandleFunc("GET /api/v1/series/{id}", get("series"))
	mux.HandleFunc("PATCH /api/v1/series/{id}", update("series"))
	mux.HandleFunc("GET /api/v1/series/{id}/seasons", list("season", "id", "series"))
	mux.HandleFunc("POST /api/v1/series/{id}/seasons", create("season", "id"))
	mux.HandleFunc("POST /api/v1/series/{id}/season-order", move("season", "series"))
	mux.HandleFunc("GET /api/v1/seasons/{id}", get("season"))
	mux.HandleFunc("PATCH /api/v1/seasons/{id}", update("season"))
	mux.HandleFunc("GET /api/v1/seasons/{id}/episodes", list("episode", "id", "season"))
	mux.HandleFunc("POST /api/v1/seasons/{id}/episodes", create("episode", "id"))
	mux.HandleFunc("POST /api/v1/seasons/{id}/episode-order", move("episode", "season"))
	mux.HandleFunc("GET /api/v1/episodes/{id}", get("episode"))
	mux.HandleFunc("PATCH /api/v1/episodes/{id}", update("episode"))
}
