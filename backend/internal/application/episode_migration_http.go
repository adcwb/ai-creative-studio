package application

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/projects"
	"github.com/adcwb/ai-creative-studio/backend/internal/modules/series"
	m "github.com/adcwb/ai-creative-studio/backend/internal/modules/seriesmigration"
	"github.com/adcwb/ai-creative-studio/backend/internal/persistence/migrationrepo"
)

func episodeMigrationError(w http.ResponseWriter, e error) {
	switch {
	case errors.Is(e, m.ErrInput), errors.Is(e, series.ErrInput), errors.Is(e, projects.ErrInvalidProjectInput):
		problem(w, 400, "invalid_episode_migration", "迁移内容或归属选择无效，请检查预览；源项目未被改动")
	case errors.Is(e, m.ErrConflict), errors.Is(e, series.ErrConflict):
		problem(w, 409, "migration_changed", "源项目、目标目录或原请求已变化，请核对迁移记录后重新预览")
	case errors.Is(e, m.ErrNotFound), errors.Is(e, series.ErrNotFound), errors.Is(e, ErrNotFound):
		problem(w, 404, "not_found", "迁移记录或项目不存在")
	case errors.Is(e, m.ErrDenied), errors.Is(e, errRuntimeDenied), errors.Is(e, series.ErrDenied):
		problem(w, 403, "role_denied", "当前权限不能迁移此项目，请重新核对空间与成员权限")
	case errors.Is(e, ErrGenerationActive):
		storeError(w, e)
	default:
		problem(w, 503, "migration_storage_error", "迁移结果尚未确认。原项目保留，请先读取原迁移记录，再继续同一任务")
	}
}

type migrationSourceSummary struct {
	ProjectVersion    int                        `json:"project_version"`
	ScriptVersion     int                        `json:"script_version"`
	MediaVersion      int                        `json:"media_version"`
	Episodes          []int                      `json:"episodes"`
	Characters        []projects.ScriptCharacter `json:"characters"`
	Shots             []migrationShotSummary     `json:"unassigned_shots"`
	HistoricalExports int                        `json:"historical_exports"`
}
type migrationShotSummary struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

type migrationJobView struct {
	Format    int                `json:"format"`
	ID        string             `json:"id"`
	Space     string             `json:"space_id"`
	Actor     string             `json:"created_by"`
	Project   string             `json:"source_project_id"`
	Target    string             `json:"target_project_id"`
	Version   int64              `json:"row_version"`
	State     string             `json:"state"`
	Phase     string             `json:"phase"`
	Error     string             `json:"error,omitempty"`
	Episode   int                `json:"source_episode_number"`
	Ordinal   int64              `json:"target_ordinal"`
	Copied    int                `json:"copied_assets"`
	Total     int                `json:"total_assets"`
	Reserved  bool               `json:"reserved"`
	Plan      *m.Plan            `json:"plan,omitempty"`
	Copies    map[string]m.Proof `json:"copies,omitempty"`
	CreatedAt time.Time          `json:"created_at"`
	UpdatedAt time.Time          `json:"updated_at"`
}

func migrationView(j m.Job, detail bool) migrationJobView {
	v := migrationJobView{Format: j.Format, ID: j.ID, Space: j.Space, Actor: j.Actor, Project: j.ProjectID, Target: j.TargetID, Version: j.Version, State: j.State, Phase: j.Phase, Error: j.Error, Episode: j.Plan.Source.Episode, Ordinal: j.Plan.Input.Ordinal, Copied: len(j.Copies), Total: len(j.Plan.Assets), Reserved: j.Reserved, CreatedAt: j.CreatedAt, UpdatedAt: j.UpdatedAt}
	if detail {
		v.Plan = &j.Plan
		v.Copies = j.Copies
	}
	return v
}

