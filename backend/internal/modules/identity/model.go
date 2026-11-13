package identity

type AuthUser struct {
	ID    string `json:"id"`
	Email string `json:"email"`
}

type AuthOrganization struct {
	CustomRoles map[string]CustomRole `json:"custom_roles,omitempty"`
	MemberRoles map[string]string     `json:"member_roles,omitempty"`
	Type        string                `json:"type,omitempty"`
	Version     int64                 `json:"version,omitempty"`
	ID          string                `json:"id"`
	Name        string                `json:"name"`
	OwnerID     string                `json:"owner_id"`
	Members     map[string]string     `json:"members"`
}

// Base is a compatibility/security ceiling, not an alias: Permissions is the
// actual editable grant set. Ownership is never assignable through a custom role.
type CustomRole struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Base        string   `json:"base_role"`
	Version     int64    `json:"version"`
	Permissions []string `json:"permissions"`
}
