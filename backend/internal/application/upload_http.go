package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/uploads"
	"github.com/adcwb/ai-creative-studio/backend/internal/persistence/uploadrepo"

	"github.com/adcwb/ai-creative-studio/backend/internal/platform/nativefs"
)

func uploadError(w http.ResponseWriter, e error) {
	switch {
	case errors.Is(e, uploads.ErrNotFound):
		problem(w, 404, "upload_not_found", "上传会话不存在")
	case errors.Is(e, uploads.ErrInput):
		problem(w, 400, "invalid_upload", "文件大小、摘要、格式或分块数据无效")
	case errors.Is(e, uploads.ErrConflict):
		problem(w, 409, "upload_conflict", "上传进度或请求内容不一致，请核对原会话")
	case errors.Is(e, uploads.ErrClosed):
		problem(w, 409, "upload_closed", "上传会话已关闭，请读取原会话结果")
	case errors.Is(e, uploads.ErrExpired):
		problem(w, 410, "upload_expired", "未完成上传已过期，请重新选择文件开始上传")
	case errors.Is(e, uploadrepo.ErrStorage):
		problem(w, 503, "upload_storage_error", "上传结果尚未确认，请保留原文件并恢复此会话")
	default:
		mediaError(w, e)
	}
}
func uploadIdentity(r *http.Request, space string) uploadrepo.Identity {
	actor := "demo"
	if scope, ok := r.Context().Value(authScopeKey{}).(authScope); ok {
		actor = scope.UserID
	}
	return uploadrepo.Identity{Space: space, Project: r.PathValue("id"), Actor: actor, ID: r.PathValue("upload_id")}
}
func registerUploadRoutes(mux *http.ServeMux, s *FileStore, slots chan struct{}) {
	if s.uploads == nil {
		return
	}
	base := "/api/v1/projects/{id}/media/upload-sessions"
	scope := func(w http.ResponseWriter, r *http.Request, write bool) (uploadrepo.Identity, string, bool) {
		var space, key string
		var ok bool
		if write {
			space, key, ok = projectWriteScope(w, r)
		} else {
			space, ok = spaceID(w, r)
		}
		if !ok {
			return uploadrepo.Identity{}, "", false
		}
		if _, e := s.GetProject(space, r.PathValue("id")); e != nil {
			storeError(w, e)
			return uploadrepo.Identity{}, "", false
		}
		if e := s.media.Available(); e != nil {
			mediaError(w, e)
			return uploadrepo.Identity{}, "", false
		}
		return uploadIdentity(r, space), key, true
	}
	mux.HandleFunc("POST "+base, func(w http.ResponseWriter, r *http.Request) {
		i, key, ok := scope(w, r, true)
		if !ok {
			return
		}
		var in uploads.Input
		if !decodeProject(w, r, &in) {
			return
		}
		if e := uploads.Validate(in); e != nil {
			uploadError(w, e)
			return
		}
		i.ID = uploads.Digest([]string{i.Space, i.Project, i.Actor, key})[:32]
		now := time.Now().UTC()
		u := uploads.Session{Input: in, ID: i.ID, Space: i.Space, Project: i.Project, Actor: i.Actor, RequestSHA: uploads.Digest(in), State: "receiving", ChunkSize: uploads.ChunkBytes, CreatedAt: now, ExpiresAt: now.Add(7 * 24 * time.Hour)}
		out, e := s.uploads.Create(r.Context(), u)
		if e != nil {
			uploadError(w, e)
			return
		}
		respond(w, 201, out)
	})
	mux.HandleFunc("GET "+base, func(w http.ResponseWriter, r *http.Request) {
		i, _, ok := scope(w, r, false)
		if !ok {
			return
		}
		limit := 20
		if raw := r.URL.Query().Get("limit"); raw != "" {
			var e error
			limit, e = strconv.Atoi(raw)
			if e != nil {
				uploadError(w, uploads.ErrInput)
				return
			}
		}
		after := r.URL.Query().Get("after")
		out, e := s.uploads.List(r.Context(), i, after, limit)
		if e != nil {
			uploadError(w, e)
			return
		}
		next := ""
		if len(out) == limit {
			next = out[len(out)-1].ID
		}
		respond(w, 200, struct {
			Items []uploads.Session `json:"items"`
			Next  string            `json:"next_after"`
		}{out, next})
	})
	mux.HandleFunc("GET "+base+"/{upload_id}", func(w http.ResponseWriter, r *http.Request) {
		i, _, ok := scope(w, r, false)
		if !ok {
			return
		}
		out, e := s.uploads.Get(r.Context(), i)
		if e != nil {
			uploadError(w, e)
			return
		}
		respond(w, 200, out)
	})
	mux.HandleFunc("PUT "+base+"/{upload_id}/parts/{offset}", func(w http.ResponseWriter, r *http.Request) {
		i, _, ok := scope(w, r, true)
		if !ok {
			return
		}
		if r.Header.Get("Content-Type") != "application/octet-stream" {
			problem(w, 415, "unsupported_media_type", "分块须使用二进制文件内容")
			return
		}
		offset, e := strconv.ParseInt(r.PathValue("offset"), 10, 64)
		if e != nil {
			uploadError(w, uploads.ErrInput)
			return
		}
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
		case <-r.Context().Done():
			return
		}
		_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(5 * time.Minute))
		defer http.NewResponseController(w).SetReadDeadline(time.Time{})
		b, e := io.ReadAll(io.LimitReader(r.Body, uploads.ChunkBytes+1))
		if e != nil {
			uploadError(w, e)
			return
		}
		var out uploads.Session
		e = s.uploads.With(r.Context(), i, func(l *uploadrepo.Locked) error {
			if scope, ok := r.Context().Value(authScopeKey{}).(authScope); ok && scope.recheckWrite != nil && !scope.recheckWrite() {
				return errRuntimeDenied
			}
			if e := l.Put(offset, b, r.Header.Get("X-Chunk-SHA256")); e != nil {
				return e
			}
			out = l.Session
			return nil
		})
		if e != nil {
			uploadError(w, e)
			return
		}
		respond(w, 200, out)
	})
	mux.HandleFunc("POST "+base+"/{upload_id}/complete", func(w http.ResponseWriter, r *http.Request) {
		i, _, ok := scope(w, r, true)
		if !ok {
			return
		}
		var empty struct{}
		if !decodeProject(w, r, &empty) {
			return
		}
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
		case <-r.Context().Done():
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
		defer cancel()
		var out uploads.Session
		e := s.uploads.With(ctx, i, func(l *uploadrepo.Locked) error {
			if l.Session.State == "completed" {
				out = l.Session
				return nil
			}
			if l.Session.State != "receiving" {
				return uploads.ErrClosed
			}
			if time.Now().After(l.Session.ExpiresAt) {
				return uploads.ErrExpired
			}
			if l.Session.Offset != l.Session.Size {
				return uploads.ErrConflict
			}
			f, e := os.CreateTemp(s.media.dir, ".resumable-")
			if e != nil {
				return e
			}
			defer os.Remove(f.Name())
			h := sha256.New()
			var n int64
			for offset := int64(0); offset < l.Session.Size; {
				if e = ctx.Err(); e != nil {
					f.Close()
					return e
				}
				p, b, e := l.Part(offset)
				if e != nil {
					f.Close()
					return e
				}
				written, e := io.MultiWriter(f, h).Write(b)
				n += int64(written)
				if e != nil {
					f.Close()
					return e
				}
				offset += p.Size
			}
			if n != l.Session.Size || hex.EncodeToString(h.Sum(nil)) != l.Session.SHA256 {
				f.Close()
				return uploads.ErrInput
			}
			e = nativefs.SyncFile(f)
			e = errors.Join(e, f.Close())
			if e != nil {
				return e
			}
			var authorize func() bool
			if scope, ok := r.Context().Value(authScopeKey{}).(authScope); ok {
				authorize = scope.recheckWrite
			}
			// Media acceptance and session completion need not share a distributed
			// transaction: the permanent upload receipt makes recovery exact if the
			// media commit succeeds but this session's acknowledgement is lost.
			key := "resumable-" + i.ID
			u := l.Session
			if _, e = s.media.addAssetFile(ctx, i.Project, i.Space, key, u.Name, u.Role, u.Character, u.ShotID, "uploaded", f.Name(), u.MIME, authorize); e != nil {
				return e
			}
			a, e := s.media.AssetForReceipt(i.Project, i.Space, key)
			if e != nil {
				return e
			}
			if e = l.Close(a.ID); e != nil {
				return e
			}
			out = l.Session
			return nil
		})
		if e != nil {
			uploadError(w, e)
			return
		}
		respond(w, 200, out)
	})
	mux.HandleFunc("POST "+base+"/{upload_id}/cancel", func(w http.ResponseWriter, r *http.Request) {
		i, _, ok := scope(w, r, true)
		if !ok {
			return
		}
		var empty struct{}
		if !decodeProject(w, r, &empty) {
			return
		}
		var out uploads.Session
		e := s.uploads.With(r.Context(), i, func(l *uploadrepo.Locked) error {
			if scope, ok := r.Context().Value(authScopeKey{}).(authScope); ok && scope.recheckWrite != nil && !scope.recheckWrite() {
				return errRuntimeDenied
			}
			if e := l.Close(""); e != nil {
				return e
			}
			out = l.Session
			return nil
		})
		if e != nil {
			uploadError(w, e)
			return
		}
		respond(w, 200, out)
	})
}
