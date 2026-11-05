// Package prompts contains text recipe and visibility rules; it never dispatches models.
package prompts

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

var ErrInput = errors.New("invalid prompt recipe")
var ErrConflict = errors.New("prompt version conflict")
var ErrArchived = errors.New("prompt archived")
var ErrNotFound = errors.New("prompt not found")
var ErrDenied = errors.New("prompt write denied")
var ID = regexp.MustCompile(`^[a-f0-9]{32}$`)

type Actor struct {
	Space, User string
	Writable    bool
	Manage      bool
}
type Defaults struct {
	Kind  string `json:"kind"`
	Ratio string `json:"ratio"`
	Style string `json:"style"`
}
type Recipe struct {
	Syntax    string     `json:"syntax"`
	Body      string     `json:"body"`
	Defaults  Defaults   `json:"defaults"`
	Notes     string     `json:"notes"`
	Variables []Variable `json:"variables,omitempty"`
}
type Metadata struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Category    string   `json:"category"`
	Tags        []string `json:"tags"`
	Visibility  string   `json:"visibility"`
}
type Template struct {
	Metadata
	ID            string    `json:"id"`
	SpaceID       string    `json:"space_id"`
	OwnerID       string    `json:"owner_id"`
	Status        string    `json:"status"`
	HeadVersionID string    `json:"head_version_id"`
	RowVersion    int64     `json:"row_version"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}
type Version struct {
	Recipe
	ID         string    `json:"id"`
	TemplateID string    `json:"template_id"`
	SpaceID    string    `json:"space_id"`
	Number     int64     `json:"number"`
	SHA256     string    `json:"sha256"`
	CreatedBy  string    `json:"created_by"`
	CreatedAt  time.Time `json:"created_at"`
}
type Result struct {
	Template Template `json:"template"`
	Version  Version  `json:"version"`
	Favorite bool     `json:"favorite"`
}
type Source struct {
	TemplateID      string            `json:"template_id"`
	VersionID       string            `json:"version_id"`
	CompiledSHA256  string            `json:"compiled_sha256"`
	EffectiveSHA256 string            `json:"effective_sha256"`
	Edited          bool              `json:"edited"`
	Variables       map[string]string `json:"variables,omitempty"`
}

func TextSHA(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }
func Digest(v any) string     { b, _ := json.Marshal(v); return TextSHA(string(b)) }
func CanRead(a Actor, t Template) bool {
	return a.User != "" && a.Space == t.SpaceID && (a.User == t.OwnerID || t.Visibility == "organization")
}
func CanWrite(a Actor, t Template) bool { return a.Writable && CanRead(a, t) }
func NormalizeMetadata(m Metadata) (Metadata, error) {
	m.Name = strings.TrimSpace(m.Name)
	m.Description = strings.TrimSpace(m.Description)
	m.Category = strings.TrimSpace(m.Category)
	if n := utf8.RuneCountInString(m.Name); n < 1 || n > 80 {
		return m, ErrInput
	}
	if !utf8.ValidString(m.Name+m.Description+m.Category) || utf8.RuneCountInString(m.Description) > 500 || utf8.RuneCountInString(m.Category) > 80 || (m.Visibility != "private" && m.Visibility != "organization") {
		return m, ErrInput
	}
	tags := []string{}
	seen := map[string]bool{}
	for _, tag := range m.Tags {
		tag = strings.TrimSpace(tag)
		if tag == "" || !utf8.ValidString(tag) || utf8.RuneCountInString(tag) > 40 {
			return m, ErrInput
		}
		if !seen[tag] {
			tags = append(tags, tag)
			seen[tag] = true
		}
	}
	m.Tags = tags
	return m, nil
}
func NormalizeRecipe(r Recipe) (Recipe, error) {
	// Syntax is explicit: upgrading never interprets braces in existing plain recipes.
	if (r.Syntax != "plain" && r.Syntax != "variables_v1") || !utf8.ValidString(r.Body+r.Notes) || utf8.RuneCountInString(strings.TrimSpace(r.Body)) < 3 || utf8.RuneCountInString(r.Body) > 1000 || utf8.RuneCountInString(r.Notes) > 500 {
		return r, ErrInput
	}
	d := r.Defaults
	if (d.Kind != "poster" && d.Kind != "portrait" && d.Kind != "general") || (d.Ratio != "1:1" && d.Ratio != "4:5" && d.Ratio != "16:9") || (d.Style != "natural" && d.Style != "studio" && d.Style != "illustration") {
		return r, ErrInput
	}
	return normalizeVariables(r)
}
