package seriesrepo

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/series"
)

var testActor = series.Actor{Space: "studio", User: "writer", Writable: true}
var ctx = context.Background()

func command(kind, parent, key string) series.Command {
	c := series.Command{Operation: "create", Kind: kind, Parent: parent, ID: series.NewID(testActor, "create", key), Input: series.Input{Title: "一起长大", Summary: "共同成长的旅程"}}
	if kind == "season" {
		c.Input = series.Input{}
	}
	if kind == "episode" {
		c.Input.Summary = ""
	}
	return c
}

// SQL acceptance owns the transaction, as the application will for Project.
func create(t *testing.T, s *Store, c series.Command, key string) series.Node {
	t.Helper()
	var n series.Node
	var err error
	if s.db == nil {
		n, err = s.Mutate(ctx, testActor, key, c, func(series.Node) error { return nil })
	} else {
		var tx *sql.Tx
		tx, err = s.db.BeginTx(ctx, nil)
		if err == nil {
			defer tx.Rollback()
			n, err = s.MutateTx(ctx, tx, testActor, key, c)
			if err == nil {
				err = tx.Commit()
			}
		}
	}
	if err != nil {
		t.Fatal(err)
	}
	return n
}
func fixture(t *testing.T, s *Store) (series.Node, series.Node) {
	t.Helper()
	root := create(t, s, command("series", "", "root-key"), "root-key")
	season := create(t, s, command("season", root.ID, "season-key"), "season-key")
	return root, season
}
func directoryContract(t *testing.T, s *Store) {
	root, season := fixture(t, s)
	if root.Profile != series.DefaultProfile() || season.Profile != root.Profile {
		t.Fatal("profile not inherited")
	}
	var first series.Node
	for i := 1; i <= 201; i++ {
		key := fmt.Sprintf("episode-%03d", i)
		n := create(t, s, command("episode", season.ID, key), key)
		if n.Ordinal != int64(i) || n.ProjectID != n.ID || n.Profile != season.Profile || n.Summary != root.Summary {
			t.Fatalf("episode %d: %+v", i, n)
		}
		if i == 1 {
			first = n
		}
	}
	var after string
	seen := map[string]bool{}
	for {
		page, err := s.List(ctx, testActor.Space, Query{Kind: "episode", Parent: season.ID, Limit: 37, After: after})
		if err != nil {
			t.Fatal(err)
		}
		for _, n := range page.Items {
			if seen[n.ID] {
				t.Fatal("duplicate cursor item")
			}
			seen[n.ID] = true
		}
		if page.Next == "" {
			break
		}
		after = page.Next
	}
	if len(seen) != 201 {
		t.Fatalf("got %d episodes", len(seen))
	}
	if _, err := s.List(ctx, testActor.Space, Query{Kind: "episode", Limit: 30, After: "1:invalid"}); !errors.Is(err, series.ErrInput) {
		t.Fatal("bad cursor accepted", err)
	}
	if _, err := s.Get(ctx, "another", "episode", first.ID); !errors.Is(err, series.ErrNotFound) {
		t.Fatal("space leak", err)
	}
	if page, err := s.List(ctx, "another", Query{Kind: "series", Limit: 20}); err != nil || len(page.Items) != 0 {
		t.Fatal("space list leak", err)
	}
	current, err := s.Get(ctx, testActor.Space, "season", season.ID)
	if err != nil {
		t.Fatal(err)
	}
	move := series.Command{Operation: "move", Kind: "episode", ID: first.ID, Expected: 1, ExpectedParent: current.Version, Ordinal: 300}
	moved, err := s.Mutate(ctx, testActor, "move-episode", move, nil)
	if err != nil || moved.Ordinal != 300 {
		t.Fatal("move", err)
	}
	// Lost response must replay the accepted command before stale CAS checks.
	if replay, err := s.Mutate(ctx, testActor, "move-episode", move, nil); err != nil || replay.Ordinal != 300 {
		t.Fatal("move replay", err)
	}
	stale := move
	stale.ID = series.NewID(testActor, "create", "episode-002")
	stale.Ordinal = 301
	if _, err = s.Mutate(ctx, testActor, "stale-move", stale, nil); !errors.Is(err, series.ErrConflict) {
		t.Fatal("stale parent accepted", err)
	}
	next := create(t, s, command("episode", season.ID, "after-move"), "after-move")
	if next.Ordinal != 301 {
		t.Fatal("ordinal counter overwrote moved position")
	}
	denied := testActor
	denied.Writable = false
	if _, err = s.Mutate(ctx, denied, "move-episode", move, nil); !errors.Is(err, series.ErrDenied) {
		t.Fatal("replay bypassed revocation")
	}
	update := series.Command{Operation: "update", Kind: "series", ID: root.ID, Expected: 2, Input: series.Input{Title: root.Title, Summary: root.Summary, Profile: root.Profile}, Status: "archived"}
	if _, err = s.Mutate(ctx, testActor, "archive-root", update, nil); err != nil {
		t.Fatal(err)
	}
	c := command("episode", season.ID, "after-archive")
	if s.db == nil {
		_, err = s.Mutate(ctx, testActor, "after-archive", c, func(series.Node) error { return nil })
	} else {
		tx, _ := s.db.BeginTx(ctx, nil)
		_, err = s.MutateTx(ctx, tx, testActor, "after-archive", c)
		tx.Rollback()
	}
	if !errors.Is(err, series.ErrConflict) {
		t.Fatal("archived root accepted new episode", err)
	}
	// Existing episodes remain readable when their root is archived.
	if _, err = s.Get(ctx, testActor.Space, "episode", first.ID); err != nil {
		t.Fatal(err)
	}
}
func TestFileDirectory201PaginationReorderAndIsolation(t *testing.T) {
	s, err := Open(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	directoryContract(t, s)
}
func TestFileConcurrentStoresAndCAS(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir, nil)
	_, season := fixture(t, s)
	var wg sync.WaitGroup
	errCh := make(chan error, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			other, err := Open(dir, nil)
			if err != nil {
				errCh <- err
				return
			}
			key := fmt.Sprintf("concurrent-%02d", i)
			_, err = other.Mutate(ctx, testActor, key, command("episode", season.ID, key), func(series.Node) error { return nil })
			errCh <- err
		}(i)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatal(err)
		}
	}
	page, err := s.List(ctx, testActor.Space, Query{Kind: "episode", Parent: season.ID, Limit: 100})
	if err != nil || len(page.Items) != 20 {
		t.Fatal("lost writes", err)
	}
	for i, n := range page.Items {
		if n.Ordinal != int64(i+1) {
			t.Fatal("duplicate ordinal")
		}
	}
	c := series.Command{Operation: "update", Kind: "season", ID: season.ID, Expected: 21, Input: series.Input{Profile: season.Profile}, Status: "active"}
	successes := 0
	errCh = make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			other, _ := Open(dir, nil)
			_, err := other.Mutate(ctx, testActor, fmt.Sprintf("cas-race-%d", i), c, nil)
			errCh <- err
		}(i)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err == nil {
			successes++
		} else if !errors.Is(err, series.ErrConflict) {
			t.Fatal(err)
		}
	}
	if successes != 1 {
		t.Fatalf("CAS winners %d", successes)
	}
}
func TestPreparingRecoveryAndReplayPreserveEditedProject(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir, nil)
	_, season := fixture(t, s)
	c := command("episode", season.ID, "interrupted-project")
	errCrash := errors.New("process interrupted after core project committed")
	calls := 0
	project := map[string]string{}
	if _, err := s.Mutate(ctx, testActor, "interrupted-project", c, func(n series.Node) error { calls++; project[n.ProjectID] = "edited title"; return errCrash }); !errors.Is(err, errCrash) {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, testActor.Space, "episode", c.ID); !errors.Is(err, series.ErrNotFound) {
		t.Fatal("preparing episode visible")
	}
	s, _ = Open(dir, nil)
	if err := s.RecoverPreparing(ctx, func(n series.Node) error {
		calls++
		if project[n.ProjectID] != "edited title" {
			t.Fatal("recovery lost stable project identity")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if n, err := s.ByProject(ctx, testActor.Space, c.ID); err != nil || n.State != "ready" {
		t.Fatal("not recovered", err)
	}
	if _, err := s.Mutate(ctx, testActor, "interrupted-project", c, func(series.Node) error { t.Fatal("replay rewrote edited project"); return nil }); err != nil {
		t.Fatal(err)
	}
	changed := c
	changed.Input.Title = "another"
	if _, err := s.Mutate(ctx, testActor, "interrupted-project", changed, nil); !errors.Is(err, ErrKey) {
		t.Fatal("key reused with changed body", err)
	}
	if calls != 2 {
		t.Fatal("unexpected project callbacks", calls)
	}
}
func TestUncommittedRowsIgnoredAndAcceptedCorruptionFailsClosed(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir, nil)
	root, _ := fixture(t, s)
	base := filepath.Join(dir, series.SHA(testActor.Space))
	nodeDir := filepath.Join(base, "nodes", "series", root.ID)
	orphan := filepath.Join(nodeDir, "00000000000009999999-"+series.SHA("no commit")+".json")
	if err := os.WriteFile(orphan, []byte("incomplete"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, testActor.Space, "series", root.ID); err != nil {
		t.Fatal("orphan exposed", err)
	}
	entries, _ := os.ReadDir(nodeDir)
	for _, entry := range entries {
		if entry.Name() != filepath.Base(orphan) {
			if err := os.WriteFile(filepath.Join(nodeDir, entry.Name()), []byte("corrupt"), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := s.Get(ctx, testActor.Space, "series", root.ID); !errors.Is(err, ErrStorage) {
		t.Fatal("accepted corruption silently bypassed", err)
	}
}

func TestFileDirectorySymlinksAreRejected(t *testing.T) {
	for _, level := range []string{"space", "kind", "node"} {
		t.Run(level, func(t *testing.T) {
			dir := t.TempDir()
			s, _ := Open(dir, nil)
			root, _ := fixture(t, s)
			base := filepath.Join(dir, series.SHA(testActor.Space))
			path := base
			if level == "kind" {
				path = filepath.Join(base, "nodes", "series")
			}
			if level == "node" {
				path = filepath.Join(base, "nodes", "series", root.ID)
			}
			target := filepath.Join(t.TempDir(), "moved")
			if err := os.Rename(path, target); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, path); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Get(ctx, testActor.Space, "series", root.ID); !errors.Is(err, ErrStorage) {
				t.Fatal("followed metadata directory symlink", err)
			}
		})
	}
}

func TestOutputIntentInheritanceDoesNotRewriteExistingEpisodes(t *testing.T) {
	s, _ := Open(t.TempDir(), nil)
	root, season := fixture(t, s)
	first := create(t, s, command("episode", season.ID, "profile-first"), "profile-first")
	profile := series.Profile{Duration: 120, Resolution: "2160p", Aspect: "16:9"}
	rootUpdate := series.Command{Operation: "update", Kind: "series", ID: root.ID, Expected: 2, Status: "active", Input: series.Input{Title: root.Title, Summary: root.Summary, Profile: profile}}
	if _, err := s.Mutate(ctx, testActor, "root-profile", rootUpdate, nil); err != nil {
		t.Fatal(err)
	}
	second := create(t, s, command("episode", season.ID, "profile-second"), "profile-second")
	if second.Profile != first.Profile {
		t.Fatal("root change rewrote existing season intent")
	}
	nextSeason := create(t, s, command("season", root.ID, "new-season"), "new-season")
	if nextSeason.Profile != profile {
		t.Fatal("future season did not inherit changed intent")
	}
	episodeUpdate := series.Command{Operation: "update", Kind: "episode", ID: first.ID, Expected: 1, Status: "writing", Input: series.Input{Profile: profile}}
	updated, err := s.Mutate(ctx, testActor, "episode-profile", episodeUpdate, nil)
	if err != nil || updated.Profile != profile || updated.Title != first.Title {
		t.Fatal("profile update", err)
	}
	episodeUpdate.Expected = 2
	episodeUpdate.Input.Title = "silently overwrite project title"
	if _, err = s.Mutate(ctx, testActor, "invalid-title-edit", episodeUpdate, nil); !errors.Is(err, series.ErrInput) {
		t.Fatal("episode title must be edited through Project", err)
	}
	unchanged, err := s.Get(ctx, testActor.Space, "episode", second.ID)
	if err != nil || unchanged.Profile != second.Profile {
		t.Fatal("existing episode rewritten", err)
	}
}
