package application

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"time"
)

type VoiceAuthorization struct {
	ID               string     `json:"id"`
	SpaceID          string     `json:"space_id"`
	ProjectID        string     `json:"project_id"`
	AudioID          string     `json:"audio_asset_id"`
	ConsentReference string     `json:"consent_reference"`
	CreatedBy        string     `json:"created_by"`
	CreatedAt        time.Time  `json:"created_at"`
	RevokedAt        *time.Time `json:"revoked_at,omitempty"`
}

func isAvatarKind(kind string) bool {
	return kind == "digital_human" || kind == "digital_human_authorized"
}
func validateVoiceAuthorizations(d creativeDisk) error {
	for id, v := range d.VoiceAuthorizations {
		if id != v.ID || !shotIDPattern.MatchString(id) || v.SpaceID == "" || v.ProjectID == "" || v.AudioID == "" || v.CreatedBy == "" || v.ConsentReference == "" || v.CreatedAt.IsZero() {
			return errors.New("invalid voice authorization")
		}
	}
	for _, j := range d.Jobs {
		if !validAvatarCheckpoint(j) {
			return errors.New("invalid avatar checkpoint")
		}
		if j.Kind == "digital_human_authorized" {
			v, ok := d.VoiceAuthorizations[j.VoiceAuthorizationID]
			if !ok || v.SpaceID != j.SpaceID || v.ProjectID != j.ProjectID || v.AudioID != j.AudioAssetID || !j.VoiceConfirmed || v.ConsentReference != j.VoiceConsentReference || j.VoiceConsentBy == "" {
				return errors.New("invalid authorized avatar job")
			}
		}
	}
	return nil
}
func (s *FileStore) voiceAuthorizationLocked(space, project, id, audio string) (VoiceAuthorization, error) {
	v, ok := s.creative.data.VoiceAuthorizations[id]
	if !ok || v.SpaceID != space || v.ProjectID != project || v.AudioID != audio {
		return v, ErrNotFound
	}
	if v.RevokedAt != nil {
		return v, providerFailure("avatar_authorization_revoked")
	}
	return v, nil
}
func (s *FileStore) saveVoiceAuthorization(space, project, key, actor, audio, reference string, confirmed bool) (VoiceAuthorization, error) {
	if _, err := s.GetProject(space, project); err != nil {
		return VoiceAuthorization{}, err
	}
	ref, err := normalizeProjectText(reference, "声音授权依据", 3, 300)
	if err != nil || !confirmed || actor == "" {
		return VoiceAuthorization{}, ErrInvalidProjectInput
	}
	state, err := s.media.GetChecked(project, space)
	if err != nil {
		return VoiceAuthorization{}, err
	}
	asset, ok := mediaAsset(state, audio)
	if !ok || asset.Kind != "audio" {
		return VoiceAuthorization{}, ErrInvalidProjectInput
	}
	c := s.creative
	c.mu.Lock()
	defer c.mu.Unlock()
	digest, _ := projectFingerprint("voice-authorization", project, []string{actor, audio, ref})
	receiptKey := projectReceiptKey(space, key)
	if old, ok := c.data.Receipts[receiptKey]; ok {
		if old.Fingerprint != digest {
			return VoiceAuthorization{}, ErrIdempotencyConflict
		}
		return c.data.VoiceAuthorizations[old.VoiceAuthorizationID], nil
	}
	id, err := newProjectID()
	if err != nil {
		return VoiceAuthorization{}, err
	}
	out := VoiceAuthorization{ID: id, SpaceID: space, ProjectID: project, AudioID: audio, ConsentReference: ref, CreatedBy: actor, CreatedAt: time.Now().UTC()}
	if c.data.VoiceAuthorizations == nil {
		c.data.VoiceAuthorizations = map[string]VoiceAuthorization{}
	}
	c.data.VoiceAuthorizations[id] = out
	c.data.Receipts[receiptKey] = creativeReceipt{Fingerprint: digest, VoiceAuthorizationID: id}
	if err = c.saveLocked(); err != nil {
		delete(c.data.VoiceAuthorizations, id)
		delete(c.data.Receipts, receiptKey)
		return VoiceAuthorization{}, err
	}
	return out, nil
}
func registerVoiceAuthorizationRoutes(mux *http.ServeMux, s *FileStore) {
	base := "/api/v1/projects/{id}/voice-authorizations"
	mux.HandleFunc("GET "+base, func(out http.ResponseWriter, r *http.Request) {
		space, ok := spaceID(out, r)
		if !ok {
			return
		}
		if _, err := s.GetProject(space, r.PathValue("id")); err != nil {
			storeError(out, err)
			return
		}
		c := s.creative
		c.mu.Lock()
		items := []VoiceAuthorization{}
		for _, v := range c.data.VoiceAuthorizations {
			if v.SpaceID == space && v.ProjectID == r.PathValue("id") {
				items = append(items, v)
			}
		}
		c.mu.Unlock()
		sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt.After(items[j].CreatedAt) })
		respond(out, 200, items)
	})
	mux.HandleFunc("POST "+base, func(out http.ResponseWriter, r *http.Request) {
		space, key, ok := projectWriteScope(out, r)
		if !ok {
			return
		}
		var in struct {
			Audio     string `json:"audio_asset_id"`
			Reference string `json:"consent_reference"`
			Confirmed bool   `json:"confirmed"`
		}
		if !decodeProject(out, r, &in) {
			return
		}
		actor, _ := r.Context().Value(authScopeKey{}).(authScope)
		v, err := s.saveVoiceAuthorization(space, r.PathValue("id"), key, actor.UserID, in.Audio, in.Reference, in.Confirmed)
		if err != nil {
			storeError(out, err)
			return
		}
		respond(out, 200, v)
	})
	mux.HandleFunc("POST "+base+"/{voice_id}/revoke", func(out http.ResponseWriter, r *http.Request) {
		space, _, ok := projectWriteScope(out, r)
		if !ok {
			return
		}
		if _, err := s.GetProject(space, r.PathValue("id")); err != nil {
			storeError(out, err)
			return
		}
		var in struct {
			Confirmed bool `json:"confirmed"`
		}
		if !decodeProject(out, r, &in) {
			return
		}
		if !in.Confirmed {
			storeError(out, ErrInvalidProjectInput)
			return
		}
		c := s.creative
		c.mu.Lock()
		v, ok := c.data.VoiceAuthorizations[r.PathValue("voice_id")]
		if !ok || v.SpaceID != space || v.ProjectID != r.PathValue("id") {
			c.mu.Unlock()
			storeError(out, ErrNotFound)
			return
		}
		before := v
		if v.RevokedAt == nil {
			now := time.Now().UTC()
			v.RevokedAt = &now
			c.data.VoiceAuthorizations[v.ID] = v
			if err := c.saveLocked(); err != nil {
				c.data.VoiceAuthorizations[v.ID] = before
				c.mu.Unlock()
				storeError(out, err)
				return
			}
		}
		c.mu.Unlock()
		respond(out, 200, v)
	})
}

