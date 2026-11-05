package libraryrepo_test

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/library"
	"github.com/adcwb/ai-creative-studio/backend/internal/persistence/libraryrepo"
)

func TestAuthorizationAtomicCASHistoryAndContentWriters(t *testing.T) {
	for _, mode := range []string{"file", "postgres"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			var s *libraryrepo.Store
			var e error
			if mode == "postgres" {
				s, e = libraryrepo.Open("", libraryTestDB(t))
			} else {
				s, e = libraryrepo.Open(dir, nil)
			}
			if e != nil {
				t.Fatal(e)
			}
			ctx := context.Background()
			old := result("actor", "v1", "space", 1)
			if _, e = s.Mutate(ctx, "space", "create", "create", "", old, false); e != nil {
				t.Fatal(e)
			}
			now := time.Now().UTC()
			in := library.AuthorizationInput{Status: "granted", VersionIDs: []string{"v1"}, Purposes: []string{"image_reference"}, Holder: "creator", Basis: "own_work", Evidence: "synthetic original", Attested: true}
			grant, e := s.SetAuthorization(ctx, "space", "actor", "grant", "g", "creator", in, now)
			if e != nil || grant.Asset.UseAuthorization.Revision != 1 {
				t.Fatal(grant, e)
			}
			bad := in
			bad.Expected = 1
			bad.VersionIDs = []string{"foreign"}
			if _, e = s.SetAuthorization(ctx, "space", "actor", "bad-scope", "b", "creator", bad, now); !errors.Is(e, libraryrepo.ErrNotFound) {
				t.Fatal("cross-version scope", e)
			}
			current, _ := s.Get(ctx, "space", "actor")
			if current.Asset.UseAuthorization.Revision != 1 {
				t.Fatal("partial update")
			}
			if _, e = s.SetAuthorization(ctx, "other", "actor", "other", "g", "creator", in, now); !errors.Is(e, libraryrepo.ErrNotFound) {
				t.Fatal("tenant isolation", e)
			}
			// Independent writers with the same expected revision have exactly one winner.
			var wg sync.WaitGroup
			out := make(chan error, 2)
			for _, key := range []string{"revoke-a", "revoke-b"} {
				wg.Add(1)
				go func(key string) {
					defer wg.Done()
					_, err := s.SetAuthorization(ctx, "space", "actor", key, key, "editor", library.AuthorizationInput{Expected: 1, Status: "revoked", Reason: "withdrawn"}, now.Add(time.Minute))
					out <- err
				}(key)
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
				t.Fatal(wins, conflicts)
			}
			// A content write prepared before revocation must preserve current authority.
			next := result("actor", "v2", "space", 2)
			next.Asset.UseAuthorization = grant.Asset.UseAuthorization
			saved, e := s.Mutate(ctx, "space", "new-version", "v2", "v1", next, false)
			if e != nil || saved.Asset.UseAuthorization.Status != "revoked" || saved.Asset.UseAuthorization.Revision != 2 {
				t.Fatal("resurrected permission", saved, e)
			}
			replay, e := s.SetAuthorization(ctx, "space", "actor", "grant", "g", "creator", in, now.Add(2*time.Hour))
			if e != nil || !reflect.DeepEqual(replay, grant) {
				t.Fatal("lost original receipt", e)
			}
			if _, e = s.SetAuthorization(ctx, "space", "actor", "grant", "changed", "creator", in, now); !errors.Is(e, libraryrepo.ErrKey) {
				t.Fatal("key conflict", e)
			}
			if mode == "file" {
				s, e = libraryrepo.Open(dir, nil)
				if e != nil {
					t.Fatal(e)
				}
			}
			current, e = s.Get(ctx, "space", "actor")
			if e != nil || current.Asset.UseAuthorization.Status != "revoked" || current.Version.ID != "v2" {
				t.Fatal(current, e)
			}
			original, e := s.Version(ctx, "space", "actor", "v1")
			if e != nil || !reflect.DeepEqual(original, old.Version) {
				t.Fatal("rewrote immutable version", e)
			}
			versions, e := s.Versions(ctx, "space", "actor", "", 20)
			if e != nil || len(versions) != 2 {
				t.Fatal("metadata wrote content version", len(versions), e)
			}
			listed, e := s.List(ctx, "space", libraryrepo.Query{Limit: 20})
			if e != nil || len(listed) != 1 || listed[0].UseAuthorization != nil {
				t.Fatal("unbounded evidence in list", e)
			}
			// A new version is not implicitly included by a later regrant of v1.
			in.Expected = 2
			_, e = s.SetAuthorization(ctx, "space", "actor", "regrant", "rg", "creator", in, now.Add(2*time.Minute))
			if e != nil {
				t.Fatal(e)
			}
			current, _ = s.Get(ctx, "space", "actor")
			if current.Asset.UseAuthorization.Allows("v2", "image_reference", now) {
				t.Fatal("future version implicitly authorized")
			}
		})
	}
}

func TestAuthorizationOfflineRoundTripKeepsCurrentRevocationAndOriginalGrant(t *testing.T) {
	ctx := context.Background()
	db := libraryTestDB(t)
	s, e := libraryrepo.Open("", db)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Mutate(ctx, "space", "create", "create", "", result("actor", "v1", "space", 1), false); e != nil {
		t.Fatal(e)
	}
	in := library.AuthorizationInput{Status: "granted", VersionIDs: []string{"v1"}, Purposes: []string{"image_reference"}, Holder: "creator", Basis: "own_work", Evidence: "synthetic original", Attested: true}
	now := time.Now().UTC()
	grant, e := s.SetAuthorization(ctx, "space", "actor", "grant", "grant", "creator", in, now)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.SetAuthorization(ctx, "space", "actor", "revoke", "revoke", "creator", library.AuthorizationInput{Expected: 1, Status: "revoked", Reason: "withdrawn"}, now.Add(time.Minute)); e != nil {
		t.Fatal(e)
	}
	dir := t.TempDir()
	tx, e := db.BeginTx(ctx, nil)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = libraryrepo.ExportFiles(ctx, tx, dir); e != nil {
		tx.Rollback()
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	file, e := libraryrepo.Open(dir, nil)
	if e != nil {
		t.Fatal(e)
	}
	current, e := file.Get(ctx, "space", "actor")
	if e != nil || current.Asset.UseAuthorization.Status != "revoked" || current.Asset.UseAuthorization.Revision != 2 {
		t.Fatal("export resurrected permission", current, e)
	}
	receipt, ok, e := file.Replay(ctx, "space", "grant", "grant")
	if e != nil || !ok || !reflect.DeepEqual(receipt, grant) {
		t.Fatal("historical grant receipt changed", e)
	}
	otherDB := libraryTestDB(t)
	tx, e = otherDB.BeginTx(ctx, nil)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = libraryrepo.ImportFiles(ctx, tx, dir); e != nil {
		tx.Rollback()
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	restored, e := libraryrepo.Open("", otherDB)
	if e != nil {
		t.Fatal(e)
	}
	restoredCurrent, e := restored.Get(ctx, "space", "actor")
	if e != nil || !reflect.DeepEqual(current, restoredCurrent) {
		t.Fatal("restore changed current authority", e)
	}
	receipt, ok, e = restored.Replay(ctx, "space", "grant", "grant")
	if e != nil || !ok || !reflect.DeepEqual(receipt, grant) {
		t.Fatal("restore lost original receipt", e)
	}
}
