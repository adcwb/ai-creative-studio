package story

import (
	"strings"
	"time"
	"unicode/utf8"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/governance"
)

// Evidence must quote a pinned, included record or one stable target scene.
// The provider cannot cite a later edit, omitted material or another tenant.
type CheckEvidence struct {
	RecordID  string `json:"record_id,omitempty"`
	VersionID string `json:"version_id,omitempty"`
	SceneID   string `json:"scene_id,omitempty"`
	Quote     string `json:"quote"`
}
type Finding struct {
	ID             string          `json:"id"`
	Title          string          `json:"title"`
	Kind           string          `json:"kind"`
	Severity       string          `json:"severity"`
	Confidence     string          `json:"confidence"`
	Explanation    string          `json:"explanation"`
	Evidence       []CheckEvidence `json:"evidence"`
	TargetRecordID string          `json:"target_record_id,omitempty"`
	Proposal       *Input          `json:"proposal,omitempty"`
}
type CheckResult struct {
	Summary  string    `json:"summary"`
	Findings []Finding `json:"findings"`
}
type CheckInput struct {
	ContextID    string `json:"context_snapshot_id"`
	ModelID      string `json:"model_config_id"`
	ModelVersion int64  `json:"model_config_version"`
}
type Check struct {
	ID        string       `json:"id"`
	Space     string       `json:"space_id"`
	SeriesID  string       `json:"series_id"`
	EpisodeID string       `json:"episode_id"`
	Input     CheckInput   `json:"input"`
	State     string       `json:"state"`
	Provider  string       `json:"provider"`
	Model     string       `json:"model"`
	Actor     string       `json:"created_by"`
	CreatedAt time.Time    `json:"created_at"`
	UpdatedAt time.Time    `json:"updated_at"`
	ErrorCode string       `json:"error_code,omitempty"`
	Result    *CheckResult `json:"result,omitempty"`
}
type CheckSummary struct {
	ID        string    `json:"id"`
	EpisodeID string    `json:"episode_id"`
	ContextID string    `json:"context_snapshot_id"`
	State     string    `json:"state"`
	Model     string    `json:"model"`
	CreatedAt time.Time `json:"created_at"`
	ErrorCode string    `json:"error_code,omitempty"`
}

func SummarizeCheck(c Check) CheckSummary {
	return CheckSummary{c.ID, c.EpisodeID, c.Input.ContextID, c.State, c.Model, c.CreatedAt, c.ErrorCode}
}
func ValidCheckInput(in CheckInput) bool {
	return ID.MatchString(in.ContextID) && Scope.MatchString(in.ModelID) && in.ModelVersion > 0 && in.ModelVersion <= MaxVersion
}
func ValidCheck(c Check) bool {
	if !ID.MatchString(c.ID) || !Scope.MatchString(c.Space) || !ID.MatchString(c.SeriesID) || !ID.MatchString(c.EpisodeID) || !Scope.MatchString(c.Actor) || !ValidCheckInput(c.Input) || c.Provider == "" || c.Model == "" || c.CreatedAt.IsZero() || c.UpdatedAt.Before(c.CreatedAt) {
		return false
	}
	switch c.State {
	case "queued", "running":
		return c.Result == nil && c.ErrorCode == ""
	case "succeeded":
		return c.Result != nil && c.ErrorCode == ""
	case "failed", "unknown", "cancelled":
		return c.Result == nil && c.ErrorCode != ""
	}
	return false
}
func ValidCheckSelection(c Check, s governance.RuntimeSelection) bool {
	return s.TenantID == c.Space && s.ActorID == c.Actor && s.ModelID == c.Input.ModelID && s.Version == c.Input.ModelVersion && s.GatewayID != "" && s.GatewayVersion > 0 && s.FeatureKey == "story_continuity" && s.Capability == "text"
}
func cleanCheckText(s string) bool {
	if strings.TrimSpace(s) == "" || !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if r < 32 && r != '\n' && r != '\t' {
			return false
		}
	}
	return true
}

