package application

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDigitalHumanReviewExactOutputCASHistoryAndRevocation(t *testing.T) {
	for _, mode := range []string{"file", "postgres"} {
		t.Run(mode, func(t *testing.T) {
			s := testStore(t)
			if mode == "postgres" {
				s = postgresTestStore(t, postgresTestDB(t))
			}
			p, avatar, in, video := avatarFixture(t, s, "avatar-review-space")
			s.creative.avatarPhoto = &avatarFixtureProvider{video: video}
			job, _, err := s.createCreativeJob(p.SpaceID, p.ID, "review-generation", in)
			if err != nil {
				t.Fatal(err)
			}
			setAvatarTestRunning(t, s, &job)
			if err = s.executeCreativeJob(context.Background(), &job); err != nil {
				t.Fatal(err)
			}
			job.Status = "succeeded"
			if err = s.creative.updateJob(job); err != nil {
				t.Fatal(err)
			}
			input := AvatarReviewInput{AssetID: job.AssetID, Outcome: "adopted"}
			if _, err = s.reviewAvatar(p.SpaceID, p.ID, job.ID, "unchecked", "reviewer", input); err == nil {
				t.Fatal("unchecked output adopted")
			}
			input.Identity = true
			input.LipSync = true
			input.Audio = true
			input.Disclosure = true
			first, err := s.reviewAvatar(p.SpaceID, p.ID, job.ID, "review-adopt", "reviewer", input)
			if err != nil || first.Version != 1 {
				t.Fatal(first, err)
			}
			if _, err = s.reviewAvatar(p.SpaceID, p.ID, job.ID, "review-race", "reviewer", input); !errors.Is(err, ErrVersionConflict) {
				t.Fatal("stale review replaced", err)
			}
			next := AvatarReviewInput{ExpectedVersion: 1, AssetID: job.AssetID, Outcome: "rework", Note: "口型右侧需要检查"}
			if _, err = s.reviewAvatar(p.SpaceID, p.ID, job.ID, "review-change", "reviewer", next); err != nil {
				t.Fatal(err)
			}
			replay, err := s.reviewAvatar(p.SpaceID, p.ID, job.ID, "review-adopt", "reviewer", input)
			if err != nil || replay != first {
				t.Fatal("original review changed", replay, err)
			}
			foreign := input
			foreign.AssetID = "not-this-output"
			if _, err = s.reviewAvatar(p.SpaceID, p.ID, job.ID, "wrong-output", "reviewer", foreign); err == nil {
				t.Fatal("unrelated output reviewed")
			}
			if _, err = s.reviewAvatar("foreign-space", p.ID, job.ID, "foreign", "reviewer", input); err == nil {
				t.Fatal("foreign review accepted")
			}
			if _, err = s.revokeDigitalHuman(p.SpaceID, p.ID, avatar.ID); err != nil {
				t.Fatal(err)
			}
			input.ExpectedVersion = 2
			if _, err = s.reviewAvatar(p.SpaceID, p.ID, job.ID, "revoked-adopt", "reviewer", input); err == nil {
				t.Fatal("revoked likeness adopted again")
			}
			next.ExpectedVersion = 2
			next.Note = "授权已撤销，请停止采用"
			if _, err = s.reviewAvatar(p.SpaceID, p.ID, job.ID, "revoked-rework", "reviewer", next); err != nil {
				t.Fatal(err)
			}
			stored := s.creative.data.Jobs[job.ID]
			if len(stored.AvatarReviews) != 3 || stored.AvatarReviews[0] != first {
				t.Fatal("review history overwritten")
			}
		})
	}
}

func TestDigitalHumanReviewHTTPRequiresCurrentWriterAndCSRF(t *testing.T) {
	for _, mode := range []string{"file", "postgres"} {
		t.Run(mode, func(t *testing.T) {
			s := testStore(t)
			if mode == "postgres" {
				s = postgresTestStore(t, postgresTestDB(t))
			}
			_, _, auth, actor := workflowFixture(t, s)
			_, token, session, err := auth.login("workflow-editor@example.test", "synthetic-password", false)
			if err != nil {
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
			in.consentActor = actor
			s.creative.avatarPhoto = &avatarFixtureProvider{video: video}
			job, _, err := s.createCreativeJob(space, p.ID, "review-secure-generation", in)
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
			other, _, e := s.createCreativeJob(space, p.ID, "review-secure-alternative", in)
			if e != nil {
				t.Fatal(e)
			}
			if e = s.executeCreativeJob(context.Background(), &other); e != nil {
				t.Fatal(e)
			}
			other.Status = "succeeded"
			if e = s.creative.updateJob(other); e != nil {
				t.Fatal(e)
			}
			h := SecureHandler(s, "http://localhost:5173", auth, AuthOptions{})
			path := "/api/v1/projects/" + p.ID + "/creative-jobs/" + job.ID + "/avatar-reviews?space_id=" + space
			body := projectJSON(t, AvatarReviewInput{ComparedJobID: other.ID, AssetID: job.AssetID, Outcome: "adopted", Identity: true, LipSync: true, Audio: true, Disclosure: true})
			authRequest(t, h, "POST", path, body, token, "", 403)
			viewer, vt, vs, e := auth.login("review-viewer@example.test", "synthetic-review-password", true)
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
			authRequest(t, h, "POST", path, body, vt, vs.CSRF, 403)
			_, ft, fs, e := auth.login("review-foreign@example.test", "synthetic-foreign-password", true)
			if e != nil {
				t.Fatal(e)
			}
			authRequest(t, h, "POST", path, body, ft, fs.CSRF, 403)
			response := authRequest(t, h, "POST", path, body, token, session.CSRF, 200)
			var review AvatarReview
			if json.Unmarshal(response.Body.Bytes(), &review) != nil || review.ReviewedBy != actor || review.ComparedJobID != other.ID || review.ComparedAssetID != other.AssetID {
				t.Fatal("review actor not server bound", response.Body.String())
			}
		})
	}
}

func TestAvatarAdmissionErrorsRemainActionable(t *testing.T) {
	for _, item := range []struct {
		code   string
		status int
	}{{"avatar_cloud_transfer_denied", 403}, {"avatar_authorization_revoked", 403}, {"avatar_cloud_input_window", 409}, {"avatar_cloud_configuration_changed", 409}} {
		w := httptest.NewRecorder()
		storeError(w, providerFailure(item.code))
		if w.Code != item.status || !strings.Contains(w.Body.String(), `"code":"`+item.code+`"`) {
			t.Fatal(item, w.Code, w.Body.String())
		}
	}
}

func TestAvatarCloudWorkerErrorsPreserveSafeActionableClassification(t *testing.T) {
	for _, code := range []string{"avatar_cloud_transfer_denied", "avatar_cloud_configuration_changed", "avatar_cloud_input_window", "avatar_cloud_task_failed", "avatar_cloud_task_unknown"} {
		actual, message := creativeFailure(providerFailure(code))
		if actual != code || message != providerMessage(code) {
			t.Fatal(code, actual, message)
		}
	}
}
