package application

import "net/http"

// Logical dissolution preserves owned history for export. It removes members
// and execution/write authority instead of racing deletion across independent
// media, authorization, journal and provider stores.
func (s *AuthStore) teamClosure(w http.ResponseWriter, r *http.Request, user AuthUser, org AuthOrganization, action string) {
	if r.Method != "POST" {
		problem(w, 405, "method_not_allowed", "方法不支持")
		return
	}
	if spaceType(org) != "team" || org.OwnerID != user.ID {
		problem(w, 403, "owner_required", "只有团队所有者可以关闭或重新开放团队")
		return
	}
	var in struct {
		Expected  int64  `json:"expected_version"`
		Name      string `json:"name"`
		Confirmed bool   `json:"confirmed"`
	}
	if !decode(w, r, &in) {
		return
	}
	if !in.Confirmed || in.Name != org.Name || in.Expected < 1 {
		problem(w, 400, "invalid_input", "请输入完整团队名称并明确确认此操作")
		return
	}
	closed := action == "close"
	if org.Closed == closed && spaceVersion(org) == in.Expected+1 {
		respond(w, 200, map[string]any{"id": org.ID, "version": spaceVersion(org), "closed": org.Closed})
		return
	}
	if spaceVersion(org) != in.Expected || org.Closed == closed {
		problem(w, 409, "membership_conflict", "团队状态已变化，请重新读取后确认")
		return
	}
	if !beginAuthAudit(w, r) {
		return
	}
	d := s.clone()
	next := d.Organizations[org.ID]
	next.Closed = closed
	next.Version = spaceVersion(org) + 1
	if closed {
		next.Members = map[string]string{user.ID: "owner"}
		next.MemberRoles = nil
	}
	d.Organizations[org.ID] = next
	if e := s.commit(d); e != nil {
		problem(w, 503, "auth_storage_error", "团队操作结果未确认，请重新读取团队状态")
		return
	}
	respond(w, 200, map[string]any{"id": org.ID, "version": next.Version, "closed": closed})
}
