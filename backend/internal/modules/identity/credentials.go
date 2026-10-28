package identity

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
)

func RandomToken(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

func TokenDigest(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

func PasswordHash(password, salt string) string {
	b, _ := hex.DecodeString(salt)
	h, err := pbkdf2.Key(sha256.New, password, b, 600000, 32)
	if err != nil {
		panic(err)
	}
	return hex.EncodeToString(h)
}
