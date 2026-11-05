package promptrepo_test

import (
	"context"
	"errors"
	"fmt"
	"github.com/adcwb/ai-creative-studio/backend/internal/modules/prompts"
	"github.com/adcwb/ai-creative-studio/backend/internal/persistence/promptrepo"
	"sync"
	"testing"
)

func command() promptrepo.Command {
	return promptrepo.Command{Operation: "create", Metadata: prompts.Metadata{Name: "我的花园 %_", Visibility: "private", Tags: []string{"花园"}}, Recipe: prompts.Recipe{Syntax: "plain", Body: "清晨花园中的紫色花朵 {{literal}}", Defaults: prompts.Defaults{Kind: "poster", Ratio: "4:5", Style: "natural"}}}
}
func TestEntityFilesConcurrencyReplayVisibilityAndHistory(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, e := promptrepo.Open(dir, nil)
	if e != nil {
		t.Fatal(e)
	}
	second, e := promptrepo.Open(dir, nil)
	if e != nil {
		t.Fatal(e)
	}
	owner := prompts.Actor{Space: "space", User: "owner", Writable: true}
	editor := prompts.Actor{Space: "space", User: "editor", Writable: true}
	id := "11111111111111111111111111111111"
	cmd := command()
	first, e := s.Mutate(ctx, owner, id, "create-key", cmd)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Get(ctx, editor, id); !errors.Is(e, prompts.ErrNotFound) {
		t.Fatal("private", e)
	}
	if _, e = s.Mutate(ctx, editor, id, "create-key", cmd); !errors.Is(e, prompts.ErrNotFound) {
		t.Fatal("private replay", e)
	}
	changed := cmd
	changed.Recipe.Body = "不同内容"
	if _, e = s.Mutate(ctx, owner, id, "create-key", changed); !errors.Is(e, promptrepo.ErrKey) {
		t.Fatal("key collision", e)
	}
	versions := make(chan error, 2)
	var wg sync.WaitGroup
	for i, repo := range []*promptrepo.Store{s, second} {
		wg.Add(1)
		go func(i int, repo *promptrepo.Store) {
			defer wg.Done()
			update := promptrepo.Command{Operation: "version", Recipe: cmd.Recipe, ExpectedHead: first.Version.ID}
			update.Recipe.Body = fmt.Sprintf("新的花园版本%d", i)
			_, e := repo.Mutate(ctx, owner, id, fmt.Sprintf("version-key%d", i), update)
			versions <- e
		}(i, repo)
	}
	wg.Wait()
	close(versions)
	wins, conflicts := 0, 0
	for e := range versions {
		if e == nil {
			wins++
		} else if errors.Is(e, prompts.ErrConflict) {
			conflicts++
		} else {
			t.Fatal(e)
		}
	}
	if wins != 1 || conflicts != 1 {
		t.Fatal(wins, conflicts)
	}
	current, e := s.Get(ctx, owner, id)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Version(ctx, owner, id, first.Version.ID); e != nil {
		t.Fatal("lost history", e)
	}
	current.Template.Visibility = "organization"
	meta := promptrepo.Command{Operation: "metadata", Metadata: current.Template.Metadata, ExpectedRow: current.Template.RowVersion, Status: "active"}
	current, e = s.Mutate(ctx, owner, id, "share-key", meta)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Get(ctx, editor, id); e != nil {
		t.Fatal("org visibility", e)
	}
	if e = s.Favorite(ctx, editor, id, true); e != nil {
		t.Fatal(e)
	}
	q := promptrepo.Query{Source: "organization", Status: "active", Text: "花园", Favorite: true, Limit: 1}
	items, e := s.List(ctx, editor, q)
	if e != nil || len(items) != 1 || !items[0].Favorite {
		t.Fatal("query", items, e)
	}
	q.Text = "%_"
	q.Favorite = false
	items, e = s.List(ctx, owner, q)
	if e != nil || len(items) != 1 {
		t.Fatal("literal search", e)
	}
	forbidden := meta
	forbidden.Metadata.Visibility = "private"
	forbidden.ExpectedRow = current.Template.RowVersion
	if _, e = s.Mutate(ctx, editor, id, "takeover-key", forbidden); !errors.Is(e, prompts.ErrDenied) {
		t.Fatal("editor privacy takeover", e)
	}
	meta.Metadata.Visibility = "private"
	meta.ExpectedRow = current.Template.RowVersion
	current, e = s.Mutate(ctx, owner, id, "private-key", meta)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Get(ctx, editor, id); !errors.Is(e, prompts.ErrNotFound) {
		t.Fatal("revoke", e)
	}
	if _, e = s.Mutate(ctx, editor, id, "takeover-key", forbidden); !errors.Is(e, prompts.ErrNotFound) {
		t.Fatal("replay leaks after privacy revoke", e)
	}
	meta.ExpectedRow = current.Template.RowVersion
	meta.Status = "archived"
	current, e = s.Mutate(ctx, owner, id, "archive-key", meta)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Mutate(ctx, owner, id, "archived-version", promptrepo.Command{Operation: "version", Recipe: cmd.Recipe, ExpectedHead: current.Version.ID}); !errors.Is(e, prompts.ErrArchived) {
		t.Fatal("archive", e)
	}
	s, e = promptrepo.Open(dir, nil)
	if e != nil {
		t.Fatal(e)
	}
	replay, e := s.Mutate(ctx, owner, id, "create-key", cmd)
	if e != nil || replay.Version.ID != first.Version.ID || replay.Template.RowVersion != 1 {
		t.Fatal("restart original receipt", e)
	}
	history, next, e := s.Versions(ctx, owner, id, "", 1)
	if e != nil || len(history) != 1 || history[0].Number != 2 || next == "" {
		t.Fatal("history page", e)
	}
	history, _, e = s.Versions(ctx, owner, id, next, 1)
	if e != nil || len(history) != 1 || history[0].Number != 1 {
		t.Fatal("history page2", e)
	}
}
