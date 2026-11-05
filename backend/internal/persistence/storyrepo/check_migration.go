package storyrepo

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"github.com/adcwb/ai-creative-studio/backend/internal/platform/jobqueue"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/story"
)

func eachCheckFile(ctx context.Context, dir string, fn func(CheckRecord) error) error {
	if e := realDir(dir); os.IsNotExist(e) {
		return nil
	} else if e != nil {
		return ErrStorage
	}
	return filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return ErrStorage
		}
		if e := ctx.Err(); e != nil {
			return e
		}
		if d.Type()&os.ModeSymlink != 0 {
			return ErrStorage
		}
		rel, e := filepath.Rel(dir, p)
		if e != nil {
			return ErrStorage
		}
		if rel == "." {
			return nil
		}
		parts := strings.Split(rel, string(os.PathSeparator))
		if len(parts) == 1 && !d.IsDir() {
			if strings.HasPrefix(d.Name(), ".lock-") {
				return nil
			}
			return ErrStorage
		}
		if len(parts) >= 1 && !story.Hash.MatchString(parts[0]) {
			return ErrStorage
		}
		if len(parts) >= 2 && !story.ID.MatchString(parts[1]) {
			return ErrStorage
		}
		if len(parts) == 3 && parts[2] != ".checks" {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return ErrStorage
		}
		if len(parts) >= 4 && !story.ID.MatchString(parts[3]) {
			return ErrStorage
		}
		if d.IsDir() {
			if len(parts) > 4 {
				return ErrStorage
			}
			return nil
		}
		if len(parts) == 5 && strings.HasPrefix(d.Name(), ".pending-") {
			return nil
		}
		if len(parts) != 5 || !d.Type().IsRegular() {
			return ErrStorage
		}
		id := strings.TrimSuffix(d.Name(), ".json")
		if !story.ID.MatchString(id) || d.Name() != id+".json" {
			return ErrStorage
		}
		// The shared reader checks parent directories, identity, digest and strict JSON.
		store := Store{dir: dir}
		f, e := os.OpenFile(p, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
		if e != nil {
			return ErrStorage
		}
		r, e := decodeCheck(f)
		closeErr := f.Close()
		if e != nil || closeErr != nil || story.TextHash(r.Check.Space) != parts[0] || r.Check.SeriesID != parts[1] {
			return ErrStorage
		}
		r, e = store.readCheck(Scope{r.Check.Space, parts[1]}, parts[3], id)
		if e != nil {
			return e
		}
		if r.Check.State == "succeeded" {
			snapshot, e := store.readContext(Scope{r.Check.Space, r.Check.SeriesID}, r.Check.EpisodeID, r.Check.Input.ContextID)
			if e != nil {
				return e
			}
			candidate, e := story.Clone(*r.Check.Result)
			if e != nil {
				return e
			}
			for i := range candidate.Findings {
				candidate.Findings[i].ID = ""
			}
			verified, e := story.ValidateResult(r.Check.ID, snapshot.Snapshot, candidate)
			if e != nil || story.Digest(verified) != story.Digest(r.Check.Result) {
				return ErrStorage
			}
		}
		return fn(r)
	})
}
func CopyCheckValidation(ctx context.Context, src, dst string) error {
	s, e := Open(dst, nil)
	if e != nil {
		return e
	}
	return eachCheckFile(ctx, src, func(r CheckRecord) error {
		return s.writeCheck(Scope{r.Check.Space, r.Check.SeriesID}, migrationCheck(r), true)
	})
}
func ImportChecks(ctx context.Context, tx *sql.Tx, dir string) (int, error) {
	var count int
	if e := tx.QueryRowContext(ctx, `SELECT count(*) FROM studio_story_checks`).Scan(&count); e != nil || count != 0 {
		return 0, ErrStorage
	}
	e := eachCheckFile(ctx, dir, func(r CheckRecord) error {
		r = migrationCheck(r)
		if e := insertCheck(ctx, tx, r); e != nil {
			return e
		}
		if r.Check.State == "queued" {
			payload, _ := json.Marshal(CheckJob{r.Check.SeriesID, r.Check.EpisodeID, r.Check.ID})
			if e := jobqueue.EnqueueTx(ctx, tx, jobqueue.Job{TenantID: r.Check.Space, ID: r.Check.ID, Kind: "story_check", IdempotencyKey: r.Check.ID, Payload: payload, TraceParent: r.TraceParent}); e != nil {
				return e
			}
		}
		count++
		return nil
	})
	return count, e
}
func ExportChecks(ctx context.Context, tx *sql.Tx, dir string) (int, error) {
	s, e := Open(dir, nil)
	if e != nil {
		return 0, e
	}
	rows, e := tx.QueryContext(ctx, `SELECT payload FROM studio_story_checks ORDER BY space_id,series_id,episode_id,id`)
	if e != nil {
		return 0, e
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var b []byte
		if rows.Scan(&b) != nil {
			return 0, ErrStorage
		}
		r, e := decodeCheck(bytes.NewReader(b))
		if e != nil {
			return 0, e
		}
		if e = s.writeCheck(Scope{r.Check.Space, r.Check.SeriesID}, migrationCheck(r), true); e != nil {
			return 0, e
		}
		count++
	}
	return count, rows.Err()
}

// Offline transfers never turn a potentially dispatched text POST into a retry.
func migrationCheck(r CheckRecord) CheckRecord {
	if r.Check.State == "running" {
		r.Check.State = "unknown"
		r.Check.ErrorCode = "execution_outcome_unknown"
		r.Check.UpdatedAt = time.Now().UTC()
	}
	return r
}
