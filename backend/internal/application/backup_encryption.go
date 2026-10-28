package application

import (
	"archive/tar"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/adcwb/ai-creative-studio/backend/internal/platform/securebackup"
)

var ErrEncryptedBackup = errors.New("encrypted backup operation refused or incomplete; verify private staging before retry")
var encryptedBackupName = regexp.MustCompile(`^studio-backup-[0-9a-f]{32}\.sbk$`)

const maxEncryptedBackupFiles = 10000

type EncryptedBackupReport struct {
	File      string    `json:"file"`
	SHA256    string    `json:"sha256"`
	Size      int64     `json:"size"`
	CreatedAt time.Time `json:"created_at"`
	Verified  bool      `json:"verified"`
}

func lockEncryptedBackups(root string) (*os.File, error) {
	st, e := os.Lstat(root)
	if e != nil || !st.IsDir() || st.Mode()&os.ModeSymlink != 0 || st.Mode().Perm()&0077 != 0 {
		return nil, ErrEncryptedBackup
	}
	f, e := os.OpenFile(filepath.Join(root, ".studio-backup.lock"), os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if e != nil {
		return nil, ErrEncryptedBackup
	}
	st, e = f.Stat()
	if e != nil || !st.Mode().IsRegular() || syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
		f.Close()
		return nil, ErrEncryptedBackup
	}
	return f, nil
}
func unlockEncryptedBackups(f *os.File) { syscall.Flock(int(f.Fd()), syscall.LOCK_UN); f.Close() }
func encryptedBackupInfo(ctx context.Context, path string, key []byte) (EncryptedBackupReport, error) {
	report := EncryptedBackupReport{File: filepath.Base(path)}
	f, e := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if e != nil {
		return report, ErrEncryptedBackup
	}
	defer f.Close()
	st, e := f.Stat()
	if e != nil || !st.Mode().IsRegular() || st.Size() > securebackup.MaxPlainBytes+(1<<30) {
		return report, ErrEncryptedBackup
	}
	digest := sha256.New()
	info, e := securebackup.Decrypt(ctx, io.Discard, io.TeeReader(f, digest), key)
	if e != nil {
		return report, ErrEncryptedBackup
	}
	report.SHA256 = hex.EncodeToString(digest.Sum(nil))
	report.Size = st.Size()
	report.CreatedAt = info.CreatedAt
	report.Verified = true
	return report, nil
}

