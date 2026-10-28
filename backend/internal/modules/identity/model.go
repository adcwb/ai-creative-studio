package identity

type AuthUser struct {
	ID    string `json:"id"`
	Email string `json:"email"`
}

type AuthOrganization struct {
	ID      string            `json:"id"`
	Name    string            `json:"name"`
	OwnerID string            `json:"owner_id"`
	Members map[string]string `json:"members"`
}
