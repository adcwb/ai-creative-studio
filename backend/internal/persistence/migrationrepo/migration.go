package migrationrepo

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/series"
	m "github.com/adcwb/ai-creative-studio/backend/internal/modules/seriesmigration"
)

func eachFile(ctx context.Context, dir string, fn func(m.Job) error) error {
	spaces, e := os.ReadDir(dir)
	if os.IsNotExist(e) {
		return nil
	}
	if e != nil {
		return e
	}
	for _, space := range spaces {
		if !space.IsDir() {
			if strings.HasPrefix(space.Name(), ".lock-") {
				continue
			}
			return ErrStorage
		}
		entries, e := os.ReadDir(filepath.Join(dir, space.Name()))
		if e != nil {
			return e
		}
		for _, entry := range entries {
			if e = ctx.Err(); e != nil {
				return e
			}
			if strings.HasPrefix(entry.Name(), ".pending-") {
				continue
			}
			if entry.IsDir() {
				return ErrStorage
			}
			j, e := readFile(filepath.Join(dir, space.Name(), entry.Name()))
			if e != nil || series.SHA(j.Space) != space.Name() || entry.Name() != j.ID+".json" {
				return ErrStorage
			}
			if e = fn(j); e != nil {
				return e
			}
		}
	}
	return nil
}

// CopyValidation copies metadata only and never mutates an offline source.
func CopyValidation(ctx context.Context, src, dst string) error {
	return eachFile(ctx, src, func(j m.Job) error { return writeFile(filepath.Join(dst, series.SHA(j.Space), j.ID+".json"), j) })
}
func ImportFiles(ctx context.Context, tx *sql.Tx, dir string) (int, error) {
	var count int
	if e := tx.QueryRowContext(ctx, `SELECT count(*) FROM studio_episode_migrations`).Scan(&count); e != nil {
		return 0, e
	}
	if count != 0 {
		return 0, errors.New("destination migration repository not empty")
	}
	e := eachFile(ctx, dir, func(j m.Job) error {
		b, e := json.Marshal(j)
		if e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, `INSERT INTO studio_episode_migrations(space_id,id,source_project_id,row_version,state,payload) VALUES($1,$2,$3,$4,$5,$6)`, j.Space, j.ID, j.ProjectID, j.Version, j.State, b)
		if e == nil {
			count++
		}
		return e
	})
	return count, e
}
func ExportFiles(ctx context.Context, tx *sql.Tx, dir string) (int, error) {
	if entries, e := os.ReadDir(dir); e != nil && !os.IsNotExist(e) || len(entries) != 0 {
		return 0, ErrStorage
	}
	rows, e := tx.QueryContext(ctx, `SELECT payload FROM studio_episode_migrations ORDER BY space_id,id`)
	if e != nil {
		return 0, e
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var b []byte
		var j m.Job
		if rows.Scan(&b) != nil {
			return 0, ErrStorage
		}
		j, e = decodeJob(bytes.NewReader(b))
		if e != nil {
			return 0, e
		}
		if e = writeFile(filepath.Join(dir, series.SHA(j.Space), j.ID+".json"), j); e != nil {
			return 0, e
		}
		count++
	}
	return count, rows.Err()
}
