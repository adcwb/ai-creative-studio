//go:build windows

package testsupport

import (
	"os/exec"
	"testing"
)

func FIFO(t *testing.T, path string) error {
	t.Helper()
	t.Skip("POSIX FIFO fixture; NTFS reparse/ACL protection tested separately")
	return nil
}
func ProcessGroup(c *exec.Cmd)    {}
func KillGroup(c *exec.Cmd) error { return c.Process.Kill() }
