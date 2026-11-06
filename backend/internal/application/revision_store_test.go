package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/library"
	"github.com/adcwb/ai-creative-studio/backend/internal/modules/revisions"
	"github.com/adcwb/ai-creative-studio/backend/internal/persistence/revisionrepo"
)

func revisionFixture(t *testing.T, s *FileStore) (Project, MediaState, revisionCaptureInput) {
	t.Helper()
	p, state, preview := animaticFixture(t, s)
	timeline := MediaTimeline{AspectRatio: "16:9", OutputResolution: "1080p", Clips: []MediaClip{{ID: "saved-cut", ShotID: preview.Shots[0].ShotID, AssetID: preview.Shots[0].AssetID, Duration: 2, Subtitle: "原来的对白", AudioAssetID: preview.Shots[0].AudioAssetID, AudioVolume: 1}}, MusicVolume: .2}
	state, e := s.media.SaveTimeline(p.ID, p.SpaceID, "revision-fixture-timeline", state.Version, timeline)
	if e != nil {
		t.Fatal(e)
	}
	return p, state, revisionCaptureInput{ExpectedProjectVersion: p.Version, ExpectedMediaVersion: state.Version, Label: "门前的停顿"}
}
func exerciseProductionRevisions(t *testing.T, s *FileStore) {
	t.Helper()
	ctx := context.Background()
	p, state, in := revisionFixture(t, s)
	q := revisionrepo.Scope{Space: p.SpaceID, Project: p.ID}
	original, e := s.CaptureProductionRevision(ctx, p.SpaceID, p.ID, "main", "owner", "production-revision-original", in, nil)
	if e != nil {
		t.Fatal(e)
	}
	if original.Branch.Version != 1 || original.Revision == nil || original.Revision.Manifest.ParentSHA256 != "" {
		t.Fatal("invalid first branch", original)
	}
	var sources revisionSources
	if e = json.Unmarshal(original.Revision.Manifest.Sources, &sources); e != nil || len(sources.Assets) != 2 || sources.Assets[0].SHA256 == "" {
		t.Fatal("actual used bytes absent", e, sources)
	}
	archived := *original.Revision
	// Returned values must not share live documents, and later edit/selection
	// state must not be rewound by exact original request recovery.
	original.Revision.Manifest.Timeline[0] = 'x'
	p, e = s.UpdateProject(p.SpaceID, p.ID, "later-production-title", ProjectUpdate{Title: "后来剪辑方向", Brief: p.Brief, ExpectedVersion: p.Version})
	if e != nil {
		t.Fatal(e)
	}
	replay, e := s.CaptureProductionRevision(ctx, p.SpaceID, p.ID, "main", "owner", "production-revision-original", in, nil)
	if e != nil || replay.Revision.ID != archived.ID || !json.Valid(replay.Revision.Manifest.Timeline) {
		t.Fatal("original input replaced", e)
	}
	if _, e = s.CaptureProductionRevision(ctx, p.SpaceID, p.ID, "main", "owner", "production-stale", in, nil); !errors.Is(e, ErrVersionConflict) {
		t.Fatal("project CAS", e)
	}
	changed := in
	changed.Label = "different input"
	if _, e = s.CaptureProductionRevision(ctx, p.SpaceID, p.ID, "main", "owner", "production-revision-original", changed, nil); !errors.Is(e, revisionrepo.ErrKey) {
		t.Fatal("key reused", e)
	}
	fork, e := s.ForkProductionRevision(ctx, p.SpaceID, p.ID, "owner", "production-new-branch", revisionForkInput{SourceID: archived.ID, Name: "更克制的方向"}, nil)
	if e != nil || fork.Branch.HeadRevisionID != archived.ID || fork.Revision != nil {
		t.Fatal("fork source", e, fork)
	}
	next := in
	next.ExpectedProjectVersion = p.Version
	next.ExpectedBranchVersion = fork.Branch.Version
	next.ExpectedHead = archived.ID
	next.Label = "保留留白"
	advanced, e := s.CaptureProductionRevision(ctx, p.SpaceID, p.ID, fork.Branch.ID, "owner", "production-branch-save", next, nil)
	if e != nil || advanced.Revision.ParentID != archived.ID || advanced.Revision.Manifest.ParentSHA256 != replay.Revision.SHA256 {
		t.Fatal("branch parent", e)
	}
	main, e := s.revisions.Branch(ctx, q, "main")
	if e != nil || main.HeadRevisionID != archived.ID {
		t.Fatal("fork moved main", e)
	}
	branches, e := s.revisions.Branches(ctx, q, "", 1)
	if e != nil || len(branches.Items) != 1 || branches.Next == "" {
		t.Fatal("branch paging", e, branches)
	}
	tail, e := s.revisions.Branches(ctx, q, branches.Next, 1)
	if e != nil || len(tail.Items) != 1 || tail.Items[0].ID == branches.Items[0].ID {
		t.Fatal("branch duplicate page", e)
	}
	if _, e = s.revisions.Get(ctx, revisionrepo.Scope{Space: "other-space", Project: p.ID}, archived.ID); !errors.Is(e, revisionrepo.ErrNotFound) {
		t.Fatal("cross space", e)
	}
	if _, e = s.revisions.Get(ctx, revisionrepo.Scope{Space: p.SpaceID, Project: "other-project"}, archived.ID); !errors.Is(e, revisionrepo.ErrNotFound) {
		t.Fatal("cross project", e)
	}
	current, e := s.GetProject(p.SpaceID, p.ID)
	if e != nil || current.Version != p.Version {
		t.Fatal("snapshot changed project", e)
	}
	if got := s.media.Get(p.ID, p.SpaceID); !reflect.DeepEqual(got, state) {
		t.Fatal("snapshot changed media")
	}
	history, e := s.revisions.History(ctx, q, "main", "", 20)
	b, _ := json.Marshal(history)
	if e != nil || len(history.Items) != 1 || strings.Contains(string(b), "manifest") || strings.Contains(string(b), "原来的对白") {
		t.Fatal("body leaked into list", e)
	}
	// Both clients hold the same branch head; exactly one may advance it.
	next.ExpectedBranchVersion = 1
	next.ExpectedHead = archived.ID
	next.ExpectedProjectVersion = p.Version
	next.Label = "并发新方向"
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, e := s.CaptureProductionRevision(ctx, p.SpaceID, p.ID, "main", "owner", fmt.Sprintf("production-concurrent-%d", i), next, nil)
			errs <- e
		}(i)
	}
	wg.Wait()
	close(errs)
	wins, conflicts := 0, 0
	for e := range errs {
		if e == nil {
			wins++
		} else if errors.Is(e, revisions.ErrConflict) {
			conflicts++
		} else {
			t.Fatal(e)
		}
	}
	if wins != 1 || conflicts != 1 {
		t.Fatal("lost update", wins, conflicts)
	}
	denied := next
	denied.ExpectedBranchVersion = 2
	head, _ := s.revisions.Branch(ctx, q, "main")
	denied.ExpectedHead = head.HeadRevisionID
	if _, e = s.CaptureProductionRevision(ctx, p.SpaceID, p.ID, "main", "owner", "production-revoked", denied, func() bool { return false }); !errors.Is(e, errRuntimeDenied) {
		t.Fatal("final access", e)
	}
	if _, e = s.CaptureProductionRevision(ctx, p.SpaceID, p.ID, "main", "owner", "production-revoked", denied, nil); e != nil {
		t.Fatal("denied attempt consumed key", e)
	}
}
func TestFileProductionRevisions(t *testing.T) { exerciseProductionRevisions(t, testStore(t)) }
func TestPostgresProductionRevisions(t *testing.T) {
	exerciseProductionRevisions(t, postgresTestStore(t, postgresTestDB(t)))
}

