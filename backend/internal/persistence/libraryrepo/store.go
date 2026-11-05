// Package libraryrepo persists library metadata using per-asset transaction files
// or indexed PostgreSQL rows. It never reads another space to answer a query.
package libraryrepo

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/library"
)

var ErrNotFound = errors.New("library resource not found")
var ErrKey = errors.New("library idempotency conflict")
var ErrStorage = errors.New("library storage unavailable")

const Schema = `CREATE TABLE IF NOT EXISTS studio_library_assets (
 space_id text NOT NULL, id text NOT NULL, kind text NOT NULL CONSTRAINT studio_library_kind_v2 CHECK(kind IN ('character','product','scene','style','voice')),
 status text NOT NULL CHECK(status IN ('active','archived')), head_version_id text NOT NULL,
 payload jsonb NOT NULL CHECK((payload->>'space_id'=space_id AND payload->>'id'=id AND payload->>'head_version_id'=head_version_id AND payload->>'status'=status AND payload->>'kind'=kind) IS TRUE), PRIMARY KEY(space_id,id));
CREATE TABLE IF NOT EXISTS studio_library_asset_versions (
 space_id text NOT NULL, asset_id text NOT NULL, id text NOT NULL, number integer NOT NULL CHECK(number>0),
 payload jsonb NOT NULL CHECK((payload->>'space_id'=space_id AND payload->>'asset_id'=asset_id AND payload->>'id'=id AND (payload->>'number')::integer=number) IS TRUE), PRIMARY KEY(space_id,asset_id,id), UNIQUE(space_id,asset_id,number),
 FOREIGN KEY(space_id,asset_id) REFERENCES studio_library_assets(space_id,id));
CREATE TABLE IF NOT EXISTS studio_library_receipts (
 space_id text NOT NULL, receipt_key text NOT NULL, digest text NOT NULL, asset_id text NOT NULL,
 payload jsonb NOT NULL, PRIMARY KEY(space_id,receipt_key),
 FOREIGN KEY(space_id,asset_id) REFERENCES studio_library_assets(space_id,id));
CREATE INDEX IF NOT EXISTS studio_library_assets_list ON studio_library_assets(space_id,kind,status,id);
-- Explicit initializer only: upgrade the known v1 constraint without rewriting
-- content, versions or receipts. Runtime readiness requires the named v2 check.
ALTER TABLE studio_library_assets DROP CONSTRAINT IF EXISTS studio_library_assets_kind_check;
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE connamespace=current_schema()::regnamespace AND conname='studio_library_kind_v2' AND conrelid='studio_library_assets'::regclass) THEN
  ALTER TABLE studio_library_assets ADD CONSTRAINT studio_library_kind_v2 CHECK(kind IN ('character','product','scene','style','voice'));
 END IF;
END $$;`

type Result struct {
	Asset   library.Asset   `json:"asset"`
	Version library.Version `json:"version"`
}
type receipt struct {
	Digest string `json:"digest"`
	Result Result `json:"result"`
}
type disk struct {
	Format   int                        `json:"format"`
	Assets   map[string]library.Asset   `json:"assets"`
	Versions map[string]library.Version `json:"versions"`
	Receipts map[string]receipt         `json:"receipts"`
}
type Store struct {
	dir string
	db  *sql.DB
}
type Query struct {
	Kind, Status, After string
	Limit               int
}

func Open(dir string, db *sql.DB) (*Store, error) {
	s := &Store{dir: dir, db: db}
	if db == nil {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return nil, err
		}
	}
	return s, nil
}
func fresh() disk {
	return disk{Format: 1, Assets: map[string]library.Asset{}, Versions: map[string]library.Version{}, Receipts: map[string]receipt{}}
}

