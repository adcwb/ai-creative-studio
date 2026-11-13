package nativefs

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestKernelLockAcrossProcesses(t *testing.T) {
	if path := os.Getenv("STUDIO_NATIVE_LOCK_HELPER"); path != "" {
		f, e := OpenFile(path, os.O_RDWR|NoFollow, 0600)
		if e != nil {
			t.Fatal(e)
		}
		defer f.Close()
		if !errors.Is(Flock(int(f.Fd()), LOCK_EX|LOCK_NB), ErrWouldBlock) {
			t.Fatal("child entered held lock")
		}
		return
	}
	dir := filepath.Join(t.TempDir(), "原生 path's")
	if e := EnsurePrivateDirectory(dir); e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(dir, "lock")
	f, e := OpenFile(path, os.O_CREATE|os.O_RDWR|NoFollow, 0600)
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	if e = Flock(int(f.Fd()), LOCK_EX|LOCK_NB); e != nil {
		t.Fatal(e)
	}
	c := exec.Command(os.Args[0], "-test.run=^TestKernelLockAcrossProcesses$")
	c.Env = append(os.Environ(), "STUDIO_NATIVE_LOCK_HELPER="+path)
	if b, e := c.CombinedOutput(); e != nil {
		t.Fatalf("child %v %s", e, b)
	}
	if e = Flock(int(f.Fd()), LOCK_UN); e != nil {
		t.Fatal(e)
	}
	other, e := OpenFile(path, os.O_RDWR|NoFollow, 0600)
	if e != nil {
		t.Fatal(e)
	}
	defer other.Close()
	if e = Flock(int(other.Fd()), LOCK_EX|LOCK_NB); e != nil {
		t.Fatal(e)
	}
}
func TestReplacementAndNoFollow(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "文件's")
	if e := EnsurePrivateDirectory(dir); e != nil {
		t.Fatal(e)
	}
	a, b := filepath.Join(dir, "a"), filepath.Join(dir, "b")
	if e := os.WriteFile(a, []byte("new"), 0600); e != nil {
		t.Fatal(e)
	}
	os.WriteFile(b, []byte("old"), 0600)
	f, e := os.Open(a)
	if e != nil {
		t.Fatal(e)
	}
	if e = SyncFile(f); e != nil {
		t.Fatal(e)
	}
	f.Close()
	if e = Rename(a, b); e != nil {
		t.Fatal(e)
	}
	data, _ := os.ReadFile(b)
	if string(data) != "new" {
		t.Fatal("replace lost data")
	}
	link := filepath.Join(dir, "link")
	if e = os.Symlink(b, link); e != nil {
		t.Fatal(e)
	}
	if f, e = OpenFile(link, os.O_RDONLY|NoFollow, 0); e == nil {
		f.Close()
		t.Fatal("followed symlink")
	}
	for _, p := range []string{a, filepath.Join(dir, "c")} {
		os.Mkdir(p, 0700)
	}
	if e = RenameNoReplace(a, filepath.Join(dir, "c")); e == nil {
		t.Fatal("replaced directory")
	}
}
