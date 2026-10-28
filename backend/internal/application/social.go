package application

// Social workflows deliberately distinguish locally prepared files from posts.
// No client-supplied status or external URL is accepted.
import (
	"errors"
	"net/http"
	"time"
)

func socialState(p MediaState) SocialState {
	if p.Social == nil {
		return SocialState{Jobs: []SocialJob{}, Connections: []SocialConnection{}}
	}
	return *p.Social
}
func socialView(p MediaState) SocialView {
	state := socialState(p)
	return SocialView{Version: p.Version, Jobs: state.Jobs, Connections: state.Connections, Platforms: []SocialPlatform{
		{ID: "tiktok", DirectStatus: "approval_required", HandoffStatus: "approval_required", ConnectionStatus: "not_configured", ReasonCode: "application_review_required", PhotoStatus: "not_configured"},
		{ID: "douyin", DirectStatus: "approval_required", HandoffStatus: "approval_required", ConnectionStatus: "not_configured", ReasonCode: "approved_share_or_bound_account_required", PhotoStatus: "not_configured"},
		{ID: "xiaohongshu", DirectStatus: "unavailable", HandoffStatus: "access_paused", ConnectionStatus: "not_configured", ReasonCode: "official_access_paused_export_only", PhotoStatus: "unavailable"},
	}}
}

