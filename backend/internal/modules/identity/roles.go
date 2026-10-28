package identity

func ValidRole(role string) bool {
	return role == "owner" || role == "admin" || role == "editor" || role == "viewer"
}
