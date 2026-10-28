package application

type ScriptGenerationInput struct {
	ModelRequest
	ExpectedModel    string `json:"expected_model,omitempty"`
	ExpectedProvider string `json:"expected_provider,omitempty"`
	ExpectedVersion  int    `json:"expected_version"`
}

type projectReceipt struct {
	SpaceID      string `json:"space_id"`
	Key          string `json:"key"`
	Operation    string `json:"operation"`
	ProjectID    string `json:"project_id"`
	Fingerprint  string `json:"fingerprint"`
	GenerationID string `json:"generation_id,omitempty"`
}