func TestPostgresProductionRevisionOfflineRoundTrip(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, e := OpenStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	p, _, in := revisionFixture(t, s)
	original, e := s.CaptureProductionRevision(ctx, p.SpaceID, p.ID, "main", "owner", "revision-offline-original", in, nil)
	if e != nil {
		t.Fatal(e)
	}
	fork, e := s.ForkProductionRevision(ctx, p.SpaceID, p.ID, "owner", "revision-offline-fork", revisionForkInput{SourceID: original.Revision.ID, Name: "第二个方向"}, nil)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Close(); e != nil {
		t.Fatal(e)
	}
	db := postgresTestDB(t)
	if _, e = ImportPostgresData(ctx, dir, db); e != nil {
		t.Fatal(e)
	}
	pg := postgresTestStore(t, db)
	v, e := pg.revisions.Get(ctx, revisionrepo.Scope{Space: p.SpaceID, Project: p.ID}, original.Revision.ID)
	if e != nil || v.SHA256 != original.Revision.SHA256 {
		t.Fatal("JSONB digest", e)
	}
	target := filepath.Join(t.TempDir(), "restored")
	if _, e = ExportPostgresData(ctx, target, db); e != nil {
		t.Fatal(e)
	}
	restored, e := OpenStore(target)
	if e != nil {
		t.Fatal(e)
	}
	defer restored.Close()
	replay, e := restored.CaptureProductionRevision(ctx, p.SpaceID, p.ID, "main", "owner", "revision-offline-original", in, nil)
	if e != nil || !sameRevisionJSON(replay, original) {
		t.Fatal("offline receipt/source changed", e)
	}
	b, e := restored.revisions.Branch(ctx, revisionrepo.Scope{Space: p.SpaceID, Project: p.ID}, fork.Branch.ID)
	if e != nil || !reflect.DeepEqual(b, fork.Branch) {
		t.Fatal("offline branch", e)
	}
	// A private pre-link temp is invisible and cannot be mistaken for an accepted
	// revision after a process restart. The linked immutable record remains read.
	pending := filepath.Join(target, "revisions", revisionrepo.Hash(p.SpaceID), p.ID, "main", ".pending-crash")
	if e = os.WriteFile(pending, []byte("partial"), 0600); e != nil {
		t.Fatal(e)
	}
	history, e := restored.revisions.History(ctx, revisionrepo.Scope{Space: p.SpaceID, Project: p.ID}, "main", "", 20)
	if e != nil || len(history.Items) != 1 {
		t.Fatal("uncommitted temp visible", e)
	}
}

