package deployconfig

import (
	"bytes"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestModelKeyPersistsAndHonorsExplicitKey(t *testing.T) {
	t.Setenv("MODEL_CONFIG_KEK_BASE64", "")
	t.Setenv("MODEL_CONFIG_KEK_FILE", "")
	t.Setenv("STORAGE_BACKEND", "file")
	dir := t.TempDir()
	first, err := ModelKey(dir)
	if err != nil || len(first) != 32 {
		t.Fatal("initialization failed", err)
	}
	info, err := os.Stat(filepath.Join(dir, "model-config.key"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("key file is not private", err)
	}
	second, err := ModelKey(dir)
	if err != nil || !bytes.Equal(first, second) {
		t.Fatal("key changed after restart", err)
	}
	explicit := bytes.Repeat([]byte{5}, 32)
	t.Setenv("MODEL_CONFIG_KEK_BASE64", base64.StdEncoding.EncodeToString(explicit))
	key, err := ModelKey(dir)
	if err != nil || !bytes.Equal(key, explicit) {
		t.Fatal("explicit key ignored", err)
	}
	t.Setenv("MODEL_CONFIG_KEK_BASE64", "invalid")
	if _, err = ModelKey(dir); !errors.Is(err, ErrKey) {
		t.Fatal("invalid key accepted", err)
	}
}
func TestModelKeyNeverReplacesMissingOrCorruptKey(t *testing.T) {
	t.Setenv("MODEL_CONFIG_KEK_BASE64", "")
	t.Setenv("MODEL_CONFIG_KEK_FILE", "")
	t.Setenv("STORAGE_BACKEND", "file")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "model-config.json"), []byte(`{"ciphertext":"old encrypted credentials"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ModelKey(dir); !errors.Is(err, ErrKey) {
		t.Fatal("lost key silently replaced", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "model-config.key"), []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ModelKey(dir); !errors.Is(err, ErrKey) {
		t.Fatal("corrupt key accepted", err)
	}
}
func TestPostgresRequiresSharedKey(t *testing.T) {
	t.Setenv("MODEL_CONFIG_KEK_BASE64", "")
	t.Setenv("MODEL_CONFIG_KEK_FILE", "")
	t.Setenv("STORAGE_BACKEND", "postgres")
	dir := t.TempDir()
	key, err := ModelKey(dir)
	if err != nil || key != nil {
		t.Fatal("private postgres key created", err)
	}
	t.Setenv("MODEL_CONFIG_KEK_FILE", filepath.Join(dir, "shared", "key"))
	a, err := ModelKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	b, err := ModelKey(t.TempDir())
	if err != nil || !bytes.Equal(a, b) {
		t.Fatal("workers do not share key", err)
	}
}

func TestConcurrentKeyInitializationPublishesOneCompleteKey(t *testing.T) {
	t.Setenv("MODEL_CONFIG_KEK_BASE64", "")
	t.Setenv("MODEL_CONFIG_KEK_FILE", "")
	t.Setenv("STORAGE_BACKEND", "file")
	dir := t.TempDir()
	keys := make([][]byte, 8)
	var wg sync.WaitGroup
	for i := range keys {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			key, err := ModelKey(dir)
			if err != nil {
				t.Error(err)
				return
			}
			keys[i] = key
		}(i)
	}
	wg.Wait()
	for _, key := range keys {
		if len(key) != 32 || !bytes.Equal(key, keys[0]) {
			t.Fatal("initializers observed different keys")
		}
	}
}
