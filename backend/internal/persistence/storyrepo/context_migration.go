package storyrepo

import (
	"bytes"
	"context"
	"database/sql"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/story"

	"github.com/adcwb/ai-creative-studio/backend/internal/platform/nativefs"
)

func eachContext(ctx context.Context, dir string, fn func(contextRecord) error) error {
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
		if len(parts) == 3 && parts[2] != ".contexts" {
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
		f, e := nativefs.OpenFile(p, os.O_RDONLY|nativefs.NoFollow|nativefs.NonBlock, 0)
		if e != nil {
			return ErrStorage
		}
		r, e := decodeContext(f)
		closeErr := f.Close()
		if e != nil || closeErr != nil || story.TextHash(r.Snapshot.Space) != parts[0] || r.Snapshot.SeriesID != parts[1] {
			return ErrStorage
		}
		r, e = store.readContext(Scope{r.Snapshot.Space, parts[1]}, parts[3], id)
		if e != nil {
			return e
		}
		return fn(r)
	})
}
func CopyContextValidation(ctx context.Context, src, dst string) error {
	s, e := Open(dst, nil)
	if e != nil {
		return e
	}
	return eachContext(ctx, src, func(r contextRecord) error { return s.appendContext(Scope{r.Snapshot.Space, r.Snapshot.SeriesID}, r) })
}
func ImportContexts(ctx context.Context, tx *sql.Tx, dir string) (int, error) {
	var count int
	if e := tx.QueryRowContext(ctx, `SELECT count(*) FROM studio_story_contexts`).Scan(&count); e != nil || count != 0 {
		return 0, ErrStorage
	}
	e := eachContext(ctx, dir, func(r contextRecord) error {
		if e := insertContext(ctx, tx, r); e != nil {
			return e
		}
		count++
		return nil
	})
	return count, e
}
func ExportContexts(ctx context.Context, tx *sql.Tx, dir string) (int, error) {
	s, e := Open(dir, nil)
	if e != nil {
		return 0, e
	}
	rows, e := tx.QueryContext(ctx, `SELECT payload FROM studio_story_contexts ORDER BY space_id,series_id,episode_id,id`)
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
		r, e := decodeContext(bytes.NewReader(b))
		if e != nil {
			return 0, e
		}
		if e = s.appendContext(Scope{r.Snapshot.Space, r.Snapshot.SeriesID}, r); e != nil {
			return 0, e
		}
		count++
	}
	return count, rows.Err()
}
