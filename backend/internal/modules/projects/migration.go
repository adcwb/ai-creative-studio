package projects

// MigrationSource is immutable provenance, not a capability granting access to
// the source project. Every source read still checks the caller's organization.
type MigrationSource struct {
	MigrationID    string `json:"migration_id"`
	ProjectID      string `json:"project_id"`
	Episode        int    `json:"episode_number"`
	ProjectVersion int    `json:"project_version"`
	ScriptVersion  int    `json:"script_version"`
	MediaVersion   int    `json:"media_version"`
	SHA256         string `json:"sha256"`
}
