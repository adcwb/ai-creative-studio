package publishing

type SocialState struct {
	Jobs        []SocialJob        `json:"jobs"`
	Connections []SocialConnection `json:"connections"`
}

type SocialConnection struct {
	RefreshLeaseUntil string `json:"refresh_lease_until,omitempty"`
	AccountKey        string `json:"account_key"`
	ID                string `json:"id"`
	Platform          string `json:"platform"`
	Status            string `json:"status"`
	DisplayName       string `json:"display_name"`
	Version           int    `json:"version"`
	ExpiresAt         string `json:"expires_at"`
}

type SocialJob struct {
	MediaType          string        `json:"media_type,omitempty"`
	PhotoAssetIDs      []string      `json:"photo_asset_ids,omitempty"`
	PhotoExpiresAt     string        `json:"photo_expires_at,omitempty"`
	PhotoAccessRevoked bool          `json:"photo_access_revoked,omitempty"`
	PhotoSourceKey     string        `json:"photo_source_key,omitempty"`
	PhotoGrantID       string        `json:"photo_grant_id,omitempty"`
	PhotoActor         string        `json:"photo_actor,omitempty"`
	NextPollAt         string        `json:"next_poll_at,omitempty"`
	HandoffExpiresAt   string        `json:"handoff_expires_at,omitempty"`
	Attempt            string        `json:"attempt,omitempty"`
	LeaseUntil         string        `json:"lease_until,omitempty"`
	ID                 string        `json:"id"`
	Platform           string        `json:"platform"`
	Mode               string        `json:"mode"`
	Status             string        `json:"status"`
	AssetID            string        `json:"asset_id,omitempty"`
	ExportID           string        `json:"export_id,omitempty"`
	Title              string        `json:"title"`
	Caption            string        `json:"caption"`
	DownloadURL        string        `json:"download_url"`
	MIMEType           string        `json:"mime_type"`
	CreatedAt          string        `json:"created_at"`
	Version            int           `json:"version"`
	ConnectionID       string        `json:"connection_id,omitempty"`
	PublishID          string        `json:"publish_id,omitempty"`
	ErrorCode          string        `json:"error_code,omitempty"`
	Intent             *SocialCreate `json:"intent,omitempty"`
}

type SocialPlatform struct {
	ID               string `json:"id"`
	DirectStatus     string `json:"direct_status"`
	HandoffStatus    string `json:"handoff_status"`
	ConnectionStatus string `json:"connection_status"`
	ReasonCode       string `json:"reason_code"`
	PhotoStatus      string `json:"photo_status,omitempty"`
}

type SocialView struct {
	Version     int                `json:"version"`
	Platforms   []SocialPlatform   `json:"platforms"`
	Jobs        []SocialJob        `json:"jobs"`
	Connections []SocialConnection `json:"connections"`
}

type SocialCreate struct {
	MediaType            string   `json:"media_type,omitempty"`
	PhotoAssetIDs        []string `json:"photo_asset_ids,omitempty"`
	PhotoCoverIndex      int      `json:"photo_cover_index,omitempty"`
	AutoAddMusic         bool     `json:"auto_add_music"`
	PublicMediaConfirmed bool     `json:"public_media_confirmed"`
	ExpectedVersion      int      `json:"expected_version"`
	Platform             string   `json:"platform"`
	Mode                 string   `json:"mode"`
	AssetID              string   `json:"asset_id"`
	ExportID             string   `json:"export_id"`
	Title                string   `json:"title"`
	Caption              string   `json:"caption"`
	Confirmed            bool     `json:"confirmed"`
	ConnectionID         string   `json:"connection_id,omitempty"`
	PrivacyLevel         string   `json:"privacy_level,omitempty"`
	AllowComment         bool     `json:"allow_comment"`
	AllowDuet            bool     `json:"allow_duet"`
	AllowStitch          bool     `json:"allow_stitch"`
	IsAIGC               bool     `json:"is_aigc"`
	BrandContent         bool     `json:"brand_content"`
	BrandOrganic         bool     `json:"brand_organic"`
	RightsConfirmed      bool     `json:"rights_confirmed"`
}