func socialSource(p MediaState, space, project string, in SocialCreate) (string, string, error) {
	if in.MediaType == "PHOTO" {
		if err := validatePhotoIntent(p, in); err != nil {
			return "", "", err
		}
		return mediaURL(space, project, "assets/"+in.PhotoAssetIDs[in.PhotoCoverIndex]+"/file"), "image/jpeg", nil
	}
	if (in.AssetID == "") == (in.ExportID == "") {
		return "", "", mediaInvalid("select exactly one social media source")
	}
	if in.AssetID != "" {
		a, ok := mediaAsset(p, in.AssetID)
		if !ok || (a.Kind != "image" && a.Kind != "video") {
			return "", "", ErrNotFound
		}
		return mediaURL(space, project, "assets/"+a.ID+"/file"), a.MIMEType, nil
	}
	for _, e := range p.Exports {
		if e.ID == in.ExportID && e.Status == "succeeded" {
			return mediaURL(space, project, "exports/"+e.ID+"/file"), "video/mp4", nil
		}
	}
	return "", "", ErrNotFound
}
func (m *MediaStore) createSocial(project, space, key string, in SocialCreate) (SocialView, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d := m.clone()
	k := mediaKey(space, project)
	p := mediaState(d, k)
	digest := mediaDigest(struct {
		Op    string
		Input SocialCreate
	}{"social-create", in})
	if replay, err := m.check(d, k, key, digest); err != nil || replay {
		return socialView(p), err
	}
	if p.Version != in.ExpectedVersion {
		return socialView(p), errMediaVersion
	}
	if !socialPlatform(in.Platform) || !in.Confirmed || !socialText(in.Title, 90) || !socialText(in.Caption, socialCaptionLimit(in)) {
		return socialView(p), mediaInvalid("invalid social destination, text or confirmation")
	}
	if in.Mode != "export" && (in.Platform != "tiktok" || (in.Mode != "direct" && in.Mode != "draft")) {
		return socialView(p), errSocialUnavailable
	}
	if in.Mode != "export" && (!in.RightsConfirmed || in.ConnectionID == "" || (in.Mode == "direct" && in.PrivacyLevel == "")) {
		return socialView(p), mediaInvalid("explicit account, rights and privacy required")
	}
	if in.Mode == "export" && in.ConnectionID != "" {
		return socialView(p), mediaInvalid("export has no account connection")
	}
	// XHS does not permit SDK auto-filling title/body/topics; exported files carry no social metadata.
	if in.Platform == "xiaohongshu" && (in.Title != "" || in.Caption != "") {
		return socialView(p), mediaInvalid("Xiaohongshu export does not accept prefilled social text")
	}
	if in.MediaType != "" && in.MediaType != "PHOTO" {
		return socialView(p), mediaInvalid("unsupported social media type")
	}
	if in.MediaType == "" && (len(in.PhotoAssetIDs) > 0 || in.PhotoCoverIndex != 0 || in.AutoAddMusic || in.PublicMediaConfirmed) {
		return socialView(p), mediaInvalid("photo fields require PHOTO media type")
	}
	if in.MediaType == "PHOTO" {
		if err := m.validatePhotoFilesLocked(p, space, project, in); err != nil {
			return socialView(p), err
		}
	}
	file, mime, err := socialSource(p, space, project, in)
	if err != nil {
		return socialView(p), err
	}
	sourceKey := ""
	if in.MediaType == "PHOTO" {
		sourceKey, err = m.photoSourceKey(space, project, in.PhotoAssetIDs)
		if err != nil {
			return socialView(p), err
		}
	}
	state := socialState(p)
	if in.Mode != "export" {
		connected := false
		for _, c := range state.Connections {
			if c.ID == in.ConnectionID && c.Platform == in.Platform && c.Status == "connected" {
				connected = true
			}
		}
		if !connected {
			return socialView(p), errSocialUnavailable
		}
		if in.MediaType != "PHOTO" && mime != "video/mp4" {
			return socialView(p), mediaInvalid("TikTok integration currently accepts MP4 only")
		}
		for _, j := range state.Jobs {
			if j.ConnectionID == in.ConnectionID && (socialSameSource(j, in) || (in.MediaType == "PHOTO" && j.MediaType == "PHOTO" && j.PhotoSourceKey == sourceKey)) && j.Status != "cancelled" && j.Status != "failed" {
				return socialView(p), mediaInvalid("source already has an unresolved or published account intent")
			}
		}
	}
	if len(state.Jobs) >= 100 {
		return socialView(p), errMediaLimit
	}
	state.Jobs = append(state.Jobs, SocialJob{PhotoSourceKey: sourceKey, MediaType: in.MediaType, PhotoAssetIDs: append([]string(nil), in.PhotoAssetIDs...), ID: mediaID(), Platform: in.Platform, Mode: in.Mode, Status: "prepared", AssetID: in.AssetID, ExportID: in.ExportID, Title: in.Title, Caption: in.Caption, DownloadURL: file, MIMEType: mime, CreatedAt: time.Now().UTC().Format(time.RFC3339), Version: 1, ConnectionID: in.ConnectionID, Intent: &in})
	p.Social = &state
	p.Version++
	d.Projects[k] = p
	d.Receipts[k+":"+key] = mediaReceipt{Digest: digest}
	return socialView(p), m.commit(d)
}
func (m *MediaStore) cancelSocial(project, space, key, id string, version int) (SocialView, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d := m.clone()
	k := mediaKey(space, project)
	p := mediaState(d, k)
	digest := mediaDigest([]any{"social-cancel", id, version})
	if replay, err := m.check(d, k, key, digest); err != nil || replay {
		return socialView(p), err
	}
	if p.Version != version {
		return socialView(p), errMediaVersion
	}
	state := socialState(p)
	found := false
	for i := range state.Jobs {
		j := &state.Jobs[i]
		if j.ID != id {
			continue
		}
		found = true
		if j.Status != "prepared" {
			return socialView(p), mediaInvalid("only undispatched jobs can be cancelled")
		}
		j.Status = "cancelled"
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
func socialError(w http.ResponseWriter, err error) {
	if errors.Is(err, errSocialUnavailable) {
		problem(w, 503, "social_not_configured", "Platform approval and a securely authorized account are required; no external request was sent")
		return
	}
	mediaError(w, err)
}
func registerSocialRoutes(mux *http.ServeMux, s *FileStore) {
	base := "/api/v1/projects/{id}/social"
	runtime := socialRuntimeFromEnv(s.media)
	runtime.douyin = douyinRuntimeFromEnv(s)
	if s.runtime != nil {
		runtime.auth = s.runtime.auth
	} else {
		runtime.approved = false
	}
	runtime.photo = newSocialPhotoBroker(runtime, s)
	mux.HandleFunc("GET "+tiktokPhotoMediaPath, runtime.photo.serve)
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
		if _, err := s.GetProject(space, r.PathValue("id")); err != nil {
			storeError(w, err)
			return "", "", false
		}
		if err := s.media.Available(); err != nil {
			mediaError(w, err)
			return "", "", false
		}
		return space, key, true
	}
	registerPhotoNormalize(mux, s, scope)
	registerPhotoRevoke(mux, s, scope)
	mux.HandleFunc("GET "+base, func(w http.ResponseWriter, r *http.Request) {
		space, _, ok := scope(w, r, false)
		if !ok {
			return
		}
		respond(w, 200, runtime.view(s.media.Get(r.PathValue("id"), space)))
	})
	mux.HandleFunc("POST "+base+"/jobs", func(w http.ResponseWriter, r *http.Request) {
		space, key, ok := scope(w, r, true)
		if !ok {
			return
		}
		var in SocialCreate
		if !decodeProject(w, r, &in) {
			return
		}
		if in.Mode != "export" && !runtime.ready() {
			socialError(w, errSocialUnavailable)
			return
		}
		if in.MediaType == "PHOTO" && !runtime.photo.ready() {
			socialError(w, errSocialUnavailable)
			return
		}
		v, err := s.media.createSocial(r.PathValue("id"), space, key, in)
		if err != nil {
			socialError(w, err)
			return
		}
		respond(w, 200, v)
	})
	runtime.routes(mux, s, base, scope)
	mux.HandleFunc("POST "+base+"/jobs/{job_id}/cancel", func(w http.ResponseWriter, r *http.Request) {
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
		v, err := s.media.cancelSocial(r.PathValue("id"), space, key, r.PathValue("job_id"), in.ExpectedVersion)
		if err != nil {
			socialError(w, err)
			return
		}
		respond(w, 200, v)
	})
}
