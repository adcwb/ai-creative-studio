//go:build !windows

package nativefs

import (
	"os"
	"syscall"
)

var ErrWouldBlock = syscall.EWOULDBLOCK

func OpenFile(p string, flags int, mode os.FileMode) (*os.File, error) {
	return os.OpenFile(p, unixFlags(flags), mode)
}
func unixFlags(f int) int {
	v := f &^ (NoFollow | NonBlock)
	if f&NoFollow != 0 {
		v |= syscall.O_NOFOLLOW
	}
	if f&NonBlock != 0 {
		v |= syscall.O_NONBLOCK
	}
	return v
}
func OpenRootFile(r *os.Root, p string, f int, m os.FileMode) (*os.File, error) {
	return r.OpenFile(p, unixFlags(f), m)
}
func Flock(fd int, op int) error {
	mode := 0
	if op&LOCK_SH != 0 {
		mode |= syscall.LOCK_SH
	}
	if op&LOCK_EX != 0 {
		mode |= syscall.LOCK_EX
	}
	if op&LOCK_NB != 0 {
		mode |= syscall.LOCK_NB
	}
	if op&LOCK_UN != 0 {
		mode |= syscall.LOCK_UN
	}
	return syscall.Flock(fd, mode)
}
func SyncFile(f *os.File) error { return f.Sync() }
func Rename(a, b string) error  { return os.Rename(a, b) }
func Private(p string) bool {
	f, e := os.Lstat(p)
	return e == nil && f.Mode()&os.ModeSymlink == 0 && f.Mode().Perm()&0077 == 0
}
func PrivateDirectoryMode(p string) bool {
	f, e := os.Lstat(p)
	return e == nil && f.IsDir() && f.Mode()&os.ModeSymlink == 0 && f.Mode().Perm()&0022 == 0
}
func EnsurePrivateDirectory(p string) error { return os.MkdirAll(p, 0700) }

// Existing Unix callers retain their original policy. Windows additionally needs
// real DACL checks because chmod permission bits do not enforce confidentiality.
func CheckPrivateFile(p string) error { return nil }
func IsExecutable(p string) bool {
	f, e := os.Stat(p)
	return e == nil && f.Mode().IsRegular() && f.Mode().Perm()&0111 != 0
}
