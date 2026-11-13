package workflowrepo

import (
	"context"
	"database/sql"
	"encoding/json"
	w "github.com/adcwb/ai-creative-studio/backend/internal/modules/workflows"
	"github.com/adcwb/ai-creative-studio/backend/internal/platform/nativefs"
	"os"
	"path/filepath"
)

func eachFile(ctx context.Context, dir string, fn func(Document, []byte) error) error {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return ErrStorage
	}
	for _, e := range entries {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if filepath.Ext(e.Name()) != ".json" {
			continue
		}
		raw, err := readDocumentFile(filepath.Join(dir, e.Name()))
		var d Document
		if err != nil || json.Unmarshal(raw, &d) != nil || !valid(d, d.Space, d.Project) || e.Name() != w.Digest([]string{d.Space, d.Project})+".json" {
			return ErrStorage
		}
		if err = fn(d, raw); err != nil {
			return err
		}
	}
	return nil
}
func CopyValidation(ctx context.Context, source, target string) error {
	return eachFile(ctx, source, func(d Document, raw []byte) error {
		if err := nativefs.EnsurePrivateDirectory(target); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(target, w.Digest([]string{d.Space, d.Project})+".json"), raw, 0600)
	})
}
func ImportFiles(ctx context.Context, tx *sql.Tx, dir string) (int, error) {
	count := 0
	err := eachFile(ctx, dir, func(d Document, raw []byte) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO studio_workflow_projects(space_id,project_id,active,payload) VALUES($1,$2,$3,$4)`, d.Space, d.Project, d.Active, raw)
		if err == nil {
			count++
		}
		return err
	})
	return count, err
}
func ExportFiles(ctx context.Context, tx *sql.Tx, dir string) (int, error) {
	rows, err := tx.QueryContext(ctx, `SELECT space_id,project_id,payload FROM studio_workflow_projects ORDER BY space_id,project_id`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var space, project string
		var raw []byte
		if rows.Scan(&space, &project, &raw) != nil {
			return 0, ErrStorage
		}
		var d Document
		if json.Unmarshal(raw, &d) != nil || !valid(d, space, project) {
			return 0, ErrStorage
		}
		if err = nativefs.EnsurePrivateDirectory(dir); err != nil {
			return 0, err
		}
		if err = os.WriteFile(filepath.Join(dir, w.Digest([]string{space, project})+".json"), raw, 0600); err != nil {
			return 0, err
		}
		count++
	}
	return count, rows.Err()
}
