package application

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"reflect"
	"testing"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/library"
	"github.com/adcwb/ai-creative-studio/backend/internal/modules/media"
	"github.com/adcwb/ai-creative-studio/backend/internal/persistence/libraryrepo"
)

func stripLegacyProvenance(t *testing.T, s *FileStore, p Project, id string) {
	t.Helper()
	s.media.mu.Lock()
	defer s.media.mu.Unlock()
	d := s.media.clone()
	k := mediaKey(p.SpaceID, p.ID)
	state := mediaState(d, k)
	for i := range state.Assets {
		if state.Assets[i].ID == id {
			state.Assets[i].ProvenanceVersion = 0
			state.Assets[i].ProvenanceUnknown = false
			state.Assets[i].LibrarySources = nil
			state.Assets[i].ParentAssetIDs = nil
			state.Assets[i].ContentSHA256 = ""
		}
	}
	d.Projects[k] = state
	if e := s.media.commit(d); e != nil {
		t.Fatal(e)
	}
}
func legacyCreativeFixture(t *testing.T, s *FileStore) (Project, libraryrepo.Result, CreativeJob, MediaAsset) {
	t.Helper()
	p, a, ref := libraryDispatchFixture(t, s)
	setLibraryDispatchAuthorization(t, s, a, 0, "legacy-grant", "granted")
	provider := newOpenAIProvider("synthetic", "http://127.0.0.1:9/images/generations", http.DefaultClient)
	s.creative.image = provider
	s.creative.imageCaps = Capabilities{Provider: "openai-images", Mode: "real", Model: provider.model, Ready: true}
	job, _, e := s.createCreativeJob(p.SpaceID, p.ID, "legacy-creative-job", CreativeJobInput{Kind: "image", ExpectedVersion: p.Version, ExpectedProvider: "openai-images", ExpectedModel: provider.model, Prompt: "合成旧结果来源", ReferenceAssetID: ref})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.media.AddAsset(p.ID, p.SpaceID, "creative-"+job.ID, "创作素材 · image", "shot", "", "", job.Provider, syntheticCreativePNG(), "image/png"); e != nil {
		t.Fatal(e)
	}
	asset, e := s.media.AssetForReceipt(p.ID, p.SpaceID, "creative-"+job.ID)
	if e != nil {
		t.Fatal(e)
	}
	job.AssetID = asset.ID
	job.Status = "succeeded"
	job.LibraryMediaPins = nil
	if e = s.creative.updateJob(job); e != nil {
		t.Fatal(e)
	}
	stripLegacyProvenance(t, s, p, ref)
	stripLegacyProvenance(t, s, p, asset.ID)
	return p, a, job, asset
}
func inspectLineage(t *testing.T, s *FileStore, p Project, id string, status string) LibraryLineageView {
	t.Helper()
	var out LibraryLineageView
	_ = json.Unmarshal(intentLibraryCall(t, s, "GET", "/api/v1/projects/"+p.ID+"/media/assets/"+id+"/lineage?space_id="+p.SpaceID, "", nil, 200), &out)
	if out.Status != status {
		t.Fatalf("lineage %s: %+v", status, out)
	}
	return out
}
func legacyLineageContract(t *testing.T, s *FileStore) {
	t.Helper()
	p, a, job, asset := legacyCreativeFixture(t, s)
	before := sourceRecords(t, s, p)
	recovered := inspectLineage(t, s, p, asset.ID, "recovered")
	if len(recovered.Sources) != 1 || recovered.Sources[0].AssetID != a.Asset.ID || len(recovered.SHA256) != 64 {
		t.Fatal("missing journal ancestry", recovered)
	}
	if before != sourceRecords(t, s, p) {
		t.Fatal("inspection rewrote original media/history")
	}
	setLibraryDispatchAuthorization(t, s, a, 1, "legacy-revoke", "revoked")
	if _, e := s.pinCreativeLibraryMedia(context.Background(), p.ID, p.SpaceID, CreativeJobInput{Kind: "image", ReferenceAssetID: asset.ID}); e == nil {
		t.Fatal("legacy result lost revocation")
	}
	inspectLineage(t, s, p, asset.ID, "recovered") // provenance is independent of rights
	setLibraryDispatchAuthorization(t, s, a, 2, "legacy-regrant", "granted")
	input := CreativeJobInput{Kind: "image", ExpectedVersion: p.Version, ExpectedProvider: job.Provider, ExpectedModel: job.Model, Prompt: "明确重用旧结果", ReferenceAssetID: asset.ID}
	accepted, _, e := s.createCreativeJob(p.SpaceID, p.ID, "legacy-reuse-new-intent", input)
	if e != nil || len(accepted.LibraryMediaPins) != 1 || accepted.LibraryMediaPins[0].Uses[0].Revision != 3 {
		t.Fatal("new pin was not current", accepted, e)
	}
	// The original ingest may have completed just before the worker lost its job
	// completion write. Its exact persistent receipt still identifies this result.
	job.AssetID = ""
	job.Status = "failed"
	if e = s.creative.updateJob(job); e != nil {
		t.Fatal(e)
	}
	inspectLineage(t, s, p, asset.ID, "recovered")
	path, _, e := s.media.file(p.ID, p.SpaceID, asset.ID, false, false)
	if e != nil {
		t.Fatal(e)
	}
	raw, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(path, append([]byte(nil), raw[:100]...), 0600); e != nil {
		t.Fatal(e)
	}
	if _, _, _, e = s.libraryMediaSources(context.Background(), p.ID, p.SpaceID, asset.ID); !errors.Is(e, ErrBlobIntegrity) {
		t.Fatal("old receipt accepted different bytes", e)
	}
	var unavailable struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(intentLibraryCall(t, s, "GET", "/api/v1/projects/"+p.ID+"/media/assets/"+asset.ID+"/lineage?space_id="+p.SpaceID, "", nil, 503), &unavailable)
	if unavailable.Error.Code != "media_lineage_unavailable" {
		t.Fatal("read-only inspection used write-retry error", unavailable.Error.Code)
	}
	if e = os.WriteFile(path, raw, 0600); e != nil {
		t.Fatal(e)
	}
	// A foreign project's coincident record cannot supply a source or actor.
	foreign, _, e := s.CreateProject("other-space", "lineage-foreign-project", ProjectInput{Title: "Foreign fixture", Brief: "Separate tenant owned synthetic project"})
	if e != nil {
		t.Fatal(e)
	}
	s.creative.mu.Lock()
	d := job
	d.ID = "foreign-job"
	d.SpaceID = "other-space"
	d.ProjectID = foreign.ID
	d.AssetID = asset.ID
	s.creative.data.Jobs[d.ID] = d
	e = s.creative.saveLocked()
	s.creative.mu.Unlock()
	if e != nil {
		t.Fatal(e)
	}
	inspectLineage(t, s, p, asset.ID, "recovered")
	// Conflicting owned claims are explicitly unresolvable, never newest-wins.
	s.creative.mu.Lock()
	d.ID = "ambiguous-owned-job"
	d.SpaceID = p.SpaceID
	d.ProjectID = p.ID
	s.creative.data.Jobs[d.ID] = d
	e = s.creative.saveLocked()
	s.creative.mu.Unlock()
	if e != nil {
		t.Fatal(e)
	}
	inspectLineage(t, s, p, asset.ID, "unverifiable")
	if _, _, e = s.createCreativeJob(p.SpaceID, p.ID, "ambiguous-legacy-intent", input); !errors.Is(e, errMediaLineageUnknown) {
		t.Fatal("ambiguous journal allowed dispatch", e)
	}
}
func TestFileLegacyMediaLineage(t *testing.T) { legacyLineageContract(t, testStore(t)) }
func TestPostgresLegacyMediaLineage(t *testing.T) {
	legacyLineageContract(t, postgresTestStore(t, postgresTestDB(t)))
}

