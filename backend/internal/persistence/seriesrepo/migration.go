package seriesrepo

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/series"
)

// ImportFiles only reads a stopped file store. An unfinished Episode/Project
// handoff must be recovered in file mode first; migration never invents a Project.
// The caller owns the transaction and must roll it back on any returned error.
func ImportFiles(ctx context.Context, tx *sql.Tx, dir string) (int, error) {
	for _, table := range []string{"studio_series_nodes", "studio_series_receipts"} {
		var count int
		if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM "+table).Scan(&count); err != nil {
			return 0, err
		}
		if count != 0 {
			return 0, errors.New("destination series repository is not empty")
		}
	}
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	count := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			if strings.HasPrefix(entry.Name(), ".lock-") {
				continue
			}
			return 0, ErrStorage
		}
		base := filepath.Join(dir, entry.Name())
		b, err := read(filepath.Join(base, ".space.json"))
		if err != nil {
			return 0, ErrStorage
		}
		var header struct {
			Format int
			Space  string
		}
		if json.Unmarshal(b, &header) != nil || header.Format != 1 || !series.ScopeID.MatchString(header.Space) || series.SHA(header.Space) != entry.Name() {
			return 0, ErrStorage
		}
		f := &fileAccess{&Store{dir: dir}, ctx, header.Space, base}
		nodes := map[string]series.Node{}
		for _, kind := range []string{"series", "season", "episode"} {
			list, err := f.children(kind, "")
			if err != nil {
				return 0, err
			}
			for _, n := range list {
				if n.State == "cancelled" {
					continue
				}
				if n.State != "ready" && n.MigrationID == "" {
					return 0, errors.New("recover preparing series episodes in file mode before migration")
				}
				if kind != "series" {
					parentKind := "series"
					if kind == "episode" {
						parentKind = "season"
					}
					parent, ok := nodes[parentKind+":"+n.Parent]
					if !ok || parent.SeriesID != n.SeriesID || parent.NextOrdinal <= n.Ordinal {
						return 0, ErrStorage
					}
				}
				nodes[kind+":"+n.ID] = n
				if err = saveSQL(ctx, tx, n); err != nil {
					return 0, err
				}
				count++
			}
		}
		commits, err := os.ReadDir(filepath.Join(base, "commits"))
		if err != nil {
			return 0, ErrStorage
		}
		for _, entry := range commits {
			if strings.HasPrefix(entry.Name(), ".pending-") {
				continue
			}
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
				return 0, ErrStorage
			}
			id := strings.TrimSuffix(entry.Name(), ".json")
			c, err := f.marker(id)
			if err != nil || c == nil {
				return 0, ErrStorage
			}
			if c.Receipt == nil {
				continue
			}
			r := c.Receipt
			if !validReceipt(*r) || r.KeyHash != id {
				return 0, ErrStorage
			}
			if _, ok := nodes[r.Kind+":"+r.ID]; !ok {
				return 0, ErrStorage
			}
			if _, err = tx.ExecContext(ctx, `INSERT INTO studio_series_receipts(space_id,key_hash,digest,kind,id) VALUES($1,$2,$3,$4,$5)`, header.Space, r.KeyHash, r.Digest, r.Kind, r.ID); err != nil {
				return 0, err
			}
		}
	}
	return count, nil
}

// ExportFiles writes a fresh staging directory. Current entities and original
// receipt digests survive the round trip; historical file implementation rows
// are not a public revision history. No cumulative JSON snapshot is created.
func ExportFiles(ctx context.Context, tx *sql.Tx, dir string) (int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil && !os.IsNotExist(err) {
		return 0, err
	}
	if len(entries) != 0 {
		return 0, errors.New("destination series directory is not empty")
	}
	s, err := Open(dir, nil)
	if err != nil {
		return 0, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT payload FROM studio_series_nodes ORDER BY space_id,CASE kind WHEN 'series' THEN 0 WHEN 'season' THEN 1 ELSE 2 END,ordinal,id`)
	if err != nil {
		return 0, err
	}
	count := 0
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			rows.Close()
			return 0, err
		}
		if n.State != "ready" && n.MigrationID == "" {
			rows.Close()
			return 0, ErrStorage
		}
		err = s.withFile(ctx, n.Space, func(f *fileAccess) error {
			id, err := randomTx()
			if err != nil {
				return err
			}
			return f.commit(id, []series.Node{n}, nil)
		})
		if err != nil {
			rows.Close()
			return 0, err
		}
		count++
	}
	if err = errors.Join(rows.Err(), rows.Close()); err != nil {
		return 0, err
	}
	rows, err = tx.QueryContext(ctx, `SELECT space_id,key_hash,digest,kind,id FROM studio_series_receipts ORDER BY space_id,key_hash`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	for rows.Next() {
		var space string
		var r receipt
		if err = rows.Scan(&space, &r.KeyHash, &r.Digest, &r.Kind, &r.ID); err != nil {
			return 0, err
		}
		// The key hash is actor-scoped but intentionally irreversible. Export does
		// not fabricate an owner; actor authorization is checked on every mutation.
		if !validDigest(r.KeyHash) || !validDigest(r.Digest) || !series.ID.MatchString(r.ID) {
			return 0, ErrStorage
		}
		err = s.withFile(ctx, space, func(f *fileAccess) error {
			n, err := f.get(r.Kind, r.ID)
			if err != nil {
				return err
			}
			return f.commit(r.KeyHash, []series.Node{n}, &r)
		})
		if err != nil {
			return 0, err
		}
	}
	return count, rows.Err()
}
func validDigest(v string) bool {
	if len(v) != 64 {
		return false
	}
	for _, r := range v {
		if r < '0' || r > '9' && r < 'a' || r > 'f' {
			return false
		}
	}
	return true
}
func validReceipt(r receipt) bool {
	return (r.Actor == "" || series.ScopeID.MatchString(r.Actor)) && validDigest(r.KeyHash) && validDigest(r.Digest) && series.ID.MatchString(r.ID) && (r.Kind == "series" || r.Kind == "season" || r.Kind == "episode")
}
