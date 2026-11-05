package application

import (
	"context"
	"time"
)

func validSyncInterval(n int) bool { return n == 0 || n >= 5 && n <= 10080 }

type GatewaySyncResult struct {
	Report   ModelManagementReport `json:"report"`
	Added    int                   `json:"added"`
	Existing int                   `json:"existing"`
}

func (s *ModelConfigStore) syncGatewayModels(ctx context.Context, tenant, id string, version int64) (GatewaySyncResult, error) {
	s.mu.Lock()
	g, ok := s.data.Tenants[tenant].Gateways[id]
	s.mu.Unlock()
	if !ok {
		return GatewaySyncResult{}, ErrNotFound
	}
	if !g.Gateway.Enabled {
		return GatewaySyncResult{}, errModelInput
	}
	result, err := s.checkGateway(ctx, tenant, id, version)
	if err != nil {
		return GatewaySyncResult{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failed != nil {
		return GatewaySyncResult{}, s.failed
	}
	d := s.cloneGatewayData()
	t := gatewayTenant(d, tenant)
	current := t.Gateways[id]
	if current.Gateway.Version != version {
		return GatewaySyncResult{}, errModelConflict
	}
	now := time.Now().UTC()
	current.Gateway.SyncAttemptedAt = &now
	if result["reachable"] != true {
		current.Gateway.SyncError = "gateway_catalog_unavailable"
		t.Gateways[id] = current
		t.Version++
		d.Tenants[tenant] = t
		if err = s.commit(d); err != nil {
			return GatewaySyncResult{}, err
		}
		return GatewaySyncResult{}, errGatewayCatalog
	}
	known := map[string]bool{}
	for _, m := range t.Models {
		if m.GatewayID == id {
			known[m.Model] = true
		}
	}
	out := GatewaySyncResult{}
	for _, modelID := range result["models"].([]string) {
		if known[modelID] {
			out.Existing++
			continue
		}
		name := modelID
		if len(name) > 80 {
			name = name[:80]
		}
		m := GatewayModel{ID: authRandom(16), GatewayID: id, Name: name, Model: modelID, Capabilities: []string{}, Pending: true, Enabled: false, Version: 1, MaxOutputTokens: 3000, JSONOutput: true, VideoSeconds: 4, VideoSize: "1280x720", Currency: "USD", UpdatedAt: now}
		t.Models[m.ID] = m
		known[modelID] = true
		out.Added++
	}
	current.Gateway.LastSyncedAt = &now
	current.Gateway.SyncError = ""
	t.Gateways[id] = current
	t.Version++
	d.Tenants[tenant] = t
	if err = s.commit(d); err != nil {
		return GatewaySyncResult{}, err
	}
	out.Report = s.managementReportLocked(tenant)
	return out, nil
}

// The API owns the scheduler; no open browser is required.
func RunGatewayModelSync(ctx context.Context, store *FileStore) {
	if store == nil || store.runtime == nil || store.runtime.auth == nil || store.runtime.auth.models == nil {
		return
	}
	s := store.runtime.auth.models
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		s.syncDueGateways(ctx, time.Now().UTC())
	}
}
func (s *ModelConfigStore) syncDueGateways(ctx context.Context, now time.Time) {
	type job struct {
		tenant  string
		gateway AIGateway
	}
	var jobs []job
	s.mu.Lock()
	for tenant, t := range s.data.Tenants {
		for _, record := range t.Gateways {
			g := record.Gateway
			if g.Enabled && g.SecretStatus == "encrypted" && validSyncInterval(g.SyncIntervalMinutes) && g.SyncIntervalMinutes > 0 && (g.SyncAttemptedAt == nil || now.Sub(*g.SyncAttemptedAt) >= time.Duration(g.SyncIntervalMinutes)*time.Minute) {
				jobs = append(jobs, job{tenant, g})
			}
		}
	}
	s.mu.Unlock()
	for _, j := range jobs {
		if ctx.Err() != nil {
			return
		}
		_, _ = s.syncGatewayModels(ctx, j.tenant, j.gateway.ID, j.gateway.Version)
	}
}
