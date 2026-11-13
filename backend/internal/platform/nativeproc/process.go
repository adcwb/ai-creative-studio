package nativeproc

import (
	"bytes"
	"os/exec"
)

func Run(c *exec.Cmd) error {
	done, e := Start(c)
	if e != nil {
		return e
	}
	defer done()
	return c.Wait()
}
func Output(c *exec.Cmd) ([]byte, error) {
	var b bytes.Buffer
	c.Stdout = &b
	e := Run(c)
	return b.Bytes(), e
}
