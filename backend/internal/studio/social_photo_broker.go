package studio

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const tiktokPhotoMediaPath = "/api/v1/social-media/tiktok-photo"

type socialPhotoBroker struct {
	runtime  *socialRuntime
	store    *FileStore
	origin   string
	verified bool
	now      func() time.Time
}
type photoCapability struct {
	Space, Project, Job, Asset, Actor, Connection, Attempt string
	Binding                                                string
	Expires                                                int64
}

func newSocialPhotoBroker(r *socialRuntime, s *FileStore) *socialPhotoBroker {
	b := &socialPhotoBroker{runtime: r, store: s, now: time.Now}
	if os.Getenv("SOCIAL_TIKTOK_PHOTO_URL_VERIFIED") != "true" {
		return b
	}
	origin := os.Getenv("SOCIAL_TIKTOK_PHOTO_PUBLIC_ORIGIN")
	u, e := url.Parse(origin)
	if e != nil || u.Scheme != "https" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" || u.RawPath != "" || (u.Port() != "" && u.Port() != "443") || !strings.Contains(u.Hostname(), ".") || net.ParseIP(u.Hostname()) != nil || strings.HasSuffix(u.Hostname(), ".localhost") || strings.HasSuffix(u.Hostname(), ".local") {
		return b
	}
	b.origin = origin
	b.verified = true
	return b
}
func (b *socialPhotoBroker) ready() bool {
	return b != nil && b.verified && b.origin != "" && b.runtime != nil && b.runtime.ready() && b.runtime.auth != nil && b.store != nil
}
func (b *socialPhotoBroker) grant(ctx context.Context, project, space string, job SocialJob) ([]string, error) {
	if !b.ready() || b.runtime.checkActor(ctx) != nil {
		return nil, errSocialUnavailable
	}
	actor, ok := ctx.Value(authScopeKey{}).(authScope)
	if !ok || actor.SpaceID != space || !runtimeRoleAllowed(b.runtime.auth.role(actor.UserID, space)) {
		return nil, errRuntimeDenied
	}
	m := b.runtime.m
	m.mu.Lock()
	defer m.mu.Unlock()
	d := m.clone()
	k := mediaKey(space, project)
	p := mediaState(d, k)
	if job.Intent == nil {
		return nil, ErrNotFound
	}
	if e := m.validatePhotoFilesLocked(p, space, project, *job.Intent); e != nil {
		return nil, e
	}
	expiry := b.now().Add(65 * time.Minute).UTC().Truncate(time.Second)
	var target *SocialJob
	if p.Social == nil {
		return nil, ErrNotFound
	}
	for i := range p.Social.Jobs {
		j := &p.Social.Jobs[i]
		if !j.PhotoAccessRevoked && j.ID == job.ID && j.Attempt == job.Attempt && j.Status == "dispatching" && j.PhotoExpiresAt == "" {
			target = j
		}
	}
	if target == nil {
		return nil, errMediaVersion
	}
	urls := []string{}
	for _, id := range job.PhotoAssetIDs {
		cap, e := b.runtime.seal(tiktokPhotoMediaPath, photoCapability{space, project, job.ID, id, actor.UserID, job.ConnectionID, job.Attempt, mediaDigest([]string{b.origin, b.runtime.clientKey}), expiry.Unix()})
		if e != nil {
			return nil, e
		}
		urls = append(urls, b.origin+tiktokPhotoMediaPath+"?cap="+url.QueryEscape(cap))
	}
	target.PhotoGrantID = job.Attempt
	target.PhotoExpiresAt = expiry.Format(time.RFC3339)
	target.PhotoActor = actor.UserID
	target.Version++
	p.Version++
	d.Projects[k] = p
	if e := m.commit(d); e != nil {
		return nil, e
	}
	return urls, nil
}
func (b *socialPhotoBroker) allowed(cap photoCapability) bool {
	if !b.ready() || cap.Binding != mediaDigest([]string{b.origin, b.runtime.clientKey}) || cap.Expires <= b.now().Unix() || cap.Expires > b.now().Add(65*time.Minute).Unix() || !runtimeRoleAllowed(b.runtime.auth.role(cap.Actor, cap.Space)) {
		return false
	}
	if _, e := b.store.GetProject(cap.Space, cap.Project); e != nil {
		return false
	}
	m := b.runtime.m
	if m.Available() != nil {
		return false
	}
	p := m.Get(cap.Project, cap.Space)
	connected := false
	for _, c := range socialState(p).Connections {
		if c.ID == cap.Connection && c.Platform == "tiktok" && (c.Status == "connected" || c.Status == "refreshing") {
			connected = true
		}
	}
	if !connected {
		return false
	}
	for _, j := range socialState(p).Jobs {
		if j.PhotoAccessRevoked || j.ID != cap.Job || j.MediaType != "PHOTO" || j.ConnectionID != cap.Connection || j.PhotoActor != cap.Actor || j.PhotoGrantID != cap.Attempt {
			continue
		}
		if j.Status != "dispatching" && j.Status != "processing" && j.Status != "unknown" {
			return false
		}
		expiry, e := time.Parse(time.RFC3339, j.PhotoExpiresAt)
		if e != nil || expiry.Unix() != cap.Expires {
			return false
		}
		for _, id := range j.PhotoAssetIDs {
			if id == cap.Asset {
				return true
			}
		}
	}
	return false
}
func (b *socialPhotoBroker) serve(w http.ResponseWriter, req *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	deny := func() { http.Error(w, "media capability unavailable", 404) }
	if req.URL.Path != tiktokPhotoMediaPath || (req.Method != "GET" && req.Method != "HEAD") || !b.ready() || len(req.URL.Query()) != 1 || len(req.URL.Query()["cap"]) != 1 || len(req.URL.Query().Get("cap")) > 4096 {
		deny()
		return
	}
	var cap photoCapability
	if b.runtime.open(tiktokPhotoMediaPath, req.URL.Query().Get("cap"), &cap) != nil || !b.allowed(cap) {
		deny()
		return
	}
	path, mime, e := b.runtime.m.file(cap.Project, cap.Space, cap.Asset, false, false)
	if e != nil || mime != "image/jpeg" || photoFile(path) != nil {
		deny()
		return
	}
	f, e := os.Open(path)
	if e != nil {
		deny()
		return
	}
	defer f.Close()
	st, e := f.Stat()
	if e != nil || !b.allowed(cap) {
		deny()
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Content-Disposition", "attachment; filename=photo.jpg")
	http.ServeContent(w, req, "photo.jpg", st.ModTime(), f)
}
