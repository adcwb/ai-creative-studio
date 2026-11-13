package workflowrepo

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	w "github.com/adcwb/ai-creative-studio/backend/internal/modules/workflows"
)

func appendRevision(d *Document) {
	v := d.Workflows["flow"]
	v.ID = "flow"
	v.Name = "Owned flow"
	v.Version++
	g := w.Graph{Nodes: []w.Node{{ID: "media", Kind: "asset", AssetKind: "image"}, {ID: "output", Kind: "output"}}, Edges: []w.Edge{{Source: "media", Target: "output", Input: "media"}}}
	r := w.Revision{ID: fmt.Sprintf("revision-%d", v.Version), Number: v.Version, Graph: g, SHA256: w.Digest(g), Actor: "actor"}
	v.Revisions = append(v.Revisions, r)
	v.Head = r.ID
	d.Workflows[v.ID] = v
}
func TestFileTransactionsAcrossInstancesAndReopen(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	a, err := Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	failures := make(chan error, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s := a
			if i%2 == 0 {
				s = b
			}
			failures <- s.Update(ctx, "team", "project", func(d *Document) error { appendRevision(d); return nil })
		}(i)
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	aborted := errors.New("synthetic interruption")
	if err = a.Update(ctx, "team", "project", func(d *Document) error { appendRevision(d); return aborted }); err != aborted {
		t.Fatal(err)
	}
	reopened, err := Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	d, err := reopened.Read(ctx, "team", "project")
	if err != nil || d.Workflows["flow"].Version != 20 {
		t.Fatal("lost or partially committed revisions", d, err)
	}
	copyDir := filepath.Join(t.TempDir(), "validated")
	if err = CopyValidation(ctx, dir, copyDir); err != nil {
		t.Fatal(err)
	}
	copied, err := Open(copyDir, nil)
	if err != nil {
		t.Fatal(err)
	}
	copyDoc, err := copied.Read(ctx, "team", "project")
	if err != nil || w.Digest(copyDoc) != w.Digest(d) {
		t.Fatal("offline copy changed history", err)
	}
	foreign, err := copied.Read(ctx, "foreign", "project")
	if err != nil || len(foreign.Workflows) != 0 {
		t.Fatal("scope leak", err)
	}
}
func TestCorruptionFailsClosedAndIsNotOverwritten(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, w.Digest([]string{"team", "project"})+".json")
	for _, raw := range []string{"", `{"format":1,"space_id":"foreign","project_id":"project"}`} {
		if err = os.WriteFile(path, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err = s.Read(ctx, "team", "project"); !errors.Is(err, ErrStorage) {
			t.Fatal("corruption read accepted", err)
		}
		if err = s.Update(ctx, "team", "project", func(d *Document) error { appendRevision(d); return nil }); !errors.Is(err, ErrStorage) {
			t.Fatal("corruption overwritten", err)
		}
		got, _ := os.ReadFile(path)
		if string(got) != raw {
			t.Fatal("corrupt source changed")
		}
	}
}