// SealBackup verifies the source bundle, streams a tar through authenticated
// encryption, verifies the new envelope, then publishes a unique immutable file.
// It never deletes the plaintext source or exports the encryption key.
func SealBackup(ctx context.Context, source, root string, key []byte) (EncryptedBackupReport, error) {
	var report EncryptedBackupReport
	if len(key) != 32 {
		return report, ErrEncryptedBackup
	}
	lock, e := lockEncryptedBackups(root)
	if e != nil {
		return report, e
	}
	defer unlockEncryptedBackups(lock)
	manifest, verified, e := VerifyPostgresBackup(ctx, source)
	if e != nil {
		return report, ErrEncryptedBackup
	}
	inventory, e := backupTarInventory(source, manifest, verified)
	if e != nil {
		return report, ErrEncryptedBackup
	}
	f, e := os.CreateTemp(root, ".encrypting-")
	if e != nil {
		return report, e
	}
	temporary := f.Name()
	defer os.Remove(temporary)
	defer f.Close()
	reader, writer := io.Pipe()
	defer reader.Close()
	done := make(chan error, 1)
	go func() { err := writeBackupTar(ctx, writer, source, inventory); writer.CloseWithError(err); done <- err }()
	_, e = securebackup.Encrypt(ctx, f, reader, key, time.Now().UTC())
	reader.CloseWithError(e)
	producerErr := <-done
	if e != nil || producerErr != nil {
		return report, ErrEncryptedBackup
	}
	if e = f.Sync(); e != nil {
		return report, ErrEncryptedBackup
	}
	if e = f.Close(); e != nil {
		return report, ErrEncryptedBackup
	}
	report, e = encryptedBackupInfo(ctx, temporary, key)
	if e != nil {
		return report, e
	}
	// Validate the original bundle again: ordinary source mutation while sealing
	// is rejected; sources must remain stopped/private throughout this operation.
	if _, _, e = VerifyPostgresBackup(ctx, source); e != nil {
		return report, ErrEncryptedBackup
	}
	var id [16]byte
	if _, e = rand.Read(id[:]); e != nil {
		return report, ErrEncryptedBackup
	}
	name := "studio-backup-" + hex.EncodeToString(id[:]) + ".sbk"
	if e = os.Link(temporary, filepath.Join(root, name)); e != nil {
		return report, ErrEncryptedBackup
	}
	report.File = name
	if e = syncMigrationDirectory(root); e != nil {
		return report, ErrEncryptedBackup
	}
	return report, nil
}
func backupTarInventory(source string, manifest PostgresBackupManifest, report PostgresBackupReport) (map[string]PostgresBackupFile, error) {
	metadata, e := backupHashFile(filepath.Join(source, "backup.json"), 32<<20)
	if e != nil || metadata.SHA256 != report.ManifestSHA256 {
		return nil, ErrEncryptedBackup
	}
	expected := map[string]PostgresBackupFile{"backup.json": metadata, "database.dump": manifest.Database}
	for key, info := range manifest.Objects {
		expected["objects/"+key] = PostgresBackupFile{Size: info.Size, SHA256: info.SHA256}
	}
	return expected, nil
}
func writeBackupTar(ctx context.Context, w io.Writer, source string, inventory map[string]PostgresBackupFile) error {
	root, e := os.OpenRoot(source)
	if e != nil {
		return e
	}
	defer root.Close()
	tw := tar.NewWriter(w)
	count := 0
	seen := map[string]bool{}
	directories := 0
	var total int64
	e = filepath.WalkDir(source, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		if path == source {
			return nil
		}
		rel, err := filepath.Rel(source, path)
		if err != nil || !filepath.IsLocal(rel) || d.Type()&os.ModeSymlink != 0 {
			return ErrEncryptedBackup
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.IsDir() {
			directories++
			if directories > 200001 {
				return ErrEncryptedBackup
			}
			return tw.WriteHeader(&tar.Header{Name: filepath.ToSlash(rel) + "/", Typeflag: tar.TypeDir, Mode: 0700})
		}
		if !info.Mode().IsRegular() || info.Size() < 0 || info.Size() > maxDatabaseBackupBytes || count >= 100002 || total > securebackup.MaxPlainBytes-info.Size() {
			return ErrEncryptedBackup
		}
		expected, ok := inventory[filepath.ToSlash(rel)]
		if !ok || expected.Size != info.Size() {
			return ErrEncryptedBackup
		}
		seen[filepath.ToSlash(rel)] = true
		f, err := root.OpenFile(rel, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
		if err != nil {
			return err
		}
		defer f.Close()
		current, err := f.Stat()
		if err != nil || !os.SameFile(info, current) {
			return ErrEncryptedBackup
		}
		if err = tw.WriteHeader(&tar.Header{Name: filepath.ToSlash(rel), Typeflag: tar.TypeReg, Mode: 0600, Size: info.Size()}); err != nil {
			return err
		}
		digest := sha256.New()
		n, err := io.CopyN(io.MultiWriter(tw, digest), f, info.Size())
		total += n
		count++
		if err != nil || hex.EncodeToString(digest.Sum(nil)) != expected.SHA256 {
			return ErrEncryptedBackup
		}
		var extra [1]byte
		if n, e := f.Read(extra[:]); n != 0 || e != io.EOF {
			return ErrEncryptedBackup
		}
		return nil
	})
	if e != nil {
		return e
	}
	if len(seen) != len(inventory) {
		return ErrEncryptedBackup
	}
	return tw.Close()
}

// OpenBackup decrypts only into a new private staging directory. Authenticated
// EOF, safe tar paths and the full PostgreSQL/blob manifest must all pass before
// publishing a NEW destination. It never restores a database or starts workers.
func OpenBackup(ctx context.Context, source, destination string, key []byte) (PostgresBackupReport, error) {
	var report PostgresBackupReport
	if len(key) != 32 {
		return report, ErrEncryptedBackup
	}
	if _, e := os.Lstat(destination); !os.IsNotExist(e) {
		return report, ErrEncryptedBackup
	}
	f, e := os.OpenFile(source, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if e != nil {
		return report, ErrEncryptedBackup
	}
	defer f.Close()
	st, e := f.Stat()
	if e != nil || !st.Mode().IsRegular() || st.Size() > securebackup.MaxPlainBytes+(1<<30) {
		return report, ErrEncryptedBackup
	}
	stage, e := os.MkdirTemp(filepath.Dir(destination), ".decrypting-")
	if e != nil {
		return report, e
	}
	defer os.RemoveAll(stage)
	reader, writer := io.Pipe()
	defer reader.Close()
	done := make(chan error, 1)
	go func() { _, err := securebackup.Decrypt(ctx, writer, f, key); writer.CloseWithError(err); done <- err }()
	e = readBackupTar(ctx, reader, stage)
	if e == nil {
		_, e = io.Copy(io.Discard, reader)
	}
	reader.CloseWithError(e)
	decryptErr := <-done
	if e != nil || decryptErr != nil {
		return report, ErrEncryptedBackup
	}
	_, report, e = VerifyPostgresBackup(ctx, stage)
	if e != nil {
		return report, ErrEncryptedBackup
	}
	if e = syncMigrationDirectory(stage); e != nil {
		return report, ErrEncryptedBackup
	}
	if e = renameDirectoryNoReplace(stage, destination); e != nil {
		return report, ErrEncryptedBackup
	}
	return report, nil
}
func readBackupTar(ctx context.Context, r io.Reader, destination string) error {
	tr := tar.NewReader(r)
	root, e := os.OpenRoot(destination)
	if e != nil {
		return e
	}
	defer root.Close()
	seen := map[string]bool{}
	count := 0
	var total int64
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		name := strings.TrimSuffix(h.Name, "/")
		if !filepath.IsLocal(name) || filepath.ToSlash(filepath.Clean(name)) != name || strings.Contains(name, "\\") || seen[name] || len(name) > 512 {
			return ErrEncryptedBackup
		}
		seen[name] = true
		if h.Typeflag == tar.TypeDir {
			if len(seen) > 300005 {
				return ErrEncryptedBackup
			}
			if e = root.MkdirAll(name, 0700); e != nil {
				return e
			}
			continue
		}
		if h.Typeflag != tar.TypeReg || h.Size < 0 || h.Size > maxDatabaseBackupBytes || count >= 100002 || total > securebackup.MaxPlainBytes-h.Size {
			return ErrEncryptedBackup
		}
		if e = root.MkdirAll(filepath.Dir(name), 0700); e != nil {
			return e
		}
		file, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		n, err := io.CopyN(file, tr, h.Size)
		if err == nil {
			err = file.Sync()
		}
		closeErr := file.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		total += n
		count++
	}
}

type BackupRetentionPlan struct {
	KeepLast           int                     `json:"keep_last"`
	MinAgeHours        int                     `json:"min_age_hours"`
	AuthenticatedFiles int                     `json:"authenticated_files"`
	Candidates         []EncryptedBackupReport `json:"candidates"`
	PlanSHA256         string                  `json:"plan_sha256"`
	Deleted            int                     `json:"deleted"`
}

// RetainBackups never deletes directories, plaintext, symlinks, foreign names or
// archives that cannot authenticate with the supplied key. All matching files
// must verify BEFORE any deletion. Apply requires the exact reviewed plan hash.
func RetainBackups(ctx context.Context, root string, key []byte, keepLast, minAgeHours int, expectedPlan string) (BackupRetentionPlan, error) {
	p := BackupRetentionPlan{KeepLast: keepLast, MinAgeHours: minAgeHours, Candidates: []EncryptedBackupReport{}}
	if len(key) != 32 || keepLast < 1 || keepLast > maxEncryptedBackupFiles || minAgeHours < 24 || minAgeHours > 876000 {
		return p, ErrEncryptedBackup
	}
	lock, e := lockEncryptedBackups(root)
	if e != nil {
		return p, e
	}
	defer unlockEncryptedBackups(lock)
	directory, e := os.Open(root)
	if e != nil {
		return p, ErrEncryptedBackup
	}
	entries, e := directory.ReadDir(maxEncryptedBackupFiles + 101)
	directory.Close()
	if (e != nil && e != io.EOF) || len(entries) > maxEncryptedBackupFiles+100 {
		return p, ErrEncryptedBackup
	}
	files := []EncryptedBackupReport{}
	now := time.Now().UTC()
	for _, entry := range entries {
		if !encryptedBackupName.MatchString(entry.Name()) {
			continue
		}
		if !entry.Type().IsRegular() {
			return p, ErrEncryptedBackup
		}
		report, err := encryptedBackupInfo(ctx, filepath.Join(root, entry.Name()), key)
		if err != nil || report.CreatedAt.After(now.Add(5*time.Minute)) {
			return p, ErrEncryptedBackup
		}
		files = append(files, report)
	}
	sort.Slice(files, func(i, j int) bool {
		if files[i].CreatedAt.Equal(files[j].CreatedAt) {
			return files[i].File < files[j].File
		}
		return files[i].CreatedAt.After(files[j].CreatedAt)
	})
	p.AuthenticatedFiles = len(files)
	for i, report := range files {
		if i >= keepLast && report.CreatedAt.Before(now.Add(-time.Duration(minAgeHours)*time.Hour)) {
			p.Candidates = append(p.Candidates, report)
		}
	}
	raw, _ := json.Marshal(p)
	sum := sha256.Sum256(raw)
	p.PlanSHA256 = hex.EncodeToString(sum[:])
	if expectedPlan == "" {
		return p, nil
	}
	if expectedPlan != p.PlanSHA256 {
		return p, ErrEncryptedBackup
	}
	for _, report := range p.Candidates {
		if e = ctx.Err(); e != nil {
			return p, e
		}
		if e = os.Remove(filepath.Join(root, report.File)); e != nil {
			return p, ErrEncryptedBackup
		}
		p.Deleted++
	}
	if e = syncMigrationDirectory(root); e != nil {
		return p, ErrEncryptedBackup
	}
	return p, nil
}
