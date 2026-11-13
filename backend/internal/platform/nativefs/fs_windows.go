//go:build windows

package nativefs

import (
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

var ErrWouldBlock = windows.ERROR_LOCK_VIOLATION

func nativePath(p string) (string, error) {
	p, e := filepath.Abs(p)
	if e != nil {
		return "", e
	}
	if strings.HasPrefix(p, `\\?\`) {
		return p, nil
	}
	if strings.HasPrefix(p, `\\`) {
		return `\\?\UNC\` + p[2:], nil
	}
	return `\\?\` + p, nil
}
func descriptor(dir bool) (*windows.SECURITY_DESCRIPTOR, error) {
	u, e := windows.GetCurrentProcessToken().GetTokenUser()
	if e != nil {
		return nil, e
	}
	sid := u.User.Sid.String()
	inherit := ""
	if dir {
		inherit = "OICI"
	}
	return windows.SecurityDescriptorFromString("O:" + sid + "D:P(A;" + inherit + ";FA;;;" + sid + ")(A;" + inherit + ";FA;;;SY)(A;" + inherit + ";FA;;;BA)")
}
func privatePrincipals(p string, depth int) (map[string]bool, error) {
	if depth > 256 {
		return nil, fs.ErrPermission
	}
	n, e := nativePath(p)
	if e != nil {
		return nil, e
	}
	attr, e := windows.GetFileAttributes(windows.StringToUTF16Ptr(n))
	if e != nil {
		return nil, e
	}
	if attr&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return nil, fs.ErrPermission
	}
	sd, e := windows.GetNamedSecurityInfo(n, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if e != nil {
		return nil, e
	}
	owner, _, e := sd.Owner()
	if e != nil || owner == nil {
		return nil, fs.ErrPermission
	}
	own := owner.String()
	if own == "S-1-1-0" || own == "S-1-5-11" || own == "S-1-5-32-545" {
		return nil, fs.ErrPermission
	}
	user, e := windows.GetCurrentProcessToken().GetTokenUser()
	if e != nil {
		return nil, e
	}
	allowed := map[string]bool{own: true, user.User.Sid.String(): true, "S-1-5-18": true, "S-1-5-32-544": true}
	acl, _, e := sd.DACL()
	if e != nil || acl == nil || acl.AceCount == 0 {
		return nil, fs.ErrPermission
	}
	found := map[string]bool{}
	var parent map[string]bool
	for i := uint32(0); i < uint32(acl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if e = windows.GetAce(acl, i, &ace); e != nil {
			return nil, e
		}
		if ace.Header.AceType == windows.ACCESS_DENIED_ACE_TYPE {
			continue
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			return nil, fs.ErrPermission
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart)).String()
		if !allowed[sid] {
			if ace.Header.AceFlags&windows.INHERITED_ACE == 0 {
				return nil, fs.ErrPermission
			}
			if parent == nil {
				up := filepath.Dir(p)
				if up == p {
					return nil, fs.ErrPermission
				}
				parent, e = privatePrincipals(up, depth+1)
				if e != nil {
					return nil, e
				}
			}
			if !parent[sid] {
				return nil, fs.ErrPermission
			}
		}
		found[sid] = true
	}
	if len(found) == 0 {
		return nil, fs.ErrPermission
	}
	return found, nil
}
func Private(p string) bool { _, e := privatePrincipals(p, 0); return e == nil }
func PrivateDirectoryMode(p string) bool {
	f, e := os.Lstat(p)
	return e == nil && f.IsDir() && Private(p)
}
func CheckPrivateFile(p string) error {
	_, e := os.Lstat(p)
	if os.IsNotExist(e) {
		return nil
	}
	if e != nil {
		return e
	}
	if !Private(p) {
		return fs.ErrPermission
	}
	return nil
}
func EnsurePrivateDirectory(p string) error {
	n, e := nativePath(p)
	if e != nil {
		return e
	}
	sd, e := descriptor(true)
	if e != nil {
		return e
	}
	sa := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}
	e = windows.CreateDirectory(windows.StringToUTF16Ptr(n), &sa)
	if e == nil {
		return nil
	}
	if errors.Is(e, windows.ERROR_PATH_NOT_FOUND) {
		parent := filepath.Dir(p)
		if parent == p {
			return e
		}
		if e = EnsurePrivateDirectory(parent); e != nil {
			return e
		}
		return EnsurePrivateDirectory(p)
	}
	if !errors.Is(e, windows.ERROR_ALREADY_EXISTS) {
		return e
	}
	st, e := os.Lstat(p)
	if e != nil || !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return fs.ErrPermission
	}
	if Private(p) {
		return nil
	}
	entries, e := os.ReadDir(p)
	if e != nil {
		return e
	}
	if len(entries) != 0 {
		if Private(p) {
			return nil
		}
		return fs.ErrPermission
	}
	owner, _, e := sd.Owner()
	if e != nil {
		return e
	}
	dacl, _, e := sd.DACL()
	if e != nil {
		return e
	}
	e = windows.SetNamedSecurityInfo(n, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, owner, nil, dacl, nil)
	if e != nil {
		return e
	}
	if !Private(p) {
		return fs.ErrPermission
	}
	return nil
}
func OpenFile(p string, flags int, mode os.FileMode) (*os.File, error) {
	if flags&NoFollow == 0 {
		return os.OpenFile(p, flags&^NonBlock, mode)
	}
	n, e := nativePath(p)
	if e != nil {
		return nil, e
	}
	access := uint32(windows.GENERIC_READ)
	if flags&os.O_RDWR != 0 {
		access |= windows.GENERIC_WRITE
	} else if flags&os.O_WRONLY != 0 {
		access = windows.GENERIC_WRITE
	}
	if flags&os.O_APPEND != 0 {
		access &^= windows.GENERIC_WRITE
		access |= windows.FILE_APPEND_DATA
	}
	creation := uint32(windows.OPEN_EXISTING)
	if flags&os.O_CREATE != 0 {
		creation = windows.OPEN_ALWAYS
		if flags&os.O_EXCL != 0 {
			creation = windows.CREATE_NEW
		}
	}
	attr := uint32(windows.FILE_ATTRIBUTE_NORMAL | windows.FILE_FLAG_OPEN_REPARSE_POINT)
	if flags&os.O_SYNC != 0 {
		attr |= windows.FILE_FLAG_WRITE_THROUGH
	}
	var sa *windows.SecurityAttributes
	var security windows.SecurityAttributes
	if flags&os.O_CREATE != 0 {
		sd, e := descriptor(false)
		if e != nil {
			return nil, e
		}
		security = windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(security)), SecurityDescriptor: sd}
		sa = &security
	}
	h, e := windows.CreateFile(windows.StringToUTF16Ptr(n), access, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, sa, creation, attr, 0)
	if e != nil {
		return nil, &os.PathError{Op: "open", Path: p, Err: e}
	}
	var info windows.ByHandleFileInformation
	if e = windows.GetFileInformationByHandle(h, &info); e != nil || info.FileAttributes&(windows.FILE_ATTRIBUTE_REPARSE_POINT|windows.FILE_ATTRIBUTE_DIRECTORY) != 0 {
		windows.CloseHandle(h)
		if e == nil {
			e = fs.ErrPermission
		}
		return nil, e
	}
	f := os.NewFile(uintptr(h), p)
	if flags&os.O_TRUNC != 0 {
		if e = f.Truncate(0); e != nil {
			f.Close()
			return nil, e
		}
	}
	return f, nil
}
func OpenRootFile(r *os.Root, p string, flags int, mode os.FileMode) (*os.File, error) {
	if flags&NoFollow == 0 {
		return r.OpenFile(p, flags&^NonBlock, mode)
	}
	if flags&(os.O_WRONLY|os.O_RDWR|os.O_CREATE|os.O_TRUNC|os.O_APPEND) != 0 {
		return nil, errors.ErrUnsupported
	}
	before, e := r.Lstat(p)
	if e != nil {
		return nil, e
	}
	if !before.Mode().IsRegular() {
		return nil, fs.ErrPermission
	}
	f, e := r.OpenFile(p, flags&^(NoFollow|NonBlock), mode)
	if e != nil {
		return nil, e
	}
	opened, e := f.Stat()
	after, ae := r.Lstat(p)
	if e != nil || ae != nil || !after.Mode().IsRegular() || !os.SameFile(before, opened) || !os.SameFile(opened, after) {
		f.Close()
		return nil, fs.ErrPermission
	}
	return f, nil
}
func Flock(fd int, op int) error {
	over := windows.Overlapped{}
	if op&LOCK_UN != 0 {
		return windows.UnlockFileEx(windows.Handle(fd), 0, 1, 0, &over)
	}
	flags := uint32(0)
	if op&LOCK_EX != 0 {
		flags |= windows.LOCKFILE_EXCLUSIVE_LOCK
	}
	if op&LOCK_NB != 0 {
		flags |= windows.LOCKFILE_FAIL_IMMEDIATELY
	}
	return windows.LockFileEx(windows.Handle(fd), flags, 0, 1, 0, &over)
}

var directoryWarning sync.Once

func SyncFile(f *os.File) error {
	st, e := f.Stat()
	if e != nil {
		return e
	}
	if st.IsDir() {
		directoryWarning.Do(func() {
			slog.Warn("Windows directory fsync has no POSIX equivalent", "event", "filesystem_durability", "posix_directory_fsync", false)
		})
		return nil
	}
	e = f.Sync()
	if !errors.Is(e, windows.ERROR_ACCESS_DENIED) {
		return e
	}
	w, e := OpenFile(f.Name(), os.O_RDWR|NoFollow, 0)
	if e != nil {
		return e
	}
	defer w.Close()
	current, e := w.Stat()
	if e != nil {
		return e
	}
	if !os.SameFile(st, current) {
		return fs.ErrPermission
	}
	return w.Sync()
}
func move(a, b string, flags uint32) error {
	a, e := nativePath(a)
	if e != nil {
		return e
	}
	b, e = nativePath(b)
	if e != nil {
		return e
	}
	return windows.MoveFileEx(windows.StringToUTF16Ptr(a), windows.StringToUTF16Ptr(b), flags|windows.MOVEFILE_WRITE_THROUGH)
}
func Rename(a, b string) error          { return move(a, b, windows.MOVEFILE_REPLACE_EXISTING) }
func RenameNoReplace(a, b string) error { return move(a, b, 0) }
func IsExecutable(p string) bool {
	f, e := os.Stat(p)
	return e == nil && f.Mode().IsRegular() && strings.EqualFold(filepath.Ext(p), ".exe")
}
