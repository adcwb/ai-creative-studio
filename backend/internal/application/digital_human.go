package application

import (
	"context"
	"net/http"
	"sort"
	"time"
)

// DigitalHuman records a project-scoped, manually reviewed likeness grant.
// Confirmations are declarations by the operator, not automated face verification.
type DigitalHuman struct {
	ID               string     `json:"id"`
	SpaceID          string     `json:"space_id"`
	ProjectID        string     `json:"project_id"`
	Name             string     `json:"name"`
	ReferenceAssetID string     `json:"reference_asset_id"`
	Route            string     `json:"route"`
	ConsentReference string     `json:"consent_reference"`
	CreatedBy        string     `json:"created_by"`
	CreatedAt        time.Time  `json:"created_at"`
	RevokedAt        *time.Time `json:"revoked_at,omitempty"`
}

type DigitalHumanInput struct {
	Name                  string `json:"name"`
	ReferenceAssetID      string `json:"reference_asset_id"`
	ConsentReference      string `json:"consent_reference"`
	LikenessConfirmed     bool   `json:"likeness_confirmed"`
	SinglePersonConfirmed bool   `json:"single_person_confirmed"`
}

// createDigitalHuman preserves consent evidence and idempotency in the creative store.
func (s *FileStore) createDigitalHuman(space, project, key, actor string, in DigitalHumanInput) (DigitalHuman, bool, error) {
	if _, err := s.GetProject(space, project); err != nil {
		return DigitalHuman{}, false, err
	}
	var err error
	in.Name, err = normalizeProjectText(in.Name, "形象名称", 1, 40)
	if err != nil {
		return DigitalHuman{}, false, err
	}
	in.ConsentReference, err = normalizeProjectText(in.ConsentReference, "形象授权依据", 3, 300)
	if err != nil {
		return DigitalHuman{}, false, err
	}
	if !in.LikenessConfirmed || !in.SinglePersonConfirmed {
		return DigitalHuman{}, false, projectInputError("请确认形象授权，并人工检查单人、清晰、无遮挡素材")
	}
	c := s.creative
	c.mu.Lock()
	defer c.mu.Unlock()
	fingerprint, _ := projectFingerprint("digital-human", project, in)
	rk := projectReceiptKey(space, key)
	if receipt, ok := c.data.Receipts[rk]; ok {
		if receipt.Fingerprint != fingerprint {
			return DigitalHuman{}, false, ErrIdempotencyConflict
		}
		return c.data.DigitalHumans[receipt.AvatarID], false, nil
	}
	if len(c.data.DigitalHumans) >= 1000 || len(c.data.Receipts) >= 1000 {
		return DigitalHuman{}, false, ErrProviderNotReady
	}
	asset, ok := mediaAsset(s.media.Get(project, space), in.ReferenceAssetID)
	if !ok || (asset.Kind != "image" && asset.Kind != "video") {
		return DigitalHuman{}, false, projectInputError("请选择本项目照片或视频")
	}
	if err = s.validateAvatarMedia(project, space, asset, false); err != nil {
		return DigitalHuman{}, false, err
	}
	route := "photo"
	if asset.Kind == "video" {
		route = "video"
	}
	id, err := newProjectID()
	if err != nil {
		return DigitalHuman{}, false, err
	}
	a := DigitalHuman{ID: id, SpaceID: space, ProjectID: project, Name: in.Name, ReferenceAssetID: asset.ID, Route: route, ConsentReference: in.ConsentReference, CreatedBy: actor, CreatedAt: time.Now().UTC()}
	if c.data.DigitalHumans == nil {
		c.data.DigitalHumans = map[string]DigitalHuman{}
	}
	c.data.DigitalHumans[id] = a
	c.data.Receipts[rk] = creativeReceipt{Fingerprint: fingerprint, AvatarID: id}
	if err = c.saveLocked(); err != nil {
		delete(c.data.DigitalHumans, id)
		delete(c.data.Receipts, rk)
		return DigitalHuman{}, false, err
	}
	return a, true, nil
}