func TestUnrecordedTransformRemainsHistoryOnlyThroughMigration(t *testing.T) {
	for _, mode := range []string{"file", "postgres"} {
		t.Run(mode, func(t *testing.T) {
			s := testStore(t)
			if mode == "postgres" {
				s = postgresTestStore(t, postgresTestDB(t))
			}
			p, season := episodeMigrationFixture(t, s)
			state, e := s.media.AddAsset(p.ID, p.SpaceID, "legacy-unrecorded-edit", "old edit", "shot", "", "", "poster-composite", syntheticCreativePNG(), "image/png")
			if e != nil {
				t.Fatal(e)
			}
			id := state.Assets[len(state.Assets)-1].ID
			stripLegacyProvenance(t, s, p, id)
			inspectLineage(t, s, p, id, "unverifiable")
			if _, _, _, e = s.libraryMediaSources(context.Background(), p.ID, p.SpaceID, id); !errors.Is(e, errMediaLineageUnknown) {
				t.Fatal(e)
			}
			// File viewing and local episode copying remain possible without inventing a grant.
			if _, _, e = s.media.file(p.ID, p.SpaceID, id, false, false); e != nil {
				t.Fatal(e)
			}
			state = s.media.Get(p.ID, p.SpaceID)
			if _, e = s.media.SaveTimeline(p.ID, p.SpaceID, "legacy-unknown-timeline", state.Version, MediaTimeline{EpisodeNumber: 1, OutputResolution: "1080p", AspectRatio: "16:9", MusicVolume: .2, Clips: []MediaClip{{ID: "unknown-clip", AssetID: id, Duration: .5}}}); e != nil {
				t.Fatal(e)
			}
			j := prepareMigration(t, s, p, season, 1, "legacy-unknown-migration")
			for j.State != "completed" {
				j = migrationStep(t, s, j)
			}
			target, e := s.GetProject(p.SpaceID, j.TargetID)
			if e != nil {
				t.Fatal(e)
			}
			copied := s.media.Get(target.ID, p.SpaceID)
			copiedID := j.Copies[id].TargetID
			var found MediaAsset
			for _, a := range copied.Assets {
				if a.ID == copiedID {
					found = a
				}
			}
			if !found.ProvenanceUnknown || found.ProvenanceVersion != 1 || len(found.ContentSHA256) != 64 {
				t.Fatal("migration erased unknown status", found)
			}
			inspectLineage(t, s, target, copiedID, "unverifiable")
		})
	}
}
func TestLegacyCompletedMigrationUsesSealedCopyProofAfterSourceCacheLoss(t *testing.T) {
	s := testStore(t)
	p, season := episodeMigrationFixture(t, s)
	state := s.media.Get(p.ID, p.SpaceID)
	in := libraryWrite{Kind: "scene", Card: library.Card{Name: "Two-angle legacy scene", Scene: &library.Scene{}, ProhibitedChanges: []string{}}, References: []libraryReferenceInput{{ProjectID: p.ID, AssetID: state.Assets[0].ID, Role: "reference"}, {ProjectID: p.ID, AssetID: state.Assets[1].ID, Role: "reference"}}}
	var a libraryrepo.Result
	_ = json.Unmarshal(intentLibraryCall(t, s, "POST", "/api/v1/library/assets?space_id="+p.SpaceID, "old-migration-library", in, 201), &a)
	bound, e := s.media.bindLibrary(context.Background(), p.ID, p.SpaceID, "old-migration-bind", libraryBindingInput{ExpectedVersion: state.Version, AssetID: a.Asset.ID, VersionID: a.Version.ID, Usage: "scene"}, "bound", a.Version)
	if e != nil {
		t.Fatal(e)
	}
	sourceID := bound.LibraryBindings[0].MediaAssetIDs[1]
	if _, e = s.media.SaveTimeline(p.ID, p.SpaceID, "old-migration-timeline", bound.Version, MediaTimeline{EpisodeNumber: 1, OutputResolution: "1080p", AspectRatio: "16:9", MusicVolume: .2, Clips: []MediaClip{{ID: "copied-reference", AssetID: sourceID, Duration: .5}}}); e != nil {
		t.Fatal(e)
	}
	j := prepareMigration(t, s, p, season, 1, "old-migration-journal")
	if len(j.Plan.Bindings) != 1 || len(j.Plan.Bindings[0].MediaAssetIDs) != 1 || j.Plan.Bindings[0].MediaAssetIDs[0] != sourceID {
		t.Fatal("fixture did not preserve a reference subset", j.Plan.Bindings)
	}
	for j.State != "completed" {
		j = migrationStep(t, s, j)
	}
	target, e := s.GetProject(p.SpaceID, j.TargetID)
	if e != nil {
		t.Fatal(e)
	}
	id := j.Copies[sourceID].TargetID
	stripLegacyProvenance(t, s, target, id)
	path, _, e := s.media.file(p.ID, p.SpaceID, sourceID, false, false)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.Remove(path); e != nil {
		t.Fatal(e)
	}
	recovered := inspectLineage(t, s, target, id, "recovered")
	expected := []media.LibrarySource{{AssetID: a.Asset.ID, VersionID: a.Version.ID}}
	if !reflect.DeepEqual(recovered.Sources, expected) || recovered.SHA256 != j.Copies[sourceID].SHA256 {
		t.Fatal("lost sealed migration provenance", recovered)
	}
	if _, e = s.pinCreativeLibraryMedia(context.Background(), target.ID, target.SpaceID, CreativeJobInput{Kind: "image", ReferenceAssetID: id}); e == nil {
		t.Fatal("migration proof manufactured authorization")
	}
	provider := newOpenAIProvider("synthetic", "http://127.0.0.1:9/images/generations", http.DefaultClient)
	s.creative.image = provider
	s.creative.imageCaps = Capabilities{Ready: true, Mode: "real", Provider: "openai-images", Model: provider.model}
	setLibraryDispatchAuthorization(t, s, a, 0, "authorize-sealed-legacy-copy", "granted")
	accepted, _, e := s.createCreativeJob(target.SpaceID, target.ID, "sealed-copy-new-intent", CreativeJobInput{Kind: "image", ExpectedVersion: target.Version, ExpectedProvider: "openai-images", ExpectedModel: provider.model, ReferenceAssetID: id, Prompt: "明确使用独立复制的参考"})
	if e != nil || len(accepted.LibraryMediaPins) != 1 {
		t.Fatal("locked acceptance did not resolve the sealed copy", e)
	}
	setLibraryDispatchAuthorization(t, s, a, 1, "revoke-sealed-legacy-copy", "revoked")
	e = s.executeCreativeJob(context.Background(), &accepted)
	var denied *ProviderError
	if !errors.As(e, &denied) || denied.Code != "library_use_denied" {
		t.Fatal("queued sealed copy did not recheck current rights", e)
	}

}

