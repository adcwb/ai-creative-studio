package studio

import (
	"errors"
	"github.com/adcwb/ai-creative-studio/backend/internal/tracing"
	"io"
	"net/http"
	"strings"
	"unicode/utf8"
)

func mediaError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errMediaVersion):
		problem(w, 409, "version_conflict", "媒体版本已变化，请重载后操作")
	case errors.Is(err, errMediaKey):
		problem(w, 409, "idempotency_conflict", "此请求键已用于不同媒体操作")
	case errors.Is(err, errMediaLimit):
		problem(w, 429, "media_limit", "达到媒体资源限额：每项目120素材/30导出、同时2个合成")
	case errors.Is(err, ErrNotFound):
		problem(w, 404, "not_found", "媒体不存在")
	case errors.Is(err, errMediaInput):
		problem(w, 400, "invalid_media", err.Error())
	default:
		problem(w, 500, "media_storage_error", "媒体操作结果不确定，请使用原请求键重试，不要重新生成")
	}
}
func registerMediaRoutes(mux *http.ServeMux, s *FileStore) {
	registerAlignmentRoutes(mux, s)
	m := s.media
	uploadSlots := make(chan struct{}, 2)
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
	mux.HandleFunc("GET "+base, func(w http.ResponseWriter, r *http.Request) {
		space, _, ok := scope(w, r, false)
		if !ok {
			return
		}
		respond(w, 200, m.Get(r.PathValue("id"), space))
	})
	mux.HandleFunc("POST "+base+"/uploads", func(w http.ResponseWriter, r *http.Request) {
		space, key, ok := scope(w, r, true)
		if !ok {
			return
		}
		select {
		case uploadSlots <- struct{}{}:
			defer func() { <-uploadSlots }()
		default:
			problem(w, 429, "media_limit", "同时最多两个上传")
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
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 32<<20))
		if err != nil {
			problem(w, 413, "upload_too_large", "单素材最大 32 MiB")
			return
		}
		p, err := m.AddAsset(r.PathValue("id"), space, key, q.Get("filename"), q.Get("role"), q.Get("character"), q.Get("shot_id"), "uploaded", body, mime)
		if err != nil {
			mediaError(w, err)
			return
		}
		respond(w, 200, p)
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
		}
		if !decodeProject(w, r, &in) {
			return
		}
		e, err := m.StartExport(r.PathValue("id"), space, key, in.ExpectedVersion, tracing.Parent(r.Context()))
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
