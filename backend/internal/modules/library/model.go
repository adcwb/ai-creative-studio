// Package library defines reusable creative identities and immutable versions.
// Storage, media ownership, authentication and model execution belong to callers.
package library

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

var ErrInput = errors.New("invalid library input")
var ErrConflict = errors.New("asset version conflict")
var ErrArchived = errors.New("asset archived")
var IDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,80}$`)

type Character struct {
	Appearance string `json:"appearance"`
	Hair       string `json:"hair"`
	Costume    string `json:"costume"`
	Expression string `json:"expression"`
	VoiceNotes string `json:"voice_notes"`
}
type Product struct {
	Model      string `json:"model"`
	Dimensions string `json:"dimensions"`
	Materials  string `json:"materials"`
	Packaging  string `json:"packaging"`
	ExactText  string `json:"exact_text"`
}
type Scene struct {
	Layout     string `json:"layout"`
	Objects    string `json:"objects"`
	Lighting   string `json:"lighting"`
	Viewpoints string `json:"viewpoints"`
}
type Reference struct {
	ID              string `json:"id"`
	Role            string `json:"role"`
	ViewAngle       string `json:"view_angle"`
	MIMEType        string `json:"mime_type"`
	Width           int    `json:"width"`
	Height          int    `json:"height"`
	SizeBytes       int64  `json:"size_bytes"`
	SHA256          string `json:"content_sha256"`
	SourceProjectID string `json:"source_project_id"`
	SourceAssetID   string `json:"source_asset_id"`
}
type Card struct {
	Name              string     `json:"name"`
	Description       string     `json:"description"`
	ProhibitedChanges []string   `json:"prohibited_changes"`
	Character         *Character `json:"character,omitempty"`
	Product           *Product   `json:"product,omitempty"`
	Scene             *Scene     `json:"scene,omitempty"`
}
type Version struct {
	ID         string      `json:"id"`
	AssetID    string      `json:"asset_id"`
	SpaceID    string      `json:"space_id"`
	Number     int         `json:"number"`
	Card       Card        `json:"card"`
	References []Reference `json:"references"`
	SHA256     string      `json:"content_sha256"`
	CreatedAt  time.Time   `json:"created_at"`
	CreatedBy  string      `json:"created_by"`
}
type Asset struct {
	ID            string    `json:"id"`
	SpaceID       string    `json:"space_id"`
	Kind          string    `json:"kind"`
	Name          string    `json:"name"`
	HeadVersionID string    `json:"head_version_id"`
	Status        string    `json:"status"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}
type Binding struct {
	AssetID   string `json:"library_asset_id"`
	VersionID string `json:"asset_version_id"`
	Usage     string `json:"usage"`
	ShotID    string `json:"shot_id,omitempty"`
}

func text(value string, maximum int, required bool) (string, error) {
	value = strings.TrimSpace(value)
	if !utf8.ValidString(value) || utf8.RuneCountInString(value) > maximum || (required && value == "") {
		return "", ErrInput
	}
	for _, r := range value {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return "", ErrInput
		}
	}
	return value, nil
}

func Normalize(kind string, in Card) (Card, error) {
	// Decode a fresh value: normalization must not mutate a saved version through pointers.
	b, err := json.Marshal(in)
	if err != nil {
		return Card{}, ErrInput
	}
	var c Card
	if json.Unmarshal(b, &c) != nil {
		return Card{}, ErrInput
	}
	if c.Name, err = text(c.Name, 80, true); err != nil {
		return Card{}, err
	}
	if c.Description, err = text(c.Description, 2000, false); err != nil {
		return Card{}, err
	}
	if len(c.ProhibitedChanges) > 20 {
		return Card{}, ErrInput
	}
	if c.ProhibitedChanges == nil {
		c.ProhibitedChanges = []string{}
	}
	seen := map[string]bool{}
	for i, item := range c.ProhibitedChanges {
		item, err = text(item, 300, true)
		if err != nil || seen[item] {
			return Card{}, ErrInput
		}
		seen[item] = true
		c.ProhibitedChanges[i] = item
	}
	var fields []*string
	switch kind {
	case "character":
		if c.Character == nil || c.Product != nil || c.Scene != nil {
			return Card{}, ErrInput
		}
		fields = []*string{&c.Character.Appearance, &c.Character.Hair, &c.Character.Costume, &c.Character.Expression, &c.Character.VoiceNotes}
	case "product":
		if c.Product == nil || c.Character != nil || c.Scene != nil {
			return Card{}, ErrInput
		}
		fields = []*string{&c.Product.Model, &c.Product.Dimensions, &c.Product.Materials, &c.Product.Packaging, &c.Product.ExactText}
	case "scene":
		if c.Scene == nil || c.Character != nil || c.Product != nil {
			return Card{}, ErrInput
		}
		fields = []*string{&c.Scene.Layout, &c.Scene.Objects, &c.Scene.Lighting, &c.Scene.Viewpoints}
	default:
		return Card{}, ErrInput
	}
	for _, f := range fields {
		if *f, err = text(*f, 2000, false); err != nil {
			return Card{}, err
		}
	}
	return c, nil
}

func Digest(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:]), nil
}

func ValidateReferences(refs []Reference) error {
	if len(refs) > 12 {
		return ErrInput
	}
	seen := map[string]bool{}
	for _, r := range refs {
		if !IDPattern.MatchString(r.ID) || seen[r.ID] || !IDPattern.MatchString(r.SourceProjectID) || !IDPattern.MatchString(r.SourceAssetID) {
			return ErrInput
		}
		seen[r.ID] = true
		if r.MIMEType != "image/png" && r.MIMEType != "image/jpeg" {
			return ErrInput
		}
		if r.Width < 1 || r.Height < 1 || r.Width > 8192 || r.Height > 8192 || int64(r.Width)*int64(r.Height) > 32_000_000 {
			return ErrInput
		}
		if r.SizeBytes < 1 || r.SizeBytes > 4<<20 {
			return ErrInput
		}
		if len(r.SHA256) != 64 {
			return ErrInput
		}
		if _, err := hex.DecodeString(r.SHA256); err != nil {
			return ErrInput
		}
		if _, err := text(r.Role, 80, true); err != nil {
			return err
		}
		if _, err := text(r.ViewAngle, 80, false); err != nil {
			return err
		}
	}
	return nil
}

func CanBind(a Asset, v Version, space string) error {
	if a.SpaceID != space || v.SpaceID != space || a.ID != v.AssetID || !IDPattern.MatchString(v.ID) {
		return ErrInput
	}
	if a.Status != "active" {
		return ErrArchived
	}
	return nil
}

func AppendVersion(a Asset, expected string, v Version) (Asset, error) {
	if a.Status != "active" {
		return Asset{}, ErrArchived
	}
	if expected != a.HeadVersionID {
		return Asset{}, ErrConflict
	}
	if v.AssetID != a.ID || v.SpaceID != a.SpaceID || v.ID == expected || !IDPattern.MatchString(v.ID) {
		return Asset{}, ErrInput
	}
	a.HeadVersionID, a.Name, a.UpdatedAt = v.ID, v.Card.Name, v.CreatedAt
	return a, nil
}
