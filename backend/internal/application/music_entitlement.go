package application

import (
	"strings"
	"time"
)

// MusicEntitlement is an operator assertion, never tenant-editable input. Binding
// the whole config version invalidates approval on credential/model changes.
// No account is probed, enrolled, charged or granted permission by this record.
type MusicEntitlement struct {
	TenantID           string    `json:"tenant_id"`
	ModelConfigID      string    `json:"model_config_id"`
	ModelConfigVersion int64     `json:"model_config_version"`
	Model              string    `json:"model"`
	Contract           string    `json:"contract"`
	Source             string    `json:"source"`
	ExpiresAt          time.Time `json:"expires_at"`
}

func (e MusicEntitlement) valid(now time.Time) bool {
	return e.TenantID != "" && len(e.TenantID) <= 128 && e.ModelConfigID != "" && len(e.ModelConfigID) <= 128 && e.ModelConfigVersion > 0 &&
		(e.Model == "music-3.0" || e.Model == "music-2.6") && e.Contract == "minimax-existing-paid-account-v1" &&
		strings.TrimSpace(e.Source) != "" && len(e.Source) <= 256 && e.ExpiresAt.After(now)
}
func validateMusicEntitlements(entries []MusicEntitlement) error {
	if len(entries) > 1000 {
		return ErrExecutionGovernance
	}
	seen := map[string]bool{}
	for _, e := range entries {
		key := e.TenantID + "\x00" + e.ModelConfigID
		if !e.valid(time.Now()) || strings.ContainsRune(e.TenantID, 0) || strings.ContainsRune(e.ModelConfigID, 0) || seen[key] {
			return ErrExecutionGovernance
		}
		seen[key] = true
	}
	return nil
}
func (p *ExecutionPolicy) MusicEntitled(tenant string, version int64, entry ModelConfigEntry) bool {
	if p == nil || entry.Provider != "minimax-music" || entry.Capability != "music" {
		return false
	}
	for _, e := range p.MusicEntitlements {
		if e.TenantID == tenant && e.ModelConfigID == entry.ID && e.ModelConfigVersion == version && e.Model == entry.Model && e.valid(time.Now()) {
			return true
		}
	}
	return false
}
