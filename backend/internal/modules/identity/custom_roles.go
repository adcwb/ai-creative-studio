package identity

import (
	"errors"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

var ErrCustomRole = errors.New("invalid custom role")
var customRoleID = regexp.MustCompile(`^[a-f0-9]{32}$`)

func ValidCustomRoleID(id string) bool { return customRoleID.MatchString(id) }

func NormalizeCustomRole(v CustomRole) (CustomRole, error) {
	v.Name = strings.TrimSpace(v.Name)
	if !customRoleID.MatchString(v.ID) || v.Version < 1 || v.Version > 9007199254740991 || v.Name == "" || utf8.RuneCountInString(v.Name) > 80 || strings.IndexFunc(v.Name, unicode.IsControl) >= 0 || v.Base == "owner" || !ValidRole(v.Base) {
		return v, ErrCustomRole
	}
	seen := map[string]bool{}
	v.Permissions = append([]string(nil), v.Permissions...)
	for _, p := range v.Permissions {
		if seen[p] || !Allows(v.Base, p) {
			return v, ErrCustomRole
		}
		seen[p] = true
	}
	if !seen["workspace.read"] {
		return v, ErrCustomRole
	}
	for _, p := range []string{"workspace.manage", "members.manage", "models.manage", "models.execute"} {
		if seen[p] && !seen["workspace.write"] {
			return v, ErrCustomRole
		}
	}
	if seen["members.manage"] && !seen["members.read"] {
		return v, ErrCustomRole
	}
	sort.Strings(v.Permissions)
	return v, nil
}
func ValidateCustomRoles(o AuthOrganization) error {
	if o.Type == "personal" && (len(o.CustomRoles) > 0 || len(o.MemberRoles) > 0) {
		return ErrCustomRole
	}
	for id, r := range o.CustomRoles {
		v, err := NormalizeCustomRole(r)
		if err != nil || id != r.ID || len(v.Permissions) != len(r.Permissions) {
			return ErrCustomRole
		}
		for i := range v.Permissions {
			if v.Permissions[i] != r.Permissions[i] {
				return ErrCustomRole
			}
		}
	}
	for user, id := range o.MemberRoles {
		r, ok := o.CustomRoles[id]
		if !ok || user == o.OwnerID || o.Members[user] != r.Base {
			return ErrCustomRole
		}
	}
	return nil
}
func EffectivePermissions(o AuthOrganization, user string) []string {
	base := Permissions(o.Members[user])
	id := o.MemberRoles[user]
	if id == "" {
		return base
	}
	r, ok := o.CustomRoles[id]
	if !ok || r.Base != o.Members[user] || user == o.OwnerID {
		return []string{}
	}
	permissions := []string{}
	for _, p := range base {
		for _, grant := range r.Permissions {
			if p == grant {
				permissions = append(permissions, p)
				break
			}
		}
	}
	return permissions
}
func EffectiveAllows(o AuthOrganization, user, permission string) bool {
	for _, p := range EffectivePermissions(o, user) {
		if p == permission {
			return true
		}
	}
	return false
}
