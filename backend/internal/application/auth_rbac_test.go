package application

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
)

func TestSpaceTypesPermissionsAndCAS(t *testing.T) {
	dir := t.TempDir()
	auth, err := OpenAuthStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	owner, token, session, err := auth.login("owner@example.test", "synthetic-password", true)
	if err != nil {
		t.Fatal(err)
	}
	member, memberToken, memberSession, err := auth.login("member@example.test", "synthetic-password", true)
	if err != nil {
		t.Fatal(err)
	}
	personal := auth.spaces(owner.ID)[0]["id"]
	h := SecureHandler(testStore(t), "http://localhost:5173", auth, AuthOptions{})
	authRequest(t, h, "PUT", "/api/v1/organizations/"+personal+"/members", `{"email":"member@example.test","role":"editor"}`, token, session.CSRF, 403)
	if auth.role(member.ID, personal) != "" || auth.can(member.ID, personal, "workspace.read") {
		t.Fatal("personal space leaked")
	}
	team := testTeamSpace(t, auth, owner.ID)
	path := "/api/v1/organizations/" + team
	authRequest(t, h, "GET", path+"/access", "", memberToken, "", 403)
	authRequest(t, h, "PUT", path+"/members", `{"email":"member@example.test","role":"editor","expected_version":1}`, token, session.CSRF, 200)
	authRequest(t, h, "PUT", path+"/members", `{"email":"member@example.test","role":"admin","expected_version":1}`, token, session.CSRF, 409)
	authRequest(t, h, "GET", path+"/members", "", memberToken, "", 200)
	authRequest(t, h, "PUT", path+"/members", `{"email":"member@example.test","role":"admin"}`, memberToken, memberSession.CSRF, 403)
	if !auth.can(member.ID, team, "models.execute") || auth.can(member.ID, team, "models.manage") || auth.can(member.ID, team, "unknown") {
		t.Fatal("editor grants")
	}
	authRequest(t, h, "PUT", path+"/members", `{"email":"member@example.test","role":"admin","expected_version":2}`, token, session.CSRF, 200)
	authRequest(t, h, "PUT", path+"/members", `{"email":"owner@example.test","role":"remove"}`, memberToken, memberSession.CSRF, 403)
	authRequest(t, h, "PUT", path+"/members", `{"email":"member@example.test","role":"remove","expected_version":3}`, token, session.CSRF, 200)
	if auth.can(member.ID, team, "workspace.read") {
		t.Fatal("revoked access")
	}
	authRequest(t, h, "GET", path+"/access", "", memberToken, "", 403)
	reloaded, err := OpenAuthStore(dir)
	if err != nil || reloaded.role(owner.ID, team) != "owner" || reloaded.role(member.ID, team) != "" {
		t.Fatal("persisted membership mismatch", err)
	}
	// Session responses expose effective permissions rather than role-only guesses.
	var access map[string]any
	w := authRequest(t, h, "GET", path+"/access", "", token, "", 200)
	if json.Unmarshal(w.Body.Bytes(), &access) != nil || access["version"] != float64(4) || access["type"] != "team" {
		t.Fatal(w.Body.String())
	}
}

