package application

import (
	"net/http"
	"sort"
)

type productionMember struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func (a *AuthStore) productionMembers(space string) []productionMember {
	a.mu.Lock()
	defer a.mu.Unlock()
	members := []productionMember{}
	for id, role := range a.data.Organizations[space].Members {
		if role != "viewer" {
			members = append(members, productionMember{id, a.data.Users[id].Email})
		}
	}
	sort.Slice(members, func(i, j int) bool { return members[i].ID < members[j].ID })
	return members
}
func reviewActors(r *http.Request) (string, []productionMember) {
	if scope, ok := r.Context().Value(authScopeKey{}).(authScope); ok {
		if scope.reviewMembers != nil {
			return scope.UserID, scope.reviewMembers()
		}
		return scope.UserID, []productionMember{{scope.UserID, scope.UserID}}
	}
	return "demo-user", []productionMember{{"demo-user", "Demo"}}
}
