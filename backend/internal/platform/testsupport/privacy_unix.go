//go:build !windows

package testsupport

import (
	"os"
	"testing"
)

func MakePublic(t *testing.T, p string) {
	t.Helper()
	if e := os.Chmod(p, 0644); e != nil {
		t.Fatal(e)
	}
}
