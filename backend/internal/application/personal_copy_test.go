package application

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/library"
	"github.com/adcwb/ai-creative-studio/backend/internal/modules/media"
	"github.com/adcwb/ai-creative-studio/backend/internal/modules/publishing"
	"github.com/adcwb/ai-creative-studio/backend/internal/modules/revisions"
	"github.com/adcwb/ai-creative-studio/backend/internal/persistence/libraryrepo"
	"github.com/adcwb/ai-creative-studio/backend/internal/persistence/revisionrepo"
)

func personalCopyFixture(t *testing.T, mode string) (*FileStore, *AuthStore, AuthUser, string, string, authSession, Project, string) {
	t.Helper()
	s := testStore(t)
	a, e := OpenAuthStore(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	if mode == "postgres" {
		db := postgresTestDB(t)
		s = postgresTestStore(t, db)
		a, e = OpenPostgresAuthStore(t.TempDir(), db)
		if e != nil {
			t.Fatal(e)
		}
	}
	u, token, session, e := a.login("copy-owner@example.test", "synthetic-copy-password", true)
	if e != nil {
		t.Fatal(e)
	}
	source := ""
	a.mu.Lock()
	for _, o := range a.data.Organizations {
		if o.OwnerID == u.ID && spaceType(o) == "personal" {
			source = o.ID
		}
	}
	a.mu.Unlock()
	if source == "" {
		t.Fatal("missing personal space")
	}
	target := testTeamSpace(t, a, u.ID)
	ConfigureManagedRuntime(s, a)
	p, _, e := s.CreateProject(source, "personal-copy-source", ProjectInput{Title: "完整保留源作品", Brief: "同一人物从雨夜走到门前，明确复制到团队。"})
	if e != nil {
		t.Fatal(e)
	}
	state, e := s.media.AddAsset(p.ID, source, "copy-source-frame", "原素材", "shot", "", "", "uploaded", syntheticCreativePNG(), "image/png")
	if e != nil {
		t.Fatal(e)
	}
	timeline := MediaTimeline{Clips: []MediaClip{{ID: "first", AssetID: state.Assets[0].ID, Duration: 2, Subtitle: "谁在门外？", AudioVolume: 1}}, MusicVolume: .2}
	state, e = s.media.SaveTimeline(p.ID, source, "copy-original-timeline", state.Version, timeline)
	if e != nil {
		t.Fatal(e)
	}
	r, e := s.CaptureProductionRevision(context.Background(), source, p.ID, "main", u.ID, "copy-original-revision", revisionCaptureInput{ExpectedProjectVersion: p.Version, ExpectedMediaVersion: state.Version, Label: "原剪辑"}, nil)
	if e != nil {
		t.Fatal(e)
	}
	_, e = s.ForkProductionRevision(context.Background(), source, p.ID, u.ID, "copy-fork", revisionForkInput{SourceID: r.Revision.ID, Name: "另一剪辑方向"}, nil)
	if e != nil {
		t.Fatal(e)
	}
	export := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	installSeasonDeliveryFixture(t, s, p, export)
	_, e = s.media.saveDeliveryMetadata(context.Background(), source, p.ID, export, "copy-release", u.ID, publishing.DeliveryInput{Title: "正式发布资料", Description: "保留标题与封面版本", Tags: []string{"短剧"}, CoverAssetID: state.Assets[0].ID, RightsConfirmed: true})
	if e != nil {
		t.Fatal(e)
	}
	return s, a, u, source, target, session, p, token
}

func TestPersonalCopyKeepsSourceMapsMediaHistoryAndRecovers(t *testing.T) {
	for _, mode := range []string{"file", "postgres"} {
		t.Run(mode, func(t *testing.T) {
			s, a, u, source, target, session, p, token := personalCopyFixture(t, mode)
			ctx := context.Background()
			snap, e := s.personalCopySnapshot(ctx, source, p.ID)
			if e != nil {
				t.Fatal(e)
			}
			digest := personalCopyDigest(source, target, snap)
			j, e := s.createPersonalCopy(ctx, u.ID, source, p.ID, target, "copy-explicit-request", digest)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = s.GetProject(target, j.TargetID); !errors.Is(e, ErrNotFound) {
				t.Fatal("unfinished target visible", e)
			}
			first, e := s.continuePersonalCopy(ctx, j.ID)
			if e != nil || len(first.Copied) != 1 {
				t.Fatal(first, e)
			}
			// Resume through a reopened store/independent API. This local PG fixture
			// uses shared disk; it does not claim distributed S3 was exercised.
			if mode == "file" {
				dir := filepath.Dir(s.path)
				if e = s.Close(); e != nil {
					t.Fatal(e)
				}
				s, e = OpenStore(dir)
				if e != nil {
					t.Fatal(e)
				}
				t.Cleanup(func() { s.Close() })
			} else {
				old := s
				s = postgresTestStore(t, s.pg.db)
				s.media.dir = old.media.dir
			}
			ConfigureManagedRuntime(s, a)
			// Simulate a crash after the first destination journal record became
			// durable. Its exact prefix must reconcile on the eventual full import.
			partial, e := revisionrepo.CloneRecords(j.Snapshot.Revisions, revisionrepo.Scope{Space: target, Project: j.TargetID}, j.ID, func(m revisions.Manifest) (revisions.Manifest, error) { return rewritePersonalManifest(j, m) })
			if e != nil {
				t.Fatal(e)
			}
			if len(partial) > 0 {
				if e = s.revisions.ImportCopy(ctx, revisionrepo.Scope{Space: target, Project: j.TargetID}, partial[:1]); e != nil {
					t.Fatal(e)
				}
			}
			for i := 0; i < 2*len(j.Snapshot.Files)+2; i++ {
				j, e = s.continuePersonalCopy(ctx, j.ID)
				if e != nil {
					t.Fatal("continue", e)
				}
				if j.State == "completed" {
					break
				}
			}
			if j.State != "completed" {
				t.Fatal(j)
			}
			copy, e := s.GetProject(target, j.TargetID)
			if e != nil || copy.SpaceID != target || copy.PersonalCopyID != j.ID || copy.LatestGenerationID != "" {
				t.Fatal(copy, e)
			}
			state, e := s.media.GetChecked(copy.ID, target)
			if e != nil {
				t.Fatal(e)
			}
			if len(state.Assets) != 1 || state.Assets[0].ID == snap.Media.Assets[0].ID || state.Timeline.Clips[0].AssetID != state.Assets[0].ID || state.Social != nil {
				t.Fatal(state)
			}
			if state.Assets[0].Source != snap.Media.Assets[0].Source {
				t.Fatal("copy rewrote original real/synthetic provenance")
			}
			if len(state.DeliveryMetadata) != 1 || state.DeliveryMetadata[0].CoverAssetID != state.Assets[0].ID || state.DeliveryMetadata[0].ExportID != state.Exports[0].ID || state.DeliveryMetadata[0].Title != "正式发布资料" {
				t.Fatal("metadata mapping", state.DeliveryMetadata)
			}
			journal, e := s.revisions.SnapshotScope(ctx, revisionrepo.Scope{Space: target, Project: copy.ID})
			if e != nil || len(journal) != len(snap.Revisions) {
				t.Fatal(journal, e)
			}
			for _, r := range journal {
				if r.Revision != nil {
					var tline MediaTimeline
					if json.Unmarshal(r.Revision.Manifest.Timeline, &tline) != nil || tline.Clips[0].AssetID != state.Assets[0].ID || r.Revision.ProjectID != copy.ID {
						t.Fatal(r)
					}
				}
			}
			if e = s.revisions.ImportCopy(ctx, revisionrepo.Scope{Space: target, Project: copy.ID}, partial); e != nil {
				t.Fatal("identical journal retry", e)
			}
			before := mediaDigest(state)
			replayed, e := s.createPersonalCopy(ctx, u.ID, source, p.ID, target, "copy-explicit-request", digest)
			if e != nil || replayed.ID != j.ID {
				t.Fatal(replayed, e)
			}
			if _, e = s.continuePersonalCopy(ctx, j.ID); e != nil {
				t.Fatal(e)
			}
			if mediaDigest(s.media.Get(copy.ID, target)) != before {
				t.Fatal("replay overwrote copied work")
			}
			after, e := s.personalCopySnapshot(ctx, source, p.ID)
			if e != nil || !reflect.DeepEqual(after, snap) {
				t.Fatal("source changed", e)
			}
			h := SecureHandler(s, "http://localhost:5173", a, AuthOptions{})
			base := "/api/v1/projects/" + p.ID + "/personal-copies"
			authRequest(t, h, "POST", base+"/"+j.ID+"/continue?space_id="+source, projectJSON(t, map[string]any{"expected_version": 1}), token, session.CSRF, 200)
			w := authRequest(t, h, "GET", "/api/v1/projects/"+copy.ID+"/media/exports/"+state.Exports[0].ID+"/delivery.zip?space_id="+target, "", token, session.CSRF, 200)
			if w.Header().Get("Content-Type") != "application/zip" {
				t.Fatal(w.Header())
			}
			if mode == "postgres" {
				var count int
				if e = s.pg.db.QueryRow(`SELECT count(*) FROM studio_personal_copies WHERE tenant_id=$1 AND parent_id=$2`, source, p.ID).Scan(&count); e != nil || count != 1 {
					t.Fatal("copy row scope", count, e)
				}
				dir := filepath.Join(t.TempDir(), "copy-roundtrip")
				if _, e = ExportPostgresData(ctx, dir, s.pg.db); e != nil {
					t.Fatal(e)
				}
				for _, f := range j.Snapshot.Files {
					for _, name := range []string{f.Source, f.Target} {
						data, e := os.ReadFile(filepath.Join(s.media.dir, name))
						if e != nil {
							t.Fatal(e)
						}
						if e = os.WriteFile(filepath.Join(dir, "media", name), data, 0600); e != nil {
							t.Fatal(e)
						}
					}
				}
				restored, e := OpenStore(dir)
				if e != nil {
					t.Fatal("copy export reopen", e)
				}
				defer restored.Close()
				restoredJob, e := restored.personalCopies.get(j.ID)
				if e != nil || restoredJob.State != "completed" {
					t.Fatal("copy checkpoint lost on export", e)
				}
				if _, e = restored.GetProject(target, j.TargetID); e != nil {
					t.Fatal("copy target lost on export", e)
				}
				db := postgresTestDB(t)
				if e = restored.Close(); e != nil {
					t.Fatal(e)
				}
				if _, e = ImportPostgresData(ctx, dir, db); e != nil {
					t.Fatal("copy file to PG", e)
				}
				again := postgresTestStore(t, db)
				if _, e = again.personalCopies.get(j.ID); e != nil {
					t.Fatal("copy grant lost on PG restore", e)
				}
			}
		})
	}
}

func TestPersonalCopyPreservesRevocableLibraryAuthority(t *testing.T) {
	for _, mode := range []string{"file", "postgres"} {
		t.Run(mode, func(t *testing.T) {
			s, a, u, source, target, session, p, token := personalCopyFixture(t, mode)
			ctx := context.Background()
			h := SecureHandler(s, "http://localhost:5173", a, AuthOptions{})
			body := libraryWrite{Kind: "character", Card: library.Card{Name: "固定主角", Character: &library.Character{Appearance: "绿衣"}, ProhibitedChanges: []string{}}, References: []libraryReferenceInput{}}
			w := authRequest(t, h, "POST", "/api/v1/library/assets?space_id="+source, projectJSON(t, body), token, session.CSRF, 201)
			var asset libraryrepo.Result
			if json.Unmarshal(w.Body.Bytes(), &asset) != nil {
				t.Fatal(w.Body.String())
			}
			_, e := s.library.SetAuthorization(ctx, source, asset.Asset.ID, "copy-library-grant", "copy-library-grant", u.ID, library.AuthorizationInput{Status: "granted", VersionIDs: []string{asset.Version.ID}, Purposes: []string{"prompt_context", "image_reference"}, Holder: "Synthetic owner", Basis: "own_work", Evidence: "Synthetic local fixture", Attested: true}, time.Now())
			if e != nil {
				t.Fatal(e)
			}
			state := s.media.Get(p.ID, source)
			state, e = s.media.bindLibrary(ctx, p.ID, source, "copy-character-binding", libraryBindingInput{ExpectedVersion: state.Version, AssetID: asset.Asset.ID, VersionID: asset.Version.ID, Usage: "character"}, "copy-character-binding", asset.Version)
			if e != nil {
				t.Fatal(e)
			}
			snap, e := s.personalCopySnapshot(ctx, source, p.ID)
			if e != nil {
				t.Fatal(e)
			}
			j, e := s.createPersonalCopy(ctx, u.ID, source, p.ID, target, "copy-with-library", personalCopyDigest(source, target, snap))
			if e != nil {
				t.Fatal(e)
			}
			for i := 0; i < 2*len(j.Snapshot.Files)+2; i++ {
				j, e = s.continuePersonalCopy(ctx, j.ID)
				if e != nil {
					t.Fatal(e)
				}
				if j.State == "completed" {
					break
				}
			}
			state = s.media.Get(j.TargetID, target)
			binding := state.LibraryBindings[0]
			_, current, v, e := s.boundLibraryVersion(ctx, j.TargetID, target, binding.ID)
			if e != nil || v.ID != asset.Version.ID || current.ID != asset.Asset.ID || binding.ShareID != j.ID {
				t.Fatal(binding, e)
			}
			in := CreativeJobInput{Kind: "image", Prompt: "保留同一人物", LibraryInputs: []libraryInputChoice{{BindingID: binding.ID, Mode: "prompt_only"}}}
			pins, _, e := s.compileLibraryInputs(ctx, j.TargetID, target, &in, Capabilities{Ready: true, Mode: "real", Provider: "gateway-images"})
			if e != nil || len(pins) != 1 || pins[0].Uses[0].Source.ShareID != j.ID {
				t.Fatal(pins, e)
			}
			if e = s.checkLibraryInputPins(ctx, target, pins); e != nil {
				t.Fatal(e)
			}
			var calls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(200) }))
			defer upstream.Close()
			guarded := s.withLibraryDispatchGuard(ctx, &CreativeJob{SpaceID: target, ProjectID: j.TargetID, Kind: "image", LibraryInputs: pins})
			dispatch := func() error {
				req, e := http.NewRequestWithContext(guarded, "POST", upstream.URL, nil)
				if e != nil {
					return e
				}
				res, e := libraryGuardedRequest(upstream.Client(), req)
				if res != nil {
					res.Body.Close()
				}
				return e
			}
			if e = dispatch(); e != nil || calls.Load() != 1 {
				t.Fatal("authorized synthetic dispatch", calls.Load(), e)
			}
			if _, e = s.library.Get(ctx, target, asset.Asset.ID); !errors.Is(e, libraryrepo.ErrNotFound) {
				t.Fatal("copy granted personal library browse", e)
			}
			_, e = s.library.SetAuthorization(ctx, source, asset.Asset.ID, "copy-library-revoke", "copy-library-revoke", u.ID, library.AuthorizationInput{Expected: 1, Status: "revoked", Reason: "Owner revoked future use"}, time.Now())
			if e != nil {
				t.Fatal(e)
			}
			if e = s.checkLibraryInputPins(ctx, target, pins); e == nil {
				t.Fatal("source revocation ignored")
			}
			if e = dispatch(); e == nil || calls.Load() != 1 {
				t.Fatal("revoked source reached upstream", calls.Load(), e)
			}
			// The copy's own grant is independently revocable and may never become
			// a grant for an arbitrary private asset/version or another destination.
			for _, src := range []media.LibrarySource{{ShareID: j.ID, AssetID: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", VersionID: asset.Version.ID}, {ShareID: j.ID, AssetID: asset.Asset.ID, VersionID: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}} {
				if _, e = s.librarySourceSpace(ctx, target, src); e == nil {
					t.Fatal("broadened copy grant")
				}
			}
			authRequest(t, h, "POST", "/api/v1/projects/"+p.ID+"/personal-copies/"+j.ID+"/revoke?space_id="+source, projectJSON(t, map[string]any{"expected_version": j.Version, "confirmed": true}), token, session.CSRF, 200)
			if _, _, _, e = s.boundLibraryVersion(ctx, j.TargetID, target, binding.ID); e == nil {
				t.Fatal("copy revoke ignored")
			}
			if _, e = s.GetProject(target, j.TargetID); e != nil {
				t.Fatal("revocation destroyed history", e)
			}
		})
	}
}

