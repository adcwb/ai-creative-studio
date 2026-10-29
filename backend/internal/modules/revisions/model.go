// Package revisions contains version-pinned creative history rules. Execution,
// billing, permissions and publishing state must never be part of a restore.
package revisions

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"time"
)

var ErrInput = errors.New("invalid revision input")
var ErrConflict = errors.New("branch version conflict")
var idPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,80}$`)

type Ref struct {
	Kind      string `json:"kind"`
	ID        string `json:"id"`
	VersionID string `json:"version_id"`
	SHA256    string `json:"content_sha256"`
}
type Manifest struct {
	Brief      json.RawMessage `json:"brief"`
	Script     json.RawMessage `json:"script"`
	Storyboard json.RawMessage `json:"storyboard"`
	Timeline   json.RawMessage `json:"timeline"`
	Poster     json.RawMessage `json:"poster"`
	References []Ref           `json:"references"`
}
type Revision struct {
	ID             string    `json:"id"`
	SpaceID        string    `json:"space_id"`
	ProjectID      string    `json:"project_id"`
	ParentID       string    `json:"parent_id,omitempty"`
	RestoredFromID string    `json:"restored_from_id,omitempty"`
	Manifest       Manifest  `json:"manifest"`
	SHA256         string    `json:"content_sha256"`
	CreatedAt      time.Time `json:"created_at"`
}
type Branch struct {
	ID             string `json:"id"`
	SpaceID        string `json:"space_id"`
	ProjectID      string `json:"project_id"`
	Name           string `json:"name"`
	HeadRevisionID string `json:"head_revision_id"`
	Version        int    `json:"version"`
}
type Edge struct {
	Consumer Ref    `json:"consumer"`
	Source   Ref    `json:"source"`
	Usage    string `json:"usage"`
	ShotID   string `json:"shot_id,omitempty"`
}

func refKey(r Ref) string { return r.Kind + ":" + r.ID + ":" + r.VersionID }
func validRef(r Ref) bool {
	if !idPattern.MatchString(r.Kind) || !idPattern.MatchString(r.ID) || !idPattern.MatchString(r.VersionID) || len(r.SHA256) != 64 {
		return false
	}
	_, err := hex.DecodeString(r.SHA256)
	return err == nil
}
func ValidateManifest(m Manifest) error {
	for _, raw := range []json.RawMessage{m.Brief, m.Script, m.Storyboard, m.Timeline, m.Poster} {
		if len(raw) > 1<<20 || !json.Valid(raw) {
			return ErrInput
		}
	}
	if len(m.References) > 512 {
		return ErrInput
	}
	seen := map[string]bool{}
	for _, r := range m.References {
		if !validRef(r) || seen[refKey(r)] {
			return ErrInput
		}
		seen[refKey(r)] = true
	}
	return nil
}
func Digest(m Manifest) (string, error) {
	if err := ValidateManifest(m); err != nil {
		return "", err
	}
	b, err := json.Marshal(m)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:]), nil
}
func Advance(b Branch, expectedVersion int, expectedHead string, next Revision) (Branch, error) {
	if b.Version != expectedVersion || b.HeadRevisionID != expectedHead {
		return Branch{}, ErrConflict
	}
	if b.Version < 1 || !idPattern.MatchString(next.ID) || next.ID == b.HeadRevisionID || next.SpaceID != b.SpaceID || next.ProjectID != b.ProjectID || next.ParentID != b.HeadRevisionID {
		return Branch{}, ErrInput
	}
	digest, err := Digest(next.Manifest)
	if err != nil || digest != next.SHA256 {
		return Branch{}, ErrInput
	}
	b.HeadRevisionID, b.Version = next.ID, b.Version+1
	return b, nil
}

// ValidateEdges bounds traversal and rejects cycles on concrete versions, not
// on asset identities: new versions may legitimately derive from old ones.
func ValidateEdges(edges []Edge) error {
	if len(edges) > 4096 {
		return ErrInput
	}
	graph := map[string][]string{}
	for _, e := range edges {
		if !validRef(e.Consumer) || !validRef(e.Source) || !idPattern.MatchString(e.Usage) {
			return ErrInput
		}
		graph[refKey(e.Consumer)] = append(graph[refKey(e.Consumer)], refKey(e.Source))
	}
	state := map[string]int{}
	var visit func(string) bool
	visit = func(n string) bool {
		if state[n] == 1 {
			return false
		}
		if state[n] == 2 {
			return true
		}
		state[n] = 1
		for _, source := range graph[n] {
			if !visit(source) {
				return false
			}
		}
		state[n] = 2
		return true
	}
	for n := range graph {
		if !visit(n) {
			return ErrInput
		}
	}
	return nil
}