func summarizeMigrationSource(src m.Source) (migrationSourceSummary, error) {
	out := migrationSourceSummary{ProjectVersion: src.Project.Version, ScriptVersion: src.Project.ScriptVersion, MediaVersion: src.Media.Version, Episodes: m.Episodes(src), Characters: []projects.ScriptCharacter{}, Shots: []migrationShotSummary{}, HistoricalExports: len(src.Media.Exports)}
	ids := map[string]bool{}
	var currentIDs []string
	if src.Project.Script != nil {
		v, _, e := projects.CompileStructure(*src.Project.Script, src.Project.SpaceID+":"+src.Project.ID, src.Project.ScriptVersion)
		if e != nil {
			return out, e
		}
		out.Characters = v.Characters
		currentIDs = projects.SceneIDs(v)
		for _, id := range currentIDs {
			ids[id] = true
		}
	}
	if b := src.Project.Storyboard; b != nil {
		sourceIDs := b.SourceSceneIDs
		if b.StructureVersion != 2 {
			if b.SourceScriptVersion == src.Project.ScriptVersion {
				sourceIDs = currentIDs
			} else if src.BoardSource != nil {
				v, _, e := projects.CompileStructure(*src.BoardSource, src.Project.SpaceID+":"+src.Project.ID, b.SourceScriptVersion)
				if e != nil {
					return out, e
				}
				sourceIDs = projects.SceneIDs(v)
			}
		}
		for _, shot := range b.Shots {
			scene := ""
			if shot.SceneIndex > 0 && shot.SceneIndex <= len(sourceIDs) {
				scene = sourceIDs[shot.SceneIndex-1]
			}
			if scene == "" || !ids[scene] {
				label := []rune(shot.Description)
				if len(label) > 120 {
					label = append(label[:120], '…')
				}
				out.Shots = append(out.Shots, migrationShotSummary{shot.ID, string(label)})
			}
		}
	}
	return out, nil
}
func registerEpisodeMigrationRoutes(mux *http.ServeMux, s *FileStore) {
	// Bound local copy/probe work by waiting, never by rejecting a user's
	// episode/asset total or assigning a per-project quota.
	slots := make(chan struct{}, 2)
	scope := func(w http.ResponseWriter, r *http.Request, write bool) (string, string, bool) {
		var space, key string
		var ok bool
		if write {
			space, key, ok = projectWriteScope(w, r)
		} else {
			space, ok = spaceID(w, r)
		}
		if !ok {
			return "", "", false
		}
		if _, e := s.GetProject(space, r.PathValue("id")); e != nil {
			storeError(w, e)
			return "", "", false
		}
		return space, key, true
	}
	base := "/api/v1/projects/{id}/episode-migrations"
	mux.HandleFunc("GET /api/v1/projects/{id}/migration-source", func(w http.ResponseWriter, r *http.Request) {
		space, _, ok := scope(w, r, false)
		if !ok {
			return
		}
		src, e := s.MigrationSource(space, r.PathValue("id"))
		if e != nil {
			episodeMigrationError(w, e)
			return
		}
		out, e := summarizeMigrationSource(src)
		if e != nil {
			episodeMigrationError(w, e)
			return
		}
		respond(w, 200, out)
	})
	mux.HandleFunc("POST /api/v1/projects/{id}/episode-migration-preview", func(w http.ResponseWriter, r *http.Request) {
		space, _, ok := scope(w, r, true)
		if !ok {
			return
		}
		var in m.Input
		if !decodeProject(w, r, &in) {
			return
		}
		p, e := s.PreviewEpisodeMigration(r.Context(), space, r.PathValue("id"), in)
		if e != nil {
			episodeMigrationError(w, e)
			return
		}
		respond(w, 200, p)
	})
	mux.HandleFunc("POST "+base, func(w http.ResponseWriter, r *http.Request) {
		space, key, ok := scope(w, r, true)
		if !ok {
			return
		}
		var body struct {
			Input  m.Input `json:"input"`
			Digest string  `json:"preview_digest"`
		}
		if !decodeProject(w, r, &body) {
			return
		}
		j, e := s.CreateEpisodeMigration(r.Context(), seriesActorFor(r, space), r.PathValue("id"), key, body.Input, body.Digest)
		if e != nil {
			episodeMigrationError(w, e)
			return
		}
		respond(w, 201, migrationView(j, true))
	})
	mux.HandleFunc("GET "+base, func(w http.ResponseWriter, r *http.Request) {
		space, _, ok := scope(w, r, false)
		if !ok {
			return
		}
		limit := 20
		if v := r.URL.Query().Get("limit"); v != "" {
			n, e := strconv.Atoi(v)
			if e != nil {
				episodeMigrationError(w, m.ErrInput)
				return
			}
			limit = n
		}
		items, next, e := s.migrations.List(r.Context(), space, r.PathValue("id"), r.URL.Query().Get("cursor"), limit)
		if e != nil {
			episodeMigrationError(w, e)
			return
		}
		views := make([]migrationJobView, 0, len(items))
		for _, j := range items {
			views = append(views, migrationView(j, false))
		}
		respond(w, 200, map[string]any{"items": views, "next_cursor": next})
	})
	mux.HandleFunc("GET "+base+"/{migration_id}", func(w http.ResponseWriter, r *http.Request) {
		space, _, ok := scope(w, r, false)
		if !ok {
			return
		}
		j, e := s.migrations.Get(r.Context(), migrationrepo.Identity{Space: space, Project: r.PathValue("id"), ID: r.PathValue("migration_id")})
		if e != nil {
			episodeMigrationError(w, e)
			return
		}
		respond(w, 200, migrationView(j, true))
	})
	step := func(cancel bool) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			space, _, ok := scope(w, r, true)
			if !ok {
				return
			}
			var body struct {
				Version int64 `json:"expected_version"`
			}
			if !decodeProject(w, r, &body) {
				return
			}
			if !cancel {
				select {
				case slots <- struct{}{}:
					defer func() { <-slots }()
				case <-r.Context().Done():
					return
				}
			}
			var authorize func() bool
			if auth, ok := r.Context().Value(authScopeKey{}).(authScope); ok {
				authorize = auth.recheckWrite
			}
			j, e := s.ContinueEpisodeMigration(r.Context(), seriesActorFor(r, space), migrationrepo.Identity{Space: space, Project: r.PathValue("id"), ID: r.PathValue("migration_id")}, body.Version, cancel, authorize)
			if e != nil {
				episodeMigrationError(w, e)
				return
			}
			respond(w, 200, migrationView(j, true))
		}
	}
	mux.HandleFunc("POST "+base+"/{migration_id}/continue", step(false))
	mux.HandleFunc("POST "+base+"/{migration_id}/cancel", step(true))
}