func TestPersonalCopyRejectsTamperingAndCurrentPermissionLoss(t *testing.T) {
	s, a, u, source, target, session, p, token := personalCopyFixture(t, "file")
	ctx := context.Background()
	snap, e := s.personalCopySnapshot(ctx, source, p.ID)
	if e != nil {
		t.Fatal(e)
	}
	digest := personalCopyDigest(source, target, snap)
	if _, e = s.createPersonalCopy(ctx, "other-user", source, p.ID, target, "copy-other", digest); !errors.Is(e, errRuntimeDenied) {
		t.Fatal(e)
	}
	j, e := s.createPersonalCopy(ctx, u.ID, source, p.ID, target, "copy-protected", digest)
	if e != nil {
		t.Fatal(e)
	}
	file := j.Snapshot.Files[0]
	path, e := s.media.localBlob(source, p.ID, file.Source)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(path, []byte("tampered"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = s.continuePersonalCopy(ctx, j.ID); e == nil {
		t.Fatal("tampered bytes copied")
	}
	if _, e = s.GetProject(target, j.TargetID); !errors.Is(e, ErrNotFound) {
		t.Fatal("failed target published", e)
	}
	h := SecureHandler(s, "http://localhost:5173", a, AuthOptions{})
	authRequest(t, h, "POST", "/api/v1/projects/"+p.ID+"/personal-copies?space_id="+source, projectJSON(t, map[string]any{"target_space_id": target, "preview_sha256": digest, "confirmed": false}), token, session.CSRF, 400)
	// Database/file access failure never falls back to a cached copy grant.
	s.personalCopies.failure = errors.New("synthetic storage failure")
	if _, e = s.personalCopies.get(j.ID); e == nil {
		t.Fatal("failed store served cached grant")
	}
}

func TestPersonalCopyStopsWhenDestinationWritePermissionIsRevoked(t *testing.T) {
	for _, mode := range []string{"file", "postgres"} {
		t.Run(mode, func(t *testing.T) {
			s, a, u, source, _, session, p, token := personalCopyFixture(t, mode)
			other, otherToken, otherSession, e := a.login("copy-team-owner@example.test", "synthetic-copy-password", true)
			if e != nil {
				t.Fatal(e)
			}
			target := testTeamSpace(t, a, other.ID)
			h := SecureHandler(s, "http://localhost:5173", a, AuthOptions{})
			path := "/api/v1/organizations/" + target + "/members"
			authRequest(t, h, "PUT", path, testMemberInput(t, a, u.Email, "editor", 1), otherToken, otherSession.CSRF, 200)
			snap, e := s.personalCopySnapshot(context.Background(), source, p.ID)
			if e != nil {
				t.Fatal(e)
			}
			j, e := s.createPersonalCopy(context.Background(), u.ID, source, p.ID, target, "copy-current-grants", personalCopyDigest(source, target, snap))
			if e != nil {
				t.Fatal(e)
			}
			authRequest(t, h, "PUT", path, testMemberInput(t, a, u.Email, "viewer", 2), otherToken, otherSession.CSRF, 200)
			base := "/api/v1/projects/" + p.ID + "/personal-copies"
			authRequest(t, h, "POST", base+"/"+j.ID+"/continue?space_id="+source, projectJSON(t, map[string]any{"expected_version": j.Version}), token, session.CSRF, 403)
			if _, e = s.GetProject(target, j.TargetID); !errors.Is(e, ErrNotFound) {
				t.Fatal("revoked actor published", e)
			}
			authRequest(t, h, "GET", base+"?space_id="+source, "", token, session.CSRF, 200)
		})
	}
}

func TestPersonalCopyDoesNotClaimUnsupportedNativeArchives(t *testing.T) {
	s, _, _, space, _, _, p, _ := personalCopyFixture(t, "file")
	for _, kind := range []string{"image", "digital-human"} {
		s.mu.Lock()
		d := s.clone()
		v := d.Projects[p.ID]
		v.Kind = kind
		d.Projects[p.ID] = v
		e := s.commit(d)
		s.mu.Unlock()
		if e != nil {
			t.Fatal(e)
		}
		if _, e = s.personalCopySnapshot(context.Background(), space, p.ID); !errors.Is(e, ErrInvalidProjectInput) {
			t.Fatal("unsupported native archive copied incompletely", kind, e)
		}
	}
}
