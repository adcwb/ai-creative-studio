package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"image"
	_ "image/jpeg"
	"io"
	"os"
	"reflect"
	"sort"
	"unicode/utf16"
)

func socialCaptionLimit(in SocialCreate) int {
	if in.MediaType == "PHOTO" {
		return 4000
	}
	return 2200
}
func socialSameSource(j SocialJob, in SocialCreate) bool {
	a, b := append([]string(nil), j.PhotoAssetIDs...), append([]string(nil), in.PhotoAssetIDs...)
	sort.Strings(a)
	sort.Strings(b)
	return j.MediaType == in.MediaType && j.AssetID == in.AssetID && j.ExportID == in.ExportID && reflect.DeepEqual(a, b)
}
func validatePhotoIntent(p MediaState, in SocialCreate) error {
	if in.Platform != "tiktok" || (in.Mode != "direct" && in.Mode != "draft") || in.AssetID != "" || in.ExportID != "" || len(in.PhotoAssetIDs) < 1 || len(in.PhotoAssetIDs) > 35 || in.PhotoCoverIndex < 0 || in.PhotoCoverIndex >= len(in.PhotoAssetIDs) || !in.PublicMediaConfirmed || in.AllowDuet || in.AllowStitch || len(utf16.Encode([]rune(in.Title))) > 90 || len(utf16.Encode([]rune(in.Caption))) > 4000 {
		return mediaInvalid("invalid PHOTO sources, cover, UTF-16 text or public media consent")
	}
	if in.Mode == "draft" && (in.AllowComment || in.AutoAddMusic || in.BrandContent || in.BrandOrganic || in.PrivacyLevel != "") {
		return mediaInvalid("PHOTO draft does not support direct-post settings")
	}
	seen := map[string]bool{}
	for _, id := range in.PhotoAssetIDs {
		a, ok := mediaAsset(p, id)
		if !ok || a.Kind != "image" || a.MIMEType != "image/jpeg" || seen[id] {
			return mediaInvalid("PHOTO requires unique owned JPEG images; explicitly normalize PNG/poster first")
		}
		seen[id] = true
	}
	return nil
}
func photoFile(path string) error {
	f, e := os.Open(path)
	if e != nil {
		return ErrNotFound
	}
	defer f.Close()
	st, e := f.Stat()
	if e != nil || !st.Mode().IsRegular() || st.Size() <= 0 || st.Size() > 20000000 {
		return mediaInvalid("PHOTO image exceeds 20MB")
	}
	cfg, format, e := image.DecodeConfig(f)
	if e != nil || format != "jpeg" || cfg.Width < 1 || cfg.Height < 1 || cfg.Width > 1080 || cfg.Height > 1080 {
		return mediaInvalid("PHOTO requires JPEG at most 1080x1080; explicitly normalize first")
	}
	if _, e = f.Seek(0, 0); e != nil {
		return e
	}
	_, format, e = image.Decode(f)
	if e != nil || format != "jpeg" {
		return mediaInvalid("PHOTO JPEG decode failed")
	}
	return nil
}
func (m *MediaStore) validatePhotoFilesLocked(p MediaState, space, project string, in SocialCreate) error {
	if e := validatePhotoIntent(p, in); e != nil {
		return e
	}
	for _, id := range in.PhotoAssetIDs {
		path, e := m.localBlob(space, project, id)
		if e != nil {
			return e
		}
		if e = photoFile(path); e != nil {
			return e
		}
	}
	return nil
}
func (r *socialRuntime) dispatchPhoto(ctx context.Context, project, space string, job SocialJob, token socialToken) (SocialView, error) {
	failed := func(code string) (SocialView, error) {
		return r.finish(project, space, job.ID, "failed", "", code, job.Attempt)
	}
	if r.photo == nil || !r.photo.ready() || job.Intent == nil {
		return failed("photo_bridge_unavailable")
	}
	in := *job.Intent
	post := map[string]any{"title": in.Title, "description": in.Caption}
	mode := "MEDIA_UPLOAD"
	if job.Mode == "direct" {
		creator, e := r.creator(ctx, project, space, job.ConnectionID)
		if e != nil {
			return failed("creator_unavailable")
		}
		valid := false
		for _, privacy := range creator.Privacy {
			if privacy == in.PrivacyLevel {
				valid = true
			}
		}
		if !valid || (in.AllowComment && creator.CommentDisabled) || (in.BrandContent && in.PrivacyLevel == "SELF_ONLY") {
			return failed("creator_or_consent_mismatch")
		}
		mode = "DIRECT_POST"
		post["privacy_level"] = in.PrivacyLevel
		post["disable_comment"] = !in.AllowComment
		post["auto_add_music"] = in.AutoAddMusic
		post["brand_content_toggle"] = in.BrandContent
		post["brand_organic_toggle"] = in.BrandOrganic
	}
	urls, e := r.photo.grant(ctx, project, space, job)
	if e != nil {
		return failed("photo_source_or_authorization_invalid")
	}
	token, e = r.token(project, space, job.ConnectionID)
	if e != nil {
		return failed("connection_unavailable")
	}
	payload := map[string]any{"media_type": "PHOTO", "post_mode": mode, "is_aigc": in.IsAIGC, "post_info": post, "source_info": map[string]any{"source": "PULL_FROM_URL", "photo_images": urls, "photo_cover_index": in.PhotoCoverIndex}}
	var result struct {
		Data struct {
			PublishID string `json:"publish_id"`
		} `json:"data"`
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	e = r.request(ctx, "/v2/post/publish/content/init/", token.Access, payload, &result)
	if e != nil || result.Error.Code != "ok" || result.Data.PublishID == "" || len(result.Data.PublishID) > 64 {
		return r.finish(project, space, job.ID, "unknown", "", "photo_create_outcome_unknown", job.Attempt)
	}
	return r.finish(project, space, job.ID, "processing", result.Data.PublishID, "", job.Attempt)
}

func (m *MediaStore) photoSourceKey(space, project string, ids []string) (string, error) {
	hashes := []string{}
	for _, id := range ids {
		path, e := m.localBlob(space, project, id)
		if e != nil {
			return "", e
		}
		f, e := os.Open(path)
		if e != nil {
			return "", e
		}
		h := sha256.New()
		n, e := io.Copy(h, io.LimitReader(f, 20000001))
		f.Close()
		if e != nil || n > 20000000 {
			return "", errMediaLimit
		}
		hashes = append(hashes, hex.EncodeToString(h.Sum(nil)))
	}
	sort.Strings(hashes)
	return mediaDigest(hashes), nil
}
