package identity

import "testing"

func TestCustomRolePermissionsAndScope(t *testing.T) {
	role := CustomRole{ID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Name: "剪辑协作", Base: "editor", Version: 1, Permissions: []string{"workspace.write", "workspace.read"}}
	normalized, err := NormalizeCustomRole(role)
	if err != nil || normalized.Permissions[0] != "workspace.read" {
		t.Fatal(normalized, err)
	}
	for _, grants := range [][]string{{"workspace.read", "models.manage"}, {"workspace.read", "members.manage_admin"}, {"models.execute"}, {"workspace.read", "models.execute"}, {"workspace.read", "workspace.read"}} {
		bad := role
		bad.Permissions = grants
		if _, err = NormalizeCustomRole(bad); err == nil {
			t.Fatal("invalid role accepted", grants)
		}
	}
	org := AuthOrganization{ID: "team", OwnerID: "owner", Members: map[string]string{"owner": "owner", "editor": "editor"}, CustomRoles: map[string]CustomRole{role.ID: normalized}, MemberRoles: map[string]string{"editor": role.ID}}
	if err = ValidateCustomRoles(org); err != nil || !EffectiveAllows(org, "editor", "workspace.write") || EffectiveAllows(org, "editor", "models.execute") {
		t.Fatal("profile not effective", err)
	}
	org.MemberRoles["owner"] = role.ID
	if ValidateCustomRoles(org) == nil {
		t.Fatal("custom owner accepted")
	}
}
