// Package nativefs preserves private storage, kernel locks and file identity on
// Linux and Windows. Platform differences are explicit rather than ignored errors.
package nativefs

import "os"

const (
	NoFollow = 1 << 29
	NonBlock = 1 << 28
	LOCK_SH  = 1
	LOCK_EX  = 2
	LOCK_NB  = 4
	LOCK_UN  = 8
)

func SyncDirectory(path string) error {
	f, e := os.Open(path)
	if e != nil {
		return e
	}
	defer f.Close()
	return SyncFile(f)
}
func MkdirTemp(dir, pattern string) (string, error) {
	p, e := os.MkdirTemp(dir, pattern)
	if e == nil {
		e = EnsurePrivateDirectory(p)
		if e != nil {
			os.Remove(p)
		}
	}
	return p, e
}
