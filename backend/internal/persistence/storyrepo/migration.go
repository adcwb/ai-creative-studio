package storyrepo

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/story"
)

func eachFile(ctx context.Context, dir string, fn func(record) error) error {
	if e := realDir(dir); os.IsNotExist(e) {
		return nil
	} else if e != nil {
		return ErrStorage
	}
	spaces, e := os.ReadDir(dir)
	if e != nil {
		return ErrStorage
	}
	for _, space := range spaces {
		if !space.IsDir() {
			if strings.HasPrefix(space.Name(), ".lock-") && space.Type()&os.ModeSymlink == 0 {
				continue
			}
			return ErrStorage
		}
		if !story.Hash.MatchString(space.Name()) {
			return ErrStorage
		}
		series, e := os.ReadDir(filepath.Join(dir, space.Name()))
		if e != nil {
			return ErrStorage
		}
		for _, root := range series {
			if !root.IsDir() || !story.ID.MatchString(root.Name()) {
				return ErrStorage
			}
			entities, e := os.ReadDir(filepath.Join(dir, space.Name(), root.Name()))
			if e != nil {
				return ErrStorage
			}
			for _, entity := range entities {
				if entity.Name() == ".contexts" {
					if !entity.IsDir() || entity.Type()&os.ModeSymlink != 0 {
						return ErrStorage
					}
					continue
				}
				if !entity.IsDir() || !story.ID.MatchString(entity.Name()) {
					return ErrStorage
				}
				list, e := os.ReadDir(filepath.Join(dir, space.Name(), root.Name(), entity.Name()))
				if e != nil {
					return ErrStorage
				}
				number := int64(0)
				for _, entry := range list {
					if e := ctx.Err(); e != nil {
						return e
					}
					if strings.HasPrefix(entry.Name(), ".pending-") {
						continue
					}
					if entry.IsDir() || !namePattern.MatchString(entry.Name()) {
						return ErrStorage
					}
					r, e := read(filepath.Join(dir, space.Name(), root.Name(), entity.Name(), entry.Name()))
					if e != nil || r.Version.ID != entity.Name() || r.Version.SeriesID != root.Name() || story.TextHash(r.Version.Space) != space.Name() || r.Version.Number != number+1 {
						return ErrStorage
					}
					number++
					if e = fn(r); e != nil {
						return e
					}
				}
			}
		}
	}
	return nil
}

// CopyValidation never opens the source through a mutating constructor.
func CopyValidation(ctx context.Context, src, dst string) error {
	s, e := Open(dst, nil)
	if e != nil {
		return e
	}
	return eachFile(ctx, src, func(r record) error { return s.appendFile(Scope{r.Version.Space, r.Version.SeriesID}, r) })
}
func ImportFiles(ctx context.Context, tx *sql.Tx, dir string) (int, error) {
	var count int
	if e := tx.QueryRowContext(ctx, `SELECT count(*) FROM studio_story_versions`).Scan(&count); e != nil || count != 0 {
		return 0, ErrStorage
	}
	e := eachFile(ctx, dir, func(r record) error {
		if e := saveSQL(ctx, tx, r); e != nil {
			return e
		}
		count++
		return nil
	})
	return count, e
}
func ExportFiles(ctx context.Context, tx *sql.Tx, dir string) (int, error) {
	if list, e := os.ReadDir(dir); e != nil && !os.IsNotExist(e) || len(list) > 0 {
		return 0, ErrStorage
	}
	s, e := Open(dir, nil)
	if e != nil {
		return 0, e
	}
	rows, e := tx.QueryContext(ctx, `SELECT payload FROM studio_story_versions ORDER BY space_id,series_id,id,number`)
	if e != nil {
		return 0, e
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var raw []byte
		if rows.Scan(&raw) != nil {
			return 0, ErrStorage
		}
		r, e := decodeBytes(raw)
		if e != nil {
			return 0, e
		}
		if e = s.appendFile(Scope{r.Version.Space, r.Version.SeriesID}, r); e != nil {
			return 0, e
		}
		count++
	}
	return count, rows.Err()
}
