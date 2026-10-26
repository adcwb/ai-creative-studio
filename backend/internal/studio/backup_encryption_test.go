package studio

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/adcwb/ai-creative-studio/backend/internal/securebackup"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func syntheticBackupBundle(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	content := []byte("synthetic database dump, not executable")
	if e := os.WriteFile(filepath.Join(dir, "database.dump"), content, 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.Mkdir(filepath.Join(dir, "objects"), 0700); e != nil {
		t.Fatal(e)
	}
	sum := sha256.Sum256(content)
	m := PostgresBackupManifest{Version: 1, CreatedAt: time.Now().UTC(), Database: PostgresBackupFile{Size: int64(len(content)), SHA256: hex.EncodeToString(sum[:])}, Objects: map[string]BlobInfo{}, QueueStates: map[string]int64{"unknown": 1}, CostStates: map[string]int64{"unknown": 1}}
	data, _ := json.Marshal(m)
	if e := os.WriteFile(filepath.Join(dir, "backup.json"), data, 0600); e != nil {
		t.Fatal(e)
	}
	return dir
}
func TestEncryptedBackupRoundTripAndWrongKey(t *testing.T) {
	ctx := context.Background()
	key := bytes.Repeat([]byte{3}, 32)
	source := syntheticBackupBundle(t)
	root := t.TempDir()
	os.Chmod(root, 0700)
	r, e := SealBackup(ctx, source, root, key)
	if e != nil || !r.Verified {
		t.Fatal(r, e)
	}
	path := filepath.Join(root, r.File)
	target := filepath.Join(t.TempDir(), "restored")
	if _, e = OpenBackup(ctx, path, target, bytes.Repeat([]byte{4}, 32)); e == nil {
		t.Fatal("wrong key")
	}
	if _, e = os.Stat(target); !os.IsNotExist(e) {
		t.Fatal("failure published target")
	}
	if _, e = OpenBackup(ctx, path, target, key); e != nil {
		t.Fatal(e)
	}
	if _, _, e = VerifyPostgresBackup(ctx, target); e != nil {
		t.Fatal(e)
	}
	if _, e = OpenBackup(ctx, path, target, key); e == nil {
		t.Fatal("overwrote target")
	}
	if _, _, e = VerifyPostgresBackup(ctx, source); e != nil {
		t.Fatal("source removed")
	}
	st, e := os.Stat(path)
	if e != nil || st.Mode().Perm() != 0600 {
		t.Fatal("permissions", e)
	}
}
func writeEncryptedFixture(t *testing.T, root, name string, key []byte, at time.Time, body []byte) {
	t.Helper()
	f, e := os.OpenFile(filepath.Join(root, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		t.Fatal(e)
	}
	_, e = securebackup.Encrypt(context.Background(), f, bytes.NewReader(body), key, at)
	f.Close()
	if e != nil {
		t.Fatal(e)
	}
}
func TestEncryptedRetentionDryRunHashAndForeignSafety(t *testing.T) {
	root := t.TempDir()
	os.Chmod(root, 0700)
	key := bytes.Repeat([]byte{6}, 32)
	old := time.Now().Add(-72 * time.Hour)
	names := []string{"studio-backup-" + strings.Repeat("1", 32) + ".sbk", "studio-backup-" + strings.Repeat("2", 32) + ".sbk"}
	for i, n := range names {
		writeEncryptedFixture(t, root, n, key, old.Add(time.Duration(i)*time.Hour), []byte("synthetic archive"))
	}
	os.WriteFile(filepath.Join(root, "foreign.txt"), []byte("keep"), 0600)
	p, e := RetainBackups(context.Background(), root, key, 1, 24, "")
	if e != nil || len(p.Candidates) != 1 || p.Deleted != 0 {
		t.Fatal(p, e)
	}
	if _, e = RetainBackups(context.Background(), root, key, 1, 24, "wrong"); e == nil {
		t.Fatal("unreviewed delete")
	}
	if _, e = RetainBackups(context.Background(), root, bytes.Repeat([]byte{7}, 32), 1, 24, p.PlanSHA256); e == nil {
		t.Fatal("wrong key")
	}
	if _, e = os.Stat(filepath.Join(root, names[0])); e != nil {
		t.Fatal("dry-run deleted")
	}
	applied, e := RetainBackups(context.Background(), root, key, 1, 24, p.PlanSHA256)
	if e != nil || applied.Deleted != 1 {
		t.Fatal(applied, e)
	}
	for _, n := range []string{names[1], "foreign.txt"} {
		if _, e = os.Stat(filepath.Join(root, n)); e != nil {
			t.Fatal("protected file removed")
		}
	}
}
func TestEncryptedBackupRejectsPathsAndRetentionSymlink(t *testing.T) {
	key := bytes.Repeat([]byte{5}, 32)
	root := t.TempDir()
	os.Chmod(root, 0700)
	name := "studio-backup-" + strings.Repeat("a", 32) + ".sbk"
	os.Symlink(filepath.Join(t.TempDir(), "other"), filepath.Join(root, name))
	if _, e := RetainBackups(context.Background(), root, key, 1, 24, ""); e == nil {
		t.Fatal("symlink accepted")
	}
	for _, unsafe := range []string{"../escape", "/absolute", "a/../../escape"} {
		var b bytes.Buffer
		w := tar.NewWriter(&b)
		w.WriteHeader(&tar.Header{Name: unsafe, Mode: 0600, Size: 1})
		w.Write([]byte("x"))
		w.Close()
		if e := readBackupTar(context.Background(), &b, t.TempDir()); e == nil {
			t.Fatal("unsafe path", unsafe)
		}
	}
	var b bytes.Buffer
	w := tar.NewWriter(&b)
	w.WriteHeader(&tar.Header{Name: "link", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"})
	w.Close()
	if e := readBackupTar(context.Background(), &b, t.TempDir()); e == nil {
		t.Fatal("tar symlink")
	}
	path := filepath.Join(t.TempDir(), "bad.sbk")
	f, _ := os.Create(path)
	securebackup.Encrypt(context.Background(), f, bytes.NewReader([]byte("not a tar")), key, time.Now())
	f.Close()
	dest := filepath.Join(t.TempDir(), "new")
	if _, e := OpenBackup(context.Background(), path, dest, key); e == nil {
		t.Fatal("invalid bundle")
	}
	if _, e := os.Stat(dest); !os.IsNotExist(e) {
		t.Fatal("published invalid bundle")
	}
}
func TestEncryptedBackupStreamFailureDoesNotPublish(t *testing.T) {
	root := t.TempDir()
	os.Chmod(root, 0700)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := SealBackup(ctx, syntheticBackupBundle(t), root, bytes.Repeat([]byte{1}, 32)); e == nil {
		t.Fatal("canceled seal")
	}
	entries, _ := os.ReadDir(root)
	for _, e := range entries {
		if e.Name() != ".studio-backup.lock" {
			t.Fatal("partial output", e.Name())
		}
	}
}

func TestEncryptedBackupChecksBytesAgainstInitialManifest(t *testing.T) {
	source := syntheticBackupBundle(t)
	ctx := context.Background()
	manifest, report, e := VerifyPostgresBackup(ctx, source)
	if e != nil {
		t.Fatal(e)
	}
	inventory, e := backupTarInventory(source, manifest, report)
	if e != nil {
		t.Fatal(e)
	}
	original, e := os.ReadFile(filepath.Join(source, "database.dump"))
	if e != nil {
		t.Fatal(e)
	}
	original[0] ^= 1
	if e = os.WriteFile(filepath.Join(source, "database.dump"), original, 0600); e != nil {
		t.Fatal(e)
	}
	var archive bytes.Buffer
	if e = writeBackupTar(ctx, &archive, source, inventory); e == nil {
		t.Fatal("source mutation after verify accepted")
	}
}

func TestEncryptedBackupRefusesFIFOWithoutBlocking(t *testing.T) {
	fifo := filepath.Join(t.TempDir(), "fifo")
	if e := syscall.Mkfifo(fifo, 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := OpenBackup(context.Background(), fifo, filepath.Join(t.TempDir(), "target"), bytes.Repeat([]byte{1}, 32)); e == nil {
		t.Fatal("fifo accepted")
	}
}
