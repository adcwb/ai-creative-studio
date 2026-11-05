// Package story contains versioned authored story material. It does not perform
// model calls, infer objective truth, or grant rights to assets or voices.
package story

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

var ErrInput = errors.New("invalid story material")
var ErrConflict = errors.New("story version conflict")
var ErrNotFound = errors.New("story material not found")
var ErrDenied = errors.New("story write denied")
var ID = regexp.MustCompile(`^[a-f0-9]{32}$`)
var Scope = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
var Hash = regexp.MustCompile(`^[a-f0-9]{64}$`)

const MaxVersion int64 = 9007199254740991

type Actor struct {
	Space, User string
	Writable    bool
}
type Character struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Desire        string `json:"desire"`
	Flaw          string `json:"flaw"`
	Arc           string `json:"arc"`
	Relationships string `json:"relationships"`
	VisualIntent  string `json:"visual_intent"`
	VoiceIntent   string `json:"voice_intent"`
}
type Bible struct {
	Theme          string      `json:"theme"`
	WorldRules     string      `json:"world_rules"`
	StoryArc       string      `json:"story_arc"`
	VisualIntent   string      `json:"visual_intent"`
	ImmutableRules string      `json:"immutable_rules"`
	Characters     []Character `json:"characters"`
}
type Knowledge struct {
	CharacterID   string `json:"character_id"`
	FromEpisodeID string `json:"from_episode_id"`
}
type Fact struct {
	Subject      string      `json:"subject"`
	Predicate    string      `json:"predicate"`
	Value        string      `json:"value"`
	Perspective  string      `json:"perspective"`
	BelieverID   string      `json:"believer_id,omitempty"`
	StoryTime    string      `json:"story_time"`
	AudienceFrom string      `json:"audience_from_episode_id,omitempty"`
	KnownBy      []Knowledge `json:"known_by"`
}
type Event struct {
	StoryTime   string `json:"story_time"`
	EpisodeID   string `json:"episode_id"`
	Description string `json:"description"`
	StateChange string `json:"state_change"`
}
type Foreshadow struct {
	SetupEpisodeID  string `json:"setup_episode_id"`
	PayoffEpisodeID string `json:"payoff_episode_id,omitempty"`
	Intent          string `json:"intent"`
	State           string `json:"state"`
}
type Evidence struct {
	EpisodeID     string `json:"episode_id"`
	SceneID       string `json:"scene_id"`
	ScriptVersion int    `json:"script_version"`
	ScriptSHA256  string `json:"script_sha256"`
	Quote         string `json:"quote"`
}
type Body struct {
	Cast          *Cast       `json:"cast,omitempty"`
	Bible         *Bible      `json:"bible,omitempty"`
	Fact          *Fact       `json:"fact,omitempty"`
	Event         *Event      `json:"event,omitempty"`
	Foreshadow    *Foreshadow `json:"foreshadow,omitempty"`
	FromEpisodeID string      `json:"from_episode_id,omitempty"`
	ToEpisodeID   string      `json:"to_episode_id,omitempty"`
	Evidence      []Evidence  `json:"evidence"`
}
type Input struct {
	ExpectedVersion int64  `json:"expected_version"`
	Title           string `json:"title"`
	Kind            string `json:"kind"`
	Status          string `json:"status"`
	Required        bool   `json:"required"`
	Body            Body   `json:"body"`
}
type Version struct {
	Format    int       `json:"format"`
	ID        string    `json:"id"`
	Space     string    `json:"space_id"`
	SeriesID  string    `json:"series_id"`
	VersionID string    `json:"version_id"`
	Number    int64     `json:"number"`
	Title     string    `json:"title"`
	Kind      string    `json:"kind"`
	Status    string    `json:"status"`
	Required  bool      `json:"required"`
	Body      Body      `json:"body"`
	SHA256    string    `json:"content_sha256"`
	Actor     string    `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
}
type Summary struct {
	ID        string `json:"id"`
	SeriesID  string `json:"series_id"`
	VersionID string `json:"version_id"`
	Number    int64  `json:"number"`
	Title     string `json:"title"`
	Kind      string `json:"kind"`
	Status    string `json:"status"`
	Required  bool   `json:"required"`
	SHA256    string `json:"content_sha256"`
}

func Summarize(v Version) Summary {
	return Summary{v.ID, v.SeriesID, v.VersionID, v.Number, v.Title, v.Kind, v.Status, v.Required, v.SHA256}
}
func Digest(v any) string {
	b, e := json.Marshal(v)
	if e != nil {
		return ""
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func TextHash(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }
func NewID(a Actor, series, key string) string {
	return TextHash(a.Space + "\x00" + a.User + "\x00" + series + "\x00" + key)[:32]
}
func Clone[T any](v T) (T, error) {
	b, e := json.Marshal(v)
	var out T
	if e == nil {
		e = json.Unmarshal(b, &out)
	}
	return out, e
}
func clean(p *string, required bool) bool {
	*p = strings.TrimSpace(*p)
	if !utf8.ValidString(*p) || required && *p == "" {
		return false
	}
	for _, r := range *p {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return false
		}
	}
	return true
}
func optionalID(s string) bool { return s == "" || ID.MatchString(s) }
func Normalize(in Input) (Input, error) {
	v, e := Clone(in)
	if e != nil {
		return Input{}, ErrInput
	}
	if v.ExpectedVersion < 0 || v.ExpectedVersion >= MaxVersion || !clean(&v.Title, true) || v.Status != "active" && v.Status != "archived" {
		return Input{}, ErrInput
	}
	if !optionalID(v.Body.FromEpisodeID) || !optionalID(v.Body.ToEpisodeID) {
		return Input{}, ErrInput
	}
	count := 0
	for _, x := range []bool{v.Body.Bible != nil, v.Body.Fact != nil, v.Body.Event != nil, v.Body.Foreshadow != nil, v.Body.Cast != nil} {
		if x {
			count++
		}
	}
	if count != 1 {
		return Input{}, ErrInput
	}
	if v.Body.Evidence == nil {
		v.Body.Evidence = []Evidence{}
	}
	seen := map[string]bool{}
	for i := range v.Body.Evidence {
		r := &v.Body.Evidence[i]
		k := r.EpisodeID + ":" + r.SceneID + ":" + r.ScriptSHA256 + ":" + r.Quote
		if !ID.MatchString(r.EpisodeID) || !ID.MatchString(r.SceneID) || r.ScriptVersion < 1 || !Hash.MatchString(r.ScriptSHA256) || !clean(&r.Quote, true) || seen[k] {
			return Input{}, ErrInput
		}
		seen[k] = true
	}
	switch v.Kind {
	case "bible":
		b := v.Body.Bible
		if b == nil || v.Body.FromEpisodeID != "" || v.Body.ToEpisodeID != "" {
			return Input{}, ErrInput
		}
		for _, p := range []*string{&b.Theme, &b.WorldRules, &b.StoryArc, &b.VisualIntent, &b.ImmutableRules} {
			if !clean(p, false) {
				return Input{}, ErrInput
			}
		}
		if b.Characters == nil {
			b.Characters = []Character{}
		}
		ids := map[string]bool{}
		for i := range b.Characters {
			c := &b.Characters[i]
			if !ID.MatchString(c.ID) || ids[c.ID] || !clean(&c.Name, true) {
				return Input{}, ErrInput
			}
			ids[c.ID] = true
			for _, p := range []*string{&c.Desire, &c.Flaw, &c.Arc, &c.Relationships, &c.VisualIntent, &c.VoiceIntent} {
				if !clean(p, false) {
					return Input{}, ErrInput
				}
			}
		}
		// VoiceIntent is prose. Saving it is never consent or an executable voice ID.
		v.Required = true
	case "cast":
		if !normalizeCast(v.Body.Cast) || v.Body.FromEpisodeID != "" || v.Body.ToEpisodeID != "" || len(v.Body.Evidence) > 0 {
			return Input{}, ErrInput
		}
		v.Required = true
	case "fact":
		f := v.Body.Fact
		if f == nil {
			return Input{}, ErrInput
		}
		for _, p := range []*string{&f.Subject, &f.Predicate, &f.Value} {
			if !clean(p, true) {
				return Input{}, ErrInput
			}
		}
		if !clean(&f.StoryTime, false) || !optionalID(f.AudienceFrom) {
			return Input{}, ErrInput
		}
		if f.Perspective != "objective" && f.Perspective != "subjective" && f.Perspective != "unconfirmed" {
			return Input{}, ErrInput
		}
		if f.Perspective == "subjective" && !ID.MatchString(f.BelieverID) || f.Perspective != "subjective" && f.BelieverID != "" {
			return Input{}, ErrInput
		}
		if f.KnownBy == nil {
			f.KnownBy = []Knowledge{}
		}
		ids := map[string]bool{}
		for _, k := range f.KnownBy {
			if !ID.MatchString(k.CharacterID) || !ID.MatchString(k.FromEpisodeID) || ids[k.CharacterID] {
				return Input{}, ErrInput
			}
			ids[k.CharacterID] = true
		}
	case "event":
		x := v.Body.Event
		if x == nil || !ID.MatchString(x.EpisodeID) || !clean(&x.Description, true) || !clean(&x.StateChange, false) || !clean(&x.StoryTime, false) {
			return Input{}, ErrInput
		}
	case "foreshadow":
		x := v.Body.Foreshadow
		if x == nil || !ID.MatchString(x.SetupEpisodeID) || !optionalID(x.PayoffEpisodeID) || !clean(&x.Intent, true) || x.State != "planned" && x.State != "planted" && x.State != "paid_off" && x.State != "abandoned" {
			return Input{}, ErrInput
		}
	default:
		return Input{}, ErrInput
	}
	return v, nil
}
func ContentDigest(in Input) string {
	return Digest(struct {
		Title, Kind, Status string
		Required            bool
		Body                Body
	}{in.Title, in.Kind, in.Status, in.Required, in.Body})
}
func Valid(v Version) bool {
	if v.Format != 1 || !ID.MatchString(v.ID) || !ID.MatchString(v.SeriesID) || !Scope.MatchString(v.Space) || !Scope.MatchString(v.Actor) || !ID.MatchString(v.VersionID) || v.Number < 1 || v.Number > MaxVersion || v.CreatedAt.IsZero() {
		return false
	}
	in := Input{Title: v.Title, Kind: v.Kind, Status: v.Status, Required: v.Required, Body: v.Body}
	n, e := Normalize(in)
	return e == nil && Digest(in) == Digest(n) && v.SHA256 == ContentDigest(n)
}