// ValidateResult assigns finding IDs, checks provenance and normalizes proposals.
// It never saves a formal record or guesses that a belief is objective truth.
func ValidateResult(job string, snapshot ContextSnapshot, in CheckResult) (CheckResult, error) {
	if !ID.MatchString(job) || !ValidSnapshot(snapshot) || snapshot.Script == nil || !cleanCheckText(in.Summary) || in.Findings == nil {
		return CheckResult{}, ErrInput
	}
	out, e := Clone(in)
	if e != nil {
		return out, e
	}
	sources := map[string]ContextItem{}
	for _, v := range snapshot.Context.Included {
		sources[v.ID] = v
	}
	for i := range out.Findings {
		f := &out.Findings[i]
		if f.ID != "" || !cleanCheckText(f.Title) || !cleanCheckText(f.Explanation) || len(f.Evidence) == 0 {
			return CheckResult{}, ErrInput
		}
		switch f.Kind {
		case "contradiction", "knowledge", "chronology", "foreshadow", "character_arc", "ambiguity":
		default:
			return CheckResult{}, ErrInput
		}
		switch f.Severity {
		case "note", "warning", "critical":
		default:
			return CheckResult{}, ErrInput
		}
		switch f.Confidence {
		case "low", "medium", "high":
		default:
			return CheckResult{}, ErrInput
		}
		for _, ev := range f.Evidence {
			if !cleanCheckText(ev.Quote) {
				return CheckResult{}, ErrInput
			}
			if ev.RecordID != "" {
				source, ok := sources[ev.RecordID]
				if !ok || source.VersionID != ev.VersionID || ev.SceneID != "" || !containsQuote(Version{Title: source.Title, Body: source.Body}, ev.Quote) {
					return CheckResult{}, ErrInput
				}
			} else {
				if ev.VersionID != "" || !ID.MatchString(ev.SceneID) {
					return CheckResult{}, ErrInput
				}
				found := false
				for _, scene := range snapshot.Script.Scenes {
					if scene.ID != ev.SceneID {
						continue
					}
					texts := []string{scene.Heading, scene.Action}
					for _, line := range scene.Dialogue {
						texts = append(texts, line.Text)
					}
					for _, text := range texts {
						found = found || strings.Contains(text, ev.Quote)
					}
				}
				if !found {
					return CheckResult{}, ErrInput
				}
			}
		}
		if f.TargetRecordID != "" {
			if _, ok := sources[f.TargetRecordID]; !ok {
				return CheckResult{}, ErrInput
			}
		}
		if f.Proposal != nil {
			normalized, e := Normalize(*f.Proposal)
			if e != nil || normalized.Kind == "cast" || len(normalized.Body.Evidence) > 0 {
				return CheckResult{}, ErrInput
			}
			if f.TargetRecordID != "" {
				v := sources[f.TargetRecordID]
				if normalized.Kind != v.Kind {
					return CheckResult{}, ErrInput
				}
				normalized.ExpectedVersion = v.Number
			} else {
				if normalized.Kind == "bible" {
					return CheckResult{}, ErrInput
				}
				normalized.ExpectedVersion = 0
			}
			// Proposal references may only identify episodes and characters already
			// represented in this pinned context. Full semantic validation is repeated
			// against current data when a human explicitly adopts an edited proposal.
			allowed := map[string]bool{snapshot.EpisodeID: true}
			for _, s := range sources {
				for _, id := range EpisodeRefs(s.Body) {
					allowed[id] = true
				}
			}
			for _, id := range EpisodeRefs(normalized.Body) {
				if !allowed[id] {
					return CheckResult{}, ErrInput
				}
			}
			f.Proposal = &normalized
		} else if f.TargetRecordID != "" {
			return CheckResult{}, ErrInput
		}
		f.ID = TextHash(job + ":" + Digest(struct {
			Index   int
			Finding Finding
		}{i, *f}))[:32]
	}
	return out, nil
}

func containsQuote(v Version, quote string) bool {
	for _, text := range authoredTexts(v) {
		if strings.Contains(text, quote) {
			return true
		}
	}
	return false
}
