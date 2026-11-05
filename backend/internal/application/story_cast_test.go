package application

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/library"
	"github.com/adcwb/ai-creative-studio/backend/internal/modules/projects"
	"github.com/adcwb/ai-creative-studio/backend/internal/modules/story"
	"github.com/adcwb/ai-creative-studio/backend/internal/persistence/libraryrepo"
	"github.com/adcwb/ai-creative-studio/backend/internal/persistence/storyrepo"
)

func castFixture(t *testing.T, s *FileStore) (string, Project, story.Input, libraryrepo.Result) {
	t.Helper()
	root, eps := storyFixture(t, s)
	b := saveStory(t, s, root, storyrepo.BibleID(root), "cast-bible-create", storyBible())
	script := scriptFixture(t)
	for i := range script.Scenes {
		script.Scenes[i].EpisodeNumber = 1
	}
	stable, _, e := projects.CompileStructure(script, eps[0].ID, 1)
	if e != nil {
		t.Fatal(e)
	}
	p, e := s.SaveProjectScript(eps[0].SpaceID, eps[0].ID, "cast-manual-script", ScriptSaveInput{ExpectedVersion: eps[0].Version, Script: stable})
	if e != nil {
		t.Fatal(e)
	}
	now := time.Now().UTC()
	card := library.Card{Name: "小满 · 原版", ProhibitedChanges: []string{}, Character: &library.Character{Costume: "雨衣"}}
	refs := []library.Reference{}
	hash, _ := library.Digest(struct {
		Card       library.Card
		References []library.Reference
	}{card, refs})
	result := libraryrepo.Result{Asset: library.Asset{ID: "cast-asset", SpaceID: storyActor.Space, Kind: "character", Name: card.Name, HeadVersionID: "cast-version-1", Status: "active", CreatedAt: now, UpdatedAt: now}, Version: library.Version{ID: "cast-version-1", AssetID: "cast-asset", SpaceID: storyActor.Space, Number: 1, Card: card, References: refs, SHA256: hash, CreatedAt: now, CreatedBy: storyActor.User}}
	result, e = s.library.Mutate(context.Background(), storyActor.Space, "cast-asset-create", "cast-asset-create", "", result, false)
	if e != nil {
		t.Fatal(e)
	}
	in := story.Input{Title: "本集选角", Kind: "cast", Status: "active", Body: story.Body{Evidence: []story.Evidence{}, Cast: &story.Cast{EpisodeID: p.ID, ScriptVersion: p.ScriptVersion, ScriptSHA256: story.Digest(p.Script), BibleVersionID: b.VersionID, Characters: []story.CastCharacter{{ScriptID: p.Script.Characters[0].ID, ScriptName: p.Script.Characters[0].Name, SeriesID: b.Body.Bible.Characters[0].ID, SeriesName: b.Body.Bible.Characters[0].Name, AssetID: result.Asset.ID, AssetVersionID: result.Version.ID, AssetName: result.Version.Card.Name, Notes: "本集保持戒备；只是表演说明"}}}}}
	return root, p, in, result
}
func castContract(t *testing.T, s *FileStore) {
	t.Helper()
	ctx := context.Background()
	root, p, in, asset := castFixture(t, s)
	id := story.CastID(root, p.ID)
	v := saveStory(t, s, root, id, "cast-original-key", in)
	if !v.Required || v.Number != 1 {
		t.Fatal(v)
	}
	options, e := s.storyCastOptions(ctx, p.SpaceID, p.ID)
	if e != nil || options.Current == nil || len(options.Problems) > 0 {
		t.Fatal(e, options)
	}
	snapshot, e := s.CreateStoryContext(ctx, storyActor, p.ID, "cast-context-original", story.Selection{})
	if e != nil || len(snapshot.Context.Problems) > 0 || len(snapshot.Context.Included) != 2 {
		t.Fatal(e, snapshot.Context.Problems)
	}
	for i, field := range []string{"script", "series", "asset"} {
		forged, _ := story.Clone(in)
		forged.ExpectedVersion = 1
		switch field {
		case "script":
			forged.Body.Cast.Characters[0].ScriptName = "伪造的显示名"
		case "series":
			forged.Body.Cast.Characters[0].SeriesName = "伪造的系列人物"
		case "asset":
			forged.Body.Cast.Characters[0].AssetName = "伪造的资产名"
		}
		if _, e := s.SaveStory(ctx, storyActor, root, id, fmt.Sprintf("cast-forged-name-%d", i), forged); !errors.Is(e, story.ErrInput) {
			t.Fatal("forged display snapshot", field, e)
		}
	}
	invalid, _ := story.Clone(in)
	invalid.ExpectedVersion = 1
	invalid.Body.Cast.Characters[0].ScriptID = fmt.Sprintf("%032x", 999)
	if _, e := s.SaveStory(ctx, storyActor, root, id, "cast-forged-character", invalid); !errors.Is(e, story.ErrInput) {
		t.Fatal("forged role", e)
	}
	invalid, _ = story.Clone(in)
	invalid.ExpectedVersion = 1
	invalid.Body.Cast.Characters[0].AssetVersionID = "missing-version"
	if _, e := s.SaveStory(ctx, storyActor, root, id, "cast-foreign-asset", invalid); !errors.Is(e, story.ErrInput) {
		t.Fatal("unknown asset version", e)
	}
	if _, e := s.SaveStory(ctx, storyActor, root, story.NewID(storyActor, root, "random-cast-key"), "random-cast-key", in); !errors.Is(e, story.ErrInput) {
		t.Fatal("duplicate cast identity accepted", e)
	}
	revised, _ := story.Clone(asset)
	revised.Asset.HeadVersionID = "cast-version-2"
	revised.Asset.Name = "小满 · 新版"
	revised.Version.ID = "cast-version-2"
	revised.Version.Number = 2
	revised.Version.Card.Name = revised.Asset.Name
	revised.Version.Card.Character.Costume = "新雨衣"
	revised.Version.SHA256, _ = library.Digest(struct {
		Card       library.Card
		References []library.Reference
	}{revised.Version.Card, revised.Version.References})
	if _, e = s.library.Mutate(ctx, p.SpaceID, "cast-asset-revision", "cast-asset-revision", asset.Version.ID, revised, false); e != nil {
		t.Fatal(e)
	}
	pinned, e := s.storyCastOptions(ctx, p.SpaceID, p.ID)
	if e != nil || pinned.Current.Body.Cast.Characters[0].AssetVersionID != asset.Version.ID {
		t.Fatal("asset head replaced a pinned version", e)
	}
	asset = revised
	script := projects.CloneScript(*p.Script)
	oldName := script.Characters[0].Name
	script.Characters[0].Name = "本集化名"
	p, e = s.SaveProjectScript(p.SpaceID, p.ID, "cast-script-rename", ScriptSaveInput{ExpectedVersion: p.Version, Script: script})
	if e != nil {
		t.Fatal(e)
	}
	stale, e := s.CreateStoryContext(ctx, storyActor, p.ID, "cast-context-stale", story.Selection{})
	if e != nil || len(stale.Context.Problems) != 1 || stale.Context.Problems[0].Code != "cast_script_changed" {
		t.Fatal("stale names did not surface", e, stale.Context.Problems)
	}
	if snapshot.Script.Characters[0].Name != oldName {
		t.Fatal("old context mutated")
	}
	old := saveStory(t, s, root, id, "cast-original-key", in)
	if old.VersionID != v.VersionID {
		t.Fatal("replay rebased old cast")
	}
	updated, _ := story.Clone(in)
	updated.ExpectedVersion = 1
	updated.Body.Cast.ScriptVersion = p.ScriptVersion
	updated.Body.Cast.ScriptSHA256 = story.Digest(p.Script)
	updated.Body.Cast.Characters[0].ScriptName = p.Script.Characters[0].Name
	var wg sync.WaitGroup
	errorsCh := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, e := s.SaveStory(ctx, storyActor, root, id, "cast-reconcile-key", updated)
			errorsCh <- e
		}()
	}
	wg.Wait()
	close(errorsCh)
	for e := range errorsCh {
		if e != nil {
			t.Fatal(e)
		}
	}
	history, e := s.story.History(ctx, storyrepo.Scope{Space: p.SpaceID, Series: root}, id, 0, 100)
	if e != nil || len(history.Items) != 2 {
		t.Fatal("duplicate saved version", e, len(history.Items))
	}
	archived := asset
	archived.Asset.Status = "archived"
	if _, e := s.library.Mutate(ctx, p.SpaceID, "cast-asset-archive", "cast-asset-archive", asset.Version.ID, archived, true); e != nil {
		t.Fatal(e)
	}
	saveStory(t, s, root, id, "cast-reconcile-key", updated)
	updated.ExpectedVersion = 2
	if _, e := s.SaveStory(ctx, storyActor, root, id, "cast-new-archived", updated); !errors.Is(e, story.ErrInput) {
		t.Fatal("archived asset newly selected", e)
	}
	denied := storyActor
	denied.Writable = false
	if _, e := s.SaveStory(ctx, denied, root, id, "cast-original-key", in); !errors.Is(e, story.ErrDenied) {
		t.Fatal("revoked write replay", e)
	}
	if _, e := s.storyCastOptions(ctx, "other-space", p.ID); e == nil {
		t.Fatal("cross-space cast exposed")
	}
	if len(s.data.ScriptGenerations) > 0 || len(s.data.StoryboardGenerations) > 0 {
		t.Fatal("cast save called a model")
	}
}
func TestFileStoryCastExplicitIdentitiesAndRecovery(t *testing.T) { castContract(t, testStore(t)) }
func TestPostgresStoryCastExplicitIdentitiesAndRecovery(t *testing.T) {
	castContract(t, postgresTestStore(t, postgresTestDB(t)))
}

