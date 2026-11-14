package application

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/adcwb/ai-creative-studio/backend/internal/persistence/libraryrepo"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/adcwb/ai-creative-studio/backend/internal/platform/nativefs"
)

// The manifest is committed before application metadata can reference a blob.
// A failed metadata commit can leave an unreferenced object, never an observable
// incomplete asset. It is deliberately not a distributed transaction claim.
type managedBlobs struct {
	mu      sync.Mutex
	store   BlobStore
	db      *sql.DB
	dir     string
	entries map[string]BlobInfo
}

func openManagedBlobs(dir string, store BlobStore) (*managedBlobs, error) {
	m := &managedBlobs{store: store, dir: dir, entries: map[string]BlobInfo{}}
	b, e := safeReadLocal(filepath.Join(dir, "blobs.json"), 64<<20)
	if e == nil {
		if json.Unmarshal(b, &m.entries) != nil || m.entries == nil {
			return nil, errors.New("invalid blob manifest")
		}
		for k, v := range m.entries {
			if k != v.Key || !validBlobInfo(v) {
				return nil, errors.New("invalid blob manifest entry")
			}
		}
	} else if !os.IsNotExist(e) {
		return nil, e
	}
	return m, nil
}
func (m *managedBlobs) commit() error {
	b, e := json.Marshal(m.entries)
	if e != nil {
		return e
	}
	f, e := os.CreateTemp(m.dir, ".blobs-")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	_, e = f.Write(b)
	if e == nil {
		e = nativefs.SyncFile(f)
	}
	e = errors.Join(e, f.Close())
	if e == nil {
		e = nativefs.Rename(f.Name(), filepath.Join(m.dir, "blobs.json"))
	}
	if e == nil {
		d, err := os.Open(m.dir)
		if err != nil {
			return err
		}
		e = nativefs.SyncFile(d)
		d.Close()
	}
	return e
}
func (m *managedBlobs) put(ctx context.Context, key string, b []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	info := blobInfo(key, b)
	if old, ok := m.entries[key]; ok && old != info {
		return ErrBlobIntegrity
	}
	actual, e := m.store.Put(ctx, key, b)
	if e != nil {
		return e
	}
	if actual != info {
		return ErrBlobIntegrity
	}
	if m.db != nil {
		return insertBlobInfo(ctx, m.db, info)
	}
	m.entries[key] = info
	return m.commit()
}
func (m *managedBlobs) info(ctx context.Context, key string) (BlobInfo, error) {
	if e := checkBlobKey(key); e != nil {
		return BlobInfo{}, e
	}
	if m.db != nil {
		var i BlobInfo
		i.Key = key
		e := m.db.QueryRowContext(ctx, "SELECT size,sha256 FROM studio_blobs WHERE key=$1", key).Scan(&i.Size, &i.SHA256)
		if e != nil {
			return BlobInfo{}, errors.New("blob manifest unavailable")
		}
		if !validBlobInfo(i) {
			return BlobInfo{}, ErrBlobIntegrity
		}
		return i, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	info, ok := m.entries[key]
	if !ok {
		return BlobInfo{}, errors.New("blob missing from manifest; offline migration required")
	}
	return info, nil
}
func (m *managedBlobs) read(ctx context.Context, key string) ([]byte, error) {
	info, e := m.info(ctx, key)
	if e != nil {
		return nil, e
	}
	return readVerifiedBlob(ctx, m.store, info)
}
func (m *managedBlobs) has(key string) bool {
	ctx, cancel := blobContext()
	defer cancel()
	_, e := m.info(ctx, key)
	return e == nil
}
func safeReadLocal(path string, limit int64) ([]byte, error) {
	f, e := nativefs.OpenFile(path, os.O_RDONLY|nativefs.NoFollow|nativefs.NonBlock, 0)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	st, e := f.Stat()
	if e != nil || !st.Mode().IsRegular() || st.Size() > limit {
		return nil, ErrBlobIntegrity
	}
	b, e := io.ReadAll(io.LimitReader(f, limit+1))
	if int64(len(b)) > limit {
		return nil, ErrBlobIntegrity
	}
	return b, e
}
func (m *managedBlobs) materialize(ctx context.Context, key, path string) (string, error) {
	info, e := m.info(ctx, key)
	if e != nil {
		return "", e
	}
	if actual, e := localBlobInfo(ctx, key, path); e == nil && actual == info {
		return path, nil
	}
	r, e := m.store.OpenRange(ctx, key, 0, -1)
	if e != nil {
		return "", e
	}
	defer r.Close()
	// Cache replacement is atomic; readers can never open partial downloads.
	f, e := os.CreateTemp(filepath.Dir(path), ".materialize-")
	if e != nil {
		return "", e
	}
	defer os.Remove(f.Name())
	e = copyBlobBytes(ctx, f, r, info)
	if e == nil {
		e = nativefs.SyncFile(f)
	}
	e = errors.Join(e, f.Close())
	if e == nil {
		e = nativefs.Rename(f.Name(), path)
	}
	if e != nil {
		return "", e
	}
	return path, nil
}
func blobContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 60*time.Second)
}
func (m *MediaStore) storeBlob(space, project, name string) error {
	ctx, cancel := blobContext()
	defer cancel()
	return m.storeBlobContext(ctx, space, project, name)
}
func (m *MediaStore) storeBlobContext(ctx context.Context, space, project, name string) error {
	if m.blobs == nil {
		return nil
	}
	if e := ctx.Err(); e != nil {
		return e
	}
	if !validMediaBlobName(name) {
		return ErrBlobIntegrity
	}
	return m.blobs.putFile(ctx, blobKey(space, project, name), filepath.Join(m.dir, name))
}
func (m *MediaStore) localBlob(space, project, name string) (string, error) {
	if !validMediaBlobName(name) {
		return "", ErrBlobIntegrity
	}
	path := filepath.Join(m.dir, name)
	if m.blobs == nil {
		return path, nil
	}
	ctx, cancel := blobContext()
	defer cancel()
	return m.blobs.materialize(ctx, blobKey(space, project, name), path)
}
func validMediaBlobName(name string) bool {
	base := name
	for _, ext := range []string{".mp4", ".srt", ".vtt"} {
		base = strings.TrimSuffix(base, ext)
	}
	return mediaFileID(base) && (name == base || name == base+".mp4" || name == base+".srt" || name == base+".vtt")
}

