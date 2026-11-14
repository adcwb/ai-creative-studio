package application

import (
	"net/http"
	"reflect"
	"sort"
	"strings"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/identity"
)

// AuthStore.mu is held across current permission, CAS and relational commit.
func (s *AuthStore) spaceRoles(w http.ResponseWriter, r *http.Request, user AuthUser, org AuthOrganization) {
	if spaceType(org) != "team" {
		problem(w, 403, "role_denied", "个人空间不支持自定义协作角色")
		return
	}
	view := func() {
		items := []identity.CustomRole{}
		for _, role := range org.CustomRoles {
			items = append(items, role)
		}
		sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
		options := map[string][]string{}
		for _, base := range []string{"admin", "editor", "viewer"} {
			options[base] = identity.Permissions(base)
		}
		respond(w, 200, map[string]any{"id": org.ID, "items": items, "version": spaceVersion(org), "permission_options": options})
	}
	if r.Method == "GET" {
		view()
		return
	}
	if !s.canLocked(user.ID, org.ID, "members.manage_admin") {
		problem(w, 403, "owner_required", "只有团队所有者可以定义权限角色")
		return
	}
	if r.Method != "POST" && r.Method != "PUT" && r.Method != "DELETE" {
		problem(w, 405, "method_not_allowed", "方法不支持")
		return
	}
	var in struct {
		ID          string   `json:"id"`
		Name        string   `json:"name"`
		Base        string   `json:"base_role"`
		Permissions []string `json:"permissions"`
		Expected    int64    `json:"expected_version"`
		RoleVersion int64    `json:"expected_role_version"`
	}
	if !decode(w, r, &in) {
		return
	}
	role, exists := org.CustomRoles[in.ID]
	if r.Method == "DELETE" {
		if !exists {
			if identity.ValidCustomRoleID(in.ID) && in.Expected >= 1 && in.RoleVersion >= 1 {
				view()
				return
			}
			problem(w, 400, "invalid_input", "角色标识或版本无效")
			return
		}
		for _, id := range org.MemberRoles {
			if id == in.ID {
				problem(w, 409, "role_in_use", "此角色仍分配给成员，请先调整成员权限")
				return
			}
		}
	} else {
		proposed, err := identity.NormalizeCustomRole(identity.CustomRole{ID: in.ID, Name: in.Name, Base: in.Base, Version: 1, Permissions: in.Permissions})
		if err != nil {
			problem(w, 400, "invalid_input", "角色名称或权限无效，必须保留工作空间读取权限")
			return
		}
		if r.Method == "POST" && exists {
			if role.Version == 1 && role.Name == proposed.Name && role.Base == proposed.Base && reflect.DeepEqual(role.Permissions, proposed.Permissions) {
				view()
				return
			}
			problem(w, 409, "membership_conflict", "角色已存在或已修改，请重新读取后确认")
			return
		}
		if r.Method == "PUT" && (!exists || role.Base != proposed.Base) {
			problem(w, 409, "membership_conflict", "角色不存在或权限范围已变化，请重新读取")
			return
		}
		if r.Method == "PUT" && role.Version == in.RoleVersion+1 && role.Name == proposed.Name && reflect.DeepEqual(role.Permissions, proposed.Permissions) {
			view()
			return
		}
		for _, permission := range proposed.Permissions {
			if !s.canLocked(user.ID, org.ID, permission) {
				problem(w, 403, "role_denied", "不能定义超出自己当前权限的角色")
				return
			}
		}
		for id, other := range org.CustomRoles {
			if id != in.ID && strings.EqualFold(other.Name, proposed.Name) {
				problem(w, 409, "membership_conflict", "团队已有同名角色，请重新读取后选择")
				return
			}
		}
		if r.Method == "POST" {
			if in.RoleVersion != 0 {
				problem(w, 400, "invalid_input", "新角色版本无效")
				return
			}
		} else {
			if role.Version >= 9007199254740991 {
				problem(w, 409, "membership_conflict", "角色版本已达到可表示边界，请创建替代角色")
				return
			}
			proposed.Version = role.Version + 1
		}
		role = proposed
	}
	if in.Expected < 1 || in.Expected != spaceVersion(org) || r.Method != "POST" && in.RoleVersion != org.CustomRoles[in.ID].Version {
		problem(w, 409, "membership_conflict", "团队或角色权限已变化，请重新读取后保存")
		return
	}
	if !beginAuthAudit(w, r) {
		return
	}
	d := s.clone()
	changed := d.Organizations[org.ID]
	changed.Version = spaceVersion(org) + 1
	if changed.CustomRoles == nil {
		changed.CustomRoles = map[string]identity.CustomRole{}
	}
	if r.Method == "DELETE" {
		delete(changed.CustomRoles, in.ID)
	} else {
		changed.CustomRoles[in.ID] = role
	}
	d.Organizations[org.ID] = changed
	if err := s.commit(d); err != nil {
		problem(w, 503, "auth_storage_error", "角色保存结果未确认，请重新读取团队权限")
		return
	}
	org = changed
	view()
}
