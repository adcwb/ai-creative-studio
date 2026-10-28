package application

import (
	"context"
	"net/http"
	"sort"
	"time"
)

type RuntimeQuotePreview struct {
	Currency     string    `json:"currency"`
	MaximumMinor int64     `json:"maximum_minor"`
	MinorUnits   int       `json:"minor_units"`
	Source       string    `json:"source"`
	ExpiresAt    time.Time `json:"expires_at"`
	Contract     string    `json:"contract"`
}
type RuntimeModelOption struct {
	Quote      *RuntimeQuotePreview `json:"quote,omitempty"`
	ID         string               `json:"id"`
	Version    int64                `json:"version"`
	Provider   string               `json:"provider"`
	Model      string               `json:"model"`
	Capability string               `json:"capability"`
	IsDefault  bool                 `json:"is_default"`
	Available  bool                 `json:"available"`
}
type RuntimeModelCatalog struct {
	GovernanceReady bool                 `json:"governance_ready"`
	Models          []RuntimeModelOption `json:"models"`
}

// The execution catalog deliberately excludes credential state, references,
// masks, endpoint and administration history. All tenant members may read it;
// viewer membership never grants execution permission.
func registerRuntimeCatalog(mux *http.ServeMux, s *FileStore) {
	mux.HandleFunc("GET /api/v1/runtime-models", func(w http.ResponseWriter, r *http.Request) {
		scope, ok := r.Context().Value(authScopeKey{}).(authScope)
		if !ok || scope.SpaceID == "" || s.runtime == nil || s.runtime.auth == nil || s.runtime.auth.models == nil {
			problem(w, 403, "tenant_denied", "模型目录需要已授权组织身份")
			return
		}
		if s.runtime.auth.role(scope.UserID, scope.SpaceID) == "" {
			problem(w, 403, "tenant_denied", "没有此工作空间的访问权限")
			return
		}
		models := s.runtime.auth.models
		models.mu.Lock()
		defer models.mu.Unlock()
		if models.failed != nil {
			problem(w, 503, "model_config_storage_error", "模型配置不可用")
			return
		}
		ready := s.execution != nil && s.execution.Ready()
		result := RuntimeModelCatalog{GovernanceReady: ready, Models: []RuntimeModelOption{}}
		tenant := models.data.Tenants[scope.SpaceID]
		for _, stored := range tenant.Entries {
			e := stored.Entry
			if !e.Enabled || e.Lifecycle != "active" {
				continue
			}
			result.Models = append(result.Models, RuntimeModelOption{ID: e.ID, Version: tenant.Version, Provider: e.Provider, Model: e.Model, Capability: e.Capability, IsDefault: e.IsDefault, Available: ready && runtimeRoleAllowed(scope.Role) && s.runtime.entryAllowed(e, scope.SpaceID, e.Capability, tenant.Version), Quote: runtimeQuotePreview(r.Context(), s.execution, e)})
		}
		sort.Slice(result.Models, func(i, j int) bool { return result.Models[i].ID < result.Models[j].ID })
		respond(w, 200, result)
	})
}

// Only a bounded policy contract can be quoted without the full task payload.
// Exact-input media rules require a separate full-request preview and are not
// advertised as generic prices. This function does not invoke a provider.
func runtimeQuotePreview(ctx context.Context, g *ExecutionGovernor, e ModelConfigEntry) *RuntimeQuotePreview {
	if !g.Ready() {
		return nil
	}
	policy, ok := g.Quoter.(*ExecutionPolicy)
	if !ok {
		return nil
	}
	for _, rule := range policy.Prices {
		if rule.Provider != e.Provider || rule.Model != e.Model || rule.Capability != e.Capability || rule.RequestContract != "text-64k-6000-v1" {
			continue
		}
		bounded := &ExecutionPolicy{Prices: []ExecutionPriceRule{rule}}
		quote, err := bounded.QuoteExecution(ctx, ExecutionRequest{Provider: e.Provider, Model: e.Model, Capability: e.Capability})
		units, ce := CurrencyMinorUnits(quote.Currency)
		if err != nil || ce != nil || quote.MaximumMinor <= 0 || quote.MaximumMinor > 9007199254740991 || !quote.ExpiresAt.After(time.Now()) {
			return nil
		}
		return &RuntimeQuotePreview{Currency: quote.Currency, MaximumMinor: quote.MaximumMinor, MinorUnits: units, Source: quote.Evidence, ExpiresAt: quote.ExpiresAt, Contract: rule.RequestContract}
	}
	return nil
}
