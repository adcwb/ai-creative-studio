package application

import (
	"net/http"
	"time"
)

// Each review is an immutable creator decision about one exact output. It is
// neither automated quality certification nor an external publishing action.
type AvatarReview struct {
	ComparedJobID   string    `json:"compared_job_id,omitempty"`
	ComparedAssetID string    `json:"compared_asset_id,omitempty"`
	ID              string    `json:"id"`
	Version         int       `json:"version"`
	AssetID         string    `json:"asset_id"`
	Outcome         string    `json:"outcome"`
	Identity        bool      `json:"identity_confirmed"`
	LipSync         bool      `json:"lip_sync_confirmed"`
	Audio           bool      `json:"audio_confirmed"`
	Disclosure      bool      `json:"disclosure_confirmed"`
	Note            string    `json:"note,omitempty"`
	ReviewedBy      string    `json:"reviewed_by"`
	CreatedAt       time.Time `json:"created_at"`
}
type AvatarReviewInput struct {
	ComparedJobID   string `json:"compared_job_id,omitempty"`
	ExpectedVersion int    `json:"expected_version"`
	AssetID         string `json:"asset_id"`
	Outcome         string `json:"outcome"`
	Identity        bool   `json:"identity_confirmed"`
	LipSync         bool   `json:"lip_sync_confirmed"`
	Audio           bool   `json:"audio_confirmed"`
	Disclosure      bool   `json:"disclosure_confirmed"`
	Note            string `json:"note,omitempty"`
}

