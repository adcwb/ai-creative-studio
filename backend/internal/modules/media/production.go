package media

// Selection is an append-only creative decision, separate from timeline edits.
type Selection struct {
	ID        string `json:"id"`
	ShotID    string `json:"shot_id"`
	AssetID   string `json:"asset_id"`
	Note      string `json:"note"`
	CreatedAt string `json:"created_at"`
}

// ReviewNote is bound to a specific asset so later replacements cannot hide feedback.
type ReviewNote struct {
	AssigneeID string `json:"assignee_id,omitempty"`
	Priority   string `json:"priority,omitempty"`
	CreatedBy  string `json:"created_by,omitempty"`
	UpdatedBy  string `json:"updated_by,omitempty"`
	UpdatedAt  string `json:"updated_at,omitempty"`

	ID        string  `json:"id"`
	AssetID   string  `json:"asset_id"`
	ShotID    string  `json:"shot_id"`
	Time      float64 `json:"time"`
	Text      string  `json:"text"`
	Resolved  bool    `json:"resolved"`
	CreatedAt string  `json:"created_at"`
}
