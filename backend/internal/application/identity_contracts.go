package application

import (
	"github.com/adcwb/ai-creative-studio/backend/internal/modules/identity"
)

type AuthUser = identity.AuthUser
type AuthOrganization = identity.AuthOrganization

func validRole(role string) bool                { return identity.ValidRole(role) }
func authRandom(n int) string                   { return identity.RandomToken(n) }
func authDigest(token string) string            { return identity.TokenDigest(token) }
func authPassword(password, salt string) string { return identity.PasswordHash(password, salt) }
