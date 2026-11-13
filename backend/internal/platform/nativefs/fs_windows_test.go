//go:build windows

package nativefs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func publicACL(t *testing.T, p string) {
	t.Helper()
	u, e := windows.GetCurrentProcessToken().GetTokenUser()
	if e != nil {
		t.Fatal(e)
	}
	sd, e := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;" + u.User.Sid.String() + ")(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;OICI;GR;;;WD)")
	if e != nil {
		t.Fatal(e)
	}
	d, _, _ := sd.DACL()
	if e = windows.SetNamedSecurityInfo(p, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, d, nil); e != nil {
		t.Fatal(e)
	}
}
func TestPrivateACLRejectsPublicNonemptyWithoutMutation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secret")
	os.WriteFile(path, []byte("preserve"), 0600)
	publicACL(t, dir)
	if Private(dir) {
		t.Fatal("public ACL accepted")
	}
	if e := EnsurePrivateDirectory(dir); e == nil {
		t.Fatal("nonempty public dir silently adopted")
	}
	b, _ := os.ReadFile(path)
	if string(b) != "preserve" {
		t.Fatal("data changed")
	}
}
func TestNativeLongPath(t *testing.T) {
	dir := t.TempDir()
	if e := EnsurePrivateDirectory(dir); e != nil {
		t.Fatal(e)
	}
	dir = filepath.Join(dir, strings.Repeat("a", 110), strings.Repeat("b", 110), "中文")
	if e := EnsurePrivateDirectory(dir); e != nil {
		t.Fatal(e)
	}
	p := filepath.Join(dir, "long")
	f, e := OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY|NoFollow, 0600)
	if e != nil {
		t.Fatal(e)
	}
	f.WriteString("complete")
	if e = SyncFile(f); e != nil {
		t.Fatal(e)
	}
	f.Close()
	if !Private(p) {
		t.Fatal("private created file not accepted")
	}
}

func TestStaleInheritedPublicACEIsNotTrusted(t *testing.T) {
	dir := t.TempDir()
	if e := EnsurePrivateDirectory(dir); e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(dir, "secret")
	os.WriteFile(path, []byte("preserve"), 0600)
	u, e := windows.GetCurrentProcessToken().GetTokenUser()
	if e != nil {
		t.Fatal(e)
	}
	sd, e := windows.SecurityDescriptorFromString("D:P(A;;FA;;;" + u.User.Sid.String() + ")(A;ID;GR;;;WD)")
	if e != nil {
		t.Fatal(e)
	}
	acl, _, _ := sd.DACL()
	if e = windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); e != nil {
		t.Fatal(e)
	}
	if Private(path) {
		t.Fatal("inherited flag trusted without parent authorization")
	}
	if !Private(dir) {
		t.Fatal("fixture parent is not private")
	}
}
