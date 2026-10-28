package application

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"time"
	"unicode/utf16"
)

func (r *socialRuntime) claim(project, space, id string, version int, poll bool) (SocialJob, error) {
	if !r.ready() {
		return SocialJob{}, errSocialUnavailable
	}
	r.m.mu.Lock()
	defer r.m.mu.Unlock()
	d := r.m.clone()
	k := mediaKey(space, project)
	p := mediaState(d, k)
	if p.Version != version {
		return SocialJob{}, errMediaVersion
	}
	ss := socialState(p)
	for i := range ss.Jobs {
		j := &ss.Jobs[i]
		if j.ID != id {
			continue
		}
		if j.Platform != "tiktok" || (j.Mode != "direct" && j.Mode != "draft") {
			return SocialJob{}, errSocialUnavailable
		}
		if poll {
			if until, e := time.Parse(time.RFC3339, j.NextPollAt); e == nil && time.Now().Before(until) {
				return SocialJob{}, mediaInvalid("status polling cooldown active")
			}
			if j.Status == "dispatching" {
				until, _ := time.Parse(time.RFC3339, j.LeaseUntil)
				if time.Now().Before(until) {
					return SocialJob{}, mediaInvalid("operation in progress; wait before recovery")
				}
			}
			if j.PublishID == "" || j.Status == "published" || j.Status == "draft" || j.Status == "failed" {
				return SocialJob{}, mediaInvalid("no recoverable publish identifier")
			}
		} else if j.Status != "prepared" {
			return SocialJob{}, mediaInvalid("already dispatched; do not resend")
		}
		j.Status = "dispatching"
		j.Attempt = mediaID()
		j.LeaseUntil = time.Now().Add(3 * time.Minute).UTC().Format(time.RFC3339)
		j.Version++
		p.Social = &ss
		p.Version++
		d.Projects[k] = p
		if e := r.m.commit(d); e != nil {
			return SocialJob{}, e
		}
		return *j, nil
	}
	return SocialJob{}, ErrNotFound
}
func (r *socialRuntime) finish(project, space, id, status, publish, errorCode, attempt string, cooldown ...time.Duration) (SocialView, error) {
	r.m.mu.Lock()
	defer r.m.mu.Unlock()
	d := r.m.clone()
	k := mediaKey(space, project)
	p := mediaState(d, k)
	ss := socialState(p)
	for i := range ss.Jobs {
		j := &ss.Jobs[i]
		if j.ID == id {
			if j.Attempt != attempt {
				return r.view(p), mediaInvalid("superseded social operation")
			}
			if status != "dispatching" {
				j.LeaseUntil = ""
			}
			j.Status = status
			if publish != "" {
				j.PublishID = publish
			}
			j.ErrorCode = errorCode
			j.NextPollAt = ""
			if status == "unknown" {
				delay := time.Minute
				if len(cooldown) > 0 && cooldown[0] > delay {
					delay = cooldown[0]
				}
				j.NextPollAt = time.Now().Add(delay).UTC().Format(time.RFC3339)
			} else if len(cooldown) > 0 && status == "processing" {
				j.NextPollAt = time.Now().Add(cooldown[0]).UTC().Format(time.RFC3339)
			}
			j.Version++
			p.Social = &ss
			p.Version++
			d.Projects[k] = p
			return r.view(p), r.m.commit(d)
		}
	}
	return SocialView{}, ErrNotFound
}
func (r *socialRuntime) execute(ctx context.Context, project, space, id string, version int, poll bool) (SocialView, error) {
	ctx, cancel := context.WithTimeout(ctx, 150*time.Second)
	defer cancel()
	if e := r.checkActor(ctx); e != nil {
		return SocialView{}, e
	}
	job, e := r.claim(project, space, id, version, poll)
	if e != nil {
		return SocialView{}, e
	}
	fail := func(code string) (SocialView, error) {
		return r.finish(project, space, id, "unknown", job.PublishID, code, job.Attempt)
	}
	ctx = context.WithValue(ctx, socialConnectionKey{}, socialConnectionScope{project, space, job.ConnectionID})
	wantScope := "video.publish"
	if job.Mode == "draft" {
		wantScope = "video.upload"
	}
	if poll {
		wantScope = ""
	}
	token, e := r.authorizedToken(ctx, project, space, job.ConnectionID, wantScope)
	if e != nil {
		if poll || job.PublishID != "" {
			return fail("connection_unavailable")
		}
		return r.finish(project, space, id, "failed", job.PublishID, "connection_unavailable", job.Attempt)
	}
	if poll {
		return r.poll(ctx, project, space, job, token)
	}
	if job.Intent == nil {
		return r.finish(project, space, id, "failed", "", "missing_intent", job.Attempt)
	}
	in := *job.Intent
	want := "video.publish"
	if job.Mode == "draft" {
		want = "video.upload"
	}
	if !socialScope(token.Scope, want) {
		return r.finish(project, space, id, "failed", "", "scope_missing", job.Attempt)
	}
	if job.MediaType == "PHOTO" {
		return r.dispatchPhoto(ctx, project, space, job, token)
	}
	path, mime, e := r.m.file(project, space, job.AssetID, false, false)
	if job.ExportID != "" {
		path, mime, e = r.m.file(project, space, job.ExportID, true, false)
	}
	if e != nil || mime != "video/mp4" {
		return r.finish(project, space, id, "failed", "", "invalid_source", job.Attempt)
	}
	f, e := os.Open(path)
	if e != nil {
		return r.finish(project, space, id, "failed", "", "invalid_source", job.Attempt)
	}
	defer f.Close()
	stat, e := f.Stat()
	if e != nil || stat.Size() <= 0 || stat.Size() > 64<<20 {
		return r.finish(project, space, id, "failed", "", "source_exceeds_64mib", job.Attempt)
	}
	probe, e := probeMedia(path)
	if e != nil {
		return r.finish(project, space, id, "failed", "", "invalid_source", job.Attempt)
	}
	payload := map[string]any{"source_info": map[string]any{"source": "FILE_UPLOAD", "video_size": stat.Size(), "chunk_size": stat.Size(), "total_chunk_count": 1}}
	endpoint := "/v2/post/publish/inbox/video/init/"
	if job.Mode == "direct" {
		creator, e := r.creator(ctx, project, space, job.ConnectionID)
		if e != nil {
			return r.finish(project, space, id, "failed", "", "creator_unavailable", job.Attempt)
		}
		valid := false
		for _, privacy := range creator.Privacy {
			if privacy == in.PrivacyLevel {
				valid = true
			}
		}
		duration := 0.0
		_, _ = fmt.Sscan(probe.Format.Duration, &duration)
		if !valid || duration > float64(creator.MaxDuration) || creator.MaxDuration <= 0 || (in.AllowComment && creator.CommentDisabled) || (in.AllowDuet && creator.DuetDisabled) || (in.AllowStitch && creator.StitchDisabled) || len(utf16.Encode([]rune(in.Caption))) > 2200 || (in.BrandContent && in.PrivacyLevel == "SELF_ONLY") {
			return r.finish(project, space, id, "failed", "", "creator_or_consent_mismatch", job.Attempt)
		}
		payload["post_info"] = map[string]any{"title": in.Caption, "privacy_level": in.PrivacyLevel, "disable_comment": !in.AllowComment, "disable_duet": !in.AllowDuet, "disable_stitch": !in.AllowStitch, "is_aigc": in.IsAIGC, "brand_content_toggle": in.BrandContent, "brand_organic_toggle": in.BrandOrganic}
		endpoint = "/v2/post/publish/video/init/"
	}
	token, e = r.token(project, space, job.ConnectionID)
	if e != nil {
		return r.finish(project, space, id, "failed", "", "connection_unavailable", job.Attempt)
	}
	var result struct {
		Data struct {
			PublishID string `json:"publish_id"`
			UploadURL string `json:"upload_url"`
		} `json:"data"`
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	e = r.request(ctx, endpoint, token.Access, payload, &result)
	if e != nil {
		return fail("create_outcome_unknown")
	}
	if result.Error.Code != "ok" || result.Data.PublishID == "" || len(result.Data.PublishID) > 64 {
		return fail("create_not_confirmed")
	}
	job.PublishID = result.Data.PublishID
	if _, e = r.finish(project, space, id, "dispatching", job.PublishID, "", job.Attempt); e != nil {
		return SocialView{}, e
	}
	u, e := url.Parse(result.Data.UploadURL)
	if e != nil || u.Scheme != "https" || u.Host != "open-upload.tiktokapis.com" || u.User != nil || u.Fragment != "" {
		return fail("upload_url_rejected")
	}
	if _, e = r.token(project, space, job.ConnectionID); e != nil {
		return fail("revoked_before_upload")
	}
	req, e := http.NewRequestWithContext(ctx, "PUT", u.String(), io.LimitReader(f, stat.Size()))
	if e != nil {
		return fail("upload_request_invalid")
	}
	req.ContentLength = stat.Size()
	req.Header.Set("Content-Type", "video/mp4")
	req.Header.Set("Content-Range", fmt.Sprintf("bytes 0-%d/%d", stat.Size()-1, stat.Size()))
	if e = r.checkActor(ctx); e != nil {
		return fail("actor_revoked_before_upload")
	}
	res, e := r.client.Do(req)
	if e != nil {
		return fail("upload_outcome_unknown")
	}
	defer res.Body.Close()
	if res.StatusCode != 201 && res.StatusCode != 206 {
		return fail("upload_not_confirmed")
	}
	return r.finish(project, space, id, "processing", job.PublishID, "", job.Attempt)
}
func (r *socialRuntime) poll(ctx context.Context, project, space string, job SocialJob, token socialToken) (SocialView, error) {
	var result struct {
		Data struct {
			Status string `json:"status"`
		} `json:"data"`
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	e := r.request(ctx, "/v2/post/publish/status/fetch/", token.Access, map[string]string{"publish_id": job.PublishID}, &result)
	if e != nil || result.Error.Code != "ok" {
		delay := time.Minute
		var rate socialRateLimit
		if errors.As(e, &rate) {
			delay = rate.Delay
		}
		return r.finish(project, space, job.ID, "unknown", job.PublishID, "status_unavailable", job.Attempt, delay)
	}
	status := "processing"
	switch result.Data.Status {
	case "PUBLISH_COMPLETE":
		if job.Mode == "direct" {
			status = "published"
		} else {
			status = "draft"
		}
	case "SEND_TO_USER_INBOX":
		status = "draft"
	case "FAILED":
		status = "failed"
	case "PROCESSING_UPLOAD", "PROCESSING_DOWNLOAD":
	default:
		status = "unknown"
	}
	return r.finish(project, space, job.ID, status, job.PublishID, "", job.Attempt, 10*time.Second)
}