// Replay is checked before preparing any copied blobs. Mutate repeats the check
// transactionally, so races cannot change the original result.
func (s *Store) Replay(ctx context.Context, space, key, digest string) (Result, bool, error) {
	if !library.IDPattern.MatchString(space) {
		return Result{}, false, library.ErrInput
	}
	var r receipt
	if s.db != nil {
		var raw []byte
		err := s.db.QueryRowContext(ctx, `SELECT digest,payload FROM studio_library_receipts WHERE space_id=$1 AND receipt_key=$2`, space, key).Scan(&r.Digest, &raw)
		if errors.Is(err, sql.ErrNoRows) {
			return Result{}, false, nil
		}
		if err != nil || json.Unmarshal(raw, &r.Result) != nil {
			return Result{}, false, ErrStorage
		}
	} else {
		var found bool
		err := s.withFileLock(ctx, space, func() error {
			var err error
			r, found, err = s.fileReceipt(ctx, space, key)
			return err
		})
		if err != nil || !found {
			return Result{}, false, err
		}
	}
	if r.Digest != digest {
		return Result{}, false, ErrKey
	}
	return r.Result, true, nil
}
func validateQuery(q Query) error {
	if q.Limit < 1 || q.Limit > 100 {
		return library.ErrInput
	}
	if q.Kind != "" && q.Kind != "character" && q.Kind != "product" && q.Kind != "scene" && q.Kind != "style" && q.Kind != "voice" {
		return library.ErrInput
	}
	if q.Status != "" && q.Status != "active" && q.Status != "archived" {
		return library.ErrInput
	}
	if q.After != "" && !library.IDPattern.MatchString(q.After) {
		return library.ErrInput
	}
	return nil
}
func (s *Store) List(ctx context.Context, space string, q Query) ([]library.Asset, error) {
	if !library.IDPattern.MatchString(space) {
		return nil, library.ErrInput
	}
	if err := validateQuery(q); err != nil {
		return nil, err
	}
	out := []library.Asset{}
	if s.db != nil {
		rows, err := s.db.QueryContext(ctx, `SELECT payload FROM studio_library_assets WHERE space_id=$1 AND ($2='' OR kind=$2) AND ($3='' OR status=$3) AND id>$4 ORDER BY id LIMIT $5`, space, q.Kind, q.Status, q.After, q.Limit)
		if err != nil {
			return nil, ErrStorage
		}
		defer rows.Close()
		for rows.Next() {
			var b []byte
			var a library.Asset
			if rows.Scan(&b) != nil || json.Unmarshal(b, &a) != nil || a.SpaceID != space {
				return nil, ErrStorage
			}
			out = append(out, a)
		}
		return out, rows.Err()
	}
	err := s.withFileLock(ctx, space, func() error {
		var err error
		out, err = s.fileList(ctx, space, q)
		return err
	})
	return out, err
}
func (s *Store) Get(ctx context.Context, space, id string) (Result, error) {
	if !library.IDPattern.MatchString(space) || !library.IDPattern.MatchString(id) {
		return Result{}, ErrNotFound
	}
	if s.db != nil {
		var a, b []byte
		err := s.db.QueryRowContext(ctx, `SELECT a.payload,v.payload FROM studio_library_assets a JOIN studio_library_asset_versions v ON v.space_id=a.space_id AND v.asset_id=a.id AND v.id=a.head_version_id WHERE a.space_id=$1 AND a.id=$2`, space, id).Scan(&a, &b)
		if errors.Is(err, sql.ErrNoRows) {
			return Result{}, ErrNotFound
		}
		var r Result
		if err != nil || json.Unmarshal(a, &r.Asset) != nil || json.Unmarshal(b, &r.Version) != nil {
			return Result{}, ErrStorage
		}
		return r, nil
	}
	var out Result
	err := s.withFileLock(ctx, space, func() error {
		var err error
		out, err = s.fileCurrent(space, id)
		return err
	})
	return out, err
}
func (s *Store) Version(ctx context.Context, space, asset, id string) (library.Version, error) {
	if !library.IDPattern.MatchString(space) || !library.IDPattern.MatchString(asset) || !library.IDPattern.MatchString(id) {
		return library.Version{}, ErrNotFound
	}
	if s.db != nil {
		var b []byte
		err := s.db.QueryRowContext(ctx, `SELECT payload FROM studio_library_asset_versions WHERE space_id=$1 AND asset_id=$2 AND id=$3`, space, asset, id).Scan(&b)
		if errors.Is(err, sql.ErrNoRows) {
			return library.Version{}, ErrNotFound
		}
		var v library.Version
		if err != nil || json.Unmarshal(b, &v) != nil {
			return v, ErrStorage
		}
		return v, nil
	}
	var out library.Version
	err := s.withFileLock(ctx, space, func() error {
		var err error
		out, err = s.fileVersion(space, asset, id)
		return err
	})
	return out, err
}
func (s *Store) Versions(ctx context.Context, space, asset, after string, limit int) ([]library.Version, error) {
	if limit < 1 || limit > 100 {
		return nil, library.ErrInput
	}
	if _, err := s.Get(ctx, space, asset); err != nil {
		return nil, err
	}
	before := 0
	if after != "" {
		v, err := s.Version(ctx, space, asset, after)
		if err != nil {
			return nil, err
		}
		before = v.Number
	}
	out := []library.Version{}
	if s.db != nil {
		rows, err := s.db.QueryContext(ctx, `SELECT payload FROM studio_library_asset_versions WHERE space_id=$1 AND asset_id=$2 AND ($3=0 OR number<$3) ORDER BY number DESC LIMIT $4`, space, asset, before, limit)
		if err != nil {
			return nil, ErrStorage
		}
		defer rows.Close()
		for rows.Next() {
			var b []byte
			var v library.Version
			if rows.Scan(&b) != nil || json.Unmarshal(b, &v) != nil {
				return nil, ErrStorage
			}
			out = append(out, v)
		}
		return out, rows.Err()
	}
	err := s.withFileLock(ctx, space, func() error {
		var err error
		out, err = s.fileVersions(ctx, space, asset, before, limit)
		return err
	})
	return out, err
}