func TestLegacyRepackedLibraryReferenceCannotCutOffAncestorRevocation(t *testing.T) {
	for _, mode := range []string{"file", "postgres"} {
		t.Run(mode, func(t *testing.T) {
			s := testStore(t)
			if mode == "postgres" {
				s = postgresTestStore(t, postgresTestDB(t))
			}
			p, a, _, generated := legacyCreativeFixture(t, s)
			// Prepare the real immutable reference object, then model an earlier valid
			// version that predates the complete inherited-scope marker.
			input := libraryWrite{Kind: "scene", Card: library.Card{Name: "旧场景卡", Scene: &library.Scene{}, ProhibitedChanges: []string{}}, References: []libraryReferenceInput{{ProjectID: p.ID, AssetID: generated.ID, Role: "reference"}}}
			var prepared libraryrepo.Result
			_ = json.Unmarshal(intentLibraryCall(t, s, "POST", "/api/v1/library/assets?space_id="+p.SpaceID, "prepare-old-card-copy", input, 201), &prepared)
			old := prepared
			old.Asset.ID = "legacy-repacked-card"
			old.Asset.HeadVersionID = "legacy-repacked-version"
			old.Version.ID = old.Asset.HeadVersionID
			old.Version.AssetID = old.Asset.ID
			old.Version.References = append([]library.Reference(nil), old.Version.References...)
			for i := range old.Version.References {
				old.Version.References[i].ProvenanceVersion = 0
				old.Version.References[i].LibrarySources = nil
			}
			old.Version.SHA256, _ = library.Digest(struct {
				Card       library.Card
				References []library.Reference
			}{old.Version.Card, old.Version.References})
			old, e := s.library.Mutate(context.Background(), p.SpaceID, "legacy-repacked-create", "legacy-repacked-create", "", old, false)
			if e != nil {
				t.Fatal(e)
			}
			state := s.media.Get(p.ID, p.SpaceID)
			binding := libraryBindingInput{ExpectedVersion: state.Version, AssetID: old.Asset.ID, VersionID: old.Version.ID, Usage: "scene"}
			var copied MediaState
			_ = json.Unmarshal(intentLibraryCall(t, s, "POST", "/api/v1/projects/"+p.ID+"/library-bindings?space_id="+p.SpaceID, "bind-old-repacked", binding, 201), &copied)
			copiedID := copied.LibraryBindings[len(copied.LibraryBindings)-1].MediaAssetIDs[0]
			setLibraryDispatchAuthorization(t, s, old, 0, "grant-old-repacked", "granted")
			setLibraryDispatchAuthorization(t, s, a, 1, "revoke-old-ancestor", "revoked")
			if _, e = s.pinCreativeLibraryMedia(context.Background(), p.ID, p.SpaceID, CreativeJobInput{Kind: "image", ReferenceAssetID: copiedID}); e == nil {
				t.Fatal("new card laundered old ancestor")
			}
			path, _, e := s.media.file(p.ID, p.SpaceID, generated.ID, false, false)
			if e != nil {
				t.Fatal(e)
			}
			if e = os.Remove(path); e != nil {
				t.Fatal(e)
			}
			stripLegacyProvenance(t, s, p, copiedID)
			recovered := inspectLineage(t, s, p, copiedID, "recovered")
			if len(recovered.Sources) != 2 {
				t.Fatal("old reference did not retain both scopes", recovered)
			}
			input.ExpectedVersion = old.Version.ID
			input.References = []libraryReferenceInput{{LibraryVersionID: old.Version.ID, LibraryReferenceID: old.Version.References[0].ID, Role: "reference"}}
			var next libraryrepo.Result
			_ = json.Unmarshal(intentLibraryCall(t, s, "POST", "/api/v1/library/assets/"+old.Asset.ID+"/versions?space_id="+p.SpaceID, "renew-old-lineage-record", input, 201), &next)
			if next.Version.References[0].ProvenanceVersion != 1 || len(next.Version.References[0].LibrarySources) != 1 || next.Version.References[0].LibrarySources[0].AssetID != a.Asset.ID {
				t.Fatal("new immutable version dropped ancestry", next.Version.References)
			}
			historical, e := s.library.Version(context.Background(), p.SpaceID, old.Asset.ID, old.Version.ID)
			if e != nil || !reflect.DeepEqual(historical, old.Version) {
				t.Fatal("rewrote old version", e)
			}
		})
	}
}
func TestLegacyMediaLineageRejectsJournalCycles(t *testing.T) {
	s := testStore(t)
	p, _, job, asset := legacyCreativeFixture(t, s)
	job.ReferenceAssetID = asset.ID
	if e := s.creative.updateJob(job); e != nil {
		t.Fatal(e)
	}
	if _, _, _, e := s.libraryMediaSources(context.Background(), p.ID, p.SpaceID, asset.ID); !errors.Is(e, ErrBlobIntegrity) {
		t.Fatal("cyclic original input graph accepted", e)
	}
}