func (s *FileStore) checkVoiceAuthorization(ctx context.Context, job *CreativeJob) error {
	if job.Kind != "digital_human_authorized" {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	c := s.creative
	c.mu.Lock()
	v, err := s.voiceAuthorizationLocked(job.SpaceID, job.ProjectID, job.VoiceAuthorizationID, job.AudioAssetID)
	c.mu.Unlock()
	if err != nil {
		return providerFailure("avatar_authorization_revoked")
	}
	if s.runtime != nil && s.runtime.auth != nil && (!s.runtime.auth.can(v.CreatedBy, job.SpaceID, "workspace.write") || !s.runtime.auth.can(job.VoiceConsentBy, job.SpaceID, "models.execute")) {
		return errRuntimeDenied
	}
	return nil
}

func wrapCreativeValidation(c *creativeStore) error {
	if err := validateVoiceAuthorizations(c.data); err != nil {
		return err
	}
	if c.pg != nil {
		original := c.pg.replace
		c.pg.replace = func(raw []byte) error {
			var next creativeDisk
			if json.Unmarshal(raw, &next) != nil {
				return errors.New("invalid creative state")
			}
			if err := validateVoiceAuthorizations(next); err != nil {
				return err
			}
			if original != nil {
				return original(raw)
			}
			c.data = next
			return nil
		}
	}
	return nil
}

func (s *FileStore) avatarDispatchContext(ctx context.Context, job *CreativeJob) context.Context {
	prior, _ := ctx.Value(libraryDispatchKey{}).(func(context.Context) error)
	return context.WithValue(ctx, libraryDispatchKey{}, func(call context.Context) error {
		if prior != nil {
			if err := prior(call); err != nil {
				return err
			}
		}
		return s.checkAvatarJob(call, job)
	})
}
