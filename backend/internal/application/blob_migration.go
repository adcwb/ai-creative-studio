package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
)

type BlobMigrationReport struct {
	Direction      string `json:"direction"`
	Objects        int    `json:"objects"`
	Bytes          int64  `json:"bytes"`
	ManifestSHA256 string `json:"manifest_sha256"`
}
type migrationBlob struct{ key, path string }

// MigrateBlobStorage requires the API and workers to be stopped. The exclusive
// store lock rejects a running file backend. It preserves metadata/IDs and does
// not delete source files or remote objects. Reverse migration materializes the
// CURRENT committed references, including writes made after switching to S3.
func MigrateBlobStorage(ctx context.Context, dir string, store BlobStore, toFile bool) (BlobMigrationReport, error) {
	report := BlobMigrationReport{Direction: "to-s3"}
	if toFile {
		report.Direction = "to-file"
	}
	for _, p := range []string{dir, filepath.Join(dir, "media"), filepath.Join(dir, "results")} {
		st, e := os.Lstat(p)
		if e != nil || !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
			return report, errors.New("migration requires regular data directories")
		}
	}
	lock, e := os.OpenFile(filepath.Join(dir, "store.lock"), os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if e != nil {
		return report, e
	}
	defer lock.Close()
	if e = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		return report, errors.New("stop API and workers before blob migration")
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	b, e := safeReadLocal(filepath.Join(dir, "state.json"), 64<<20)
	if e != nil {
		return report, e
	}
	var state snapshot
	if json.Unmarshal(b, &state) != nil || state.Version != 3 || state.Tasks == nil {
		return report, errors.New("invalid application metadata")
	}
	b, e = safeReadLocal(filepath.Join(dir, "media", "state.json"), 64<<20)
	if e != nil {
		return report, e
	}
	var media mediaDisk
	if json.Unmarshal(b, &media) != nil || media.Version != 1 || media.Projects == nil {
		return report, errors.New("invalid media metadata")
	}
	var objects []migrationBlob
	for _, t := range state.Tasks {
		if t.ResultPath != "" {
			name, e := resultName(t.ID)
			if e != nil || name != t.ResultPath {
				return report, ErrBlobIntegrity
			}
			objects = append(objects, migrationBlob{blobKey(t.SpaceID, "results", name), filepath.Join(dir, "results", name)})
		}
	}
	for k, p := range media.Projects {
		parts := strings.SplitN(k, ":", 2)
		if len(parts) != 2 {
			return report, ErrBlobIntegrity
		}
		for _, a := range p.Assets {
			if !mediaFileID(a.ID) {
				return report, ErrBlobIntegrity
			}
			objects = append(objects, migrationBlob{blobKey(parts[0], parts[1], a.ID), filepath.Join(dir, "media", a.ID)})
		}
		for _, x := range p.Exports {
			if x.Status == "running" || x.Status == "queued" {
				return report, errors.New("resolve interrupted exports before migration")
			}
			if x.Status == "succeeded" {
				if !mediaFileID(x.ID) {
					return report, ErrBlobIntegrity
				}
				for _, ext := range []string{".mp4", ".srt", ".vtt"} {
					objects = append(objects, migrationBlob{blobKey(parts[0], parts[1], x.ID+ext), filepath.Join(dir, "media", x.ID+ext)})
				}
			}
		}
	}
	sort.Slice(objects, func(i, j int) bool { return objects[i].key < objects[j].key })
	manifest, e := openManagedBlobs(dir, store)
	if e != nil {
		return report, e
	}
	for _, obj := range objects {
		if e = ctx.Err(); e != nil {
			return report, e
		}
		var data []byte
		if toFile {
			data, e = manifest.read(ctx, obj.key)
			if e != nil {
				return report, e
			}
			existing, readErr := safeReadLocal(obj.path, MaxBlobBytes)
			if readErr == nil {
				if blobInfo(obj.key, existing) != blobInfo(obj.key, data) {
					return report, errors.New("rollback refuses to overwrite mismatched local file")
				}
			}
			if readErr != nil {
				if !os.IsNotExist(readErr) {
					return report, readErr
				}
				tmp, e := os.CreateTemp(filepath.Dir(obj.path), ".rollback-")
				if e != nil {
					return report, e
				}
				_, e = tmp.Write(data)
				if e == nil {
					e = tmp.Sync()
				}
				e = errors.Join(e, tmp.Close())
				if e == nil {
					e = os.Link(tmp.Name(), obj.path)
				}
				os.Remove(tmp.Name())
				if e != nil {
					return report, e
				}
			}
		} else {
			data, e = safeReadLocal(obj.path, MaxBlobBytes)
			if e != nil {
				return report, e
			}
			if e = manifest.put(ctx, obj.key, data); e != nil {
				return report, e
			}
			// A real read-after-write with SHA256 validation, not merely HEAD metadata.
			if _, e = manifest.read(ctx, obj.key); e != nil {
				return report, fmt.Errorf("migration verification failed: %w", e)
			}
		}
		report.Objects++
		report.Bytes += int64(len(data))
	}
	for _, p := range []string{dir, filepath.Join(dir, "media"), filepath.Join(dir, "results")} {
		f, e := os.Open(p)
		if e != nil {
			return report, e
		}
		e = f.Sync()
		f.Close()
		if e != nil {
			return report, e
		}
	}
	b, e = safeReadLocal(filepath.Join(dir, "blobs.json"), 64<<20)
	if e != nil && len(objects) > 0 {
		return report, e
	}
	report.ManifestSHA256 = s3Hash(string(b))
	return report, nil
}

func S3BlobStoreFromEnv() (*S3BlobStore, error) {
	return NewS3BlobStore(S3BlobConfig{Endpoint: os.Getenv("BLOB_S3_ENDPOINT"), Bucket: os.Getenv("BLOB_S3_BUCKET"), Region: os.Getenv("BLOB_S3_REGION"), AccessKey: os.Getenv("BLOB_S3_ACCESS_KEY"), SecretKey: os.Getenv("BLOB_S3_SECRET_KEY"), SessionToken: os.Getenv("BLOB_S3_SESSION_TOKEN"), AllowLoopbackHTTP: os.Getenv("BLOB_S3_ALLOW_LOOPBACK_HTTP") == "true"})
}
