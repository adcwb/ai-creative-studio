package application

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/media"
	"github.com/adcwb/ai-creative-studio/backend/internal/platform/tracing"

	"github.com/adcwb/ai-creative-studio/backend/internal/platform/nativefs"
)

func mediaError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errMediaLineageUnknown):
		problem(w, 409, "media_lineage_unverified", "素材来源记录不足，不能用于新的模型生成；历史作品保留，请从有明确来源的素材重新制作")
	case errors.Is(err, errMediaVersion):
		problem(w, 409, "version_conflict", "媒体版本已变化，请重载后操作")
	case errors.Is(err, errMediaKey):
		problem(w, 409, "idempotency_conflict", "此请求键已用于不同媒体操作")
	case errors.Is(err, errRuntimeDenied):
		problem(w, 403, "role_denied", "当前权限已变化，请重新登录或联系管理员")
	case errors.Is(err, errMediaLimit):
		problem(w, 413, "unsupported_media_payload", "当前操作无法处理此媒体输入，请检查文件格式与尺寸")
	case errors.Is(err, ErrNotFound):
		problem(w, 404, "not_found", "媒体不存在")
	case errors.Is(err, errMediaInput):
		problem(w, 400, "invalid_media", media.InputMessage(err))
	default:
		problem(w, 500, "media_storage_error", "媒体操作结果不确定，请使用原请求键重试，不要重新生成")
	}
}
func registerMediaRoutes(mux *http.ServeMux, s *FileStore) {
	registerLibraryLineageRoutes(mux, s)
	registerAlignmentRoutes(mux, s)
	registerPosterRoutes(mux, s)
	registerSocialRoutes(mux, s)
	registerSocialDouyinRoutes(mux, s)
	m := s.media
	uploadSlots := make(chan struct{}, 2)
	registerUploadRoutes(mux, s, uploadSlots)
	scope := func(w http.ResponseWriter, r *http.Request, write bool) (string, string, bool) {
		if err := m.Available(); err != nil {
			mediaError(w, err)
			return "", "", false
		}
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
		if _, err := s.GetProject(space, r.PathValue("id")); err != nil {
			storeError(w, err)
			return "", "", false
		}
		return space, key, true
	}
	base := "/api/v1/projects/{id}/media"
	registerMediaPreviewRoutes(mux, s, scope)
	registerDeliveryRoute(mux, s, scope)
	mux.HandleFunc("GET "+base+"/review-members", func(w http.ResponseWriter, r *http.Request) {
		if _, _, ok := scope(w, r, false); !ok {
			return
		}
		_, members := reviewActors(r)
		respond(w, 200, members)
	})
	// Adoption and review share the project's authorization and media CAS boundary.
	mux.HandleFunc("POST "+base+"/decisions", func(w http.ResponseWriter, r *http.Request) {
		space, key, ok := scope(w, r, true)
		if !ok {
			return
		}
		var in ProductionDecision
		if !decodeProject(w, r, &in) {
			return
		}
		if in.ShotID != "" {
			project, err := s.GetProject(space, r.PathValue("id"))
			if err != nil {
				storeError(w, err)
				return
			}
			found := false
			if project.Storyboard != nil {
				for _, shot := range project.Storyboard.Shots {
					if shot.ID == in.ShotID {
						found = true
					}
				}
			}
			if !found {
				mediaError(w, mediaInvalid("镜头不存在"))
				return
			}
		}
		actor, members := reviewActors(r)
		in.actor = actor
		in.assignees = map[string]bool{}
		for _, member := range members {
			in.assignees[member.ID] = true
		}
		p, err := m.SaveProductionDecision(r.PathValue("id"), space, key, in)
		if err != nil {
			mediaError(w, err)
			return
		}
		respond(w, 200, p)
	})
	mux.HandleFunc("GET "+base, func(w http.ResponseWriter, r *http.Request) {
		space, _, ok := scope(w, r, false)
		if !ok {
			return
		}
		state, err := m.GetChecked(r.PathValue("id"), space)
		if err != nil {
			mediaError(w, err)
			return
		}
		respond(w, 200, state)
	})
	mux.HandleFunc("POST "+base+"/uploads", func(w http.ResponseWriter, r *http.Request) {
		space, key, ok := scope(w, r, true)
		if !ok {
			return
		}
		select {
		case uploadSlots <- struct{}{}:
			defer func() { <-uploadSlots }()
		case <-r.Context().Done():
			return
		}
		mime := r.Header.Get("Content-Type")
		if mime != "image/png" && mime != "image/jpeg" && mime != "audio/wav" && mime != "audio/mpeg" && mime != "video/mp4" {
			problem(w, 415, "unsupported_media_type", "仅接收 PNG/JPEG、WAV/MP3、MP4 原始文件")
			return
		}
		q := r.URL.Query()
		for _, field := range []string{"filename", "role", "character", "shot_id"} {
			if utf8.RuneCountInString(q.Get(field)) > 120 || strings.ContainsAny(q.Get(field), "\x00\r\n") {
				problem(w, 400, "invalid_input", "素材标签过长或无效")
				return
			}
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
		defer cancel()
		_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(5 * time.Minute))
		defer http.NewResponseController(w).SetReadDeadline(time.Time{})
		f, err := os.CreateTemp(m.dir, ".upload-")
		if err != nil {
			problem(w, 503, "media_storage_error", "暂时无法保存文件，请保留原请求并稍后重试")
			return
		}
		defer os.Remove(f.Name())
		_, err = io.CopyBuffer(f, contextBlobReader{ctx, r.Body}, make([]byte, 64<<10))
		if err == nil {
			err = nativefs.SyncFile(f)
		}
		err = errors.Join(err, f.Close())
		if err != nil {
			mediaError(w, err)
			return
		}
		var authorize func() bool
		if scope, ok := r.Context().Value(authScopeKey{}).(authScope); ok {
			authorize = scope.recheckWrite
		}
		p, err := m.addAssetFile(ctx, r.PathValue("id"), space, key, q.Get("filename"), q.Get("role"), q.Get("character"), q.Get("shot_id"), "uploaded", f.Name(), mime, authorize)
		if err != nil {
			mediaError(w, err)
			return
		}
		asset, err := m.AssetForReceipt(r.PathValue("id"), space, key)
		if err != nil {
			mediaError(w, err)
			return
		}
		respond(w, 200, struct {
			MediaState
			UploadedAssetID string `json:"uploaded_asset_id"`
		}{p, asset.ID})
	})
	mux.HandleFunc("PUT "+base+"/timeline", func(w http.ResponseWriter, r *http.Request) {
		space, key, ok := scope(w, r, true)
		if !ok {
			return
		}
		var in struct {
			ExpectedVersion int           `json:"expected_version"`
			Timeline        MediaTimeline `json:"timeline"`
		}
		if !decodeProject(w, r, &in) {
			return
		}
		p, err := m.SaveTimeline(r.PathValue("id"), space, key, in.ExpectedVersion, in.Timeline)
		if err != nil {
			mediaError(w, err)
			return
		}
		respond(w, 200, p)
	})
	mux.HandleFunc("POST "+base+"/demo", func(w http.ResponseWriter, r *http.Request) {
		space, key, ok := scope(w, r, true)
		if !ok {
			return
		}
		var in struct {
			ExpectedVersion int `json:"expected_version"`
		}
		if !decodeProject(w, r, &in) {
			return
		}
		project, _ := s.GetProject(space, r.PathValue("id"))
		var shots []StoryboardShot
		if project.Storyboard != nil {
			shots = project.Storyboard.Shots
		}
		p, err := m.Demo(project.ID, space, key, in.ExpectedVersion, shots)
		if err != nil {
			mediaError(w, err)
			return
		}
		respond(w, 200, p)
	})
	mux.HandleFunc("POST "+base+"/exports", func(w http.ResponseWriter, r *http.Request) {
		space, key, ok := scope(w, r, true)
		if !ok {
			return
		}
		var in struct {
			ExpectedVersion int `json:"expected_version"`
			EpisodeNumber   int `json:"episode_number,omitempty"`
		}
		if !decodeProject(w, r, &in) {
			return
		}
		e, err := m.StartEpisodeExport(r.PathValue("id"), space, key, in.ExpectedVersion, in.EpisodeNumber, tracing.Parent(r.Context()))
		if err != nil {
			mediaError(w, err)
			return
		}
		respond(w, 202, e)
	})
	mux.HandleFunc("POST "+base+"/exports/{export_id}/cancel", func(w http.ResponseWriter, r *http.Request) {
		space, key, ok := scope(w, r, true)
		if !ok {
			return
		}
		var in struct{}
		if !decodeProject(w, r, &in) {
			return
		}
		e, err := m.CancelExport(r.PathValue("id"), space, key, r.PathValue("export_id"))
		if err != nil {
			mediaError(w, err)
			return
		}
		respond(w, 200, e)
	})
	mux.HandleFunc("POST "+base+"/exports/{export_id}/resume", func(w http.ResponseWriter, r *http.Request) {
		space, key, ok := scope(w, r, true)
		if !ok {
			return
		}
		var in struct{}
		if !decodeProject(w, r, &in) {
			return
		}
		e, err := m.ResumeExport(r.PathValue("id"), space, key, r.PathValue("export_id"))
		if err != nil {
			mediaError(w, err)
			return
		}
		respond(w, 202, e)
	})
	serve := func(export, subtitles bool) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			space, _, ok := scope(w, r, false)
			if !ok {
				return
			}
			id := r.PathValue("asset_id")
			if export {
				id = r.PathValue("export_id")
			}
			path, mime, err := m.file(r.PathValue("id"), space, id, export, subtitles)
			if err != nil {
				if errors.Is(err, ErrNotFound) {
					problem(w, 404, "not_found", "素材或导出不存在")
				} else {
					mediaError(w, err)
				}
				return
			}
			if subtitles && strings.HasSuffix(r.URL.Path, ".vtt") {
				path, err = m.localBlob(space, r.PathValue("id"), id+".vtt")
				if err != nil {
					mediaError(w, err)
					return
				}
				mime = "text/vtt; charset=utf-8"
			}
			if export && !subtitles && r.URL.Query().Get("download") == "1" {
				if preview, err := m.preview(r.PathValue("id"), space, id); err == nil && preview.Purpose == "animatic" {
					w.Header().Set("Content-Disposition", "attachment; filename=animatic-"+id+".mp4")
				}
			}
			w.Header().Set("Content-Type", mime)
			w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
			if subtitles && !strings.HasSuffix(r.URL.Path, ".vtt") {
				w.Header().Set("Content-Disposition", "attachment; filename=subtitle.srt")
			}
			http.ServeFile(w, r, path)
		}
	}
	mux.HandleFunc("GET "+base+"/assets/{asset_id}/file", serve(false, false))
	mux.HandleFunc("GET "+base+"/exports/{export_id}/file", serve(true, false))
	mux.HandleFunc("GET "+base+"/exports/{export_id}/subtitles", serve(true, true))
	mux.HandleFunc("GET "+base+"/exports/{export_id}/subtitles.vtt", serve(true, true))
}
