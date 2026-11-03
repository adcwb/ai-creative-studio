package deployconfig

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// Never replace a missing encryption key when an existing file has ciphertext.
// Independent database workers must use an explicit shared file or environment key.
var ErrKey = errors.New("credential encryption key unavailable")

func ModelKey(dir string) ([]byte, error) {
	if os.Getenv("MODEL_CONFIG_KEK_BASE64") != "" {
		key, err := base64.StdEncoding.DecodeString(os.Getenv("MODEL_CONFIG_KEK_BASE64"))
		if err != nil || len(key) != 32 {
			return nil, ErrKey
		}
		return key, nil
	}
	path := os.Getenv("MODEL_CONFIG_KEK_FILE")
	if path == "" {
		if os.Getenv("STORAGE_BACKEND") == "postgres" {
			return nil, nil
		}
		path = filepath.Join(dir, "model-config.key")
	}
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		data, readErr := os.ReadFile(filepath.Join(dir, "model-config.json"))
		if readErr != nil && !os.IsNotExist(readErr) {
			return nil, readErr
		}
		if bytes.Contains(data, []byte(`"ciphertext"`)) {
			return nil, ErrKey
		}
		key := make([]byte, 32)
		if _, err = rand.Read(key); err != nil {
			return nil, err
		}
		defer clear(key)
		if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return nil, err
		}
		f, createErr := os.CreateTemp(filepath.Dir(path), ".model-key-*")
		if createErr == nil {
			defer os.Remove(f.Name())
			if err = f.Chmod(0600); err != nil {
				f.Close()
				return nil, err
			}
			_, err = f.WriteString(base64.StdEncoding.EncodeToString(key))
			if err == nil {
				err = f.Sync()
			}
			closeErr := f.Close()
			if err == nil {
				err = closeErr
			}
			if err != nil {
				return nil, err
			}
			// Publish the complete file atomically, without replacing another
			// process's key when the API and worker initialize concurrently.
			if err = os.Link(f.Name(), path); err != nil && !os.IsExist(err) {
				return nil, err
			}
			if err = syncDirectory(filepath.Dir(path)); err != nil {
				return nil, err
			}
		} else {
			return nil, createErr
		}
		raw, err = os.ReadFile(path)
	}
	if err != nil {
		return nil, err
	}
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(raw)))
	clear(raw)
	if err != nil || len(key) != 32 {
		return nil, ErrKey
	}
	return key, nil
}
