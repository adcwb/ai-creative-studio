package promptrepo

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/adcwb/ai-creative-studio/backend/internal/modules/prompts"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ImportFiles runs within the caller's stopped-writer migration transaction.
func ImportFiles(ctx context.Context, tx *sql.Tx, dir string) (int, error) {
	for _, table := range []string{"studio_prompt_templates", "studio_prompt_versions", "studio_prompt_receipts", "studio_prompt_favorites"} {
		var n int
		if e := tx.QueryRowContext(ctx, `SELECT count(*) FROM `+table).Scan(&n); e != nil {
			return 0, e
		}
		if n != 0 {
			return 0, errors.New("destination prompt repository is not empty")
		}
	}
	if _, e := os.Stat(dir); os.IsNotExist(e) {
		return 0, nil
	}
	s := &Store{dir: dir}
	root, e := os.OpenRoot(dir)
	if e != nil {
		return 0, e
	}
	defer root.Close()
	paths := []string{}
	e = filepath.WalkDir(dir, func(path string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.Type()&os.ModeSymlink != 0 {
			return ErrStorage
		}
		if d.IsDir() {
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") {
			return nil
		}
		rel, e := filepath.Rel(dir, path)
		if e != nil {
			return e
		}
		paths = append(paths, rel)
		return nil
	})
	if e != nil {
		return 0, e
	}
	sort.Strings(paths)
	count := 0
	favorites := []favorite{}
	for _, rel := range paths {
		if e = ctx.Err(); e != nil {
			return 0, e
		}
		f, e := root.Open(rel)
		if e != nil {
			return 0, ErrStorage
		}
		b, e := io.ReadAll(io.LimitReader(f, (2<<20)+1))
		e = errors.Join(e, f.Close())
		if e != nil || len(b) > 2<<20 {
			return 0, ErrStorage
		}
		parts := strings.Split(filepath.ToSlash(rel), "/")
		if len(parts) == 4 && parts[1] == "favorites" {
			var v favorite
			if json.Unmarshal(b, &v) != nil || parts[0] != prompts.TextSHA(v.Space) || parts[2] != prompts.TextSHA(v.Actor) || parts[3] != v.Template+".json" {
				return 0, ErrStorage
			}
			favorites = append(favorites, v)
			continue
		}
		if len(parts) != 3 {
			return 0, ErrStorage
		}
		var r record
		if json.Unmarshal(b, &r) != nil || parts[0] != prompts.TextSHA(r.Result.Template.SpaceID) || parts[1] != r.Result.Template.ID || parts[2] != recordName(r) {
			return 0, ErrStorage
		}
		if _, e = s.readRecord(r.Result.Template.SpaceID, r.Result.Template.ID, parts[2]); e != nil {
			return 0, e
		}
		if e = saveSQL(ctx, tx, r); e != nil {
			return 0, e
		}
		count++
	}
	for _, v := range favorites {
		if _, e = tx.ExecContext(ctx, `INSERT INTO studio_prompt_favorites(space_id,actor_id,template_id,enabled) VALUES($1,$2,$3,$4)`, v.Space, v.Actor, v.Template, v.Enabled); e != nil {
			return 0, e
		}
	}
	return count, nil
}
func ExportFiles(ctx context.Context, tx *sql.Tx, dir string) (int, error) {
	s, e := Open(dir, nil)
	if e != nil {
		return 0, e
	}
	rows, e := tx.QueryContext(ctx, `SELECT payload FROM studio_prompt_receipts ORDER BY space_id,template_id,(payload->'result'->'template'->>'row_version')::bigint`)
	if e != nil {
		return 0, e
	}
	count := 0
	for rows.Next() {
		var b []byte
		var r record
		if rows.Scan(&b) != nil || json.Unmarshal(b, &r) != nil {
			rows.Close()
			return 0, ErrStorage
		}
		if e = s.writeRecord(r); e != nil {
			rows.Close()
			return 0, e
		}
		count++
	}
	e = errors.Join(rows.Err(), rows.Close())
	if e != nil {
		return 0, e
	}
	rows, e = tx.QueryContext(ctx, `SELECT space_id,actor_id,template_id,enabled FROM studio_prompt_favorites`)
	if e != nil {
		return 0, e
	}
	defer rows.Close()
	for rows.Next() {
		var v favorite
		if rows.Scan(&v.Space, &v.Actor, &v.Template, &v.Enabled) != nil {
			return 0, ErrStorage
		}
		if e = s.writeFavorite(prompts.Actor{Space: v.Space, User: v.Actor}, v.Template, v.Enabled); e != nil {
			return 0, e
		}
	}
	return count, rows.Err()
}
