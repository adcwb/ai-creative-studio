package application

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/library"
	"github.com/adcwb/ai-creative-studio/backend/internal/persistence/libraryrepo"
)

func libraryDispatchFixture(t *testing.T, s *FileStore) (Project, libraryrepo.Result, string) {
	t.Helper()
	space := "personal-demo"
	p, _, e := s.CreateProject(space, "dispatch-source", ProjectInput{Title: "授权参考", Brief: "纯本地合成上游验证"})
	if e != nil {
		t.Fatal(e)
	}
	state, e := s.media.AddAsset(p.ID, space, "source-image", "合成参考", "reference", "", "", "uploaded", syntheticCreativePNG(), "image/png")
	if e != nil {
		t.Fatal(e)
	}
	in := libraryWrite{Kind: "character", Card: library.Card{Name: "主角", Character: &library.Character{}, ProhibitedChanges: []string{}}, References: []libraryReferenceInput{{ProjectID: p.ID, AssetID: state.Assets[0].ID, Role: "reference", ViewAngle: "front"}}}
	var a libraryrepo.Result
	_ = json.Unmarshal(intentLibraryCall(t, s, "POST", "/api/v1/library/assets?space_id="+space, "create-dispatch-card", in, 201), &a)
	state = s.media.Get(p.ID, space)
	binding := libraryBindingInput{ExpectedVersion: state.Version, AssetID: a.Asset.ID, VersionID: a.Version.ID, Usage: "character"}
	state, e = s.media.bindLibrary(context.Background(), p.ID, space, "copy-dispatch-card", binding, "copy", a.Version)
	if e != nil {
		t.Fatal(e)
	}
	return p, a, state.LibraryBindings[0].MediaAssetIDs[0]
}
func setLibraryDispatchAuthorization(t *testing.T, s *FileStore, a libraryrepo.Result, expected int64, key, status string) LibraryAuthorizationView {
	t.Helper()
	in := library.AuthorizationInput{Expected: expected, Status: status, Reason: "synthetic declaration change"}
	if status == "granted" {
		in.VersionIDs = []string{a.Version.ID}
		in.Purposes = []string{"image_reference", "video_reference"}
		in.Holder = "Synthetic creator"
		in.Basis = "own_work"
		in.Evidence = "Synthetic fixture made locally"
		in.Attested = true
	}
	var view LibraryAuthorizationView
	_ = json.Unmarshal(intentLibraryCall(t, s, "POST", "/api/v1/library/assets/"+a.Asset.ID+"/use-authorization?space_id="+a.Asset.SpaceID, key, in, 200), &view)
	return view
}
func TestLibraryAuthorizationGatesActualImageDispatchAndPreservesLineage(t *testing.T) {
	for _, mode := range []string{"file", "postgres"} {
		t.Run(mode, func(t *testing.T) {
			s := testStore(t)
			if mode == "postgres" {
				s = postgresTestStore(t, postgresTestDB(t))
			}
			p, a, id := libraryDispatchFixture(t, s)
			var calls atomic.Int32
			output := pngFixture(t, 1536, 864)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.URL.Path != "/images/edits" || r.Method != "POST" {
					t.Error("unexpected native route")
				}
				if e := r.ParseMultipartForm(4 << 20); e != nil {
					t.Error(e)
				}
				f, header, e := r.FormFile("image[]")
				if e != nil {
					t.Error(e)
				} else {
					raw, _ := io.ReadAll(f)
					f.Close()
					if !reflect.DeepEqual(raw, syntheticCreativePNG()) || header.Header.Get("Content-Type") != "image/png" {
						t.Error("reference bytes/MIME omitted")
					}
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]string{"b64_json": base64.StdEncoding.EncodeToString(output)}}})
			}))
			defer upstream.Close()
			provider := newOpenAIProvider("synthetic", upstream.URL+"/images/generations", upstream.Client())
			s.creative.image = provider
			s.creative.imageCaps = Capabilities{Ready: true, Mode: "real", Provider: "openai-images", Model: provider.model}
			input := CreativeJobInput{Kind: "image", Prompt: "合成雨夜场景", ExpectedVersion: p.Version, ExpectedProvider: "openai-images", ExpectedModel: provider.model, ReferenceAssetID: id}
			if _, _, e := s.createCreativeJob(p.SpaceID, p.ID, "no-grant", input); e == nil {
				t.Fatal("unattested dispatch accepted")
			}
			missing := map[string]any{"status": "granted", "version_ids": []string{a.Version.ID}, "purposes": []string{"image_reference"}, "rights_holder": "creator", "basis": "own_work", "evidence": "synthetic", "attested": true}
			intentLibraryCall(t, s, "POST", "/api/v1/library/assets/"+a.Asset.ID+"/use-authorization?space_id="+a.Asset.SpaceID, "missing-required-revision", missing, 400)
			missing["expected_revision"] = nil
			intentLibraryCall(t, s, "POST", "/api/v1/library/assets/"+a.Asset.ID+"/use-authorization?space_id="+a.Asset.SpaceID, "null-required-revision", missing, 400)
			granted := setLibraryDispatchAuthorization(t, s, a, 0, "grant-first", "granted")
			job, _, e := s.createCreativeJob(p.SpaceID, p.ID, "first-image", input)
			if e != nil || len(job.LibraryMediaPins) != 1 {
				t.Fatal(job, e)
			}
			if e = s.executeCreativeJob(context.Background(), &job); e != nil {
				t.Fatal(e)
			}
			result, e := s.media.AssetForReceipt(p.ID, p.SpaceID, "creative-"+job.ID)
			if e != nil || len(result.LibrarySources) != 1 || len(result.ContentSHA256) != 64 || calls.Load() != 1 {
				t.Fatal("atomic output lineage", result, e, calls.Load())
			}
			queued, _, e := s.createCreativeJob(p.SpaceID, p.ID, "queued-image", input)
			if e != nil {
				t.Fatal(e)
			}
			setLibraryDispatchAuthorization(t, s, a, 1, "revoke-first", "revoked")
			if e = s.executeCreativeJob(context.Background(), &queued); e == nil || calls.Load() != 1 {
				t.Fatal("queued authorization bypass", e, calls.Load())
			}
			replay, created, e := s.createCreativeJob(p.SpaceID, p.ID, "queued-image", input)
			if e != nil || created || replay.ID != queued.ID {
				t.Fatal("accepted key became fresh intent", e)
			}
			receipt := setLibraryDispatchAuthorization(t, s, a, 0, "grant-first", "granted")
			if !reflect.DeepEqual(receipt, granted) {
				t.Fatal("original receipt changed")
			}
			current, _ := s.library.Get(context.Background(), p.SpaceID, a.Asset.ID)
			if current.Asset.UseAuthorization.Status != "revoked" {
				t.Fatal("replayed receipt resurrected permission")
			}
			derived := input
			derived.ReferenceAssetID = result.ID
			if _, _, e = s.createCreativeJob(p.SpaceID, p.ID, "derived-revoked", derived); e == nil {
				t.Fatal("derived media laundered rights")
			}

			stateNow := s.media.Get(p.ID, p.SpaceID)
			normalized, e := s.media.normalizeSocialPhoto(p.ID, p.SpaceID, "normalize-revoked", photoNormalize{ExpectedVersion: stateNow.Version, AssetID: id, Confirmed: true})
			if e != nil {
				t.Fatal(e)
			}
			normalizedID := normalized.Assets[len(normalized.Assets)-1].ID
			if _, e = s.pinCreativeLibraryMedia(context.Background(), p.ID, p.SpaceID, CreativeJobInput{Kind: "image", ReferenceAssetID: normalizedID}); e == nil {
				t.Fatal("JPEG normalization erased revocation")
			}
			poster := PosterDocument{Width: 256, Height: 256, Background: "#123456", Layers: []PosterLayer{{ID: "scene", Kind: "logo", AssetID: normalizedID, X: 0, Y: 0, Width: 256, Height: 256, Opacity: 1}}}
			saved, e := s.media.SavePoster(p.ID, p.SpaceID, "save-library-poster", normalized.Version, poster)
			if e != nil {
				t.Fatal(e)
			}
			rendered, e := s.media.ExportPoster(p.ID, p.SpaceID, "render-library-poster", saved.Version)
			if e != nil {
				t.Fatal(e)
			}
			posterID := rendered.Assets[len(rendered.Assets)-1].ID
			if _, e = s.pinCreativeLibraryMedia(context.Background(), p.ID, p.SpaceID, CreativeJobInput{Kind: "video", ReferenceAssetID: posterID}); e == nil {
				t.Fatal("poster composition erased revocation")
			}
			// Copying a derived result into another library card retains its ancestors.
			newCard := libraryWrite{Kind: "scene", Card: library.Card{Name: "衍生场景", Scene: &library.Scene{}, ProhibitedChanges: []string{}}, References: []libraryReferenceInput{{ProjectID: p.ID, AssetID: result.ID, Role: "reference"}}}
			var child libraryrepo.Result
			_ = json.Unmarshal(intentLibraryCall(t, s, "POST", "/api/v1/library/assets?space_id="+p.SpaceID, "derived-card", newCard, 201), &child)
			state := s.media.Get(p.ID, p.SpaceID)
			state, e = s.media.bindLibrary(context.Background(), p.ID, p.SpaceID, "copy-derived", libraryBindingInput{ExpectedVersion: state.Version, AssetID: child.Asset.ID, VersionID: child.Version.ID, Usage: "scene"}, "derived", child.Version)
			if e != nil {
				t.Fatal(e)
			}
			childID := state.LibraryBindings[len(state.LibraryBindings)-1].MediaAssetIDs[0]
			setLibraryDispatchAuthorization(t, s, child, 0, "grant-child", "granted")
			derived.ReferenceAssetID = childID
			if _, _, e = s.createCreativeJob(p.SpaceID, p.ID, "child-revoked", derived); e == nil {
				t.Fatal("new card removed ancestor revocation")
			}
			setLibraryDispatchAuthorization(t, s, a, 2, "grant-again", "granted")
			if e = s.executeCreativeJob(context.Background(), &queued); e == nil || calls.Load() != 1 {
				t.Fatal("regrant revived stale queued intent", e)
			}
			if _, _, e = s.createCreativeJob(p.SpaceID, p.ID, "child-reconfirmed", derived); e != nil {
				t.Fatal("fresh explicit intent denied", e)
			}
			// Historical copies and immutable versions remain readable after revocation.
			if old, e := s.library.Version(context.Background(), p.SpaceID, a.Asset.ID, a.Version.ID); e != nil || !reflect.DeepEqual(old, a.Version) {
				t.Fatal("history damaged", e)
			}
			path, _, e := s.media.file(p.ID, p.SpaceID, id, false, false)
			if e != nil {
				t.Fatal(e)
			}
			original, e := os.ReadFile(path)
			if e != nil {
				t.Fatal(e)
			}
			if e = os.WriteFile(path, syntheticCreativePNG()[:100], 0600); e != nil {
				t.Fatal(e)
			}
			if _, _, e = s.createCreativeJob(p.SpaceID, p.ID, "tampered-input", input); !errors.Is(e, ErrBlobIntegrity) {
				t.Fatal("tampered bytes accepted", e)
			}
			if e = os.WriteFile(path, original, 0600); e != nil {
				t.Fatal(e)
			}
		})
	}
}
func TestLibraryRevocationImmediatelyBeforePOSTAndReadOnlyRecovery(t *testing.T) {
	s := testStore(t)
	p, a, id := libraryDispatchFixture(t, s)
	setLibraryDispatchAuthorization(t, s, a, 0, "grant-transport", "granted")
	input := CreativeJobInput{Kind: "video", ReferenceAssetID: id}
	pins, e := s.pinCreativeLibraryMedia(context.Background(), p.ID, p.SpaceID, input)
	if e != nil {
		t.Fatal(e)
	}
	job := CreativeJob{ProjectID: p.ID, SpaceID: p.SpaceID, Kind: "video", ReferenceAssetID: id, LibraryMediaPins: pins}
	ctx := s.withLibraryDispatchGuard(context.Background(), &job)
	if e = checkLibraryDispatch(ctx); e != nil {
		t.Fatal(e)
	}
	var dispatches, calls int
	transport := runtimeTransport{guard: func(context.Context) error { return nil }, lifetime: ctx, onDispatch: func() { dispatches++ }, base: runtimeFixtureTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{}`)), Header: http.Header{}}, nil
	})}
	// Revoke after preparation, before the actual HTTP boundary.
	setLibraryDispatchAuthorization(t, s, a, 1, "revoke-transport", "revoked")
	req, _ := http.NewRequestWithContext(ctx, "POST", "https://example.invalid/videos", strings.NewReader(`{}`))
	if _, e = transport.RoundTrip(req); e == nil || dispatches != 0 || calls != 0 {
		t.Fatal("revoked POST observed as sent", e, dispatches, calls)
	}
	// Known upstream read recovery remains permitted; no new generation POST.
	req, _ = http.NewRequestWithContext(ctx, "GET", "https://example.invalid/videos/existing", nil)
	response, e := transport.RoundTrip(req)
	if e != nil || calls != 1 {
		t.Fatal("revocation prevented read-only reconciliation", e)
	}
	response.Body.Close()
	// An acknowledged job can read the upstream after revoke, while failure or an
	// unknown response never grants permission to submit again.
	job.UpstreamID = "video_existing"
	var posts, gets atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			posts.Add(1)
			w.WriteHeader(500)
			return
		}
		gets.Add(1)
		if strings.HasSuffix(r.URL.Path, "/content") {
			w.WriteHeader(503)
			return
		}
		_ = json.NewEncoder(w).Encode(VideoJob{ID: job.UpstreamID, Status: "completed"})
	}))
	defer upstream.Close()
	video := newCreativeProvider("synthetic", upstream.URL, upstream.Client())
	job.Provider = "openai-video"
	job.Model = "synthetic-video"
	SetCreativeVideo(s, video, Capabilities{Ready: true, Mode: "real", Provider: job.Provider, Model: job.Model})
	if e = s.executeCreativeJob(context.Background(), &job); e == nil || posts.Load() != 0 || gets.Load() != 2 {
		t.Fatal("read recovery resubmitted or blocked", e, posts.Load(), gets.Load())
	}
	// Current revocations/expiry are checked on every new use, not cached in pins.
	setLibraryDispatchAuthorization(t, s, a, 2, "grant-expiring", "granted")
	current, _ := s.library.Get(context.Background(), p.SpaceID, a.Asset.ID)
	expiry := time.Now().Add(50 * time.Millisecond)
	in := library.AuthorizationInput{Expected: 3, Status: "granted", VersionIDs: []string{a.Version.ID}, Purposes: []string{"video_reference"}, Holder: "creator", Basis: "own_work", Evidence: "synthetic", Attested: true, ExpiresAt: &expiry}
	if _, e = s.library.SetAuthorization(context.Background(), p.SpaceID, a.Asset.ID, "expire", "expire", "creator", in, time.Now()); e != nil {
		t.Fatal(e)
	}
	_ = current
	<-time.After(time.Until(expiry))
	if _, e = s.pinCreativeLibraryMedia(context.Background(), p.ID, p.SpaceID, input); e == nil {
		t.Fatal("expired authorization used")
	}
}

func TestLibraryRevocationSurvivesEpisodeMigration(t *testing.T) {
	for _, mode := range []string{"file", "postgres"} {
		t.Run(mode, func(t *testing.T) {
			s := testStore(t)
			if mode == "postgres" {
				s = postgresTestStore(t, postgresTestDB(t))
			}
			p, season := episodeMigrationFixture(t, s)
			state := s.media.Get(p.ID, p.SpaceID)
			in := libraryWrite{Kind: "scene", Card: library.Card{Name: "迁移场景", Scene: &library.Scene{}, ProhibitedChanges: []string{}}, References: []libraryReferenceInput{{ProjectID: p.ID, AssetID: state.Assets[0].ID, Role: "reference"}}}
			var a libraryrepo.Result
			_ = json.Unmarshal(intentLibraryCall(t, s, "POST", "/api/v1/library/assets?space_id="+p.SpaceID, "migration-library-scene", in, 201), &a)
			state, e := s.media.bindLibrary(context.Background(), p.ID, p.SpaceID, "bind-migration-scene", libraryBindingInput{ExpectedVersion: state.Version, AssetID: a.Asset.ID, VersionID: a.Version.ID, Usage: "scene"}, "binding", a.Version)
			if e != nil {
				t.Fatal(e)
			}
			sourceID := state.LibraryBindings[0].MediaAssetIDs[0]
			normalized, e := s.media.normalizeSocialPhoto(p.ID, p.SpaceID, "normalize-migration-scene", photoNormalize{ExpectedVersion: state.Version, AssetID: sourceID, Confirmed: true})
			if e != nil {
				t.Fatal(e)
			}
			originalDerived := normalized.Assets[len(normalized.Assets)-1]
			sourceID = originalDerived.ID
			state = normalized

			if _, e = s.media.SaveTimeline(p.ID, p.SpaceID, "migration-library-timeline", state.Version, MediaTimeline{EpisodeNumber: 1, OutputResolution: "1080p", AspectRatio: "16:9", MusicVolume: .2, Clips: []MediaClip{{ID: "library-clip", AssetID: sourceID, Duration: .5}}}); e != nil {
				t.Fatal(e)
			}
			setLibraryDispatchAuthorization(t, s, a, 0, "migration-grant", "granted")
			setLibraryDispatchAuthorization(t, s, a, 1, "migration-revoke", "revoked")
			j := prepareMigration(t, s, p, season, 1, "migration-library-episode")
			for j.State != "completed" {
				j = migrationStep(t, s, j)
			}
			target := s.media.Get(j.TargetID, p.SpaceID)
			var copied *MediaAsset
			for i := range target.Assets {
				if target.Assets[i].ID == j.Copies[sourceID].TargetID {
					copied = &target.Assets[i]
					break
				}
			}
			if copied == nil || copied.LibrarySources[0].AssetID != a.Asset.ID || copied.ContentSHA256 != originalDerived.ContentSHA256 || len(copied.ParentAssetIDs) > 0 {
				t.Fatal("migration lost ancestry", copied)
			}
			if _, _, e = s.media.file(j.TargetID, p.SpaceID, copied.ID, false, false); e != nil {
				t.Fatal("history copy unavailable", e)
			}
			if _, e = s.pinCreativeLibraryMedia(context.Background(), j.TargetID, p.SpaceID, CreativeJobInput{Kind: "image", ReferenceAssetID: copied.ID}); e == nil {
				t.Fatal("migration reset authorization")
			}
		})
	}
}
