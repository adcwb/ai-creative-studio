package identity_test

import (
	"encoding/hex"
	"testing"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/identity"
)

func TestCredentialsAndRoleVocabularyRemainCompatible(t *testing.T) {
	salt := "00112233445566778899aabbccddeeff"
	hash := identity.PasswordHash("synthetic-test-passphrase", salt)
	decoded, err := hex.DecodeString(hash)
	if err != nil || len(decoded) != 32 || hash != identity.PasswordHash("synthetic-test-passphrase", salt) {
		t.Fatal("password hash format or determinism changed")
	}
	if hash == identity.PasswordHash("different-synthetic-passphrase", salt) {
		t.Fatal("different passwords share a hash")
	}
	token := identity.RandomToken(16)
	if len(token) != 32 || len(identity.TokenDigest(token)) != 64 {
		t.Fatal("token encoding changed")
	}
	for _, role := range []string{"owner", "admin", "editor", "viewer"} {
		if !identity.ValidRole(role) {
			t.Fatalf("role rejected: %s", role)
		}
	}
	if identity.ValidRole("super-admin") || identity.ValidRole("") {
		t.Fatal("unknown role accepted")
	}
}
