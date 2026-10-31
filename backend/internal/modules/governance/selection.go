package governance

// RuntimeSelection is persisted with the accepted task. ModelID identifies a
// tenant configuration, not a vendor model. ActorID must come from authentication.
type RuntimeSelection struct {
	GatewayID      string `json:"gateway_id,omitempty"`
	GatewayVersion int64  `json:"gateway_version,omitempty"`
	FeatureKey     string `json:"feature_key,omitempty"`
	RequestDigest  string `json:"request_sha256,omitempty"`
	TenantID       string `json:"tenant_id"`
	ActorID        string `json:"actor_id"`
	ModelID        string `json:"model_config_id"`
	Version        int64  `json:"model_config_version"`
	Capability     string `json:"capability"`
	Voice          string `json:"voice,omitempty"`
	Currency       string `json:"budget_currency,omitempty"`
	MaximumMinor   int64  `json:"budget_maximum_minor,omitempty"`
}
