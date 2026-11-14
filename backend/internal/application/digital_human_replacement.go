package application

import (
	"context"
	"errors"
	"github.com/adcwb/ai-creative-studio/backend/internal/platform/tracing"
	"net/http"
	"slices"
	"sort"
)

type avatarReplacementInput struct {
	SourceAsset     string `json:"source_asset_id"`
	ExpectedVersion int    `json:"expected_project_version"`
	Segments        []int  `json:"segments"`
	Confirmed       bool   `json:"confirmed"`
}
type avatarReplacementPrepared struct {
	source       CreativeJob
	checkpoint   *avatarCheckpoint
	fingerprint  string
	target       string
	selected     []int
	sourceDigest string
}

func avatarSegmentExpectedKey(job CreativeJob, index int, seg avatarSegment) string {
	if seg.ReusedFrom != "" {
		job.ID = seg.ReusedFrom
	}
	return avatarSegmentKey(job, index)
}
func avatarReplacementID(space, project, key string) string {
	sha, _ := projectFingerprint("avatar-replacement-id", project, []string{space, key})
	return sha[:32]
}
func avatarReplacementDigest(job CreativeJob) string {
	sha, _ := projectFingerprint("avatar-replacement-source", job.ID, []any{job.AvatarCheckpoint, job.AssetID, job.AvatarID, job.AudioAssetID, job.VoiceAuthorizationID})
	return sha
}
func validateAvatarDerivatives(d creativeDisk) error {
	for _, job := range d.Jobs {
		if job.ReplacementOf == "" {
			if len(job.ReplacementSegments) > 0 || job.ReplacementSourceAsset != "" {
				return errors.New("orphan avatar replacement")
			}
			for _, v := range checkpointSegments(job) {
				if v.ReusedFrom != "" {
					return errors.New("orphan reused avatar segment")
				}
			}
			continue
		}
		source, ok := d.Jobs[job.ReplacementOf]
		if !ok || source.ID == job.ID || source.Status != "succeeded" || source.SpaceID != job.SpaceID || source.ProjectID != job.ProjectID || source.AssetID == "" || source.AssetID != job.ReplacementSourceAsset || source.AvatarID != job.AvatarID || source.AudioAssetID != job.AudioAssetID || source.Model != job.Model || source.Provider != job.Provider || source.VoiceAuthorizationID != job.VoiceAuthorizationID || source.ReferenceAssetID != job.ReferenceAssetID || source.AvatarSplitMode != job.AvatarSplitMode || !slices.Equal(source.AvatarCutPoints, job.AvatarCutPoints) || source.AvatarPlanSHA != job.AvatarPlanSHA || source.AvatarCheckpoint == nil || source.AvatarCheckpoint.Format != 2 || job.AvatarCheckpoint == nil || job.AvatarCheckpoint.Format != 2 || len(job.ReplacementSegments) == 0 {
			return errors.New("invalid avatar replacement source")
		}
		a, b := source.AvatarCheckpoint, job.AvatarCheckpoint
		if a.TotalSamples != b.TotalSamples || a.Planner != b.Planner || a.CloudDestination != b.CloudDestination || len(a.Segments) != len(b.Segments) {
			return errors.New("changed avatar replacement plan")
		}
		chosen := map[int]bool{}
		for _, i := range job.ReplacementSegments {
			if i < 1 || i > len(a.Segments) || chosen[i] {
				return errors.New("invalid avatar replacement selection")
			}
			chosen[i] = true
		}
		for i, v := range b.Segments {
			old := a.Segments[i]
			if v.Start != old.Start || v.Samples != old.Samples || v.AudioSHA != old.AudioSHA || v.CutReason != old.CutReason {
				return errors.New("retimed avatar replacement")
			}
			if chosen[i+1] {
				if v.ReusedFrom != "" {
					return errors.New("selected segment was reused")
				}
				continue
			}
			origin := source.ID
			if old.ReusedFrom != "" {
				origin = old.ReusedFrom
			}
			if v.Phase != "done" || v.ReusedFrom != origin || v.RemoteID != old.RemoteID || !sameAvatarBlob(v.Video, old.Video) {
				return errors.New("unselected avatar segment changed")
			}
			original, ok := d.Jobs[origin]
			if !ok || original.ID == job.ID || original.SpaceID != job.SpaceID || original.ProjectID != job.ProjectID || original.Status != "succeeded" || original.AvatarID != job.AvatarID || original.AudioAssetID != job.AudioAssetID || original.AvatarCheckpoint == nil || len(original.AvatarCheckpoint.Segments) <= i {
				return errors.New("invalid reused avatar proof")
			}
			proof := original.AvatarCheckpoint.Segments[i]
			if proof.ReusedFrom != "" || proof.Phase != "done" || !sameAvatarBlob(v.Video, proof.Video) || proof.Start != v.Start || proof.Samples != v.Samples || proof.AudioSHA != v.AudioSHA {
				return errors.New("reused avatar provenance changed")
			}
		}
		seen := map[string]bool{job.ID: true}
		for parent := job.ReplacementOf; parent != ""; parent = d.Jobs[parent].ReplacementOf {
			if seen[parent] {
				return errors.New("avatar replacement cycle")
			}
			seen[parent] = true
			if _, ok := d.Jobs[parent]; !ok {
				return errors.New("missing avatar ancestor")
			}
		}
	}
	return nil
}
func checkpointSegments(job CreativeJob) []avatarSegment {
	if job.AvatarCheckpoint == nil {
		return nil
	}
	return job.AvatarCheckpoint.Segments
}
func (s *FileStore) replaceAvatarSegments(ctx context.Context, space, project, id, key, actor string, in avatarReplacementInput) (CreativeJob, error) {
	if _, err := s.GetProject(space, project); err != nil {
		return CreativeJob{}, err
	}
	if actor == "" || !in.Confirmed || in.ExpectedVersion < 1 || in.SourceAsset == "" || len(in.Segments) == 0 {
		return CreativeJob{}, ErrInvalidProjectInput
	}
	selected := append([]int(nil), in.Segments...)
	sort.Ints(selected)
	for i, n := range selected {
		if n < 1 || i > 0 && n == selected[i-1] {
			return CreativeJob{}, ErrInvalidProjectInput
		}
	}
	in.Segments = selected
	fingerprint, _ := projectFingerprint("avatar-replace-request", id, []any{actor, in})
	c := s.creative
	c.mu.Lock()
	if r, ok := c.data.Receipts[projectReceiptKey(space, key)]; ok {
		if r.Fingerprint != fingerprint {
			c.mu.Unlock()
			return CreativeJob{}, ErrIdempotencyConflict
		}
		job := cloneCreativeJob(c.data.Jobs[r.JobID])
		c.mu.Unlock()
		return job, nil
	}
	source, ok := c.data.Jobs[id]
	source = cloneCreativeJob(source)
	c.mu.Unlock()
	if !ok || source.SpaceID != space || source.ProjectID != project {
		return CreativeJob{}, ErrNotFound
	}
	if source.Status != "succeeded" || source.AssetID != in.SourceAsset || source.Provider != wanAvatarProvider || source.Kind != "digital_human_authorized" || source.AvatarCheckpoint == nil || source.AvatarCheckpoint.Format != 2 || !avatarCheckpointReady(source) || !validAvatarCheckpoint(source) || selected[len(selected)-1] > len(source.AvatarCheckpoint.Segments) {
		return CreativeJob{}, ErrConflict
	}
	// Check the current caller against the original grants, without pretending the
	// historical consent recorder has rejoined the team or reauthorizing a grant.
	current := source
	current.VoiceConsentBy = actor
	if err := s.checkAvatarJob(ctx, &current); err != nil {
		return CreativeJob{}, err
	}
	reference, _, err := s.media.file(project, space, source.ReferenceAssetID, false, false)
	if err != nil {
		return CreativeJob{}, err
	}
	audio, _, err := s.media.file(project, space, source.AudioAssetID, false, false)
	if err != nil {
		return CreativeJob{}, err
	}
	work := &avatarWork{referencePath: reference, audioPath: audio, route: "photo"}
	actual, err := avatarSourceSHA(ctx, source, work)
	if err != nil {
		return CreativeJob{}, err
	}
	if actual != source.AvatarCheckpoint.SourceSHA256 {
		return CreativeJob{}, providerFailure("avatar_source_changed")
	}
	target := avatarReplacementID(space, project, key)
	prepared := &avatarReplacementPrepared{source: source, checkpoint: cloneAvatarCheckpoint(source.AvatarCheckpoint), fingerprint: fingerprint, target: target, selected: selected, sourceDigest: avatarReplacementDigest(source)}
	prepared.checkpoint.Phase = "segments"
	prepared.checkpoint.Engine = nil
	prepared.checkpoint.Composed = nil
	chosen := map[int]bool{}
	for _, n := range selected {
		chosen[n] = true
	}
	for i, v := range prepared.checkpoint.Segments {
		if chosen[i+1] {
			v.Phase = "queued"
			v.RemoteID = ""
			v.Video = nil
			v.ReusedFrom = ""
		} else {
			if v.ReusedFrom == "" {
				v.ReusedFrom = source.ID
			}
			if v.Video == nil {
				return CreativeJob{}, ErrConflict
			}
		}
		prepared.checkpoint.Segments[i] = v
	}
	// Hash the exact new job identity/actor now, outside the creative transaction.
	current.ID = target
	current.AvatarCheckpoint = nil
	prepared.checkpoint.SourceSHA256, err = avatarSourceSHA(ctx, current, work)
	if err != nil {
		return CreativeJob{}, err
	}
	input := CreativeJobInput{Kind: source.Kind, ExpectedVersion: in.ExpectedVersion, ExpectedProvider: source.Provider, ExpectedModel: source.Model, AvatarID: source.AvatarID, AudioAssetID: source.AudioAssetID, VoiceAuthorizationID: source.VoiceAuthorizationID, VoiceConfirmed: true, VoiceConsentReference: source.VoiceConsentReference, CloudTransferConfirmed: true, Prompt: source.Prompt, ShotID: source.ShotID, Character: source.Character, AvatarSplitMode: source.AvatarSplitMode, AvatarCutPoints: append([]float64(nil), source.AvatarCutPoints...), AvatarPlanSHA: source.AvatarPlanSHA, consentActor: actor, replacement: prepared}
	input.traceParent = tracing.Parent(ctx)
	job, _, err := s.createCreativeJob(space, project, key, input)
	return job, err
}

