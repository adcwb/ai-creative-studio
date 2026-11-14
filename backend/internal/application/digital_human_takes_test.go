package application

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestAvatarTakeComparisonPinsExactSameSourceAndPreservesHistory(t *testing.T) {
	for _, mode := range []string{"file", "postgres"} {
		t.Run(mode, func(t *testing.T) {
			s := testStore(t)
			if mode == "postgres" {
				s = postgresTestStore(t, postgresTestDB(t))
			}
			p, _, in, video := avatarFixture(t, s, "takes-space")
			s.creative.avatarPhoto = &avatarFixtureProvider{video: video}
			makeTake := func(key string) CreativeJob {
				job, _, e := s.createCreativeJob(p.SpaceID, p.ID, key, in)
				if e != nil {
					t.Fatal(e)
				}
				setAvatarTestRunning(t, s, &job)
				if e = s.executeCreativeJob(context.Background(), &job); e != nil {
					t.Fatal(e)
				}
				job.Status = "succeeded"
				if e = s.creative.updateJob(job); e != nil {
					t.Fatal(e)
				}
				return job
			}
			a, b := makeTake("take-a"), makeTake("take-b")
			input := AvatarReviewInput{ComparedJobID: b.ID, AssetID: a.AssetID, Outcome: "adopted", Identity: true, LipSync: true, Audio: true, Disclosure: true}
			review, e := s.reviewAvatar(p.SpaceID, p.ID, a.ID, "compare-adopt", "reviewer", input)
			if e != nil || review.ComparedJobID != b.ID || review.ComparedAssetID != b.AssetID {
				t.Fatal(review, e)
			}
			changed := input
			changed.ComparedJobID = a.ID
			if _, e = s.reviewAvatar(p.SpaceID, p.ID, a.ID, "compare-adopt", "reviewer", changed); e == nil {
				t.Fatal("same-key comparison changed")
			}
			changed.ExpectedVersion = 1
			if _, e = s.reviewAvatar(p.SpaceID, p.ID, a.ID, "self-compare", "reviewer", changed); e == nil {
				t.Fatal("take compared with itself")
			}
			changed.ComparedJobID = "other-space-take"
			if _, e = s.reviewAvatar(p.SpaceID, p.ID, a.ID, "foreign-compare", "reviewer", changed); e == nil {
				t.Fatal("foreign or absent take accepted")
			}
			// A distinct likeness grant remains a distinct source even for the same photo.
			source := s.creative.data.DigitalHumans[in.AvatarID]
			avatar, _, e := s.createDigitalHuman(p.SpaceID, p.ID, "new-likeness", "test-actor", DigitalHumanInput{Name: "Another authorization", ReferenceAssetID: source.ReferenceAssetID, ConsentReference: "different-grant", LikenessConfirmed: true, SinglePersonConfirmed: true})
			if e != nil {
				t.Fatal(e)
			}
			in.AvatarID = avatar.ID
			c := makeTake("take-c")
			changed.ComparedJobID = c.ID
			if _, e = s.reviewAvatar(p.SpaceID, p.ID, a.ID, "different-source", "reviewer", changed); e == nil {
				t.Fatal("different authorization mixed into comparison")
			}
			next := AvatarReviewInput{ExpectedVersion: 1, ComparedJobID: b.ID, AssetID: a.AssetID, Outcome: "rework", Note: "第二版口型更好，保留此版作为备选"}
			if _, e = s.reviewAvatar(p.SpaceID, p.ID, a.ID, "compare-rework", "reviewer", next); e != nil {
				t.Fatal(e)
			}
			replay, e := s.reviewAvatar(p.SpaceID, p.ID, a.ID, "compare-adopt", "reviewer", input)
			if e != nil || replay != review {
				t.Fatal("comparison receipt overwritten", e)
			}
			if mode == "postgres" {
				dir := filepath.Join(t.TempDir(), "take-backup")
				if _, e = ExportPostgresData(context.Background(), dir, s.pg.db); e != nil {
					t.Fatal(e)
				}
				restored, e := OpenStore(dir)
				if e != nil {
					t.Fatal(e)
				}
				defer restored.Close()
				history := restored.creative.data.Jobs[a.ID].AvatarReviews
				if len(history) != 2 || history[0] != review {
					t.Fatal("comparison backup lost exact assets")
				}
			}
			raw, _ := json.Marshal(publicCreativeJob(s.creative.data.Jobs[a.ID]))
			var public CreativeJob
			if json.Unmarshal(raw, &public) != nil || len(public.AvatarReviews) != 2 {
				t.Fatal("comparison history unavailable to scoped client")
			}
		})
	}
}
