package libraryrepo_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/library"
	"github.com/adcwb/ai-creative-studio/backend/internal/persistence/libraryrepo"
)

func result(id, version, space string, number int) libraryrepo.Result {
	c, _ := library.Normalize("character", library.Card{Name: "主角", Character: &library.Character{Hair: "黑发"}})
	v := library.Version{ID: version, AssetID: id, SpaceID: space, Number: number, Card: c, References: []library.Reference{}, CreatedAt: time.Now().UTC(), CreatedBy: "test"}
	v.SHA256, _ = library.Digest(struct {
		Card       library.Card
		References []library.Reference
	}{v.Card, v.References})
	return libraryrepo.Result{Asset: library.Asset{ID: id, SpaceID: space, Kind: "character", Name: c.Name, HeadVersionID: version, Status: "active", CreatedAt: v.CreatedAt, UpdatedAt: v.CreatedAt}, Version: v}
}
func TestDurableVersionsReceiptsAndIsolation(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := libraryrepo.Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	r := result("actor", "version1", "space", 1)
	if _, err = s.Mutate(ctx, "space", "create-key", "digest1", "", r, false); err != nil {
		t.Fatal(err)
	}
	r2 := result("actor", "version2", "space", 2)
	if _, err = s.Mutate(ctx, "space", "update-key", "digest2", "version1", r2, false); err != nil {
		t.Fatal(err)
	}
	s, err = libraryrepo.Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	old, err := s.Version(ctx, "space", "actor", "version1")
	if err != nil || old.Number != 1 {
		t.Fatal("lost old version", err)
	}
	replay, ok, err := s.Replay(ctx, "space", "create-key", "digest1")
	if err != nil || !ok || replay.Version.ID != "version1" {
		t.Fatal("changed replay", err)
	}
	if _, _, err = s.Replay(ctx, "space", "create-key", "different"); !errors.Is(err, libraryrepo.ErrKey) {
		t.Fatal("key conflict", err)
	}
	if _, err = s.Get(ctx, "other", "actor"); !errors.Is(err, libraryrepo.ErrNotFound) {
		t.Fatal("tenant leak", err)
	}
	if _, err = s.Version(ctx, "space", "other", "version1"); !errors.Is(err, libraryrepo.ErrNotFound) {
		t.Fatal("asset leak", err)
	}
	r2.Asset.Status = "archived"
	if _, err = s.Mutate(ctx, "space", "archive-key", "digest3", "version2", r2, true); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Mutate(ctx, "space", "again-key", "digest4", "version2", result("actor", "version3", "space", 3), false); !errors.Is(err, library.ErrArchived) {
		t.Fatal("archive reopened", err)
	}
	if _, err = s.Version(ctx, "space", "actor", "version1"); err != nil {
		t.Fatal("archive removed history", err)
	}
}
func TestConcurrentUpdatesHaveOneWinner(t *testing.T) {
	ctx := context.Background()
	s, _ := libraryrepo.Open(t.TempDir(), nil)
	if _, err := s.Mutate(ctx, "space", "create-key", "create", "", result("actor", "v1", "space", 1), false); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	out := make(chan error, 2)
	for _, id := range []string{"v2", "v3"} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			_, err := s.Mutate(ctx, "space", id, id, "v1", result("actor", id, "space", 2), false)
			out <- err
		}(id)
	}
	wg.Wait()
	close(out)
	wins, conflicts := 0, 0
	for err := range out {
		if err == nil {
			wins++
		} else if errors.Is(err, library.ErrConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if wins != 1 || conflicts != 1 {
		t.Fatalf("wins=%d conflicts=%d", wins, conflicts)
	}
}
func TestCorruptStorageFailsClosed(t *testing.T) {
	dir := t.TempDir()
	s, _ := libraryrepo.Open(dir, nil)
	if err := os.WriteFile(filepath.Join(dir, "space.json"), []byte(`{"format":1}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.List(context.Background(), "space", libraryrepo.Query{Limit: 20}); !errors.Is(err, libraryrepo.ErrStorage) {
		t.Fatal("accepted incomplete store", err)
	}
	if _, err := s.Get(context.Background(), "../escape", "actor"); err == nil {
		t.Fatal("accepted path")
	}
}

func TestVersionPaginationUsesCreationOrderAndPinnedCursor(t *testing.T) {
	s, err := libraryrepo.Open(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	expected := ""
	for i, id := range []string{"z_oldest", "a_middle", "m_newest"} {
		if _, err = s.Mutate(ctx, "space", id, id, expected, result("actor", id, "space", i+1), false); err != nil {
			t.Fatal(err)
		}
		expected = id
	}
	page, err := s.Versions(ctx, "space", "actor", "", 2)
	if err != nil || len(page) != 2 || page[0].ID != "m_newest" || page[1].ID != "a_middle" {
		t.Fatal("wrong history order", page, err)
	}
	if _, err = s.Mutate(ctx, "space", "n_later", "later", expected, result("actor", "n_later", "space", 4), false); err != nil {
		t.Fatal(err)
	}
	page, err = s.Versions(ctx, "space", "actor", page[1].ID, 2)
	if err != nil || len(page) != 1 || page[0].ID != "z_oldest" {
		t.Fatal("cursor repeated new history", page, err)
	}
}