// Proof references remain private and immutable. No copying or new paid request
// happens while accepting the replacement; worker materialization checks hashes.
func (s *FileStore) attachAvatarReplacementLocked(space, project, key string, job *CreativeJob, p *avatarReplacementPrepared) error {
	source, ok := s.creative.data.Jobs[p.source.ID]
	if !ok || source.SpaceID != space || source.ProjectID != project || source.Status != "succeeded" || source.AssetID != p.source.AssetID || avatarReplacementDigest(source) != p.sourceDigest || p.target != avatarReplacementID(space, project, key) || job.Provider != wanAvatarProvider || job.Kind != "digital_human_authorized" {
		return ErrConflict
	}
	cloud, ok := s.creative.avatarPhoto.(*avatarCloud)
	if !ok || cloud.fingerprint != source.AvatarCheckpoint.CloudDestination {
		return ErrProviderChanged
	}
	job.ID = p.target
	job.ReplacementOf = source.ID
	job.ReplacementSourceAsset = source.AssetID
	job.ReplacementSegments = append([]int(nil), p.selected...)
	job.AvatarCheckpoint = cloneAvatarCheckpoint(p.checkpoint)
	// External object keys are scoped to the new task, never to the old take.
	opaque, _ := projectFingerprint("cloud-avatar-inputs", job.ID, []string{space, project})
	job.AvatarCheckpoint.CloudImageKey = "avatar-inputs/" + opaque + "/image"
	job.AvatarCheckpoint.CloudAudioKey = "avatar-inputs/" + opaque + "/audio"
	if !validAvatarCheckpoint(*job) {
		return ErrInvalidProjectInput
	}
	return nil
}
func registerAvatarReplacementRoutes(mux *http.ServeMux, s *FileStore) {
	mux.HandleFunc("POST /api/v1/projects/{id}/creative-jobs/{jobId}/segment-replacements", func(w http.ResponseWriter, r *http.Request) {
		space, key, ok := projectWriteScope(w, r)
		if !ok {
			return
		}
		var in avatarReplacementInput
		if !decodeProject(w, r, &in) {
			return
		}
		scope, _ := r.Context().Value(authScopeKey{}).(authScope)
		job, err := s.replaceAvatarSegments(r.Context(), space, r.PathValue("id"), r.PathValue("jobId"), key, scope.UserID, in)
		if err != nil {
			storeError(w, err)
			return
		}
		respond(w, 200, publicCreativeJob(job))
	})
}
