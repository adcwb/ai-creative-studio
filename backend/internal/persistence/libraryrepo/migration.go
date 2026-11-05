package libraryrepo

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/library"
)

// FileReferences is an offline/startup inventory, not a request query. The
// application holds the exclusive data-directory lock while using it.
func FileReferences(dir string) (map[string][]library.Reference, error) {
	spaces, err := fileSpaces(dir)
	if err != nil {
		return nil, err
	}
	s := &Store{dir: dir}
	out := map[string][]library.Reference{}
	for _, space := range spaces {
		seen := map[string]bool{}
		collect := func(v library.Version) {
			for _, r := range v.References {
				if !seen[r.ID] {
					out[space] = append(out[space], r)
					seen[r.ID] = true
				}
			}
		}
		if _, err = os.Lstat(s.fileSpace(space)); os.IsNotExist(err) {
			d, err := s.readLegacy(space)
			if err != nil {
				return nil, err
			}
			for _, v := range d.Versions {
				collect(v)
			}
		} else {
			if err != nil {
				return nil, ErrStorage
			}
			if err = s.ensureFileSpace(space); err != nil {
				return nil, err
			}
			ids, err := s.fileEntities(space)
			if err != nil {
				return nil, err
			}
			for _, id := range ids {
				names, err := s.fileEntries(space, id)
				if err != nil {
					return nil, err
				}
				for _, name := range names {
					if strings.HasPrefix(name[20:], "-v-") {
						r, err := s.readFileRecord(space, id, name)
						if err != nil {
							return nil, err
						}
						collect(r.Result.Version)
					}
				}
			}
		}
	}
	return out, nil
}

func fileSpaces(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, ErrStorage
	}
	spaces := map[string]bool{}
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".library-") || strings.HasPrefix(name, ".lock-") {
			continue
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil, ErrStorage
		}
		var space string
		if entry.IsDir() && strings.HasSuffix(name, ".entities") {
			space = strings.TrimSuffix(name, ".entities")
		} else if !entry.IsDir() && strings.HasSuffix(name, ".json") {
			space = strings.TrimSuffix(name, ".json")
		} else {
			return nil, ErrStorage
		}
		if !library.IDPattern.MatchString(space) {
			return nil, ErrStorage
		}
		spaces[space] = true
	}
	out := []string{}
	for space := range spaces {
		out = append(out, space)
	}
	sort.Strings(out)
	return out, nil
}

func readFiles(dir string) (map[string]disk, error) {
	spaces, err := fileSpaces(dir)
	if err != nil {
		return nil, err
	}
	out := map[string]disk{}
	s := &Store{dir: dir}
	// The caller holds the exclusive application-directory lock. Offline import
	// must not upgrade or otherwise mutate its source, including on rollback.
	for _, space := range spaces {
		var d disk
		if _, err = os.Lstat(s.fileSpace(space)); os.IsNotExist(err) {
			d, err = s.readLegacy(space)
		} else if err == nil {
			if err = s.ensureFileSpace(space); err == nil {
				d, err = s.read(space)
			}
		}
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
		if _, err = os.Lstat(s.fileSpace(space)); err == nil || !errors.Is(err, os.ErrNotExist) {
			return 0, ErrConflictDestination
		}
		if err = s.publishFileSpace(space, d); err != nil {
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

// read is an offline snapshot for file/SQL migration only. Runtime queries use
// entity records and never construct this whole-space value.
func (s *Store) read(space string) (disk, error) {
	d := fresh()
	ids, err := s.fileEntities(space)
	if err != nil {
		return d, err
	}
	for _, id := range ids {
		current, err := s.fileCurrent(space, id)
		if err != nil {
			return d, err
		}
		d.Assets[id] = current.Asset
		names, err := s.fileEntries(space, id)
		if err != nil {
			return d, err
		}
		for _, name := range names {
			r, err := s.readFileRecord(space, id, name)
			if err != nil {
				return d, err
			}
			if r.NewVersion {
				if _, exists := d.Versions[r.Result.Version.ID]; exists {
					return d, ErrStorage
				}
				d.Versions[r.Result.Version.ID] = r.Result.Version
			}
			if r.Key != "" {
				if _, exists := d.Receipts[r.Key]; exists {
					return d, ErrStorage
				}
				d.Receipts[r.Key] = receipt{r.Digest, r.Result}
			}
		}
	}
	return validateDisk(space, d)
}
