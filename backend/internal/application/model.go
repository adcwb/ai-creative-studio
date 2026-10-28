package application

import (
	"time"
)

type Input struct {
	ModelRequest
	Prompt string `json:"prompt"`
	Kind   string `json:"kind"`
	Ratio  string `json:"ratio"`
	Style  string `json:"style"`
}

type Task struct {
	TraceParent      string            `json:"trace_parent,omitempty"`
	RuntimeSelection *RuntimeSelection `json:"runtime_selection,omitempty"`
	UpstreamID       string            `json:"upstream_id,omitempty"`
	ID               string            `json:"id"`
	SpaceID          string            `json:"space_id"`
	Input
	Status       string    `json:"status"`
	Progress     int       `json:"progress"`
	Provider     string    `json:"provider"`
	Model        string    `json:"model,omitempty"`
	RetryOf      string    `json:"retry_of,omitempty"`
	ResultURL    string    `json:"result_url,omitempty"`
	ResultMIME   string    `json:"result_mime,omitempty"`
	ResultWidth  int       `json:"result_width,omitempty"`
	ResultHeight int       `json:"result_height,omitempty"`
	Error        string    `json:"error,omitempty"`
	ErrorCode    string    `json:"error_code,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	SVG          string    `json:"result_svg,omitempty"`
	Key          string    `json:"idempotency_key,omitempty"`
	ResultPath   string    `json:"result_path,omitempty"`
}

func (t Task) Public() Task {
	t.RuntimeSelection = nil
	t.SVG = ""
	t.Key = ""
	t.ResultPath = ""
	return t
}

type Capabilities struct {
	// Only configureLocalTransform sets this process-local provenance marker.
	// Never deserialize it from API/config JSON or infer it from a provider name.
	localExecution bool
	Provider       string `json:"provider"`
	Mode           string `json:"mode"`
	Ready          bool   `json:"ready"`
	Model          string `json:"model,omitempty"`
	ReasonCode     string `json:"reason_code,omitempty"`
	Message        string `json:"message"`
}
