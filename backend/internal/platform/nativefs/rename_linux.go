//go:build linux

package nativefs

import "golang.org/x/sys/unix"

func RenameNoReplace(a, b string) error {
	return unix.Renameat2(unix.AT_FDCWD, a, unix.AT_FDCWD, b, unix.RENAME_NOREPLACE)
}
