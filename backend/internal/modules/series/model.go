// Package series contains the creation directory, without execution or storage.
package series

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

var ErrInput = errors.New("invalid series input")
var ErrNotFound = errors.New("series resource not found")
var ErrConflict = errors.New("series version or position conflict")
var ErrDenied = errors.New("series write denied")
var ID = regexp.MustCompile(`^[a-f0-9]{32}$`)
var ScopeID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

const MaxOrdinal int64 = 9007199254740991

type Actor struct {
	Space, User string
	Writable    bool
}
type Profile struct {
	Duration   int64  `json:"duration_seconds"`
	Resolution string `json:"output_resolution"`
	Aspect     string `json:"aspect_ratio"`
}

func DefaultProfile() Profile { return Profile{90, "1080p", "9:16"} }
func (p Profile) Valid() bool {
	return p.Duration > 0 && p.Duration <= MaxOrdinal && (p.Resolution == "1080p" || p.Resolution == "1440p" || p.Resolution == "1800p" || p.Resolution == "2160p") && (p.Aspect == "16:9" || p.Aspect == "9:16" || p.Aspect == "1:1")
}

type Node struct {
	ID          string    `json:"id"`
	Space       string    `json:"space_id"`
	Kind        string    `json:"kind"`
	Parent      string    `json:"parent_id,omitempty"`
	SeriesID    string    `json:"series_id"`
	Ordinal     int64     `json:"ordinal,omitempty"`
	NextOrdinal int64     `json:"next_ordinal"`
	Version     int64     `json:"row_version"`
	Title       string    `json:"title"`
	Summary     string    `json:"summary"`
	Audience    string    `json:"audience,omitempty"`
	Format      string    `json:"format,omitempty"`
	Profile     Profile   `json:"profile"`
	Status      string    `json:"status"`
	State       string    `json:"state"`
	ProjectID   string    `json:"project_id,omitempty"`
	CreatedBy   string    `json:"created_by"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}
type Input struct {
	Title    string  `json:"title"`
	Summary  string  `json:"summary"`
	Audience string  `json:"audience,omitempty"`
	Format   string  `json:"format,omitempty"`
	Profile  Profile `json:"profile"`
}
type Update struct {
	Input
	Expected int64  `json:"expected_version"`
	Status   string `json:"status"`
}
type Command struct {
	Operation, Kind, ID, Parent string
	Input                       Input
	Expected                    int64
	ExpectedParent              int64
	Status                      string
	Ordinal                     int64
	Mode                        string `json:"mode,omitempty"`
}

func SHA(v string) string { h := sha256.Sum256([]byte(v)); return hex.EncodeToString(h[:]) }
func Digest(v any) string { b, _ := json.Marshal(v); return SHA(string(b)) }
func NewID(a Actor, operation, key string) string {
	return SHA(a.Space + "\x00" + a.User + "\x00" + operation + "\x00" + key)[:32]
}
func clean(s string, min, max int) (string, error) {
	s = strings.TrimSpace(s)
	n := utf8.RuneCountInString(s)
	if n < min || n > max || strings.IndexByte(s, 0) >= 0 {
		return "", ErrInput
	}
	return s, nil
}
func Normalize(in Input, kind string) (Input, error) {
	var e error
	minTitle, minSummary := 1, 3
	if kind == "season" {
		minTitle, minSummary = 0, 0
	}
	in.Title, e = clean(in.Title, minTitle, 80)
	if e != nil {
		return in, e
	}
	maxSummary := 300
	if kind == "episode" {
		maxSummary = 2000
	}
	in.Summary, e = clean(in.Summary, minSummary, maxSummary)
	if e != nil {
		return in, e
	}
	in.Audience, e = clean(in.Audience, 0, 400)
	if e != nil {
		return in, e
	}
	if kind == "series" {
		if in.Format == "" {
			in.Format = "motion-comic"
		}
		if in.Format != "motion-comic" && in.Format != "video" && in.Format != "mixed" {
			return in, ErrInput
		}
	} else if in.Audience != "" || in.Format != "" {
		return in, ErrInput
	}
	if !in.Profile.Valid() {
		return in, ErrInput
	}
	return in, nil
}
func Valid(n Node) bool {
	if !ID.MatchString(n.ID) || !ScopeID.MatchString(n.Space) || !ScopeID.MatchString(n.CreatedBy) || !ID.MatchString(n.SeriesID) || n.Version < 1 || n.Version > MaxOrdinal || n.NextOrdinal < 1 || n.NextOrdinal > MaxOrdinal || n.CreatedAt.IsZero() || n.UpdatedAt.IsZero() {
		return false
	}
	in, e := Normalize(Input{n.Title, n.Summary, n.Audience, n.Format, n.Profile}, n.Kind)
	if e != nil || in.Title != n.Title || in.Summary != n.Summary || in.Audience != n.Audience || in.Format != n.Format {
		return false
	}
	if n.Kind == "series" {
		return n.Parent == "" && n.SeriesID == n.ID && n.Ordinal == 0 && n.ProjectID == "" && (n.Status == "active" || n.Status == "archived") && n.State == "ready"
	}
	if !ID.MatchString(n.Parent) || n.Ordinal < 1 || n.Ordinal > MaxOrdinal {
		return false
	}
	if n.Kind == "season" {
		return n.Parent == n.SeriesID && n.ProjectID == "" && (n.Status == "active" || n.Status == "archived") && n.State == "ready"
	}
	if n.Kind != "episode" || n.ProjectID != n.ID || (n.State != "ready" && n.State != "preparing") {
		return false
	}
	switch n.Status {
	case "planned", "writing", "storyboard", "production", "review", "released":
		return true
	}
	return false
}
