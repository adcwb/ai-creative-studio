package story

import (
	"strings"
	"time"
	"unicode/utf8"
)

type DecisionInput struct {
	Action             string `json:"action"`
	Reason             string `json:"reason"`
	ReviewSHA256       string `json:"review_sha256"`
	AcknowledgeChanged bool   `json:"acknowledge_changed"`
	Proposal           *Input `json:"proposal,omitempty"`
}
type Decision struct {
	ID             string    `json:"id"`
	CheckID        string    `json:"check_id"`
	EpisodeID      string    `json:"episode_id"`
	FindingID      string    `json:"finding_id"`
	Action         string    `json:"action"`
	Reason         string    `json:"reason"`
	ReviewSHA256   string    `json:"review_sha256"`
	ContextID      string    `json:"context_snapshot_id"`
	TargetRecordID string    `json:"target_record_id,omitempty"`
	VersionID      string    `json:"version_id,omitempty"`
	Actor          string    `json:"created_by"`
	CreatedAt      time.Time `json:"created_at"`
}

func NormalizeDecision(in DecisionInput) (DecisionInput, error) {
	in.Reason = strings.TrimSpace(in.Reason)
	if !Hash.MatchString(in.ReviewSHA256) || !utf8.ValidString(in.Reason) {
		return in, ErrInput
	}
	for _, r := range in.Reason {
		if r < 32 && r != '\n' && r != '\t' {
			return in, ErrInput
		}
	}
	switch in.Action {
	case "adopt":
		if in.Proposal == nil {
			return in, ErrInput
		}
		p, e := Normalize(*in.Proposal)
		if e != nil || p.Kind == "cast" {
			return in, ErrInput
		}
		in.Proposal = &p
	case "reject", "intentional":
		if in.Reason == "" || in.Proposal != nil {
			return in, ErrInput
		}
	default:
		return in, ErrInput
	}
	return in, nil
}
func DecisionID(check, finding string) string {
	return TextHash("story-decision-v1:" + check + ":" + finding)[:32]
}
func AdoptionTarget(check string, f Finding) string {
	if f.TargetRecordID != "" {
		return f.TargetRecordID
	}
	return TextHash("story-adoption-v1:" + check + ":" + f.ID)[:32]
}
func ValidDecision(d Decision) bool {
	if !ID.MatchString(d.EpisodeID) || !ID.MatchString(d.CheckID) || !ID.MatchString(d.FindingID) || d.ID != DecisionID(d.CheckID, d.FindingID) || !Hash.MatchString(d.ReviewSHA256) || !ID.MatchString(d.ContextID) || !Scope.MatchString(d.Actor) || d.CreatedAt.IsZero() {
		return false
	}
	switch d.Action {
	case "adopt":
		return ID.MatchString(d.TargetRecordID) && ID.MatchString(d.VersionID)
	case "reject", "intentional":
		return cleanCheckText(d.Reason) && d.TargetRecordID == "" && d.VersionID == ""
	}
	return false
}