func (s *FileStore) listDigitalHumans(space, project string) ([]DigitalHuman, error) {
	if _, err := s.GetProject(space, project); err != nil {
		return nil, err
	}
	c := s.creative
	c.mu.Lock()
	defer c.mu.Unlock()
	result := []DigitalHuman{}
	for _, a := range c.data.DigitalHumans {
		if a.SpaceID == space && a.ProjectID == project {
			result = append(result, a)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CreatedAt.After(result[j].CreatedAt) })
	return result, nil
}

// Revocation is permanent; a new grant requires a new record. Existing outputs
// remain auditable. Workers check this record again before publishing an output.
func (s *FileStore) revokeDigitalHuman(space, project, id string) (DigitalHuman, error) {
	if _, err := s.GetProject(space, project); err != nil {
		return DigitalHuman{}, err
	}
	c := s.creative
	c.mu.Lock()
	defer c.mu.Unlock()
	a, ok := c.data.DigitalHumans[id]
	if !ok || a.SpaceID != space || a.ProjectID != project {
		return DigitalHuman{}, ErrNotFound
	}
	if a.RevokedAt != nil {
		return a, nil
	}
	before := a
	now := time.Now().UTC()
	a.RevokedAt = &now
	c.data.DigitalHumans[id] = a
	if err := c.saveLocked(); err != nil {
		c.data.DigitalHumans[id] = before
		return DigitalHuman{}, err
	}
	return a, nil
}

func (c *creativeStore) avatarLocked(space, project, id string) (DigitalHuman, error) {
	a, ok := c.data.DigitalHumans[id]
	if !ok || a.SpaceID != space || a.ProjectID != project {
		return DigitalHuman{}, ErrNotFound
	}
	if a.RevokedAt != nil {
		return DigitalHuman{}, projectInputError("形象授权已撤销")
	}
	return a, nil
}

func (s *FileStore) checkAvatarJob(ctx context.Context, job *CreativeJob) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c := s.creative
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, err := c.avatarLocked(job.SpaceID, job.ProjectID, job.AvatarID); err != nil {
		return providerFailure("avatar_authorization_revoked")
	}
	if saved, ok := c.data.Jobs[job.ID]; ok && saved.Status == "cancelled" {
		return context.Canceled
	}
	return nil
}

func registerDigitalHumanRoutes(mux *http.ServeMux, s *FileStore) {
	base := "/api/v1/projects/{id}/digital-humans"
	mux.HandleFunc("GET "+base, func(w http.ResponseWriter, r *http.Request) {
		space, ok := spaceID(w, r)
		if !ok {
			return
		}
		items, err := s.listDigitalHumans(space, r.PathValue("id"))
		if err != nil {
			storeError(w, err)
			return
		}
		respond(w, 200, items)
	})
	mux.HandleFunc("POST "+base, func(w http.ResponseWriter, r *http.Request) {
		space, key, ok := projectWriteScope(w, r)
		if !ok {
			return
		}
		var in DigitalHumanInput
		if !decodeProject(w, r, &in) {
			return
		}
		scope, _ := r.Context().Value(authScopeKey{}).(authScope)
		item, created, err := s.createDigitalHuman(space, r.PathValue("id"), key, scope.UserID, in)
		if err != nil {
			storeError(w, err)
			return
		}
		status := 200
		if created {
			status = 201
		}
		respond(w, status, item)
	})
	mux.HandleFunc("POST "+base+"/{avatarId}/revoke", func(w http.ResponseWriter, r *http.Request) {
		space, _, ok := projectWriteScope(w, r)
		if !ok {
			return
		}
		var in struct{}
		if !decodeProject(w, r, &in) {
			return
		}
		item, err := s.revokeDigitalHuman(space, r.PathValue("id"), r.PathValue("avatarId"))
		if err != nil {
			storeError(w, err)
			return
		}
		respond(w, 200, item)
	})
}
