// Package libraryrepo persists library metadata using per-space file snapshots
// or indexed PostgreSQL rows. It never reads another space to answer a query.
package libraryrepo

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"sync"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/library"
)

var ErrNotFound = errors.New("library resource not found")
var ErrKey = errors.New("library idempotency conflict")
var ErrLimit = errors.New("library capacity exceeded")
var ErrStorage = errors.New("library storage unavailable")

const Schema = `CREATE TABLE IF NOT EXISTS studio_library_assets (
 space_id text NOT NULL, id text NOT NULL, kind text NOT NULL CHECK(kind IN ('character','product','scene')),
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
CREATE INDEX IF NOT EXISTS studio_library_assets_list ON studio_library_assets(space_id,kind,status,id);`

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
	mu  sync.Mutex
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
		s.mu.Lock()
		defer s.mu.Unlock()
		d, err := s.read(space)
		if err != nil {
			return Result{}, false, err
		}
		var ok bool
		r, ok = d.Receipts[key]
		if !ok {
			return Result{}, false, nil
		}
	}
	if r.Digest != digest {
		return Result{}, false, ErrKey
	}
	return r.Result, true, nil
}
func (s *Store) read(space string) (disk, error) {
	if !library.IDPattern.MatchString(space) {
		return disk{}, library.ErrInput
	}
	var d disk
	root, err := os.OpenRoot(s.dir)
	if err != nil {
		return disk{}, ErrStorage
	}
	defer root.Close()
	f, err := root.Open(space + ".json")
	if os.IsNotExist(err) {
		return fresh(), nil
	}
	if err != nil {
		return disk{}, ErrStorage
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 64<<20 {
		return disk{}, ErrStorage
	}
	b, err := io.ReadAll(io.LimitReader(f, (64<<20)+1))
	if err != nil {
		return disk{}, ErrStorage
	}
	if len(b) > 64<<20 || json.Unmarshal(b, &d) != nil || d.Format != 1 || d.Assets == nil || d.Versions == nil || d.Receipts == nil {
		return disk{}, ErrStorage
	}
	for id, a := range d.Assets {
		if a.ID != id || a.SpaceID != space || (a.Status != "active" && a.Status != "archived") {
			return disk{}, ErrStorage
		}
		v, ok := d.Versions[a.HeadVersionID]
		if !ok || v.AssetID != id || v.SpaceID != space {
			return disk{}, ErrStorage
		}
	}
	for id, v := range d.Versions {
		if v.ID != id || v.SpaceID != space || d.Assets[v.AssetID].ID == "" {
			return disk{}, ErrStorage
		}
		if _, e := library.Normalize(d.Assets[v.AssetID].Kind, v.Card); e != nil {
			return disk{}, ErrStorage
		}
		if v.Number < 1 || library.ValidateReferences(v.References) != nil {
			return disk{}, ErrStorage
		}
		h, e := library.Digest(struct {
			Card       library.Card
			References []library.Reference
		}{v.Card, v.References})
		if e != nil || h != v.SHA256 {
			return disk{}, ErrStorage
		}
	}
	for _, r := range d.Receipts {
		if r.Result.Asset.SpaceID != space || r.Result.Version.SpaceID != space || r.Result.Asset.ID != r.Result.Version.AssetID || d.Versions[r.Result.Version.ID].ID == "" {
			return disk{}, ErrStorage
		}
	}
	return d, nil
}
func (s *Store) save(space string, d disk) error {
	b, err := json.Marshal(d)
	if err != nil || len(b) > 64<<20 {
		return ErrLimit
	}
	f, err := os.CreateTemp(s.dir, ".library-")
	if err != nil {
		return ErrStorage
	}
	defer os.Remove(f.Name())
	_, err = f.Write(b)
	if err == nil {
		err = f.Sync()
	}
	err = errors.Join(err, f.Close())
	if err != nil {
		return ErrStorage
	}
	if os.Rename(f.Name(), filepath.Join(s.dir, space+".json")) != nil {
		return ErrStorage
	}
	// Production runs on Linux. Sync the directory entry as well as contents;
	// Windows unit tests exercise rules/replay, not power-loss durability.
	if runtime.GOOS != "windows" {
		dir, e := os.Open(s.dir)
		if e != nil {
			return ErrStorage
		}
		if e = errors.Join(dir.Sync(), dir.Close()); e != nil {
			return ErrStorage
		}
	}
	return nil
}
func validateQuery(q Query) error {
	if q.Limit < 1 || q.Limit > 100 {
		return library.ErrInput
	}
	if q.Kind != "" && q.Kind != "character" && q.Kind != "product" && q.Kind != "scene" {
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
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := s.read(space)
	if err != nil {
		return nil, err
	}
	for _, a := range d.Assets {
		if a.ID > q.After && (q.Kind == "" || a.Kind == q.Kind) && (q.Status == "" || a.Status == q.Status) {
			out = append(out, a)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	if len(out) > q.Limit {
		out = out[:q.Limit]
	}
	return out, nil
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
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := s.read(space)
	if err != nil {
		return Result{}, err
	}
	a, ok := d.Assets[id]
	if !ok {
		return Result{}, ErrNotFound
	}
	return Result{a, d.Versions[a.HeadVersionID]}, nil
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
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := s.read(space)
	if err != nil {
		return library.Version{}, err
	}
	v, ok := d.Versions[id]
	if !ok || v.AssetID != asset {
		return library.Version{}, ErrNotFound
	}
	return v, nil
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
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := s.read(space)
	if err != nil {
		return nil, err
	}
	for _, v := range d.Versions {
		if v.AssetID == asset && (before == 0 || v.Number < before) {
			out = append(out, v)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Number > out[j].Number })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
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
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := s.read(space)
	if err != nil {
		return Result{}, err
	}
	if r, ok := d.Receipts[key]; ok {
		if r.Digest != digest {
			return Result{}, ErrKey
		}
		return r.Result, nil
	}
	old, exists := d.Assets[result.Asset.ID]
	if err = validateMutation(old, exists, expected, result, archive); err != nil {
		return Result{}, err
	}
	if !archive {
		if _, ok := d.Versions[result.Version.ID]; ok {
			return Result{}, library.ErrConflict
		}
		n := 1
		if exists {
			n = d.Versions[old.HeadVersionID].Number + 1
		}
		if result.Version.Number != n {
			return Result{}, library.ErrConflict
		}
	}
	var size int64
	for _, v := range d.Versions {
		for _, ref := range v.References {
			size += ref.SizeBytes
		}
	}
	if !archive {
		for _, ref := range result.Version.References {
			size += ref.SizeBytes
		}
	}
	if size > 128<<20 {
		return Result{}, ErrLimit
	}
	if len(d.Assets) >= 1000 && !exists || len(d.Versions) >= 10000 || len(d.Receipts) >= 20000 {
		return Result{}, ErrLimit
	}
	d.Assets[result.Asset.ID] = result.Asset
	if !archive {
		d.Versions[result.Version.ID] = result.Version
	}
	d.Receipts[key] = receipt{digest, result}
	if err = s.save(space, d); err != nil {
		return Result{}, err
	}
	return result, nil
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
