package identity

import "sort"

// Built-in roles are not editable by tenant requests. Unknown permissions deny.
var roleGrants = map[string][]string{
	"owner":  {"workspace.read", "workspace.write", "workspace.manage", "members.read", "members.manage", "members.manage_admin", "models.manage", "models.execute", "usage.read"},
	"admin":  {"workspace.read", "workspace.write", "workspace.manage", "members.read", "members.manage", "models.manage", "models.execute", "usage.read"},
	"editor": {"workspace.read", "workspace.write", "members.read", "models.execute", "usage.read"},
	"viewer": {"workspace.read", "members.read", "usage.read"},
}

func Permissions(role string) []string {
	values := append([]string{}, roleGrants[role]...)
	sort.Strings(values)
	return values
}
func Allows(role, permission string) bool {
	for _, value := range roleGrants[role] {
		if value == permission {
			return true
		}
	}
	return false
}
func Roles() []string { return []string{"owner", "admin", "editor", "viewer"} }
