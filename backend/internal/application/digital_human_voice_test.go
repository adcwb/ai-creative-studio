package application

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestVoiceAuthorizationRevocationStopsDispatchAndPublication(t *testing.T) {
	for _, mode := range []string{"file", "postgres"} {
		t.Run(mode, func(t *testing.T) {
			s := testStore(t)
			if mode == "postgres" {
				s = postgresTestStore(t, postgresTestDB(t))
			}
			_, _, auth, actor := workflowFixture(t, s)
			user, token, session, err := auth.login("workflow-editor@example.test", "synthetic-password", false)
			if err != nil || user.ID != actor {
				t.Fatal(err)
			}
			space := ""
			auth.mu.Lock()
			for id, o := range auth.data.Organizations {
				if o.Members[actor] == "editor" {
					space = id
				}
			}
			auth.mu.Unlock()
			p, _, in, video := avatarFixture(t, s, space)
			provider := &avatarFixtureProvider{video: video}
			s.creative.avatarPhoto = provider
			handler := SecureHandler(s, "http://localhost:5173", auth, AuthOptions{})
			path := "/api/v1/projects/" + p.ID + "/voice-authorizations?space_id=" + space
			body := projectJSON(t, map[string]any{"audio_asset_id": in.AudioAssetID, "consent_reference": in.VoiceConsentReference, "confirmed": true})
			authRequest(t, handler, "POST", path, body, token, "", 403)
			viewer, viewerToken, viewerSession, e := auth.login("voice-viewer@example.test", "synthetic-viewer-password", true)
			if e != nil {
				t.Fatal(e)
			}
			auth.mu.Lock()
			snapshot := auth.clone()
			org := snapshot.Organizations[space]
			org.Members[viewer.ID] = "viewer"
			org.Version++
			snapshot.Organizations[space] = org
			e = auth.commit(snapshot)
			auth.mu.Unlock()
			if e != nil {
				t.Fatal(e)
			}
			authRequest(t, handler, "GET", path, "", viewerToken, "", 200)
			authRequest(t, handler, "POST", path, body, viewerToken, viewerSession.CSRF, 403)
			_, foreignToken, _, e := auth.login("voice-other@example.test", "synthetic-foreign-password", true)
			if e != nil {
				t.Fatal(e)
			}
			authRequest(t, handler, "GET", path, "", foreignToken, "", 403)
			response := authRequest(t, handler, "POST", path, body, token, session.CSRF, 200)
			var grant VoiceAuthorization
			if json.Unmarshal(response.Body.Bytes(), &grant) != nil || grant.CreatedBy != actor {
				t.Fatal(response.Body.String())
			}
			in.Kind = "digital_human_authorized"
			in.VoiceAuthorizationID = grant.ID
			in.consentActor = actor
			invalid := in
			invalid.VoiceAuthorizationID = "missing"
			if _, _, err = s.createCreativeJob(space, p.ID, "bad-grant", invalid); err == nil {
				t.Fatal("missing grant accepted")
			}
			job, _, err := s.createCreativeJob(space, p.ID, "voice-authorized", in)
			if err != nil {
				t.Fatal(err)
			}
			if err = s.executeCreativeJob(context.Background(), &job); err != nil {
				t.Fatal(err)
			}
			job.Status = "succeeded"
			if err = s.creative.updateJob(job); err != nil {
				t.Fatal(err)
			}
			original := job.AssetID
			provider.before = func() {
				authRequest(t, handler, "POST", "/api/v1/projects/"+p.ID+"/voice-authorizations/"+grant.ID+"/revoke?space_id="+space, `{"confirmed":true}`, token, session.CSRF, 200)
			}
			next, _, err := s.createCreativeJob(space, p.ID, "voice-revoked-inflight", in)
			if err != nil {
				t.Fatal(err)
			}
			if err = s.executeCreativeJob(context.Background(), &next); err == nil {
				t.Fatal("revoked in-flight result published")
			}
			if provider.calls != 2 {
				t.Fatal(provider.calls)
			}
			if err = s.executeCreativeJob(context.Background(), &next); err == nil || provider.calls != 2 {
				t.Fatal("revoked grant dispatched", err, provider.calls)
			}
			state := s.media.Get(p.ID, space)
			if _, ok := mediaAsset(state, original); !ok {
				t.Fatal("history deleted")
			}
			if _, err = s.media.AssetForReceipt(p.ID, space, "creative-"+next.ID); err == nil {
				t.Fatal("revoked output observable")
			}
			if _, _, err = s.createCreativeJob(space, p.ID, "revoked-new", in); err == nil {
				t.Fatal("revoked grant reused")
			}
			if mode == "postgres" {
				target := filepath.Join(t.TempDir(), "voice-backup")
				if _, err = ExportPostgresData(context.Background(), target, s.pg.db); err != nil {
					t.Fatal(err)
				}
				restored, err := OpenStore(target)
				if err != nil {
					t.Fatal(err)
				}
				defer restored.Close()
				if restored.creative.data.VoiceAuthorizations[grant.ID].RevokedAt == nil {
					t.Fatal("backup lost revoke")
				}
			}
		})
	}
}
