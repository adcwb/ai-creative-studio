package libraryrepo_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/library"
	"github.com/adcwb/ai-creative-studio/backend/internal/persistence/libraryrepo"
)

func TestLibraryFileCrashHelper(t *testing.T) {
	if os.Getenv("STUDIO_LIBRARY_CRASH_HELPER") != "1" {
		return
	}
	s, err := libraryrepo.Open(os.Getenv("STUDIO_LIBRARY_CRASH_DIR"), nil)
	if err != nil {
		t.Fatal(err)
	}
	for i, id := range []string{"v1", "v2"} {
		expected := ""
		if i > 0 {
			expected = "v1"
		}
		if _, err = s.Mutate(context.Background(), "space", id, id, expected, result("actor", id, "space", i+1), false); err != nil {
			t.Fatal(err)
		}
	}
	fmt.Fprintln(os.Stdout, "committed")
	// Parent kills this process after acknowledgement, without clean shutdown.
	_, _ = bufio.NewReader(os.Stdin).ReadByte()
}

func TestAcknowledgedRecordsAndOriginalReceiptsSurviveProcessKill(t *testing.T) {
	dir := t.TempDir()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable, "-test.run=^TestLibraryFileCrashHelper$")
	cmd.Env = append(os.Environ(), "STUDIO_LIBRARY_CRASH_HELPER=1", "STUDIO_LIBRARY_CRASH_DIR="+dir)
	input, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	output, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	line, err := bufio.NewReader(output).ReadString('\n')
	if err != nil || line != "committed\n" {
		t.Fatal("child did not acknowledge", line, err)
	}
	if err = cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err = cmd.Wait(); err == nil {
		t.Fatal("process was not killed")
	}
	s, err := libraryrepo.Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	current, err := s.Get(context.Background(), "space", "actor")
	if err != nil || current.Version.ID != "v2" {
		t.Fatal(current, err)
	}
	old, ok, err := s.Replay(context.Background(), "space", "v1", "v1")
	if err != nil || !ok || old.Version.ID != "v1" {
		t.Fatal(old, ok, err)
	}
}

