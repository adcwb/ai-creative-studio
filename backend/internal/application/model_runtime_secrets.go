package application

import (
	"context"
	"errors"
)

// SecretBinding is deployment configuration, never accepted from tenant HTTP.
// ExternalID names an entry in an external secret store; it is not a URL to fetch.
type SecretBinding struct {
	TenantID, ModelConfigID, Provider, Reference, ExternalID string
}

// ApprovedSecretResolver makes both tenant and exact model configuration part
// of secret-store authorization. Merely saving an environment-variable name in
// the UI grants no access. Fetch implementations must honor context cancellation
// and transfer an owned byte slice (never a shared/cache buffer) to the caller.
type ApprovedSecretResolver struct {
	bindings map[[4]string]string
	fetch    func(context.Context, string) ([]byte, error)
}

func NewApprovedSecretResolver(bindings []SecretBinding, fetch func(context.Context, string) ([]byte, error)) (*ApprovedSecretResolver, error) {
	if fetch == nil {
		return nil, errors.New("secret resolver fetch required")
	}
	r := &ApprovedSecretResolver{bindings: map[[4]string]string{}, fetch: fetch}
	for _, b := range bindings {
		if b.TenantID == "" || b.ModelConfigID == "" || b.ExternalID == "" || len(b.ExternalID) > 512 {
			return nil, errors.New("invalid secret binding")
		}
		allowed := false
		for _, a := range ModelAdapters() {
			if a.Provider == b.Provider {
				for _, ref := range a.CredentialRefs {
					allowed = allowed || ref == b.Reference
				}
			}
		}
		if !allowed {
			return nil, errors.New("unsupported secret reference")
		}
		key := [4]string{b.TenantID, b.ModelConfigID, b.Provider, b.Reference}
		if _, ok := r.bindings[key]; ok {
			return nil, errors.New("duplicate secret binding")
		}
		r.bindings[key] = b.ExternalID
	}
	return r, nil
}

func (r *ApprovedSecretResolver) approved(tenant string, e ModelConfigEntry) bool {
	if r == nil {
		return false
	}
	_, ok := r.bindings[[4]string{tenant, e.ID, e.Provider, e.SecretReference}]
	return ok
}

func (r *ApprovedSecretResolver) resolve(ctx context.Context, tenant string, e ModelConfigEntry) ([]byte, error) {
	if !r.approved(tenant, e) {
		return nil, errRuntimeDenied
	}
	raw, err := r.fetch(ctx, r.bindings[[4]string{tenant, e.ID, e.Provider, e.SecretReference}])
	if err != nil {
		for i := range raw {
			raw[i] = 0
		}
		return nil, errRuntimeUnavailable
	}
	// Copy ownership, then erase the fetch result; no value is retained in config.
	if len(raw) > 4096 || !validModelSecret(e.Provider, string(raw)) {
		for i := range raw {
			raw[i] = 0
		}
		return nil, errRuntimeUnavailable
	}
	secret := append([]byte(nil), raw...)
	for i := range raw {
		raw[i] = 0
	}
	return secret, nil
}
