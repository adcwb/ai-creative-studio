package application

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNativeStoreRejectsSymlinkLockWithoutChangingTarget(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "target")
	os.WriteFile(outside, []byte("preserve"), 0600)
	if e := os.Symlink(outside, filepath.Join(dir, "store.lock")); e != nil {
		t.Fatal(e)
	}
	s, e := OpenStore(dir)
	if e == nil {
		s.Close()
		t.Fatal("symlink lock accepted")
	}
	b, _ := os.ReadFile(outside)
	if string(b) != "preserve" {
		t.Fatal("target changed")
	}
}