// Mutation is executed under the same lock/transaction as its receipt. The
// supplied version is already prepared; media I/O must finish before this call.
func (s *Store) Mutate(ctx context.Context, space, key, digest, expected string, result Result, archive bool) (Result, error) {
	if !library.IDPattern.MatchString(space) || result.Asset.SpaceID != space || !library.IDPattern.MatchString(result.Asset.ID) {
		return Result{}, library.ErrInput
	}
	if s.db != nil {
		return s.mutateSQL(ctx, space, key, digest, expected, result, archive)
	}
	var out Result
	err := s.withFileLock(ctx, space, func() error {
		var err error
		out, err = s.fileMutate(ctx, space, key, digest, expected, result, archive)
		return err
	})
	return out, err
}
func validateMutation(old library.Asset, exists bool, expected string, r Result, archive bool) error {
	if !exists && expected != "" {
		return ErrNotFound
	}
	if exists && (old.HeadVersionID != expected || old.Kind != r.Asset.Kind) {
		return library.ErrConflict
	}
	if exists && old.Status == "archived" {
		return library.ErrArchived
	}
	if !exists && archive {
		return ErrNotFound
	}
	if archive {
		if r.Asset.Status != "archived" || r.Asset.HeadVersionID != old.HeadVersionID || r.Version.ID != old.HeadVersionID {
			return library.ErrInput
		}
		return nil
	}
	card, err := library.Normalize(r.Asset.Kind, r.Version.Card)
	if err != nil {
		return err
	}
	if e := library.ValidateContent(card, r.Version.References); e != nil {
		return e
	}
	if library.ValidateReferences(r.Version.References) != nil || r.Version.AssetID != r.Asset.ID || r.Version.SpaceID != r.Asset.SpaceID || r.Version.ID != r.Asset.HeadVersionID || r.Asset.Status != "active" || !library.IDPattern.MatchString(r.Version.ID) {
		return library.ErrInput
	}
	h, err := library.Digest(struct {
		Card       library.Card
		References []library.Reference
	}{card, r.Version.References})
	if err != nil || h != r.Version.SHA256 {
		return library.ErrInput
	}
	return nil
}