func (s *FileStore) reviewAvatar(space, project, id, key, actor string, in AvatarReviewInput) (AvatarReview, error) {
	if _, err := s.GetProject(space, project); err != nil {
		return AvatarReview{}, err
	}
	if actor == "" || in.ExpectedVersion < 0 || (in.Outcome != "adopted" && in.Outcome != "rework") {
		return AvatarReview{}, ErrInvalidProjectInput
	}
	note, err := normalizeProjectText(in.Note, "审片意见", 0, 2000)
	if err != nil {
		return AvatarReview{}, err
	}
	in.Note = note
	if in.Outcome == "adopted" && !(in.Identity && in.LipSync && in.Audio && in.Disclosure) || in.Outcome == "rework" && note == "" {
		return AvatarReview{}, projectInputError("采用前请逐项检查成片；需要修改时请填写具体意见")
	}
	c := s.creative
	c.mu.Lock()
	defer c.mu.Unlock()
	job, ok := c.data.Jobs[id]
	if !ok || job.SpaceID != space || job.ProjectID != project {
		return AvatarReview{}, ErrNotFound
	}
	if !isAvatarKind(job.Kind) || job.Status != "succeeded" || job.AssetID == "" || job.AssetID != in.AssetID {
		return AvatarReview{}, ErrInvalidProjectInput
	}
	digest, _ := projectFingerprint("avatar-review", id, []any{actor, in})
	rk := projectReceiptKey(space, key)
	if r, ok := c.data.Receipts[rk]; ok {
		if r.Fingerprint != digest || r.ReviewVersion < 1 || r.ReviewVersion > len(job.AvatarReviews) {
			return AvatarReview{}, ErrIdempotencyConflict
		}
		return job.AvatarReviews[r.ReviewVersion-1], nil
	}
	if len(job.AvatarReviews) != in.ExpectedVersion {
		return AvatarReview{}, ErrVersionConflict
	}
	if in.Outcome == "adopted" {
		if _, err = c.avatarLocked(space, project, job.AvatarID); err != nil {
			return AvatarReview{}, providerFailure("avatar_authorization_revoked")
		}
		if job.Kind == "digital_human_authorized" {
			if _, err = s.voiceAuthorizationLocked(space, project, job.VoiceAuthorizationID, job.AudioAssetID); err != nil {
				return AvatarReview{}, err
			}
		}
	}
	state, err := s.media.GetChecked(project, space)
	if err != nil {
		return AvatarReview{}, err
	}
	asset, ok := mediaAsset(state, job.AssetID)
	if !ok || asset.Kind != "video" {
		return AvatarReview{}, ErrInvalidProjectInput
	}
	if in.Outcome == "adopted" {
		_, ref := mediaAsset(state, job.ReferenceAssetID)
		_, audio := mediaAsset(state, job.AudioAssetID)
		if !ref || !audio {
			return AvatarReview{}, projectInputError("原始形象或配音不可用，请核对后再采用；历史审片保留")
		}
	}
	comparedAsset := ""
	if in.ComparedJobID != "" {
		other, ok := c.data.Jobs[in.ComparedJobID]
		if !ok || other.ID == job.ID || other.SpaceID != space || other.ProjectID != project || !isAvatarKind(other.Kind) || other.Status != "succeeded" || other.AvatarID != job.AvatarID || other.AudioAssetID != job.AudioAssetID || other.AssetID == "" {
			return AvatarReview{}, projectInputError("只能对比本项目同形象、同配音的已完成版本")
		}
		if a, ok := mediaAsset(state, other.AssetID); !ok || a.Kind != "video" {
			return AvatarReview{}, ErrInvalidProjectInput
		}
		comparedAsset = other.AssetID
	}
	reviewID, _ := projectFingerprint("avatar-review-record", id, len(job.AvatarReviews)+1)
	v := AvatarReview{ComparedJobID: in.ComparedJobID, ComparedAssetID: comparedAsset, ID: reviewID, Version: len(job.AvatarReviews) + 1, AssetID: in.AssetID, Outcome: in.Outcome, Identity: in.Identity, LipSync: in.LipSync, Audio: in.Audio, Disclosure: in.Disclosure, Note: note, ReviewedBy: actor, CreatedAt: time.Now().UTC()}
	old := job
	job.AvatarReviews = append(append([]AvatarReview(nil), job.AvatarReviews...), v)
	c.data.Jobs[id] = job
	c.data.Receipts[rk] = creativeReceipt{Fingerprint: digest, JobID: id, ReviewVersion: v.Version}
	if err = c.saveLocked(); err != nil {
		c.data.Jobs[id] = old
		delete(c.data.Receipts, rk)
		return AvatarReview{}, err
	}
	return v, nil
}
func registerAvatarReviewRoutes(mux *http.ServeMux, s *FileStore) {
	mux.HandleFunc("POST /api/v1/projects/{id}/creative-jobs/{jobId}/avatar-reviews", func(w http.ResponseWriter, r *http.Request) {
		space, key, ok := projectWriteScope(w, r)
		if !ok {
			return
		}
		var in AvatarReviewInput
		if !decodeProject(w, r, &in) {
			return
		}
		actor, _ := r.Context().Value(authScopeKey{}).(authScope)
		v, err := s.reviewAvatar(space, r.PathValue("id"), r.PathValue("jobId"), key, actor.UserID, in)
		if err != nil {
			storeError(w, err)
			return
		}
		respond(w, 200, v)
	})
}
func validateAvatarReviews(job CreativeJob) bool {
	for i, v := range job.AvatarReviews {
		expectedID, _ := projectFingerprint("avatar-review-record", job.ID, i+1)
		if (v.ComparedJobID == "") != (v.ComparedAssetID == "") || v.ComparedJobID == job.ID || v.ID != expectedID {
			return false
		}
		if !isAvatarKind(job.Kind) || job.Status != "succeeded" || v.Version != i+1 || v.AssetID != job.AssetID || v.ReviewedBy == "" || v.CreatedAt.IsZero() || (v.Outcome != "adopted" && v.Outcome != "rework") || v.Outcome == "adopted" && !(v.Identity && v.LipSync && v.Audio && v.Disclosure) || v.Outcome == "rework" && v.Note == "" {
			return false
		}
	}
	return true
}
