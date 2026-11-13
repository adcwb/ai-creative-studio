package application

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/series"
	"github.com/adcwb/ai-creative-studio/backend/internal/persistence/seriesrepo"
)

// The preview pins immutable exports, not the current mutable edit. No media
// bytes are buffered and the number of episodes is not a product quota.
type seasonDeliveryEpisode struct {
	EpisodeID        string       `json:"episode_id"`
	ProjectID        string       `json:"project_id"`
	Number           int64        `json:"episode_number"`
	Title            string       `json:"title"`
	ExportID         string       `json:"export_id,omitempty"`
	Duration         float64      `json:"duration_seconds"`
	OutputResolution string       `json:"output_resolution,omitempty"`
	Issue            string       `json:"issue,omitempty"`
	Export           *MediaExport `json:"-"`
}
type seasonDeliveryPreview struct {
	SeasonID string                  `json:"season_id"`
	SeriesID string                  `json:"series_id"`
	Title    string                  `json:"title"`
	Episodes []seasonDeliveryEpisode `json:"episodes"`
	Ready    bool                    `json:"ready"`
	SHA256   string                  `json:"selection_sha256"`
}

func closeDelivery(files []deliveryInput) {
	for _, f := range files {
		_ = f.file.Close()
	}
}

func (s *FileStore) seasonDelivery(ctx context.Context, space, id string) (seasonDeliveryPreview, error) {
	season, err := s.series.Get(ctx, space, "season", id)
	if err != nil {
		return seasonDeliveryPreview{}, err
	}
	out := seasonDeliveryPreview{SeasonID: id, SeriesID: season.SeriesID, Title: season.Title, Episodes: []seasonDeliveryEpisode{}, Ready: true}
	// Include every ready episode in directory order. A missing final must be
	// shown, never silently omitted from a supposedly complete season.
	q := seriesrepo.Query{Kind: "episode", Parent: id, Limit: 100}
	basis := []any{space, season}
	for {
		page, err := s.series.List(ctx, space, q)
		if err != nil {
			return out, err
		}
		for _, node := range page.Items {
			p, err := s.GetProject(space, node.ProjectID)
			if err != nil {
				return out, err
			}
			if p.SeriesEpisodeID != node.ID {
				return out, seriesrepo.ErrStorage
			}
			state, err := s.media.GetChecked(p.ID, space)
			if err != nil {
				return out, err
			}
			item := seasonDeliveryEpisode{EpisodeID: node.ID, ProjectID: p.ID, Number: node.Ordinal, Title: p.Title}
			for _, v := range state.Exports {
				if v.Purpose == "" && v.Status == "succeeded" && (v.EpisodeNumber == 0 || v.EpisodeNumber == 1) {
					v := v
					item.Export = &v
				}
			}
			if item.Export == nil {
				item.Issue = "missing_final_export"
			} else {
				item.ExportID, item.Duration, item.OutputResolution = item.Export.ID, item.Export.Duration, item.Export.Timeline.OutputResolution
				_, files, err := prepareDelivery(s.media, space, p.ID, item.ExportID)
				if err != nil {
					item.Issue = "export_files_unavailable"
				} else {
					closeDelivery(files)
				}
			}
			if item.Issue != "" {
				out.Ready = false
			}
			out.Episodes = append(out.Episodes, item)
			basis = append(basis, node, p.Version, p.Title, item, item.Export)
		}
		if page.Next == "" {
			break
		}
		q.After = page.Next
	}
	if len(out.Episodes) == 0 {
		out.Ready = false
	}
	current, err := s.series.Get(ctx, space, "season", id)
	if err != nil {
		return out, err
	}
	if current.Version != season.Version {
		return out, series.ErrConflict
	}
	out.SHA256 = series.Digest(basis)
	return out, nil
}

func (s *FileStore) writeSeasonDelivery(ctx context.Context, w io.Writer, space string, preview seasonDeliveryPreview) error {
	archive := zip.NewWriter(w)
	type episodeManifest struct {
		Episode       seasonDeliveryEpisode `json:"episode"`
		Timeline      MediaTimeline         `json:"timeline"`
		SeriesEpisode any                   `json:"series_episode,omitempty"`
		Files         []deliveryFile        `json:"files"`
	}
	manifest := struct {
		Format    int                   `json:"format"`
		Selection seasonDeliveryPreview `json:"selection"`
		Episodes  []episodeManifest     `json:"episodes"`
	}{1, preview, []episodeManifest{}}
	for _, item := range preview.Episodes {
		if err := ctx.Err(); err != nil {
			return err
		}
		export, files, err := prepareDelivery(s.media, space, item.ProjectID, item.ExportID)
		if err != nil {
			return err
		}
		// Metadata cannot be swapped for a different export after previewing.
		if series.Digest(export) != series.Digest(item.Export) {
			closeDelivery(files)
			return series.ErrConflict
		}
		entry := episodeManifest{item, export.Timeline, export.SeriesEpisode, []deliveryFile{}}
		err = func() error {
			defer closeDelivery(files)
			for _, f := range files {
				name := fmt.Sprintf("episode-%06d-%s/%s", item.Number, item.EpisodeID, f.name)
				dest, err := archive.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
				if err != nil {
					return err
				}
				size, hash, err := copyDeliveryFile(ctx, dest, f.file)
				if err != nil {
					return err
				}
				entry.Files = append(entry.Files, deliveryFile{name, size, hash})
			}
			return nil
		}()
		if err != nil {
			return err
		}
		manifest.Episodes = append(manifest.Episodes, entry)
	}
	entry, err := archive.Create("manifest.json")
	if err != nil {
		return err
	}
	if err = json.NewEncoder(entry).Encode(manifest); err != nil {
		return err
	}
	entry, err = archive.Create("README.txt")
	if err != nil {
		return err
	}
	if _, err = io.WriteString(entry, "Fixed completed exports, subtitles and timelines in season directory order. SHA-256 describes the included bytes. This does not certify licensing, approval, story consistency, native resolution or publication. No external platform was contacted.\n"); err != nil {
		return err
	}
	return archive.Close()
}

func registerSeasonDeliveryRoutes(mux *http.ServeMux, s *FileStore) {
	base := "/api/v1/seasons/{id}/delivery"
	handler := func(w http.ResponseWriter, r *http.Request) {
		space, ok := spaceID(w, r)
		if !ok {
			return
		}
		preview, err := s.seasonDelivery(r.Context(), space, r.PathValue("id"))
		if err != nil {
			seriesError(w, err)
			return
		}
		w.Header().Set("Cache-Control", "private, no-store")
		if r.URL.Path[len(r.URL.Path)-4:] != ".zip" {
			respond(w, 200, preview)
			return
		}
		if !preview.Ready {
			problem(w, 409, "season_delivery_incomplete", "本季仍有缺失或不可读取的正式成片，请重新检查交付清单")
			return
		}
		if r.URL.Query().Get("selection_sha256") != preview.SHA256 {
			problem(w, 409, "season_delivery_changed", "本季目录或成片选择已变化，请重新预览并确认交付清单")
			return
		}
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", `attachment; filename="season-delivery.zip"`)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if err = s.writeSeasonDelivery(r.Context(), w, space, preview); err != nil {
			panic(http.ErrAbortHandler)
		}
	}
	mux.HandleFunc("GET "+base, handler)
	mux.HandleFunc("GET "+base+".zip", handler)
}
