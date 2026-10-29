package media

import (
	"regexp"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/publishing"
)

type SocialState = publishing.SocialState

var shotIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,40}$`)

type MediaAsset struct {
	ID        string  `json:"id"`
	Kind      string  `json:"kind"`
	MIMEType  string  `json:"mime_type"`
	Name      string  `json:"name"`
	Role      string  `json:"role"`
	Character string  `json:"character"`
	ShotID    string  `json:"shot_id"`
	Duration  float64 `json:"duration"`
	Source    string  `json:"source"`
	FileURL   string  `json:"file_url"`
}

type MediaClip struct {
	// A replacement may keep sound from an earlier video without copying bytes.
	SourceAudioAssetID   string        `json:"source_audio_asset_id,omitempty"`
	SourceAudioTrimStart float64       `json:"source_audio_trim_start,omitempty"`
	ShotID               string        `json:"shot_id,omitempty"`
	PreserveSourceAudio  bool          `json:"preserve_source_audio,omitempty"`
	SourceAudioVolume    float64       `json:"source_audio_volume,omitempty"`
	Transition           string        `json:"transition,omitempty"`
	TransitionDuration   float64       `json:"transition_duration,omitempty"`
	SubtitleCues         []SubtitleCue `json:"subtitle_cues,omitempty"`
	SubtitleSource       string        `json:"subtitle_source,omitempty"`
	SubtitleAlignmentID  string        `json:"subtitle_alignment_id,omitempty"`
	ID                   string        `json:"id"`
	AssetID              string        `json:"asset_id"`
	Duration             float64       `json:"duration"`
	TrimStart            float64       `json:"trim_start"`
	Subtitle             string        `json:"subtitle"`
	AudioAssetID         string        `json:"audio_asset_id"`
	AudioVolume          float64       `json:"audio_volume"`
	// A normalized crop, applied before fitting to the output frame.
	CropX      float64 `json:"crop_x"`
	CropY      float64 `json:"crop_y"`
	CropWidth  float64 `json:"crop_width"`
	CropHeight float64 `json:"crop_height"`
}

type MediaAudioTrack struct {
	ID        string  `json:"id"`
	AssetID   string  `json:"asset_id"`
	Start     float64 `json:"start"`
	TrimStart float64 `json:"trim_start"`
	Duration  float64 `json:"duration"`
	Volume    float64 `json:"volume"`
}

type MediaVideoOverlay struct {
	Keyframes           []MediaKeyframe `json:"keyframes,omitempty"`
	ID                  string          `json:"id"`
	AssetID             string          `json:"asset_id"`
	Start               float64         `json:"start"`
	TrimStart           float64         `json:"trim_start"`
	Duration            float64         `json:"duration"`
	X                   float64         `json:"x"`
	Y                   float64         `json:"y"`
	Width               float64         `json:"width"`
	Height              float64         `json:"height"`
	Opacity             float64         `json:"opacity"`
	PreserveSourceAudio bool            `json:"preserve_source_audio"`
	SourceAudioVolume   float64         `json:"source_audio_volume"`
}

type MediaExport struct {
	EpisodeNumber  int           `json:"episode_number,omitempty"`
	TraceParent    string        `json:"trace_parent,omitempty"`
	SubtitleVTTURL string        `json:"subtitle_vtt_url"`
	ID             string        `json:"id"`
	Status         string        `json:"status"`
	FileURL        string        `json:"file_url"`
	SubtitleURL    string        `json:"subtitle_url"`
	Error          string        `json:"error"`
	Duration       float64       `json:"duration"`
	Timeline       MediaTimeline `json:"timeline"`
}

type MediaState struct {
	EpisodeTimelines map[int]MediaTimeline `json:"episode_timelines,omitempty"`
	Selections       []Selection           `json:"selections,omitempty"`
	Reviews          []ReviewNote          `json:"reviews,omitempty"`
	LibraryBindings  []LibraryBinding      `json:"library_bindings,omitempty"`
	Poster           *PosterDocument       `json:"poster,omitempty"`
	Social           *SocialState          `json:"social,omitempty"`
	Version          int                   `json:"version"`
	Assets           []MediaAsset          `json:"assets"`
	Timeline         MediaTimeline         `json:"timeline"`
	Exports          []MediaExport         `json:"exports"`
}

// LibraryBinding records the exact library version copied into this project.
// Later library edits cannot alter these project-owned media references.
type LibraryBinding struct {
	ID            string   `json:"id"`
	AssetID       string   `json:"library_asset_id"`
	VersionID     string   `json:"asset_version_id"`
	Name          string   `json:"name"`
	Kind          string   `json:"kind"`
	Usage         string   `json:"usage"`
	ShotID        string   `json:"shot_id"`
	MediaAssetIDs []string `json:"media_asset_ids"`
	CreatedAt     string   `json:"created_at"`
}
