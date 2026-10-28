package studio

import "net/http"

func (m *MediaStore) revokePhotoAccess(project, space, key, id string, version int) (SocialView, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d := m.clone()
	k := mediaKey(space, project)
	p := mediaState(d, k)
	digest := mediaDigest([]any{"photo-access-revoke", id, version})
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
		if j.ID == id && j.MediaType == "PHOTO" {
			j.PhotoAccessRevoked = true
			j.Version++
			found = true
		}
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
func registerPhotoRevoke(mux *http.ServeMux, s *FileStore, scope func(http.ResponseWriter, *http.Request, bool) (string, string, bool)) {
	mux.HandleFunc("POST /api/v1/projects/{id}/social/jobs/{job_id}/revoke-photo-access", func(w http.ResponseWriter, r *http.Request) {
		space, key, ok := scope(w, r, true)
		if !ok {
			return
		}
		var in struct {
			ExpectedVersion int  `json:"expected_version"`
			Confirmed       bool `json:"confirmed"`
		}
		if !decodeProject(w, r, &in) {
			return
		}
		if !in.Confirmed {
			socialError(w, mediaInvalid("explicit media access revocation confirmation required"))
			return
		}
		out, e := s.media.revokePhotoAccess(r.PathValue("id"), space, key, r.PathValue("job_id"), in.ExpectedVersion)
		if e != nil {
			socialError(w, e)
			return
		}
		respond(w, 200, out)
	})
}
