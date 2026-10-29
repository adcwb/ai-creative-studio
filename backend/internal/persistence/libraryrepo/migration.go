package libraryrepo

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/library"
)

// FileReferences is an offline/startup inventory, not a request query. The
// application holds the exclusive data-directory lock while using it.
func FileReferences(dir string) (map[string][]library.Reference, error) {
	states, err := readFiles(dir)
	if err != nil {
		return nil, err
	}
	out := map[string][]library.Reference{}
	for space, d := range states {
		seen := map[string]bool{}
		for _, v := range d.Versions {
			for _, r := range v.References {
				if !seen[r.ID] {
					out[space] = append(out[space], r)
					seen[r.ID] = true
				}
			}
		}
	}
	return out, nil
}
func readFiles(dir string) (map[string]disk, error) {
	out := map[string]disk{}
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return out, nil
	}
	if err != nil {
		return nil, ErrStorage
	}
	s := &Store{dir: dir}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".library-") {
			continue
		}
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || !strings.HasSuffix(entry.Name(), ".json") {
			return nil, ErrStorage
		}
		space := strings.TrimSuffix(entry.Name(), ".json")
		d, err := s.read(space)
		if err != nil {
			return nil, err
		}
		out[space] = d
	}
	return out, nil
}

// ImportFiles joins the caller's existing offline migration transaction.
func ImportFiles(ctx context.Context, tx *sql.Tx, dir string) (int, error) {
	states, err := readFiles(dir)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, table := range []string{"studio_library_assets", "studio_library_asset_versions", "studio_library_receipts"} {
		var n int
		if tx.QueryRowContext(ctx, "SELECT count(*) FROM "+table).Scan(&n) != nil {
			return 0, ErrStorage
		}
		if n != 0 {
			return 0, ErrConflictDestination
		}
	}
	for space, d := range states {
		for _, a := range d.Assets {
			b, _ := json.Marshal(a)
			if _, err = tx.ExecContext(ctx, `INSERT INTO studio_library_assets(space_id,id,kind,status,head_version_id,payload) VALUES($1,$2,$3,$4,$5,$6)`, space, a.ID, a.Kind, a.Status, a.HeadVersionID, b); err != nil {
				return 0, err
			}
			count++
		}
		for _, v := range d.Versions {
			b, _ := json.Marshal(v)
			if _, err = tx.ExecContext(ctx, `INSERT INTO studio_library_asset_versions(space_id,asset_id,id,number,payload) VALUES($1,$2,$3,$4,$5)`, space, v.AssetID, v.ID, v.Number, b); err != nil {
				return 0, err
			}
			count++
		}
		for key, r := range d.Receipts {
			b, _ := json.Marshal(r.Result)
			if _, err = tx.ExecContext(ctx, `INSERT INTO studio_library_receipts(space_id,receipt_key,digest,asset_id,payload) VALUES($1,$2,$3,$4,$5)`, space, key, r.Digest, r.Result.Asset.ID, b); err != nil {
				return 0, err
			}
			count++
		}
	}
	return count, nil
}

// ExportFiles writes into the caller's private staging directory. The outer
// migration publishes that directory only after all domains are complete.
func ExportFiles(ctx context.Context, tx *sql.Tx, dir string) (int, error) {
	states := map[string]disk{}
	count := 0
	for _, table := range []string{"studio_library_assets", "studio_library_asset_versions", "studio_library_receipts"} {
		query := "SELECT space_id,id,payload,'' FROM " + table
		if table == "studio_library_receipts" {
			query = "SELECT space_id,receipt_key,payload,digest FROM " + table
		}
		rows, err := tx.QueryContext(ctx, query)
		if err != nil {
			return 0, err
		}
		for rows.Next() {
			var space, id, digest string
			var b []byte
			if rows.Scan(&space, &id, &b, &digest) != nil {
				rows.Close()
				return 0, ErrStorage
			}
			d, ok := states[space]
			if !ok {
				d = fresh()
			}
			switch table {
			case "studio_library_assets":
				var a library.Asset
				if json.Unmarshal(b, &a) != nil {
					rows.Close()
					return 0, ErrStorage
				}
				d.Assets[id] = a
			case "studio_library_asset_versions":
				var v library.Version
				if json.Unmarshal(b, &v) != nil {
					rows.Close()
					return 0, ErrStorage
				}
				d.Versions[id] = v
			default:
				var r Result
				if json.Unmarshal(b, &r) != nil {
					rows.Close()
					return 0, ErrStorage
				}
				d.Receipts[id] = receipt{digest, r}
			}
			states[space] = d
			count++
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return 0, err
		}
	}
	s, err := Open(dir, nil)
	if err != nil {
		return 0, err
	}
	for space, d := range states {
		if !library.IDPattern.MatchString(space) {
			return 0, ErrStorage
		}
		if err = s.save(space, d); err != nil {
			return 0, err
		}
		if _, err = s.read(space); err != nil {
			return 0, err
		}
	}
	return count, nil
}

// Kept distinct from a normal concurrent version conflict.
var ErrConflictDestination = &destinationError{}

type destinationError struct{}

func (*destinationError) Error() string { return "destination library is not empty" }

func HasFileData(dir string) bool {
	items, err := os.ReadDir(filepath.Clean(dir))
	return err == nil && len(items) > 0
}