func TestPostgresRBACRelationsMigrationAndRevokedGrants(t *testing.T) {
	db := postgresTestDB(t)
	a, err := OpenPostgresAuthStore(t.TempDir(), db)
	if err != nil {
		t.Fatal(err)
	}
	b, err := OpenPostgresAuthStore(t.TempDir(), db)
	if err != nil {
		t.Fatal(err)
	}
	owner, _, _, err := a.login("rbac@example.test", "synthetic-password", true)
	if err != nil {
		t.Fatal(err)
	}
	member, _, _, err := a.login("rbac-member@example.test", "synthetic-password", true)
	if err != nil {
		t.Fatal(err)
	}
	team := testTeamSpace(t, a, owner.ID)
	a.mu.Lock()
	d := a.clone()
	o := d.Organizations[team]
	o.Members[member.ID] = "editor"
	d.Organizations[team] = o
	err = a.commit(d)
	a.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if !b.can(member.ID, team, "models.execute") {
		t.Fatal("cross-instance membership missing")
	}
	if _, err = db.Exec(`DELETE FROM studio_rbac_role_permissions WHERE role_id='editor' AND permission_id='models.execute'`); err != nil {
		t.Fatal(err)
	}
	if b.can(member.ID, team, "models.execute") {
		t.Fatal("revoked DB grant ignored")
	}
	if err = InitPostgres(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	if b.can(member.ID, team, "models.execute") {
		t.Fatal("migration resurrected revoked grant")
	}
	if _, err = db.Exec(`DELETE FROM studio_space_memberships WHERE space_id=$1 AND user_id=$2`, team, member.ID); err != nil {
		t.Fatal(err)
	}
	if err = InitPostgres(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	if b.role(member.ID, team) != "" {
		t.Fatal("migration resurrected revoked member")
	}
	if _, err = db.Exec(`DELETE FROM studio_space_memberships WHERE space_id=$1 AND user_id=$2`, team, owner.ID); err == nil {
		t.Fatal("owner removal accepted")
	}
	if b.role(owner.ID, team) != "owner" {
		t.Fatal("failed mutation lost owner")
	}
}

func TestPostgresRBACLegacyBackfillPreservesTeams(t *testing.T) {
	db := postgresTestDB(t)
	a, err := OpenPostgresAuthStore(t.TempDir(), db)
	if err != nil {
		t.Fatal(err)
	}
	owner, _, _, err := a.login("legacy-rbac@example.test", "synthetic-password", true)
	if err != nil {
		t.Fatal(err)
	}
	member, _, _, err := a.login("legacy-rbac-member@example.test", "synthetic-password", true)
	if err != nil {
		t.Fatal(err)
	}
	team := testTeamSpace(t, a, owner.ID)
	a.mu.Lock()
	d := a.clone()
	o := d.Organizations[team]
	o.Members[member.ID] = "viewer"
	d.Organizations[team] = o
	err = a.commit(d)
	a.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	// Recreate an older deployment: JSON organizations only, without explicit types.
	if _, err = db.Exec(`DROP TABLE studio_space_memberships,studio_space_access,studio_rbac_role_permissions,studio_rbac_permissions,studio_rbac_roles CASCADE; UPDATE studio_organizations SET payload=payload-'type'-'version'`); err != nil {
		t.Fatal(err)
	}
	if err = InitPostgres(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	b, err := OpenPostgresAuthStore(t.TempDir(), db)
	if err != nil {
		t.Fatal(err)
	}
	if b.role(member.ID, team) != "viewer" || b.spaces(owner.ID)[0]["type"] != "team" {
		t.Fatal("legacy team membership lost or inferred personal")
	}
	if _, _, _, err = b.login("legacy-rbac@example.test", "synthetic-password", false); err != nil {
		t.Fatal(err)
	}
	if b.spaces(owner.ID)[0]["type"] != "personal" || b.role(member.ID, team) != "viewer" {
		t.Fatal("personal bootstrap changed existing team")
	}
}

func TestFileRBACRejectsAmbiguousOwners(t *testing.T) {
	dir := t.TempDir()
	a, err := OpenAuthStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	owner, _, _, err := a.login("file-owner@example.test", "synthetic-password", true)
	if err != nil {
		t.Fatal(err)
	}
	member, _, _, err := a.login("file-member@example.test", "synthetic-password", true)
	if err != nil {
		t.Fatal(err)
	}
	team := testTeamSpace(t, a, owner.ID)
	d := a.clone()
	o := d.Organizations[team]
	o.Members[member.ID] = "owner"
	d.Organizations[team] = o
	raw, _ := json.Marshal(d)
	if err = os.WriteFile(a.path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = OpenAuthStore(dir); err == nil {
		t.Fatal("ambiguous extra owner accepted")
	}
}

func TestPostgresRBACExportUsesCurrentRelationsAndRejectsLossyGrants(t *testing.T) {
	db := postgresTestDB(t)
	a, err := OpenPostgresAuthStore(t.TempDir(), db)
	if err != nil {
		t.Fatal(err)
	}
	owner, _, _, err := a.login("export-owner@example.test", "synthetic-password", true)
	if err != nil {
		t.Fatal(err)
	}
	member, _, _, err := a.login("export-member@example.test", "synthetic-password", true)
	if err != nil {
		t.Fatal(err)
	}
	team := testTeamSpace(t, a, owner.ID)
	a.mu.Lock()
	d := a.clone()
	o := d.Organizations[team]
	o.Members[member.ID] = "editor"
	d.Organizations[team] = o
	err = a.commit(d)
	a.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`DELETE FROM studio_space_memberships WHERE space_id=$1 AND user_id=$2`, team, member.ID); err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	raw, _, err := readPostgresScopeTx(context.Background(), tx, "auth", authSnapshot{Version: 1})
	if err != nil {
		t.Fatal(err)
	}
	var snapshot authSnapshot
	if json.Unmarshal(raw, &snapshot) != nil || snapshot.Organizations[team].Members[member.ID] != "" {
		t.Fatal("export resurrected revoked membership")
	}
	if err = validateFileExportGrants(context.Background(), tx); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(`DELETE FROM studio_rbac_role_permissions WHERE role_id='editor' AND permission_id='models.execute'`); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(validateFileExportGrants(context.Background(), tx), errRBACFileExport) {
		t.Fatal("lossy role export accepted")
	}
}
