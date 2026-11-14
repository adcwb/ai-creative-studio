package publishing

import (
	"errors"
	"strings"
	"time"
	"unicode"
)

// DeliveryMetadata is authored, immutable release preparation for one fixed
// export. RightsConfirmed records an assertion; it is not a licensing verdict.
type DeliveryMetadata struct {
	ID              string    `json:"id"`
	ExportID        string    `json:"export_id"`
	Version         int       `json:"version"`
	Title           string    `json:"title"`
	Description     string    `json:"description"`
	Tags            []string  `json:"tags"`
	CoverAssetID    string    `json:"cover_asset_id,omitempty"`
	CoverSHA256     string    `json:"cover_sha256,omitempty"`
	CoverMIME       string    `json:"cover_mime,omitempty"`
	RightsConfirmed bool      `json:"rights_confirmed"`
	CreatedAt       time.Time `json:"created_at"`
	CreatedBy       string    `json:"created_by"`
}
type DeliveryInput struct {
	ExpectedMetadataVersion int      `json:"expected_metadata_version"`
	Title                   string   `json:"title"`
	Description             string   `json:"description"`
	Tags                    []string `json:"tags"`
	CoverAssetID            string   `json:"cover_asset_id,omitempty"`
	RightsConfirmed         bool     `json:"rights_confirmed"`
}

var ErrDeliveryInput = errors.New("invalid delivery metadata")

func NormalizeDelivery(in DeliveryInput) (DeliveryInput, error) {
	in.Title = strings.TrimSpace(in.Title)
	in.Description = strings.TrimSpace(in.Description)
	if in.ExpectedMetadataVersion < 0 || in.Title == "" || !ValidText(in.Title, 200) || strings.IndexFunc(in.Title, unicode.IsControl) >= 0 || !ValidText(in.Description, 12000) || !in.RightsConfirmed {
		return in, ErrDeliveryInput
	}
	seen := map[string]bool{}
	tags := []string{}
	for _, tag := range in.Tags {
		tag = strings.TrimSpace(tag)
		if tag == "" || !ValidText(tag, 80) || strings.IndexFunc(tag, unicode.IsControl) >= 0 || seen[strings.ToLower(tag)] {
			return in, ErrDeliveryInput
		}
		seen[strings.ToLower(tag)] = true
		tags = append(tags, tag)
	}
	in.Tags = tags
	return in, nil
}
