//go:build windows

package application

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/adcwb/ai-creative-studio/backend/internal/platform/nativefs"
	"github.com/adcwb/ai-creative-studio/backend/internal/platform/testsupport"
)

func TestNativeCredentialsRejectPublicFileACL(t *testing.T) {
	dir := t.TempDir()
	key := bytes.Repeat([]byte{7}, 32)
	s, e := OpenModelConfigStore(dir, key)
	if e != nil {
		t.Fatal(e)
	}
	seedRotationModels(t, s)
	path := filepath.Join(dir, "model-config.json")
	if !nativefs.Private(path) {
		t.Fatal("initial file not private")
	}
	before, _ := os.ReadFile(path)
	testsupport.MakePublic(t, path)
	if _, e = OpenModelConfigStore(dir, key); e == nil {
		t.Fatal("public ciphertext file accepted")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("ciphertext changed")
	}
}
