package application

import (
	"encoding/base64"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/media"
	"github.com/adcwb/ai-creative-studio/backend/internal/platform/tracing"
)

type previewSummary struct {
	ID              string                `json:"id"`
	Purpose         string                `json:"purpose"`
	Status          string                `json:"status"`
	FileURL         string                `json:"file_url"`
	Duration        float64               `json:"duration"`
	Error           string                `json:"error"`
	ResumeAvailable bool                  `json:"resume_available"`
	RenderProgress  *media.RenderProgress `json:"render_progress,omitempty"`
	CreatedAt       string                `json:"created_at"`
	SourceSHA256    string                `json:"source_sha256"`
	ProjectVersion  int                   `json:"project_version"`
	MediaVersion    int                   `json:"media_version"`
	EpisodeNumber   int                   `json:"episode_number"`
	SoundMode       string                `json:"sound_mode"`
}
type previewPage struct {
	Items      []previewSummary `json:"items"`
	NextCursor string           `json:"next_cursor"`
}

func previewMetadata(e MediaExport) previewSummary {
	v := previewSummary{ID: e.ID, Purpose: e.Purpose, Status: e.Status, FileURL: e.FileURL, Duration: e.Duration, Error: e.Error, ResumeAvailable: e.ResumeAvailable, RenderProgress: e.RenderProgress}
	if s := e.PreviewSource; s != nil {
		v.CreatedAt = s.CreatedAt
		v.SourceSHA256 = s.SHA256
		v.ProjectVersion = s.ProjectVersion
		v.MediaVersion = s.MediaVersion
		v.EpisodeNumber = s.EpisodeNumber
		v.SoundMode = s.SoundMode
	}
	return v
}
func (m *MediaStore) preview(project, space, id string) (MediaExport, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, v := range mediaState(m.data, mediaKey(space, project)).Exports {
		if v.ID == id && v.Purpose == "animatic" {
			v = cloneMediaExport(v)
			if v.Status == "running" {
				v.RenderProgress = m.renderProgress(space, project, id)
			}
			return v, nil
		}
	}
	return MediaExport{}, ErrNotFound
}
func (m *MediaStore) previews(project, space, cursor string, limit int) (previewPage, error) {
	page := previewPage{Items: []previewSummary{}}
	if limit < 1 || limit > 100 {
		return page, mediaInvalid("预演分页大小需为1–100")
	}
	before := ""
	if cursor != "" {
		b, e := base64.RawURLEncoding.DecodeString(cursor)
		prefix := space + ":" + project + ":"
		if e != nil || !strings.HasPrefix(string(b), prefix) || !mediaFileID(strings.TrimPrefix(string(b), prefix)) {
			return page, mediaInvalid("预演游标与项目或空间不匹配")
		}
		before = strings.TrimPrefix(string(b), prefix)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	items := mediaState(m.data, mediaKey(space, project)).Exports
	found := before == ""
	for i := len(items) - 1; i >= 0; i-- {
		v := items[i]
		if v.Purpose != "animatic" {
			continue
		}
		if !found {
			if v.ID == before {
				found = true
			}
			continue
		}
		if len(page.Items) == limit {
			page.NextCursor = base64.RawURLEncoding.EncodeToString([]byte(space + ":" + project + ":" + page.Items[len(page.Items)-1].ID))
			break
		}
		if v.Status == "running" {
			v.RenderProgress = m.renderProgress(space, project, v.ID)
		}
		page.Items = append(page.Items, previewMetadata(v))
	}
	if !found {
		return page, mediaInvalid("预演游标不存在，请返回最新预演")
	}
	return page, nil
}
func previewError(w http.ResponseWriter, e error) {
	if errors.Is(e, ErrVersionConflict) || errors.Is(e, ErrInvalidProjectInput) {
		storeError(w, e)
		return
	}
	mediaError(w, e)
}
func registerMediaPreviewRoutes(mux *http.ServeMux, s *FileStore, scope func(http.ResponseWriter, *http.Request, bool) (string, string, bool)) {
	base := "/api/v1/projects/{id}/media/previews"
	mux.HandleFunc("GET "+base+"/options", func(w http.ResponseWriter, r *http.Request) {
		space, _, ok := scope(w, r, false)
		if !ok {
			return
		}
		v, e := s.MediaPreviewOptions(space, r.PathValue("id"))
		if e != nil {
			previewError(w, e)
			return
		}
		respond(w, 200, v)
	})
	mux.HandleFunc("POST "+base, func(w http.ResponseWriter, r *http.Request) {
		space, key, ok := scope(w, r, true)
		if !ok {
			return
		}
		var in PreviewInput
		if !decodeProject(w, r, &in) {
			return
		}
		var authorize func() bool
		if v, ok := r.Context().Value(authScopeKey{}).(authScope); ok {
			authorize = v.recheckWrite
		}
		v, e := s.CreateMediaPreview(r.Context(), space, r.PathValue("id"), key, in, tracing.Parent(r.Context()), authorize)
		if e != nil {
			previewError(w, e)
			return
		}
		respond(w, 202, v)
	})
	mux.HandleFunc("GET "+base, func(w http.ResponseWriter, r *http.Request) {
		space, _, ok := scope(w, r, false)
		if !ok {
			return
		}
		limit := 20
		var e error
		if raw := r.URL.Query().Get("limit"); raw != "" {
			limit, e = strconv.Atoi(raw)
			if e != nil {
				mediaError(w, mediaInvalid("分页大小无效"))
				return
			}
		}
		v, e := s.media.previews(r.PathValue("id"), space, r.URL.Query().Get("cursor"), limit)
		if e != nil {
			mediaError(w, e)
			return
		}
		respond(w, 200, v)
	})
	mux.HandleFunc("GET "+base+"/{preview_id}", func(w http.ResponseWriter, r *http.Request) {
		space, _, ok := scope(w, r, false)
		if !ok {
			return
		}
		v, e := s.media.preview(r.PathValue("id"), space, r.PathValue("preview_id"))
		if e != nil {
			mediaError(w, e)
			return
		}
		respond(w, 200, v)
	})
	for _, action := range []string{"cancel", "resume"} {
		mux.HandleFunc("POST "+base+"/{preview_id}/"+action, func(w http.ResponseWriter, r *http.Request) {
			space, key, ok := scope(w, r, true)
			if !ok {
				return
			}
			var in struct{}
			if !decodeProject(w, r, &in) {
				return
			}
			id := r.PathValue("preview_id")
			if _, e := s.media.preview(r.PathValue("id"), space, id); e != nil {
				mediaError(w, e)
				return
			}
			var v MediaExport
			var e error
			if action == "cancel" {
				v, e = s.media.CancelExport(r.PathValue("id"), space, key, id)
			} else {
				v, e = s.media.ResumeExport(r.PathValue("id"), space, key, id)
			}
			if e != nil {
				mediaError(w, e)
				return
			}
			respond(w, 200, v)
		})
	}
}
