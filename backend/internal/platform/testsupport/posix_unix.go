//go:build !windows

package testsupport

import (
	"os/exec"
	"syscall"
	"testing"
)

func FIFO(t *testing.T, path string) error { t.Helper(); return syscall.Mkfifo(path, 0600) }
func ProcessGroup(c *exec.Cmd)             { c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }
func KillGroup(c *exec.Cmd) error          { return syscall.Kill(-c.Process.Pid, syscall.SIGKILL) }
