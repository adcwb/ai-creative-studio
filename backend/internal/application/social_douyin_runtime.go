package application

// Approved deployments use a bounded official ticket provider or an injected
// ephemeral ticket. Media sharing requires explicit consent and lasts five minutes.
import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const douyinMediaPath = "/api/v1/social-media/douyin"

type douyinRuntime struct {
	tickets douyinTicketProvider
	store   *FileStore
	auth    *AuthStore
	config  DouyinHandoffConfig
	key     cipher.AEAD
	now     func() time.Time
}
type douyinCapability struct {
	Space   string
	Project string
	Actor   string
	Job     string
	Expires int64
}
type douyinPrepare struct {
	ExpectedVersion int    `json:"expected_version"`
	AssetID         string `json:"asset_id"`
	ExportID        string `json:"export_id"`
	Title           string `json:"title"`
	Confirmed       bool   `json:"confirmed"`
	RightsConfirmed bool   `json:"rights_confirmed"`
}
type douyinPrepared struct {
	JobID     string `json:"job_id"`
	URL       string `json:"handoff_url"`
	ExpiresAt string `json:"expires_at"`
	Version   int    `json:"version"`
}

func douyinRuntimeFromEnv(s *FileStore) *douyinRuntime {
	r := &douyinRuntime{store: s, now: time.Now}
	if s.runtime == nil || s.runtime.auth == nil {
		return r
	}
	r.auth = s.runtime.auth
	// Unapproved/development configurations do not read any credential values.
	if os.Getenv("SOCIAL_DOUYIN_H5_APPROVED") != "true" {
		return r
	}
	origin := os.Getenv("SOCIAL_DOUYIN_PUBLIC_ORIGIN")
	u, e := url.Parse(origin)
	if e != nil || u.Scheme != "https" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" || u.RawPath != "" || (u.Port() != "" && u.Port() != "443") || !strings.Contains(u.Hostname(), ".") || net.ParseIP(u.Hostname()) != nil || strings.HasSuffix(u.Hostname(), ".localhost") || strings.HasSuffix(u.Hostname(), ".local") || strings.ContainsAny(u.Host, "\\\r\n") {
		return r
	}
	raw, e := base64.StdEncoding.DecodeString(os.Getenv("SOCIAL_DOUYIN_MEDIA_KEY_BASE64"))
	if e != nil || len(raw) != 32 {
		return r
	}
	block, e := aes.NewCipher(raw)
	if e != nil {
		return r
	}
	r.key, e = cipher.NewGCM(block)
	if e != nil {
		return r
	}
	r.config = DouyinHandoffConfig{Approved: true, Scopes: strings.Fields(os.Getenv("SOCIAL_DOUYIN_SCOPES")), ClientKey: os.Getenv("SOCIAL_DOUYIN_CLIENT_KEY"), MediaPrefix: origin + "/api/v1/social-media/"}
	r.tickets = newDouyinTicketProviderFromEnv(s, r.key, r.config.ClientKey)
	if r.tickets != nil || os.Getenv("SOCIAL_DOUYIN_TICKET_FETCH_ENABLED") == "true" {
		return r
	}
	expiry, e := time.Parse(time.RFC3339, os.Getenv("SOCIAL_DOUYIN_TICKET_EXPIRES_AT"))
	if e != nil {
		return r
	}
	r.config = DouyinHandoffConfig{Approved: true, Scopes: strings.Fields(os.Getenv("SOCIAL_DOUYIN_SCOPES")), ClientKey: os.Getenv("SOCIAL_DOUYIN_CLIENT_KEY"), Ticket: os.Getenv("SOCIAL_DOUYIN_TICKET"), TicketExpiresAt: expiry, MediaPrefix: origin + "/api/v1/social-media/"}
	return r
}
func (r *douyinRuntime) ready() bool {
	if r == nil || r.auth == nil || r.key == nil || r.now == nil {
		return false
	}
	config := r.config
	if r.tickets != nil {
		if !r.tickets.Ready() {
			return false
		}
		config.Ticket = "readiness-only-not-a-platform-ticket"
		config.TicketExpiresAt = r.now().Add(time.Hour)
	}
	_, e := buildDouyinHandoff(config, r.now(), "readiness-check-nonce", config.MediaPrefix+"douyin", "image", "")
	return e == nil
}
func (r *douyinRuntime) seal(aad string, v any) (string, error) {
	b, e := json.Marshal(v)
	if e != nil {
		return "", e
	}
	nonce := make([]byte, r.key.NonceSize())
	if _, e = rand.Read(nonce); e != nil {
		return "", e
	}
	return base64.RawURLEncoding.EncodeToString(r.key.Seal(nonce, nonce, b, []byte(aad))), nil
}
func (r *douyinRuntime) open(aad, s string, v any) error {
	if len(s) > 4096 {
		return ErrNotFound
	}
	b, e := base64.RawURLEncoding.DecodeString(s)
	if e != nil || len(b) < r.key.NonceSize() {
		return ErrNotFound
	}
	n := r.key.NonceSize()
	plain, e := r.key.Open(nil, b[:n], b[n:], []byte(aad))
	if e != nil {
		return ErrNotFound
	}
	return json.Unmarshal(plain, v)
}
func (r *douyinRuntime) actor(space, user string) bool {
	return r.auth != nil && runtimeRoleAllowed(r.auth.role(user, space))
}
func (r *douyinRuntime) prepare(project, space, user, key string, in douyinPrepare, guards ...func() bool) (douyinPrepared, error) {
	return r.prepareContext(context.Background(), project, space, user, key, in, guards...)
}
func (r *douyinRuntime) prepareContext(ctx context.Context, project, space, user, key string, in douyinPrepare, guards ...func() bool) (douyinPrepared, error) {
	var out douyinPrepared
	for _, guard := range guards {
		if !guard() {
			return out, errSocialUnavailable
		}
	}
	if !r.ready() || !r.actor(space, user) {
		return out, errSocialUnavailable
	}
	if _, e := r.store.GetProject(space, project); e != nil {
		return out, e
	}
	if !in.Confirmed || !in.RightsConfirmed || !socialText(in.Title, 90) {
		return out, mediaInvalid("explicit media sharing and rights consent required")
	}
	// Reject stale/foreign inputs and recover durable replay BEFORE acquiring
	// any platform token. The same checks run under the write lock after fetching.
	preflight := func() (douyinPrepared, bool, error) {
		m := r.store.media
		m.mu.Lock()
		defer m.mu.Unlock()
		d := m.clone()
		k := mediaKey(space, project)
		p := mediaState(d, k)
		replay, e := m.check(d, k, key, mediaDigest([]any{"douyin-prepare", user, in}))
		if e != nil {
			return douyinPrepared{}, false, e
		}
		if replay {
			var prior douyinPrepared
			if e = r.open(k+":"+key, d.Receipts[k+":"+key].SocialSecret, &prior); e != nil {
				return prior, true, e
			}
			expiry, _ := time.Parse(time.RFC3339, prior.ExpiresAt)
			if !expiry.After(r.now()) {
				return prior, true, errSocialUnavailable
			}
			for _, j := range socialState(p).Jobs {
				if j.ID == prior.JobID && (j.Status == "prepared" || j.Status == "handed_off") {
					prior.Version = p.Version
					return prior, true, nil
				}
			}
			return prior, true, ErrNotFound
		}
		if p.Version != in.ExpectedVersion {
			return douyinPrepared{}, false, errMediaVersion
		}
		_, mime, e := socialSource(p, space, project, SocialCreate{AssetID: in.AssetID, ExportID: in.ExportID})
		if e != nil {
			return douyinPrepared{}, false, e
		}
		if mime != "image/png" && mime != "image/jpeg" && mime != "video/mp4" {
			return douyinPrepared{}, false, mediaInvalid("unsupported Douyin media")
		}
		id := in.AssetID
		if in.ExportID != "" {
			id = in.ExportID + ".mp4"
		}
		path, e := m.localBlob(space, project, id)
		if e != nil {
			return douyinPrepared{}, false, e
		}
		info, e := os.Stat(path)
		limit := int64(128 << 20)
		if mime != "video/mp4" {
			limit = 20 << 20
		}
		if e != nil || !info.Mode().IsRegular() || info.Size() > limit {
			return douyinPrepared{}, false, mediaInvalid("Douyin image maximum 20MiB; video maximum 128MiB")
		}
		if len(socialState(p).Jobs) >= 100 {
			return douyinPrepared{}, false, errMediaLimit
		}
		return douyinPrepared{}, false, nil
	}
	if prior, replay, e := preflight(); e != nil || replay {
		if e != nil {
			return douyinPrepared{}, e
		}
		return prior, nil
	}
	config := r.config
	if r.tickets != nil {
		guarded := context.WithValue(ctx, douyinTicketGuardKey{}, func() bool {
			if !r.actor(space, user) {
				return false
			}
			for _, guard := range guards {
				if !guard() {
					return false
				}
			}
			return true
		})
		ticket, expiry, e := r.tickets.Ticket(guarded)
		if e != nil {
			return out, e
		}
		config.Ticket = ticket
		config.TicketExpiresAt = expiry
	}
	if ctx.Err() != nil {
		return out, ctx.Err()
	}
	for _, guard := range guards {
		if !guard() {
			return out, errSocialUnavailable
		}
	}
	m := r.store.media
	m.mu.Lock()
	defer m.mu.Unlock()
	d := m.clone()
	k := mediaKey(space, project)
	p := mediaState(d, k)
	digest := mediaDigest([]any{"douyin-prepare", user, in})
	if replay, e := m.check(d, k, key, digest); e != nil || replay {
		if e != nil {
			return out, e
		}
		if e = r.open(k+":"+key, d.Receipts[k+":"+key].SocialSecret, &out); e != nil {
			return douyinPrepared{}, e
		}
		expiry, _ := time.Parse(time.RFC3339, out.ExpiresAt)
		if !expiry.After(r.now()) {
			return douyinPrepared{}, errSocialUnavailable
		}
		for _, j := range socialState(p).Jobs {
			if j.ID == out.JobID && (j.Status == "prepared" || j.Status == "handed_off") {
				out.Version = p.Version
				return out, nil
			}
		}
		return douyinPrepared{}, ErrNotFound
	}
	if p.Version != in.ExpectedVersion {
		return out, errMediaVersion
	}
	file, mime, e := socialSource(p, space, project, SocialCreate{AssetID: in.AssetID, ExportID: in.ExportID})
	if e != nil {
		return out, e
	}
	kind := "image"
	if mime == "video/mp4" {
		kind = "video"
	} else if mime != "image/png" && mime != "image/jpeg" {
		return out, mediaInvalid("unsupported Douyin media")
	}
	sourceID := in.AssetID
	if in.ExportID != "" {
		sourceID = in.ExportID + ".mp4"
	}
	local, e := m.localBlob(space, project, sourceID)
	if e != nil {
		return out, e
	}
	info, e := os.Stat(local)
	limit := int64(128 << 20)
	if kind == "image" {
		limit = 20 << 20
	}
	if e != nil || !info.Mode().IsRegular() || info.Size() > limit {
		return out, mediaInvalid("Douyin image maximum 20MiB; video maximum 128MiB")
	}
	for _, guard := range guards {
		if !guard() {
			return out, errSocialUnavailable
		}
	}
	state := socialState(p)
	if len(state.Jobs) >= 100 {
		return out, errMediaLimit
	}
	now := r.now()
	expiry := now.Add(5 * time.Minute)
	if cap := config.TicketExpiresAt.Add(-time.Minute); cap.Before(expiry) {
		expiry = cap
	}
	id := mediaID()
	capability, e := r.seal(douyinMediaPath, douyinCapability{space, project, user, id, expiry.Unix()})
	if e != nil {
		return out, e
	}
	source := r.config.MediaPrefix + "douyin?cap=" + url.QueryEscape(capability)
	handoff, e := buildDouyinHandoff(config, now, authRandom(18), source, kind, in.Title)
	if e != nil {
		return out, e
	}
	state.Jobs = append(state.Jobs, SocialJob{ID: id, Platform: "douyin", Mode: "handoff", Status: "prepared", AssetID: in.AssetID, ExportID: in.ExportID, Title: in.Title, DownloadURL: file, MIMEType: mime, CreatedAt: now.UTC().Format(time.RFC3339), Version: 1, HandoffExpiresAt: expiry.UTC().Format(time.RFC3339)})
	p.Social = &state
	p.Version++
	d.Projects[k] = p
	out = douyinPrepared{id, handoff, expiry.UTC().Format(time.RFC3339), p.Version}
	sealed, e := r.seal(k+":"+key, out)
	if e != nil {
		return douyinPrepared{}, e
	}
	d.Receipts[k+":"+key] = mediaReceipt{Digest: digest, SocialSecret: sealed}
	if e = m.commit(d); e != nil {
		return douyinPrepared{}, e
	}
	return out, nil
}
func (r *douyinRuntime) change(project, space, user, key, id, action string, version int) (SocialView, error) {
	if r.auth == nil || !r.actor(space, user) {
		return SocialView{}, errSocialUnavailable
	}
	if action == "ack" && !r.ready() {
		return SocialView{}, errSocialUnavailable
	}
	m := r.store.media
	m.mu.Lock()
	defer m.mu.Unlock()
	d := m.clone()
	k := mediaKey(space, project)
	p := mediaState(d, k)
	digest := mediaDigest([]any{"douyin-change", user, id, action, version})
	if replay, e := m.check(d, k, key, digest); e != nil || replay {
		return socialView(p), e
	}
	if p.Version != version {
		return socialView(p), errMediaVersion
	}
	state := socialState(p)
	found := false
	for i := range state.Jobs {
		j := &state.Jobs[i]
		if j.ID != id || j.Platform != "douyin" || j.Mode != "handoff" {
			continue
		}
		found = true
		if action == "revoke" {
			j.Status = "cancelled"
		} else if action == "ack" && (j.Status == "prepared" || j.Status == "handed_off") {
			expiry, _ := time.Parse(time.RFC3339, j.HandoffExpiresAt)
			if !expiry.After(r.now()) {
				return socialView(p), errSocialUnavailable
			}
			j.Status = "handed_off"
		} else {
			return socialView(p), mediaInvalid("invalid handoff state")
		}
		j.Version++
	}
	if !found {
		return socialView(p), ErrNotFound
	}
	p.Social = &state
	p.Version++
	d.Projects[k] = p
	d.Receipts[k+":"+key] = mediaReceipt{Digest: digest}
	return socialView(p), m.commit(d)
}
func (r *douyinRuntime) serve(w http.ResponseWriter, req *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	deny := func() { http.Error(w, "media capability unavailable", http.StatusNotFound) }
	if req.URL.Path != douyinMediaPath || (req.Method != "GET" && req.Method != "HEAD") || !r.ready() || len(req.URL.Query()) != 1 || len(req.URL.Query()["cap"]) != 1 {
		deny()
		return
	}
	var cap douyinCapability
	if r.open(douyinMediaPath, req.URL.Query().Get("cap"), &cap) != nil || cap.Expires <= r.now().Unix() || cap.Expires > r.now().Add(5*time.Minute).Unix() || !r.actor(cap.Space, cap.Actor) {
		deny()
		return
	}
	if _, e := r.store.GetProject(cap.Space, cap.Project); e != nil {
		deny()
		return
	}
	m := r.store.media
	if m.Available() != nil {
		deny()
		return
	}
	p := m.Get(cap.Project, cap.Space)
	var job *SocialJob
	for _, j := range socialState(p).Jobs {
		if j.ID == cap.Job && j.Platform == "douyin" && j.Mode == "handoff" && (j.Status == "prepared" || j.Status == "handed_off") {
			copy := j
			job = &copy
		}
	}
	if job == nil {
		deny()
		return
	}
	expiry, _ := time.Parse(time.RFC3339, job.HandoffExpiresAt)
	if expiry.Unix() != cap.Expires {
		deny()
		return
	}
	id := job.AssetID
	export := false
	if job.ExportID != "" {
		id = job.ExportID
		export = true
	}
	path, mime, e := m.file(cap.Project, cap.Space, id, export, false)
	if e != nil {
		deny()
		return
	}
	f, e := os.Open(path)
	if e != nil {
		deny()
		return
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil || !info.Mode().IsRegular() || (info.Size() > 128<<20 || (mime != "video/mp4" && info.Size() > 20<<20)) {
		deny()
		return
	}
	// A revoke stops future requests, but cannot recall bytes already downloaded.
	if !r.actor(cap.Space, cap.Actor) || !r.ready() || cap.Expires <= r.now().Unix() {
		deny()
		return
	}
	latest := m.Get(cap.Project, cap.Space)
	valid := false
	for _, j := range socialState(latest).Jobs {
		if j.ID == cap.Job && (j.Status == "prepared" || j.Status == "handed_off") {
			valid = true
		}
	}
	if !valid {
		deny()
		return
	}
	w.Header().Set("Content-Type", mime)
	w.Header().Set("Content-Disposition", "attachment; filename=shared-media")
	http.ServeContent(w, req, "shared-media", info.ModTime(), f)
}
func registerSocialDouyinRoutes(mux *http.ServeMux, s *FileStore) {
	r := douyinRuntimeFromEnv(s)
	base := "/api/v1/projects/{id}/social/douyin"
	mux.HandleFunc("GET "+douyinMediaPath, r.serve)
	scope := func(w http.ResponseWriter, req *http.Request) (string, string, string, bool) {
		space, key, ok := projectWriteScope(w, req)
		if !ok {
			return "", "", "", false
		}
		if _, e := s.GetProject(space, req.PathValue("id")); e != nil {
			storeError(w, e)
			return "", "", "", false
		}
		if e := s.media.Available(); e != nil {
			mediaError(w, e)
			return "", "", "", false
		}
		actor, _ := req.Context().Value(authScopeKey{}).(authScope)
		if r.auth == nil || !r.actor(space, actor.UserID) {
			socialError(w, errSocialUnavailable)
			return "", "", "", false
		}
		return space, key, actor.UserID, true
	}
	mux.HandleFunc("GET "+base, func(w http.ResponseWriter, req *http.Request) {
		space, ok := spaceID(w, req)
		if !ok {
			return
		}
		if _, e := s.GetProject(space, req.PathValue("id")); e != nil {
			storeError(w, e)
			return
		}
		mode := "operator_injected_ephemeral"
		if r.tickets != nil {
			mode = "official_cached_ticket"
		}
		respond(w, 200, map[string]any{"ready": r.ready(), "max_ttl_seconds": 300, "ticket_mode": mode, "result_query_available": false})
	})
	mux.HandleFunc("POST "+base+"/prepare", func(w http.ResponseWriter, req *http.Request) {
		space, key, user, ok := scope(w, req)
		if !ok {
			return
		}
		var in douyinPrepare
		if !decodeProject(w, req, &in) {
			return
		}
		cookie, _ := req.Cookie("studio_session")
		sessionToken := ""
		if cookie != nil {
			sessionToken = cookie.Value
		}
		guard := func() bool {
			actor, _, ok := r.auth.authenticate(sessionToken)
			return ok && actor.ID == user && r.actor(space, user)
		}
		out, e := r.prepareContext(req.Context(), req.PathValue("id"), space, user, key, in, guard)
		if e != nil {
			socialError(w, e)
			return
		}
		respond(w, 200, out)
	})
	for _, action := range []string{"ack", "revoke"} {
		mux.HandleFunc("POST "+base+"/{job_id}/"+action, func(w http.ResponseWriter, req *http.Request) {
			space, key, user, ok := scope(w, req)
			if !ok {
				return
			}
			var in struct {
				ExpectedVersion int  `json:"expected_version"`
				Confirmed       bool `json:"confirmed"`
			}
			if !decodeProject(w, req, &in) {
				return
			}
			if !in.Confirmed {
				socialError(w, mediaInvalid("explicit handoff confirmation required"))
				return
			}
			out, e := r.change(req.PathValue("id"), space, user, key, req.PathValue("job_id"), action, in.ExpectedVersion)
			if e != nil {
				socialError(w, e)
				return
			}
			respond(w, 200, out)
		})
	}
}
