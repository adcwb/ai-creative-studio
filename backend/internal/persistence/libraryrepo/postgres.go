package libraryrepo

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/adcwb/ai-creative-studio/backend/internal/modules/library"
)

func (s *Store) mutateSQL(ctx context.Context, space, key, digest, expected string, r Result, archive bool) (Result, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Result{}, ErrStorage
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SET LOCAL synchronous_commit = on`); err != nil {
		return Result{}, ErrStorage
	}
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock_shared(728219117)`); err != nil {
		return Result{}, ErrStorage
	}
	// Serialize this tenant's quota/idempotency admission, never the whole library.
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,728219120))`, space); err != nil {
		return Result{}, ErrStorage
	}
	var oldDigest string
	var raw []byte
	err = tx.QueryRowContext(ctx, `SELECT digest,payload FROM studio_library_receipts WHERE space_id=$1 AND receipt_key=$2`, space, key).Scan(&oldDigest, &raw)
	if err == nil {
		if oldDigest != digest {
			return Result{}, ErrKey
		}
		var previous Result
		if json.Unmarshal(raw, &previous) != nil {
			return Result{}, ErrStorage
		}
		return previous, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Result{}, ErrStorage
	}
	var old library.Asset
	err = tx.QueryRowContext(ctx, `SELECT payload FROM studio_library_assets WHERE space_id=$1 AND id=$2 FOR UPDATE`, space, r.Asset.ID).Scan(&raw)
	exists := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Result{}, ErrStorage
	}
	if exists && json.Unmarshal(raw, &old) != nil {
		return Result{}, ErrStorage
	}
	if err = validateMutation(old, exists, expected, r, archive); err != nil {
		return Result{}, err
	}
	if !archive {
		n := 1
		if exists {
			if tx.QueryRowContext(ctx, `SELECT number+1 FROM studio_library_asset_versions WHERE space_id=$1 AND asset_id=$2 AND id=$3`, space, old.ID, old.HeadVersionID).Scan(&n) != nil {
				return Result{}, ErrStorage
			}
		}
		if r.Version.Number != n {
			return Result{}, library.ErrConflict
		}
	}
	var size int64
	err = tx.QueryRowContext(ctx, `SELECT COALESCE(sum((ref->>'size_bytes')::bigint),0) FROM studio_library_asset_versions, jsonb_array_elements(payload->'references') ref WHERE space_id=$1`, space).Scan(&size)
	if err != nil {
		return Result{}, ErrStorage
	}
	if !archive {
		for _, ref := range r.Version.References {
			size += ref.SizeBytes
		}
	}
	if size > 128<<20 {
		return Result{}, ErrLimit
	}
	var assets, versions, receipts int
	err = tx.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM studio_library_assets WHERE space_id=$1),(SELECT count(*) FROM studio_library_asset_versions WHERE space_id=$1),(SELECT count(*) FROM studio_library_receipts WHERE space_id=$1)`, space).Scan(&assets, &versions, &receipts)
	if err != nil {
		return Result{}, ErrStorage
	}
	if assets >= 1000 && !exists || versions >= 10000 || receipts >= 20000 {
		return Result{}, ErrLimit
	}
	a, _ := json.Marshal(r.Asset)
	v, _ := json.Marshal(r.Version)
	reply, _ := json.Marshal(r)
	_, err = tx.ExecContext(ctx, `INSERT INTO studio_library_assets(space_id,id,kind,status,head_version_id,payload) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(space_id,id) DO UPDATE SET status=EXCLUDED.status,head_version_id=EXCLUDED.head_version_id,payload=EXCLUDED.payload`, space, r.Asset.ID, r.Asset.Kind, r.Asset.Status, r.Asset.HeadVersionID, a)
	if err != nil {
		return Result{}, ErrStorage
	}
	if !archive {
		_, err = tx.ExecContext(ctx, `INSERT INTO studio_library_asset_versions(space_id,asset_id,id,number,payload) VALUES($1,$2,$3,$4,$5)`, space, r.Asset.ID, r.Version.ID, r.Version.Number, v)
		if err != nil {
			return Result{}, ErrStorage
		}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO studio_library_receipts(space_id,receipt_key,digest,asset_id,payload) VALUES($1,$2,$3,$4,$5)`, space, key, digest, r.Asset.ID, reply)
	if err != nil {
		return Result{}, ErrStorage
	}
	if tx.Commit() != nil {
		return Result{}, ErrStorage
	}
	return r, nil
}
