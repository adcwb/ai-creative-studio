package library

import (
	"encoding/json"
	"sort"
	"time"
)

// A current, creator-attested use record. It is independent of content versions
// and never represents a verified legal certificate or a provider capability.
type UseAuthorization struct {
	Revision   int64      `json:"revision"`
	Status     string     `json:"status"`
	VersionIDs []string   `json:"version_ids"`
	Purposes   []string   `json:"purposes"`
	Holder     string     `json:"rights_holder"`
	Basis      string     `json:"basis"`
	Evidence   string     `json:"evidence"`
	Attested   bool       `json:"attested"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	Reason     string     `json:"reason"`
	DeclaredBy string     `json:"declared_by,omitempty"`
	DeclaredAt *time.Time `json:"declared_at,omitempty"`
	UpdatedBy  string     `json:"updated_by"`
	UpdatedAt  time.Time  `json:"updated_at"`
}
type AuthorizationInput struct {
	Expected   int64      `json:"expected_revision"`
	Status     string     `json:"status"`
	VersionIDs []string   `json:"version_ids,omitempty"`
	Purposes   []string   `json:"purposes,omitempty"`
	Holder     string     `json:"rights_holder,omitempty"`
	Basis      string     `json:"basis,omitempty"`
	Evidence   string     `json:"evidence,omitempty"`
	Attested   bool       `json:"attested,omitempty"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	Reason     string     `json:"reason"`
}

func NormalizeAuthorization(in AuthorizationInput) (AuthorizationInput, error) {
	b, e := json.Marshal(in)
	if e != nil {
		return in, ErrInput
	}
	if len(b) > 256<<10 {
		return in, ErrPayload
	}
	var out AuthorizationInput
	if json.Unmarshal(b, &out) != nil {
		return in, ErrInput
	}
	if out.Expected < 0 || out.Expected >= 9007199254740991 {
		return out, ErrInput
	}
	if out.Reason, e = text(out.Reason, 2000, out.Status == "revoked"); e != nil {
		return out, e
	}
	switch out.Status {
	case "revoked":
		if len(out.VersionIDs) > 0 || len(out.Purposes) > 0 || out.Holder != "" || out.Basis != "" || out.Evidence != "" || out.Attested || out.ExpiresAt != nil {
			return out, ErrInput
		}
		out.VersionIDs = nil
		out.Purposes = nil
	case "granted":
		if !out.Attested || len(out.VersionIDs) == 0 || len(out.Purposes) == 0 {
			return out, ErrInput
		}
		if out.Holder, e = text(out.Holder, 200, true); e != nil {
			return out, e
		}
		if out.Evidence, e = text(out.Evidence, 4000, true); e != nil {
			return out, e
		}
		if out.Basis != "own_work" && out.Basis != "licensed" && out.Basis != "consent" {
			return out, ErrInput
		}
		seen := map[string]bool{}
		for _, id := range out.VersionIDs {
			if !IDPattern.MatchString(id) || seen[id] {
				return out, ErrInput
			}
			seen[id] = true
		}
		sort.Strings(out.VersionIDs)
		seen = map[string]bool{}
		for _, p := range out.Purposes {
			if !AuthorizationPurpose(p) || seen[p] {
				return out, ErrInput
			}
			seen[p] = true
		}
		sort.Strings(out.Purposes)
		if out.ExpiresAt != nil {
			v := out.ExpiresAt.UTC()
			out.ExpiresAt = &v
		}
	default:
		return out, ErrInput
	}
	return out, nil
}
func AuthorizationPurpose(p string) bool {
	switch p {
	case "prompt_context", "image_reference", "video_reference", "voice_reference", "speech_preset":
		return true
	}
	return false
}
func ApplyAuthorization(a Asset, in AuthorizationInput, actor string, now time.Time) (UseAuthorization, error) {
	in, e := NormalizeAuthorization(in)
	if e != nil {
		return UseAuthorization{}, e
	}
	if !IDPattern.MatchString(actor) || now.IsZero() {
		return UseAuthorization{}, ErrInput
	}
	old := UseAuthorization{VersionIDs: []string{}, Purposes: []string{}}
	if a.UseAuthorization != nil {
		b, _ := json.Marshal(a.UseAuthorization)
		if json.Unmarshal(b, &old) != nil || !ValidAuthorization(&old) {
			return old, ErrInput
		}
	}
	if old.Revision != in.Expected {
		return old, ErrConflict
	}
	if in.Status == "granted" {
		if a.Status != "active" {
			return old, ErrArchived
		}
		if in.ExpiresAt != nil && !in.ExpiresAt.After(now) {
			return old, ErrInput
		}
		for _, purpose := range in.Purposes {
			if a.Kind == "voice" && (purpose == "image_reference" || purpose == "video_reference") || a.Kind != "voice" && (purpose == "voice_reference" || purpose == "speech_preset") {
				return old, ErrInput
			}
		}
		declared := now.UTC()
		old = UseAuthorization{Revision: old.Revision, VersionIDs: in.VersionIDs, Purposes: in.Purposes, Holder: in.Holder, Basis: in.Basis, Evidence: in.Evidence, Attested: true, ExpiresAt: in.ExpiresAt, DeclaredBy: actor, DeclaredAt: &declared}
	}
	old.Revision++
	old.Status = in.Status
	old.Reason = in.Reason
	old.UpdatedBy = actor
	old.UpdatedAt = now.UTC()
	return old, nil
}
func ValidAuthorization(a *UseAuthorization) bool {
	if a == nil {
		return true
	}
	if a.Revision < 1 || a.Revision > 9007199254740991 || !IDPattern.MatchString(a.UpdatedBy) || a.UpdatedAt.IsZero() {
		return false
	}
	if a.Status != "granted" && a.Status != "revoked" {
		return false
	}
	if a.Status == "revoked" && a.Reason == "" {
		return false
	}
	if !a.Attested {
		return a.Status == "revoked" && len(a.VersionIDs) == 0 && len(a.Purposes) == 0 && a.DeclaredBy == "" && a.DeclaredAt == nil && a.Holder == "" && a.Basis == "" && a.Evidence == "" && a.ExpiresAt == nil
	}
	if !IDPattern.MatchString(a.DeclaredBy) || a.DeclaredAt == nil || a.DeclaredAt.IsZero() {
		return false
	}
	_, e := NormalizeAuthorization(AuthorizationInput{Expected: a.Revision - 1, Status: "granted", VersionIDs: a.VersionIDs, Purposes: a.Purposes, Holder: a.Holder, Basis: a.Basis, Evidence: a.Evidence, Attested: a.Attested, ExpiresAt: a.ExpiresAt, Reason: a.Reason})
	return e == nil
}
func (a *UseAuthorization) Allows(version, purpose string, now time.Time) bool {
	if a == nil || !ValidAuthorization(a) || a.Status != "granted" || a.ExpiresAt != nil && !a.ExpiresAt.After(now) {
		return false
	}
	versionOK, purposeOK := false, false
	for _, id := range a.VersionIDs {
		versionOK = versionOK || id == version
	}
	for _, p := range a.Purposes {
		purposeOK = purposeOK || p == purpose
	}
	return versionOK && purposeOK
}