func legacyFixture(t *testing.T, dir string, versions []libraryrepo.Result, receipts map[string]any) string {
	t.Helper()
	path := filepath.Join(dir, "space.json")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	if _, err = f.WriteString(`{"format":1,"assets":{"actor":`); err != nil {
		t.Fatal(err)
	}
	if err = enc.Encode(versions[len(versions)-1].Asset); err != nil {
		t.Fatal(err)
	}
	if _, err = f.WriteString(`},"versions":{`); err != nil {
		t.Fatal(err)
	}
	for i, r := range versions {
		if i > 0 {
			if _, err = f.WriteString(","); err != nil {
				t.Fatal(err)
			}
		}
		if _, err = fmt.Fprintf(f, "%q:", r.Version.ID); err != nil {
			t.Fatal(err)
		}
		if err = enc.Encode(r.Version); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = f.WriteString(`},"receipts":`); err != nil {
		t.Fatal(err)
	}
	if err = enc.Encode(receipts); err != nil {
		t.Fatal(err)
	}
	if _, err = f.WriteString("}"); err != nil {
		t.Fatal(err)
	}
	if err = f.Sync(); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLegacyUpgradePreservesArchiveAndExactReceipts(t *testing.T) {
	dir := t.TempDir()
	v1 := result("actor", "v1", "space", 1)
	v2 := result("actor", "v2", "space", 2)
	v2.Asset.Status = "archived"
	path := legacyFixture(t, dir, []libraryrepo.Result{v1, v2}, map[string]any{"original": map[string]any{"digest": "first", "result": v1}, "archive": map[string]any{"digest": "archived", "result": v2}})
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s, err := libraryrepo.Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(context.Background(), "space", "actor")
	if err != nil || got.Asset.Status != "archived" || got.Version.ID != "v2" {
		t.Fatal(got, err)
	}
	old, ok, err := s.Replay(context.Background(), "space", "original", "first")
	if err != nil || !ok || old.Asset.Status != "active" || old.Version.ID != "v1" {
		t.Fatal(old, ok, err)
	}
	if _, err = s.Version(context.Background(), "space", "actor", "v1"); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil || string(before) != string(after) {
		t.Fatal("legacy recovery copy changed", err)
	}
	// Once the complete new format is published it is authoritative. Stale old
	// files must not resurrect archived objects or overwrite accepted receipts.
	if err = os.WriteFile(path, []byte("invalid stale recovery snapshot"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Get(context.Background(), "space", "actor"); err != nil {
		t.Fatal(err)
	}
	if _, err = libraryrepo.FileReferences(dir); err != nil {
		t.Fatal("new format offline inventory", err)
	}
}

func TestSeparateFileStoresSerializeCASAndGlobalReceiptKeys(t *testing.T) {
	dir := t.TempDir()
	a, _ := libraryrepo.Open(dir, nil)
	b, _ := libraryrepo.Open(dir, nil)
	ctx := context.Background()
	if _, err := a.Mutate(ctx, "space", "first", "d1", "", result("actor", "v1", "space", 1), false); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	out := make(chan error, 2)
	var wg sync.WaitGroup
	for i, s := range []*libraryrepo.Store{a, b} {
		wg.Add(1)
		go func(i int, s *libraryrepo.Store) {
			defer wg.Done()
			<-start
			_, err := s.Mutate(ctx, "space", fmt.Sprint(i), fmt.Sprint(i), "v1", result("actor", fmt.Sprint("next", i), "space", 2), false)
			out <- err
		}(i, s)
	}
	close(start)
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
		t.Fatal(wins, conflicts)
	}
	if _, err := b.Mutate(ctx, "space", "first", "different", "", result("another", "v9", "space", 1), false); !errors.Is(err, libraryrepo.ErrKey) {
		t.Fatal("receipt was scoped to wrong asset", err)
	}
}

func TestHyphenatedVersionIdentitiesAreExact(t *testing.T) {
	s, _ := libraryrepo.Open(t.TempDir(), nil)
	ctx := context.Background()
	previous := ""
	for i, id := range []string{"v1-other", "v1", "v1-other-again"} {
		if _, err := s.Mutate(ctx, "space", id, id, previous, result("actor", id, "space", i+1), false); err != nil {
			t.Fatal(err)
		}
		previous = id
	}
	for i, id := range []string{"v1-other", "v1", "v1-other-again"} {
		v, err := s.Version(ctx, "space", "actor", id)
		if err != nil || v.ID != id || v.Number != i+1 {
			t.Fatal(v, err)
		}
	}
	if _, err := s.Version(ctx, "space", "actor", "v1-oth"); !errors.Is(err, libraryrepo.ErrNotFound) {
		t.Fatal("accepted prefix identity", err)
	}
}

func TestReferenceInventoryReadsLegacyAndEntitiesWithoutChangingSource(t *testing.T) {
	dir := t.TempDir()
	v1, v2 := result("actor", "v1", "space", 1), result("actor", "v2", "space", 2)
	for i, r := range []*libraryrepo.Result{&v1, &v2} {
		r.Version.References = []library.Reference{{ID: fmt.Sprint("ref", i), Role: "reference", MIMEType: "image/png", Width: 16, Height: 16, SizeBytes: 100, SHA256: strings.Repeat("a", 64), SourceProjectID: "project", SourceAssetID: fmt.Sprint("source", i)}}
		r.Version.SHA256, _ = library.Digest(struct {
			Card       library.Card
			References []library.Reference
		}{r.Version.Card, r.Version.References})
	}
	v2.Asset.Status = "archived"
	legacyFixture(t, dir, []libraryrepo.Result{v1, v2}, map[string]any{})
	for _, upgraded := range []bool{false, true} {
		if upgraded {
			s, _ := libraryrepo.Open(dir, nil)
			if _, err := s.Get(context.Background(), "space", "actor"); err != nil {
				t.Fatal(err)
			}
		}
		before, _ := os.ReadDir(dir)
		refs, err := libraryrepo.FileReferences(dir)
		if err != nil || len(refs["space"]) != 2 {
			t.Fatal("lost historical reference", refs, err)
		}
		seen := map[string]bool{}
		for _, ref := range refs["space"] {
			seen[ref.ID] = true
		}
		if !seen["ref0"] || !seen["ref1"] {
			t.Fatal(refs)
		}
		after, _ := os.ReadDir(dir)
		if len(before) != len(after) {
			t.Fatal("inventory changed source directory")
		}
	}
}

func TestFileLockWaitHonorsCancellation(t *testing.T) {
	dir := t.TempDir()
	s, _ := libraryrepo.Open(dir, nil)
	f, err := os.OpenFile(filepath.Join(dir, ".lock-space"), os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err = s.Get(ctx, "space", "actor"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("lock ignored deadline", err)
	}
}

func TestAssetQueriesDoNotDecodeOtherAssetsOrUnselectedHistory(t *testing.T) {
	dir := t.TempDir()
	s, _ := libraryrepo.Open(dir, nil)
	ctx := context.Background()
	for _, id := range []string{"actor", "other"} {
		version := "v1"
		if id == "other" {
			version = "unrelated"
		}
		if _, err := s.Mutate(ctx, "space", id, id, "", result(id, version, "space", 1), false); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Mutate(ctx, "space", "next", "next", "v1", result("actor", "v2", "space", 2), false); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"actor", "other"} {
		entries, err := os.ReadDir(filepath.Join(dir, "space.entities", id))
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			if strings.Contains(e.Name(), "-v-v1-") || strings.Contains(e.Name(), "-v-unrelated-") {
				if err = os.WriteFile(filepath.Join(dir, "space.entities", id, e.Name()), []byte("corrupted historical fixture"), 0600); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	if r, err := s.Get(ctx, "space", "actor"); err != nil || r.Version.ID != "v2" {
		t.Fatal("unrelated/history bodies loaded", r, err)
	}
	if v, err := s.Versions(ctx, "space", "actor", "", 1); err != nil || len(v) != 1 || v[0].ID != "v2" {
		t.Fatal(v, err)
	}
	if _, err := s.Version(ctx, "space", "actor", "v1"); !errors.Is(err, libraryrepo.ErrStorage) {
		t.Fatal("corrupt requested history accepted", err)
	}
}

func TestIncompleteUpgradeSymlinksAndInterruptedRecordsFailClosed(t *testing.T) {
	for _, mode := range []string{"missing-marker", "bad-marker", "fifo-marker", "fifo-lock", "symlink-record", "trailing-data"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			s, _ := libraryrepo.Open(dir, nil)
			ctx := context.Background()
			if _, err := s.Mutate(ctx, "space", "key", "digest", "", result("actor", "v1", "space", 1), false); err != nil {
				t.Fatal(err)
			}
			entity := filepath.Join(dir, "space.entities", "actor")
			entries, _ := os.ReadDir(entity)
			record := filepath.Join(entity, entries[0].Name())
			var err error
			switch mode {
			case "missing-marker":
				err = os.Remove(filepath.Join(dir, "space.entities", ".format"))
			case "bad-marker":
				err = os.WriteFile(filepath.Join(dir, "space.entities", ".format"), []byte("future"), 0600)
			case "fifo-marker":
				path := filepath.Join(dir, "space.entities", ".format")
				if err = os.Remove(path); err == nil {
					err = syscall.Mkfifo(path, 0600)
				}
			case "fifo-lock":
				path := filepath.Join(dir, ".lock-space")
				if err = os.Remove(path); err == nil {
					err = syscall.Mkfifo(path, 0600)
				}
			case "symlink-record":
				target := filepath.Join(dir, "target")
				b, _ := os.ReadFile(record)
				if err = os.WriteFile(target, b, 0600); err == nil {
					err = os.Remove(record)
				}
				if err == nil {
					err = os.Symlink(target, record)
				}
			case "trailing-data":
				f, e := os.OpenFile(record, os.O_APPEND|os.O_WRONLY, 0)
				if e != nil {
					t.Fatal(e)
				}
				_, err = f.WriteString(` {"extra":true}`)
				f.Close()
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.Get(ctx, "space", "actor"); !errors.Is(err, libraryrepo.ErrStorage) {
				t.Fatal("accepted corrupt state", err)
			}
		})
	}
	// An uncommitted temporary record is harmless, and must not become a head.
	dir := t.TempDir()
	s, _ := libraryrepo.Open(dir, nil)
	if _, err := s.Mutate(context.Background(), "space", "key", "d", "", result("actor", "v1", "space", 1), false); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "space.entities", "actor", ".pending-interrupted"), []byte("partial"), 0600); err != nil {
		t.Fatal(err)
	}
	if r, err := s.Get(context.Background(), "space", "actor"); err != nil || r.Version.ID != "v1" {
		t.Fatal(r, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Get(ctx, "space", "actor"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestLegacyOver64MiBUpgradesAndContinuesWithoutCumulativeLimit(t *testing.T) {
	if testing.Short() {
		t.Skip("large legacy-format regression")
	}
	dir := t.TempDir()
	card := library.Card{Name: "Large synthetic character", Description: strings.Repeat("文", 2000), Character: &library.Character{Appearance: strings.Repeat("貌", 2000), Hair: strings.Repeat("发", 2000), Costume: strings.Repeat("衣", 2000), Expression: strings.Repeat("情", 2000), VoiceNotes: strings.Repeat("音", 2000)}}
	for i := 0; i < 20; i++ {
		card.ProhibitedChanges = append(card.ProhibitedChanges, strings.Repeat("规", 295)+fmt.Sprint(i))
	}
	card, err := library.Normalize("character", card)
	if err != nil {
		t.Fatal(err)
	}
	versions := make([]libraryrepo.Result, 1320)
	for i := range versions {
		r := result("actor", fmt.Sprintf("v%04d", i+1), "space", i+1)
		r.Version.Card = card
		r.Asset.Name = card.Name
		r.Version.SHA256, _ = library.Digest(struct {
			Card       library.Card
			References []library.Reference
		}{r.Version.Card, r.Version.References})
		versions[i] = r
	}
	path := legacyFixture(t, dir, versions, map[string]any{"original": map[string]any{"digest": "original", "result": versions[0]}})
	st, err := os.Stat(path)
	if err != nil || st.Size() <= 64<<20 {
		t.Fatal("fixture did not cross old boundary", st, err)
	}
	s, _ := libraryrepo.Open(dir, nil)
	ctx := context.Background()
	started := time.Now()
	r, err := s.Get(ctx, "space", "actor")
	if err != nil || r.Version.Number != len(versions) {
		t.Fatal("large legacy upgrade rejected", err)
	}
	next := result("actor", "after_upgrade", "space", len(versions)+1)
	if _, err = s.Mutate(ctx, "space", "after", "after", r.Version.ID, next, false); err != nil {
		t.Fatal("cannot append beyond old limit", err)
	}
	if old, ok, err := s.Replay(ctx, "space", "original", "original"); err != nil || !ok || old.Version.Number != 1 {
		t.Fatal(old, ok, err)
	}
	page, err := s.Versions(ctx, "space", "actor", r.Version.ID, 2)
	if err != nil || len(page) != 2 || page[0].Number != len(versions)-1 {
		t.Fatal(page, err)
	}
	var total int64
	err = filepath.WalkDir(filepath.Join(dir, "space.entities"), func(path string, e os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !e.IsDir() {
			st, err := e.Info()
			if err != nil {
				return err
			}
			total += st.Size()
		}
		return nil
	})
	if err != nil || total <= 64<<20 {
		t.Fatal("new format fixture too small", total, err)
	}
	t.Logf("legacy=%d new-records=%d bytes; upgrade/append/replay/page=%s", st.Size(), total, time.Since(started))
}