// ConfigureBlobStorageFromEnv is startup-only. No network probes or automatic
// migration occur; all old references must have a durable migration manifest.
func ConfigureBlobStorageFromEnv(s *FileStore) error {
	if _, e := renderSegmentFrames(); e != nil {
		return e
	}
	mode := os.Getenv("BLOB_STORE")
	if mode == "" || mode == "file" {
		if s.pg != nil {
			return errors.New("PostgreSQL mode requires explicit shared-file or S3 blob storage")
		}
		return nil
	}
	if mode == "shared-file" {
		root := os.Getenv("BLOB_FILE_ROOT")
		if !filepath.IsAbs(root) {
			return errors.New("shared blob root must be absolute")
		}
		store, e := NewFileBlobStore(root)
		if e != nil {
			return e
		}
		if e = configureBlobStorage(s, store); e != nil {
			store.Close()
			return e
		}
		return nil
	}
	if mode != "s3" {
		return errors.New("unsupported blob storage mode")
	}
	store, e := NewS3BlobStore(S3BlobConfig{Endpoint: os.Getenv("BLOB_S3_ENDPOINT"), Bucket: os.Getenv("BLOB_S3_BUCKET"), Region: os.Getenv("BLOB_S3_REGION"), AccessKey: os.Getenv("BLOB_S3_ACCESS_KEY"), SecretKey: os.Getenv("BLOB_S3_SECRET_KEY"), SessionToken: os.Getenv("BLOB_S3_SESSION_TOKEN"), AllowLoopbackHTTP: os.Getenv("BLOB_S3_ALLOW_LOOPBACK_HTTP") == "true"})
	if e != nil {
		return e
	}
	return configureBlobStorage(s, store)
}
func configureBlobStorage(s *FileStore, store BlobStore) error {
	if remote, ok := store.(*S3BlobStore); ok {
		var db *sql.DB
		if s.pg != nil {
			db = s.pg.db
		}
		if e := remote.configureMultipart(filepath.Join(filepath.Dir(s.path), "s3-multipart"), db); e != nil {
			return e
		}
	}
	var m *managedBlobs
	var e error
	if s.pg != nil {
		m = &managedBlobs{store: store, db: s.pg.db, dir: filepath.Dir(s.path), entries: map[string]BlobInfo{}}
	} else {
		m, e = openManagedBlobs(filepath.Dir(s.path), store)
	}
	if e != nil {
		return e
	}
	for _, t := range s.data.Tasks {
		if t.ResultPath != "" && !m.has(blobKey(t.SpaceID, "results", t.ResultPath)) {
			return errors.New("image results require offline blob migration")
		}
	}
	for k, p := range s.media.data.Projects {
		parts := strings.SplitN(k, ":", 2)
		if len(parts) != 2 {
			return ErrBlobIntegrity
		}
		for _, a := range p.Assets {
			if !m.has(blobKey(parts[0], parts[1], a.ID)) {
				return errors.New("media assets require offline blob migration")
			}
		}
		for _, x := range p.Exports {
			if x.Status == "succeeded" {
				for _, ext := range []string{".mp4", ".srt", ".vtt"} {
					if !m.has(blobKey(parts[0], parts[1], x.ID+ext)) {
						return errors.New("exports require offline blob migration")
					}
				}
			}
		}
	}
	if s.pg == nil {
		refs, e := libraryrepo.FileReferences(filepath.Join(filepath.Dir(s.path), "library"))
		if e != nil {
			return e
		}
		for space, items := range refs {
			for _, ref := range items {
				if !m.has(blobKey(space, "library", ref.ID)) {
					return errors.New("library references require offline blob migration")
				}
			}
		}
	}
	if s.pg == nil {
		refs, e := fileRenderReferences(filepath.Join(s.media.dir, "render-checkpoints"))
		if e != nil {
			return e
		}
		for _, ref := range refs {
			if !m.has(ref.Key) {
				return errors.New("render checkpoints require offline blob migration")
			}
		}
	} else {
		var missing bool
		if e := s.pg.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM studio_render_checkpoints c LEFT JOIN studio_blobs b ON b.key=c.payload->'info'->>'key' WHERE c.step<>'manifest' AND c.payload->>'state'='ready' AND b.key IS NULL)`).Scan(&missing); e != nil || missing {
			return errors.New("render checkpoints require a complete blob manifest")
		}
	}
	if s.creative != nil {
		refs, e := avatarCheckpointReferences(s.creative.data)
		if e != nil {
			return e
		}
		for _, ref := range refs {
			actual, e := m.info(context.Background(), ref.Key)
			if e != nil || actual != ref {
				return errors.New("avatar checkpoints require a complete verified blob migration")
			}
		}
	}
	s.blobs = m
	s.media.blobs = m
	if remote, ok := store.(*S3BlobStore); ok {
		remote.startMultipartCleanup()
	}
	return nil
}

const BlobSchema = `CREATE TABLE IF NOT EXISTS studio_blobs (
 key text PRIMARY KEY CHECK(key ~ '^[a-f0-9]{64}/[a-f0-9]{64}/[a-f0-9]{32}(\.(png|mp4|srt|vtt|glb))?$'),
 tenant_hash text NOT NULL CHECK(tenant_hash=split_part(key,'/',1)),
 project_hash text NOT NULL CHECK(project_hash=split_part(key,'/',2)),
 size bigint NOT NULL CHECK(size>=0),
 sha256 text NOT NULL CHECK(sha256 ~ '^[a-f0-9]{64}$')
);
ALTER TABLE studio_blobs DROP CONSTRAINT IF EXISTS studio_blobs_key_check;
ALTER TABLE studio_blobs DROP CONSTRAINT IF EXISTS studio_blobs_key_kind_v2;
ALTER TABLE studio_blobs ADD CONSTRAINT studio_blobs_key_kind_v2 CHECK(key ~ '^[a-f0-9]{64}/[a-f0-9]{64}/[a-f0-9]{32}(\.(png|mp4|srt|vtt|glb))?$');
ALTER TABLE studio_blobs DROP CONSTRAINT IF EXISTS studio_blobs_size_check;
ALTER TABLE studio_blobs DROP CONSTRAINT IF EXISTS studio_blobs_size_nonnegative;
ALTER TABLE studio_blobs ADD CONSTRAINT studio_blobs_size_nonnegative CHECK(size>=0);`

type blobSQLExecutor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func insertBlobInfo(ctx context.Context, db blobSQLExecutor, i BlobInfo) error {
	if pool, ok := db.(*sql.DB); ok {
		tx, e := pool.BeginTx(ctx, nil)
		if e != nil {
			return errors.New("blob manifest transaction unavailable")
		}
		defer tx.Rollback()
		if e = insertBlobInfo(ctx, tx, i); e != nil {
			return e
		}
		if e = tx.Commit(); e != nil {
			return errors.New("blob manifest commit outcome uncertain")
		}
		return nil
	}
	if _, e := db.ExecContext(ctx, "SELECT pg_advisory_xact_lock_shared(728219117)"); e != nil {
		return errors.New("blob manifest migration barrier unavailable")
	}
	if !validBlobInfo(i) {
		return ErrBlobIntegrity
	}
	parts := strings.Split(i.Key, "/")
	result, e := db.ExecContext(ctx, `INSERT INTO studio_blobs(key,tenant_hash,project_hash,size,sha256) VALUES($1,$2,$3,$4,$5) ON CONFLICT(key) DO UPDATE SET key=EXCLUDED.key WHERE studio_blobs.size=EXCLUDED.size AND studio_blobs.sha256=EXCLUDED.sha256`, i.Key, parts[0], parts[1], i.Size, i.SHA256)
	if e != nil {
		return errors.New("blob manifest commit failed")
	}
	n, e := result.RowsAffected()
	if e != nil || n != 1 {
		return ErrBlobIntegrity
	}
	return nil
}
func ImportBlobManifest(ctx context.Context, dir string, db *sql.DB) error {
	m, e := openManagedBlobs(dir, nil)
	if e != nil {
		return e
	}
	tx, e := db.BeginTx(ctx, nil)
	if e != nil {
		return errors.New("blob import transaction unavailable")
	}
	defer tx.Rollback()
	for _, i := range m.entries {
		if e = insertBlobInfo(ctx, tx, i); e != nil {
			return e
		}
	}
	if e = tx.Commit(); e != nil {
		return errors.New("blob manifest import commit failed")
	}
	return nil
}
func ExportBlobManifest(ctx context.Context, dir string, db *sql.DB) error {
	m := &managedBlobs{dir: dir, entries: map[string]BlobInfo{}}
	rows, e := db.QueryContext(ctx, "SELECT key,size,sha256 FROM studio_blobs ORDER BY key")
	if e != nil {
		return errors.New("blob manifest export unavailable")
	}
	defer rows.Close()
	for rows.Next() {
		var i BlobInfo
		if e = rows.Scan(&i.Key, &i.Size, &i.SHA256); e != nil {
			return e
		}
		if !validBlobInfo(i) {
			return ErrBlobIntegrity
		}
		m.entries[i.Key] = i
	}
	if e = rows.Err(); e != nil {
		return e
	}
	return m.commit()
}
