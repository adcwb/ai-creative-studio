package main

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/json"
	"flag"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/adcwb/ai-creative-studio/backend/internal/application"
)

func TestRotationConnectionRequiresExplicitIsolatedParameters(t *testing.T) {
	for _, raw := range []string{"", "host=/tmp user=synthetic dbname=example", "postgres://synthetic@db/example", "postgres://db/example?sslmode=disable", "postgres://synthetic@/example?sslmode=disable", "postgres://synthetic@db/example?sslmode=disable&service=existing", "postgres://synthetic@db/example?sslmode=disable&passfile=/secret", "postgres://synthetic@db/example?sslmode=disable&host=a&host=b"} {
		if _, cleanup, e := rotationConnection(raw); e == nil {
			cleanup()
			t.Fatal("unsafe connection accepted")
		}
	}
	dsn, cleanup, e := rotationConnection("postgres://synthetic@/example?host=/tmp&port=55439&sslmode=disable")
	if e != nil {
		t.Fatal(e)
	}
	u, e := url.Parse(dsn)
	if e != nil {
		t.Fatal(e)
	}
	path := u.Query().Get("passfile")
	raw, e := os.ReadFile(path)
	if e != nil || len(raw) != 0 {
		t.Fatal("empty private passfile missing", e)
	}
	st, e := os.Stat(path)
	if e != nil || st.Mode().Perm() != 0600 {
		t.Fatal("passfile permissions", e)
	}
	cleanup()
	if _, e = os.Stat(path); !os.IsNotExist(e) {
		t.Fatal("private connection file leaked")
	}
}

func TestRotationCLIFileApplyWithOnlySyntheticKeys(t *testing.T) {
	old := bytes.Repeat([]byte{61}, 32)
	next := bytes.Repeat([]byte{72}, 32)
	t.Setenv("MODEL_ROTATION_OLD_KEK_BASE64", base64.StdEncoding.EncodeToString(old))
	t.Setenv("MODEL_ROTATION_NEW_KEK_BASE64", base64.StdEncoding.EncodeToString(next))
	t.Setenv("MODEL_ROTATION_CONFIRM_KEK_BASE64", base64.StdEncoding.EncodeToString(next))
	t.Setenv("STORAGE_BACKEND", "file")
	dir := t.TempDir()
	if e := os.WriteFile(filepath.Join(dir, "state.json"), []byte(`{}`), 0600); e != nil {
		t.Fatal(e)
	}
	block, _ := aes.NewCipher(old)
	aead, _ := cipher.NewGCM(block)
	nonce := bytes.Repeat([]byte{31}, aead.NonceSize())
	aad := []byte("synthetic-tenant\x00synthetic-config\x00openai-text")
	sealed := aead.Seal(nonce, nonce, []byte("synthetic-credential-only"), aad)
	snapshot := map[string]any{"version": 1, "tenants": map[string]any{"synthetic-tenant": map[string]any{"version": 1, "entries": map[string]any{"synthetic-config": map[string]any{"entry": map[string]any{"id": "synthetic-config", "provider": "openai-text", "model": "synthetic-model", "capability": "text", "region": "global", "endpoint": "https://api.openai.com/v1", "protocol": "chat-completions", "enabled": true, "is_default": true, "lifecycle": "active", "secret_status": "encrypted", "secret_mask": "••••••••", "updated_at": "2026-01-01T00:00:00Z"}, "ciphertext": base64.StdEncoding.EncodeToString(sealed)}}}}}
	raw, _ := json.Marshal(snapshot)
	path := filepath.Join(dir, "model-config.json")
	if e := os.WriteFile(path, raw, 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := application.OpenModelConfigStore(dir, old); e != nil {
		t.Fatal("synthetic configuration invalid", e)
	}
	oldArgs, oldFlags := os.Args, flag.CommandLine
	t.Cleanup(func() { os.Args = oldArgs; flag.CommandLine = oldFlags })
	os.Args = []string{"model-key-rotate", "--data-dir=" + dir, "--confirm-stopped", "--apply"}
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ContinueOnError)
	if e := run(); e != nil {
		t.Fatal(e)
	}
	changed, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	var d struct {
		Tenants map[string]struct {
			Version int
			Entries map[string]struct{ Ciphertext string }
		}
	}
	if e = json.Unmarshal(changed, &d); e != nil {
		t.Fatal(e)
	}
	if d.Tenants["synthetic-tenant"].Version != 2 {
		t.Fatal("version not advanced")
	}
	ciphertext, e := base64.StdEncoding.DecodeString(d.Tenants["synthetic-tenant"].Entries["synthetic-config"].Ciphertext)
	if e != nil {
		t.Fatal(e)
	}
	block, _ = aes.NewCipher(next)
	aead, _ = cipher.NewGCM(block)
	plain, e := aead.Open(nil, ciphertext[:aead.NonceSize()], ciphertext[aead.NonceSize():], aad)
	if e != nil || string(plain) != "synthetic-credential-only" {
		t.Fatal("CLI output could not be recovered with synthetic new key")
	}
}
