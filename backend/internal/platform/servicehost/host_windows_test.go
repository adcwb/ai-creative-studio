//go:build windows

package servicehost

import (
	"context"
	"testing"
	"time"

	"golang.org/x/sys/windows/svc"
)

func TestSCMStopCancelsSameRuntime(t *testing.T) {
	h := host{run: func(ctx context.Context) error { <-ctx.Done(); return nil }}
	commands := make(chan svc.ChangeRequest)
	status := make(chan svc.Status, 8)
	done := make(chan uint32, 1)
	go func() { _, code := h.Execute(nil, commands, status); done <- code }()
	<-status
	<-status
	commands <- svc.ChangeRequest{Cmd: svc.Stop}
	select {
	case code := <-done:
		if code != 0 {
			t.Fatal(code)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("SCM stop did not cancel runtime")
	}
}
