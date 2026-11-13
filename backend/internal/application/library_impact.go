package application

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/series"
	"github.com/adcwb/ai-creative-studio/backend/internal/persistence/libraryrepo"
)

type libraryImpact struct {
	ProjectID     string   `json:"project_id"`
	Title         string   `json:"title"`
	EpisodeID     string   `json:"episode_id,omitempty"`
	SeriesID      string   `json:"series_id,omitempty"`
	SeasonID      string   `json:"season_id,omitempty"`
	EpisodeNumber int64    `json:"episode_number,omitempty"`
	MediaVersion  int      `json:"media_version"`
	Bindings      int      `json:"binding_count"`
	Media         int      `json:"media_count"`
	Versions      []string `json:"pinned_version_ids"`
	Outdated      bool     `json:"outdated"`
}

func impactFromMedia(project, asset, head string, state MediaState) libraryImpact {
	out := libraryImpact{ProjectID: project, MediaVersion: state.Version, Versions: []string{}}
	versions := map[string]bool{}
	for _, b := range state.LibraryBindings {
		if b.AssetID == asset {
			out.Bindings++
			versions[b.VersionID] = true
		}
	}
	for _, a := range state.Assets {
		matched := false
		for _, source := range a.LibrarySources {
			if source.AssetID == asset {
				versions[source.VersionID] = true
				matched = true
			}
		}
		if matched {
			out.Media++
		}
	}
	for v := range versions {
		out.Versions = append(out.Versions, v)
		out.Outdated = out.Outdated || v != head
	}
	sort.Strings(out.Versions)
	return out
}

// This is a read-only view of recorded bindings and persistent media ancestry,
// not a complete dependency graph or a permission to replace/recompute work.
// Old media without trusted lineage is deliberately not guessed into the view.
func (s *FileStore) libraryImpactPage(ctx context.Context, space, asset, head, after string, limit int) ([]libraryImpact, error) {
	rows := []libraryImpact{}
	if s.pg == nil {
		if err := s.media.Available(); err != nil {
			return nil, err
		}
	}
	if s.pg != nil {
		needle, _ := json.Marshal(map[string]any{"asset": asset})
		bindings, _ := json.Marshal(map[string]any{"library_bindings": []any{map[string]string{"library_asset_id": asset}}})
		mediaSources, _ := json.Marshal(map[string]any{"assets": []any{map[string]any{"library_sources": []any{map[string]string{"asset_id": asset}}}}})
		result, err := s.pg.db.QueryContext(ctx, `SELECT p.entity_id,COALESCE(p.payload->>'title',''),COALESCE(p.payload->>'series_episode_id',''),
 jsonb_build_object('version',m.payload->'version','library_bindings',jsonb_path_query_array(m.payload,'$.library_bindings[*] ? (@.library_asset_id == $asset)',$4::jsonb),'assets',jsonb_path_query_array(m.payload,'$.assets[*] ? (@.library_sources[*].asset_id == $asset)',$4::jsonb))
 FROM studio_media_projects m JOIN studio_projects p ON p.tenant_id=m.tenant_id AND p.entity_id=m.entity_id
 WHERE m.tenant_id=$1 AND m.entity_id>$2 AND (m.payload @> $5::jsonb OR m.payload @> $6::jsonb)
 ORDER BY m.entity_id LIMIT $3`, space, after, limit, needle, bindings, mediaSources)
		if err != nil {
			return nil, libraryrepo.ErrStorage
		}
		defer result.Close()
		for result.Next() {
			var id, title, episode string
			var raw []byte
			var state MediaState
			if result.Scan(&id, &title, &episode, &raw) != nil || json.Unmarshal(raw, &state) != nil || state.Version < 1 {
				return nil, libraryrepo.ErrStorage
			}
			row := impactFromMedia(id, asset, head, state)
			row.Title = title
			row.EpisodeID = episode
			rows = append(rows, row)
		}
		if result.Err() != nil {
			return nil, libraryrepo.ErrStorage
		}
	} else {
		s.media.mu.Lock()
		ids := []string{}
		prefix := space + ":"
		for key := range s.media.data.Projects {
			if strings.HasPrefix(key, prefix) {
				id := strings.TrimPrefix(key, prefix)
				if id > after {
					ids = append(ids, id)
				}
			}
		}
		sort.Strings(ids)
		s.media.mu.Unlock()
		for _, id := range ids {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			p, err := s.GetProject(space, id)
			if errors.Is(err, ErrNotFound) {
				continue
			}
			if err != nil {
				return nil, err
			}
			s.media.mu.Lock()
			row := impactFromMedia(id, asset, head, s.media.data.Projects[mediaKey(space, id)])
			s.media.mu.Unlock()
			if row.Bindings+row.Media == 0 {
				continue
			}
			row.Title = p.Title
			row.EpisodeID = p.SeriesEpisodeID
			rows = append(rows, row)
			if len(rows) == limit {
				break
			}
		}

	}
	for i := range rows {
		if rows[i].EpisodeID == "" {
			continue
		}
		episode, err := s.series.Get(ctx, space, "episode", rows[i].EpisodeID)
		if errors.Is(err, series.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		rows[i].SeriesID = episode.SeriesID
		rows[i].SeasonID = episode.Parent
		rows[i].EpisodeNumber = episode.Ordinal
	}
	return rows, nil
}
func registerLibraryImpactRoute(mux *http.ServeMux, s *FileStore) {
	mux.HandleFunc("GET /api/v1/library/assets/{assetId}/impact", func(w http.ResponseWriter, r *http.Request) {
		space, ok := spaceID(w, r)
		if !ok {
			return
		}
		asset, err := s.library.Get(r.Context(), space, r.PathValue("assetId"))
		if err != nil {
			libraryError(w, err)
			return
		}
		after, limit, err := libraryPage(r, space, "impact:"+asset.Asset.ID)
		if err != nil {
			libraryError(w, err)
			return
		}
		rows, err := s.libraryImpactPage(r.Context(), space, asset.Asset.ID, asset.Asset.HeadVersionID, after, limit+1)
		if err != nil {
			libraryError(w, err)
			return
		}
		next := ""
		if len(rows) > limit {
			rows = rows[:limit]
			next = libraryNext(space, "impact:"+asset.Asset.ID, rows[len(rows)-1].ProjectID)
		}
		status := "unconfirmed"
		var revision int64
		if asset.Asset.UseAuthorization != nil {
			status = asset.Asset.UseAuthorization.Status
			revision = asset.Asset.UseAuthorization.Revision
			if status == "granted" && asset.Asset.UseAuthorization.ExpiresAt != nil && !time.Now().UTC().Before(*asset.Asset.UseAuthorization.ExpiresAt) {
				status = "expired"
			}
		}
		respond(w, 200, map[string]any{"asset_id": asset.Asset.ID, "head_version_id": asset.Asset.HeadVersionID, "asset_status": asset.Asset.Status, "authorization_status": status, "authorization_revision": revision, "coverage": "recorded_bindings_and_media_sources", "items": rows, "next_cursor": next})
	})
}
