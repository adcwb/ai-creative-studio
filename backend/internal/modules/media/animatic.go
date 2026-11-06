package media

import "github.com/adcwb/ai-creative-studio/backend/internal/modules/projects"

// Animatics are local rehearsal artifacts. No permission, credentials, model
// selection or publishing state belongs in their immutable creative sources.
type AnimaticShot struct {
	Shot          projects.StoryboardShot `json:"shot"`
	OriginalIndex int                     `json:"original_index"`
	Duration      float64                 `json:"duration"`
	AssetID       string                  `json:"asset_id"`
	AudioAssetID  string                  `json:"audio_asset_id,omitempty"`
}
type AnimaticAsset struct {
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	ID     string `json:"id"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}
type AnimaticSource struct {
	CreatedAt               string          `json:"created_at"`
	SHA256                  string          `json:"sha256"`
	ProjectVersion          int             `json:"project_version"`
	ScriptVersion           int             `json:"script_version"`
	StoryboardScriptVersion int             `json:"storyboard_script_version"`
	StoryboardSHA256        string          `json:"storyboard_sha256"`
	MediaVersion            int             `json:"media_version"`
	EpisodeNumber           int             `json:"episode_number"`
	SoundMode               string          `json:"sound_mode"`
	Shots                   []AnimaticShot  `json:"shots"`
	Assets                  []AnimaticAsset `json:"assets"`
}
