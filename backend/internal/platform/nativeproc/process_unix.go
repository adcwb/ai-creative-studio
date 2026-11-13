//go:build !windows

package nativeproc

import (
	"os"
	"os/exec"
	"syscall"
)

func Start(c *exec.Cmd) (func(), error) {
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if c.Cancel != nil {
		c.Cancel = func() error {
			if c.Process == nil {
				return os.ErrProcessDone
			}
			return syscall.Kill(-c.Process.Pid, syscall.SIGKILL)
		}
	}
	if e := c.Start(); e != nil {
		return nil, e
	}
	return func() { _ = syscall.Kill(-c.Process.Pid, syscall.SIGKILL) }, nil
}
func PrivateEnvironment(command, dir string) []string {
	return []string{"PATH=/usr/local/bin:/usr/bin:/bin", "LANG=C.UTF-8", "HOME=" + dir}
}