func TestPostgresCastOfflineRoundTripKeepsPinnedIdentitiesAndReceipts(t *testing.T) {
	ctx := context.Background()
	db := postgresTestDB(t)
	dir := t.TempDir()
	file, e := OpenStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	root, p, in, _ := castFixture(t, file)
	original := saveStory(t, file, root, story.CastID(root, p.ID), "cast-offline-original", in)
	snapshot, e := file.CreateStoryContext(ctx, storyActor, p.ID, "cast-offline-context", story.Selection{})
	if e != nil {
		t.Fatal(e)
	}
	if e = file.Close(); e != nil {
		t.Fatal(e)
	}
	report, e := ImportPostgresData(ctx, dir, db)
	if e != nil || report.Records["story_versions"] != 2 || report.Records["story_contexts"] != 1 {
		t.Fatal("import", e, report.Records)
	}
	pg := postgresTestStore(t, db)
	replayed := saveStory(t, pg, root, original.ID, "cast-offline-original", in)
	if !reflect.DeepEqual(replayed, original) {
		t.Fatal("PG receipt changed")
	}
	replayedSnapshot, e := pg.CreateStoryContext(ctx, storyActor, p.ID, "cast-offline-context", story.Selection{})
	if e != nil || !reflect.DeepEqual(snapshot, replayedSnapshot) {
		t.Fatal("PG context changed", e)
	}
	dst := filepath.Join(t.TempDir(), "restored")
	if _, e = ExportPostgresData(ctx, dst, db); e != nil {
		t.Fatal(e)
	}
	restored, e := OpenStore(dst)
	if e != nil {
		t.Fatal(e)
	}
	defer restored.Close()
	replayed = saveStory(t, restored, root, original.ID, "cast-offline-original", in)
	if !reflect.DeepEqual(original, replayed) {
		t.Fatal("file receipt changed")
	}
	options, e := restored.storyCastOptions(ctx, p.SpaceID, p.ID)
	if e != nil || options.Current == nil || !reflect.DeepEqual(options.Current.Body.Cast, in.Body.Cast) {
		t.Fatal("cast identities changed", e)
	}
}
