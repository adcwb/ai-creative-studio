package governance

import (
	"time"
)

type ModelAdapter struct {
	Provider       string   `json:"provider"`
	Protocol       string   `json:"protocol"`
	Capabilities   []string `json:"capabilities"`
	Regions        []string `json:"regions"`
	Endpoint       string   `json:"endpoint"`
	CredentialRefs []string `json:"credential_refs"`
}

type ModelConfigEntry struct {
	ID              string    `json:"id"`
	Provider        string    `json:"provider"`
	Model           string    `json:"model"`
	Capability      string    `json:"capability"`
	Region          string    `json:"region"`
	Endpoint        string    `json:"endpoint"`
	Protocol        string    `json:"protocol"`
	Enabled         bool      `json:"enabled"`
	IsDefault       bool      `json:"is_default"`
	Lifecycle       string    `json:"lifecycle"`
	SecretStatus    string    `json:"secret_status"`
	SecretMask      string    `json:"secret_mask"`
	SecretReference string    `json:"secret_reference"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type ModelConfigInput struct {
	ID         string `json:"id"`
	Provider   string `json:"provider"`
	Model      string `json:"model"`
	Capability string `json:"capability"`
	Region     string `json:"region"`
	Enabled    bool   `json:"enabled"`
	IsDefault  bool   `json:"is_default"`
	Lifecycle  string `json:"lifecycle"`
}

type ModelCredentialInput struct {
	Action    string `json:"action"`
	Secret    string `json:"secret,omitempty"`
	Reference string `json:"reference,omitempty"`
}

type ModelConfigReport struct {
	Version                int64              `json:"version"`
	Entries                []ModelConfigEntry `json:"entries"`
	Adapters               []ModelAdapter     `json:"adapters"`
	SecretStorageAvailable bool               `json:"secret_storage_available"`
	LiveEnabled            bool               `json:"live_enabled"`
	RuntimeApplied         bool               `json:"runtime_applied"`
	Scope                  string             `json:"scope"`
}
