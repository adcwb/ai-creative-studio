package generation

import (
	"time"
)

type Asset struct {
	ID           string    `json:"id"`
	SpaceID      string    `json:"space_id"`
	TaskID       string    `json:"task_id"`
	Prompt       string    `json:"prompt"`
	Kind         string    `json:"kind"`
	Ratio        string    `json:"ratio"`
	Style        string    `json:"style"`
	Provider     string    `json:"provider"`
	Model        string    `json:"model,omitempty"`
	ResultURL    string    `json:"result_url"`
	ResultMIME   string    `json:"result_mime,omitempty"`
	ResultWidth  int       `json:"result_width,omitempty"`
	ResultHeight int       `json:"result_height,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}
