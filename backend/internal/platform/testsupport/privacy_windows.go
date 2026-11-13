//go:build windows

package testsupport

import (
	"github.com/adcwb/ai-creative-studio/backend/internal/platform/nativefs"
	"testing"

	"golang.org/x/sys/windows"
)

func MakePublic(t *testing.T, p string) {
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

func MakePrivateDirectory(t *testing.T, p string) {
	t.Helper()
	if e := nativefs.EnsurePrivateDirectory(p); e != nil {
		t.Fatal(e)
	}
}
