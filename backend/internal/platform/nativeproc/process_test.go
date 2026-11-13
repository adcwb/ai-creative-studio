package nativeproc

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestProcessTreeCancellation(t *testing.T) {
	if mode := os.Getenv("STUDIO_NATIVE_CHILD"); mode != "" {
		if mode == "parent" {
			c := exec.Command(os.Args[0], "-test.run=^TestProcessTreeCancellation$")
			c.Env = append(os.Environ(), "STUDIO_NATIVE_CHILD=heartbeat")
			if e := c.Start(); e != nil {
				os.Exit(3)
			}
			for {
				time.Sleep(time.Second)
			}
		}
		for {
			os.WriteFile(os.Getenv("STUDIO_NATIVE_HEARTBEAT"), []byte(time.Now().String()), 0600)
			time.Sleep(25 * time.Millisecond)
		}
	}
	path := filepath.Join(t.TempDir(), "heartbeat")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestProcessTreeCancellation$")
	c.Env = append(os.Environ(), "STUDIO_NATIVE_CHILD=parent", "STUDIO_NATIVE_HEARTBEAT="+path)
	result := make(chan error, 1)
	go func() { result <- Run(c) }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, e := os.Stat(path); e == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("child never executed")
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	select {
	case <-result:
	case <-time.After(5 * time.Second):
		t.Fatal("cancel blocked")
	}
	time.Sleep(100 * time.Millisecond)
	a, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	time.Sleep(150 * time.Millisecond)
	b, _ := os.ReadFile(path)
	if string(a) != string(b) {
		t.Fatal("descendant survived cancellation")
	}
}
func TestPrivateEnvironmentExcludesInheritedCredentials(t *testing.T) {
	t.Setenv("STUDIO_FAKE_SECRET", "do-not-inherit")
	for _, v := range PrivateEnvironment(os.Args[0], t.TempDir()) {
		if strings.Contains(v, "STUDIO_FAKE_SECRET") || strings.Contains(v, "do-not-inherit") {
			t.Fatal("inherited secret")
		}
	}
}