func sameRevisionJSON(a, b any) bool {
	var av, bv any
	ar, _ := json.Marshal(a)
	br, _ := json.Marshal(b)
	if json.Unmarshal(ar, &av) != nil || json.Unmarshal(br, &bv) != nil {
		return false
	}
	return reflect.DeepEqual(av, bv)
}

func TestProductionRevisionPinsLibraryContentWithoutRestoringAuthorization(t *testing.T) {
	s := testStore(t)
	p, asset, _, _ := inputLibraryFixture(t, s, "style", library.Card{Name: "雨夜", Description: "留出门框", ProhibitedChanges: []string{}, Style: &library.Style{Palette: "墨绿"}})
	inputLibraryGrant(t, s, asset, 0, "revision-library-grant", "prompt_context", "image_reference")
	state := s.media.Get(p.ID, p.SpaceID)
	in := revisionCaptureInput{ExpectedProjectVersion: p.Version, ExpectedMediaVersion: state.Version, Label: "固定风格资料"}
	v, e := s.CaptureProductionRevision(context.Background(), p.SpaceID, p.ID, "main", "owner", "revision-library-source", in, nil)
	if e != nil {
		t.Fatal(e)
	}
	var sources revisionSources
	_ = json.Unmarshal(v.Revision.Manifest.Sources, &sources)
	if len(sources.LibraryVersions) != 1 || sources.LibraryVersions[0].SHA256 != asset.Version.SHA256 || len(sources.Assets) != 1 {
		t.Fatal("library version or real reference absent")
	}
	raw, _ := json.Marshal(v.Revision)
	for _, field := range []string{"authorization_revision", "use_authorization", "runtime_selection", "dispatch", "voice_consent", "reserved_minor"} {
		if strings.Contains(string(raw), field) {
			t.Fatal("operational state entered restorable manifest", field)
		}
	}
	intentLibraryCall(t, s, "POST", "/api/v1/library/assets/"+asset.Asset.ID+"/use-authorization?space_id="+p.SpaceID, "revision-library-revoke", library.AuthorizationInput{Expected: 1, Status: "revoked", Reason: "仅用于核验历史仍可查看"}, 200)
	got, e := s.revisions.Get(context.Background(), revisionrepo.Scope{Space: p.SpaceID, Project: p.ID}, v.Revision.ID)
	if e != nil || got.SHA256 != v.Revision.SHA256 {
		t.Fatal("revocation rewrote history", e)
	}
}

func TestPostgresProductionRevisionTwoAPIsUseDurableBranchCAS(t *testing.T) {
	db := postgresTestDB(t)
	a, b := postgresTestStore(t, db), postgresTestStore(t, db)
	p := directorFixture(t, a)
	in := revisionCaptureInput{ExpectedProjectVersion: p.Version, ExpectedMediaVersion: 1, Label: "两个进程的同一方向"}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i, s := range []*FileStore{a, b} {
		wg.Add(1)
		go func(i int, s *FileStore) {
			defer wg.Done()
			_, e := s.CaptureProductionRevision(context.Background(), p.SpaceID, p.ID, "main", "owner", fmt.Sprintf("two-api-revision-%d", i), in, nil)
			errs <- e
		}(i, s)
	}
	wg.Wait()
	close(errs)
	winners, conflicts := 0, 0
	for e := range errs {
		if e == nil {
			winners++
		} else if errors.Is(e, revisions.ErrConflict) {
			conflicts++
		} else {
			t.Fatal(e)
		}
	}
	if winners != 1 || conflicts != 1 {
		t.Fatal("database CAS lost", winners, conflicts)
	}
}
