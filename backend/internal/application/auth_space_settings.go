package application

import (
	"net/http"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Called while auth.mu holds the current database-backed RBAC view. The name
// and membership share the existing organization CAS and transaction; no new
// plan, quota, billing state, or personal-space sharing capability is created.
func (s *AuthStore) spaceSettings(w http.ResponseWriter, r *http.Request, user AuthUser, org AuthOrganization, action string) {
	if spaceType(org) != "team" {
		problem(w, 403, "role_denied", "个人空间不支持团队管理操作")
		return
	}
	if action == "settings" && r.Method != "PATCH" || action == "leave" && r.Method != "POST" {
		problem(w, 405, "method_not_allowed", "方法不支持")
		return
	}
	if action == "settings" && !s.canLocked(user.ID, org.ID, "members.manage") {
		problem(w, 403, "role_denied", "当前角色不能管理团队信息")
		return
	}
	if action == "leave" && org.OwnerID == user.ID {
		problem(w, 403, "owner_cannot_leave", "团队所有者不能退出，请先保留团队管理责任")
		return
	}
	var in struct {
		Name            string `json:"name"`
		ExpectedVersion int64  `json:"expected_version"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.ExpectedVersion < 1 {
		problem(w, 400, "invalid_input", "请先读取团队版本")
		return
	}
	if in.ExpectedVersion != spaceVersion(org) {
		problem(w, 409, "membership_conflict", "团队信息已变化，请重新读取后确认")
		return
	}
	if action == "settings" {
		in.Name = strings.TrimSpace(in.Name)
		if in.Name == "" || utf8.RuneCountInString(in.Name) > 80 || strings.IndexFunc(in.Name, unicode.IsControl) >= 0 {
			problem(w, 400, "invalid_input", "团队名称需要1–80个字符，不能包含控制字符")
			return
		}
	}
	if !beginAuthAudit(w, r) {
		return
	}
	d := s.clone()
	changed := d.Organizations[org.ID]
	changed.Version = spaceVersion(org) + 1
	if action == "settings" {
		changed.Name = in.Name
	} else {
		delete(changed.Members, user.ID)
	}
	d.Organizations[org.ID] = changed
	if err := s.commit(d); err != nil {
		problem(w, 503, "auth_storage_error", "团队操作结果未确认，请重新读取团队状态")
		return
	}
	respond(w, 200, map[string]any{"id": org.ID, "version": changed.Version, "updated": true})
}
