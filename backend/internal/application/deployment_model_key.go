package application

import (
	"encoding/base64"
	"errors"

	"github.com/adcwb/ai-creative-studio/backend/internal/platform/deployconfig"
)

func deploymentModelKey(dir string) ([]byte, error) {
	key, err := deployconfig.ModelKey(dir)
	if errors.Is(err, deployconfig.ErrKey) {
		return nil, errModelKey
	}
	return key, err
}

// A newly initialized shared file must never allow writes using a different
// key from the one that encrypted an existing database snapshot.
func (s *ModelConfigStore) verifyDeploymentModelKey() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failed != nil {
		return s.failed
	}
	check := func(encoded string, aad []byte) error {
		if s.aead == nil {
			return errModelKey
		}
		raw, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil || len(raw) < s.aead.NonceSize() {
			return errModelKey
		}
		plain, err := s.aead.Open(nil, raw[:s.aead.NonceSize()], raw[s.aead.NonceSize():], aad)
		clear(plain)
		if err != nil {
			return errModelKey
		}
		return nil
	}
	for tenant, t := range s.data.Tenants {
		for id, m := range t.Entries {
			if m.Ciphertext != "" {
				if err := check(m.Ciphertext, []byte(tenant+"\x00"+id+"\x00"+m.Entry.Provider)); err != nil {
					return err
				}
			}
		}
		for _, g := range t.Gateways {
			if g.Ciphertext != "" {
				if err := check(g.Ciphertext, gatewayAAD(tenant, g.Gateway)); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
