package application

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/identity"
)

func TestCustomRolesAndOwnershipFileAndPostgres(t *testing.T) {
	for _, mode := range []string{"file", "postgres"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			a, err := OpenAuthStore(dir)
			if err != nil {
				t.Fatal(err)
			}
			var remote *AuthStore
			if mode == "postgres" {
				db := postgresTestDB(t)
				a, err = OpenPostgresAuthStore(dir, db)
				if err != nil {
					t.Fatal(err)
				}
				remote, err = OpenPostgresAuthStore(t.TempDir(), db)
				if err != nil {
					t.Fatal(err)
				}
			}
			owner, token, session, err := a.login("roles-owner@example.test", "synthetic-password", true)
			if err != nil {
				t.Fatal(err)
			}
			member, memberToken, memberSession, err := a.login("roles-editor@example.test", "synthetic-password", true)
			if err != nil {
				t.Fatal(err)
			}
			h := SecureHandler(testStore(t), "http://localhost:5173", a, AuthOptions{})
			team := testTeamSpace(t, a, owner.ID)
			path := "/api/v1/organizations/" + team
			authRequest(t, h, "PUT", path+"/members", testMemberInput(t, a, member.Email, "editor", 1), token, session.CSRF, 200)
			roleID := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
			permissions := []string{"workspace.read", "workspace.write", "members.read", "models.execute"}
			input := map[string]any{"id": roleID, "name": "分镜创作者", "base_role": "editor", "permissions": permissions, "expected_version": 2, "expected_role_version": 0}
			authRequest(t, h, "POST", path+"/roles", projectJSON(t, input), memberToken, memberSession.CSRF, 403)
			authRequest(t, h, "POST", path+"/roles", projectJSON(t, input), token, "", 403)
			bad := map[string]any{"id": roleID, "name": "越权", "base_role": "editor", "permissions": []string{"workspace.read", "members.manage_admin"}, "expected_version": 2}
			authRequest(t, h, "POST", path+"/roles", projectJSON(t, bad), token, session.CSRF, 400)
			authRequest(t, h, "POST", path+"/roles", projectJSON(t, input), token, session.CSRF, 200)
			// Exact stable-ID creation recovery is read-only even after the space advanced.
			authRequest(t, h, "POST", path+"/roles", projectJSON(t, input), token, session.CSRF, 200)
			assignment := map[string]any{"user_id": member.ID, "email": member.Email, "role": "editor", "custom_role_id": roleID, "expected_version": 3}
			authRequest(t, h, "PUT", path+"/members", projectJSON(t, assignment), token, session.CSRF, 200)
			if mode == "postgres" {
				var stored string
				if err = a.pg.db.QueryRow(`SELECT role_id FROM studio_space_memberships WHERE space_id=$1 AND user_id=$2`, team, member.ID).Scan(&stored); err != nil || stored != "custom" || validRole(stored) {
					t.Fatal("legacy SQL reader could grant the compatibility ceiling", stored, err)
				}
			} else {
				raw, err := os.ReadFile(a.path)
				var envelope struct{ Version int }
				if err != nil || json.Unmarshal(raw, &envelope) != nil || envelope.Version != 2 {
					t.Fatal("old file reader would accept custom policy", err, envelope)
				}
			}
			if !a.can(member.ID, team, "models.execute") || a.can(member.ID, team, "models.manage") {
				t.Fatal("custom effective grants")
			}
			other := testTeamSpace(t, a, owner.ID)
			if mode == "postgres" {
				tx, err := a.pg.db.BeginTx(context.Background(), nil)
				if err != nil {
					t.Fatal(err)
				}
				_, err = tx.Exec(`INSERT INTO studio_space_memberships(space_id,user_id,role_id,space_kind,space_owner_id)VALUES($1,$2,'custom','team',$3)`, other, member.ID, owner.ID)
				if err != nil {
					tx.Rollback()
					t.Fatal(err)
				}
				_, err = tx.Exec(`INSERT INTO studio_space_member_roles(space_id,user_id,role_id,base_role)VALUES($1,$2,$3,'editor')`, other, member.ID, roleID)
				if err != nil {
					tx.Rollback()
					t.Fatal(err)
				}
				if err = tx.Commit(); err == nil {
					t.Fatal("database accepted cross-space role assignment")
				}
			}
			assignment["expected_version"] = 1
			authRequest(t, h, "PUT", "/api/v1/organizations/"+other+"/members", projectJSON(t, assignment), token, session.CSRF, 409)
			assignment["expected_version"] = 0
			authRequest(t, h, "PUT", path+"/members", projectJSON(t, assignment), token, session.CSRF, 409)
			authRequest(t, h, "DELETE", path+"/roles", projectJSON(t, map[string]any{"id": roleID, "expected_version": 4, "expected_role_version": 1}), token, session.CSRF, 409)
			lifetime, cancel := context.WithCancel(context.Background())
			defer cancel()
			unregister := a.registerRuntimeCall(&runtimeCall{selection: RuntimeSelection{ActorID: member.ID, TenantID: team}, cancel: cancel})
			defer unregister()
			input["expected_version"] = 4
			input["expected_role_version"] = 1
			input["permissions"] = []string{"workspace.read", "members.read"}
			authRequest(t, h, "PUT", path+"/roles", projectJSON(t, input), token, session.CSRF, 200)
			select {
			case <-lifetime.Done():
			case <-time.After(time.Second):
				t.Fatal("custom grant revocation did not cancel active runtime")
			}
			if a.can(member.ID, team, "models.execute") || a.can(member.ID, team, "workspace.write") {
				t.Fatal("custom readonly role retains write or generation")
			}
			if remote != nil && (remote.can(member.ID, team, "models.execute") || remote.can(member.ID, team, "workspace.write")) {
				t.Fatal("other instance retains revoked custom grants")
			}
			if mode == "postgres" {
				if err = InitPostgres(context.Background(), a.pg.db); err != nil || a.can(member.ID, team, "models.execute") {
					t.Fatal("initializer restored a revoked scoped grant", err)
				}
				target := filepath.Join(t.TempDir(), "exported")
				if _, err = ExportPostgresData(context.Background(), target, a.pg.db); err != nil {
					t.Fatal("custom role export", err)
				}
				exported, err := OpenAuthStore(target)
				if err != nil || exported.can(member.ID, team, "models.execute") || exported.role(member.ID, team) != "editor" {
					t.Fatal("role policy lost on PG-to-file export", err)
				}
				db2 := postgresTestDB(t)
				if _, err = ImportPostgresData(context.Background(), target, db2); err != nil {
					t.Fatal("custom role import", err)
				}
				restored, err := OpenPostgresAuthStore(t.TempDir(), db2)
				if err != nil || restored.can(member.ID, team, "workspace.write") || restored.role(member.ID, team) != "editor" {
					t.Fatal("role policy lost on file-to-PG import", err)
				}
			}
			authRequest(t, h, "POST", "/api/v1/projects?space_id="+team, `{"title":"Forbidden","brief":"Must never be created"}`, memberToken, memberSession.CSRF, 403)
			_, _, err = NewModelRuntime(a, func(context.Context, RuntimeSelection) error { return nil }).Resolve(context.Background(), team, member.ID, "missing", "text")
			if !errors.Is(err, errRuntimeDenied) {
				t.Fatal("runtime ignored custom role", err)
			}
			var access struct {
				CustomID    string   `json:"custom_role_id"`
				Permissions []string `json:"permissions"`
			}
			_ = json.Unmarshal(authRequest(t, h, "GET", path+"/access", "", memberToken, "", 200).Body.Bytes(), &access)
			if access.CustomID != roleID || len(access.Permissions) != 2 {
				t.Fatal("session not effective custom role", access)
			}
			authRequest(t, h, "PUT", path+"/roles", projectJSON(t, input), token, session.CSRF, 200)
			stale := map[string]any{}
			for k, v := range input {
				stale[k] = v
			}
			stale["name"] = "过期请求不能覆盖"
			authRequest(t, h, "PUT", path+"/roles", projectJSON(t, stale), token, session.CSRF, 409)
			authRequest(t, h, "POST", path+"/transfer", projectJSON(t, map[string]any{"target_user_id": member.ID, "expected_version": 5, "confirmed": false}), token, session.CSRF, 400)
			authRequest(t, h, "POST", path+"/transfer", projectJSON(t, map[string]any{"target_user_id": owner.ID, "expected_version": 5, "confirmed": true}), token, session.CSRF, 400)
			authRequest(t, h, "POST", path+"/transfer", projectJSON(t, map[string]any{"target_user_id": member.ID, "expected_version": 5, "confirmed": true}), token, session.CSRF, 200)
			if a.role(member.ID, team) != "owner" || a.role(owner.ID, team) != "admin" || !a.can(member.ID, team, "members.manage_admin") {
				t.Fatal("ownership transfer not atomic")
			}
			authRequest(t, h, "POST", path+"/transfer", projectJSON(t, map[string]any{"target_user_id": member.ID, "expected_version": 6, "confirmed": true}), token, session.CSRF, 403)
			authRequest(t, h, "DELETE", path+"/roles", projectJSON(t, map[string]any{"id": roleID, "expected_version": 6, "expected_role_version": 2}), memberToken, memberSession.CSRF, 200)
			if remote != nil {
				if remote.role(member.ID, team) != "owner" {
					t.Fatal("other instance owner stale")
				}
			} else {
				reloaded, err := OpenAuthStore(dir)
				if err != nil || reloaded.role(member.ID, team) != "owner" {
					t.Fatal("file roles/ownership not durable", err)
				}
			}
		})
	}
}

func TestCustomRoleSnapshotFailsClosed(t *testing.T) {
	org := AuthOrganization{ID: "space", OwnerID: "owner", Members: map[string]string{"owner": "owner", "writer": "editor"}, CustomRoles: map[string]identity.CustomRole{}, MemberRoles: map[string]string{"writer": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}
	if identity.ValidateCustomRoles(org) == nil || identity.EffectiveAllows(org, "writer", "models.execute") {
		t.Fatal("missing role became builtin editor authority")
	}
}

func TestCustomRoleCanLeaveWithoutReadingOtherMembers(t *testing.T) {
	a, err := OpenAuthStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	owner, _, _, err := a.login("custom-owner@example.test", "synthetic-password", true)
	if err != nil {
		t.Fatal(err)
	}
	member, token, session, err := a.login("custom-private@example.test", "synthetic-password", true)
	if err != nil {
		t.Fatal(err)
	}
	team := testTeamSpace(t, a, owner.ID)
	roleID := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	a.mu.Lock()
	d := a.clone()
	org := d.Organizations[team]
	org.Members[member.ID] = "editor"
	org.CustomRoles = map[string]identity.CustomRole{roleID: {ID: roleID, Name: "仅看作品", Base: "editor", Version: 1, Permissions: []string{"workspace.read"}}}
	org.MemberRoles = map[string]string{member.ID: roleID}
	d.Organizations[team] = org
	err = a.commit(d)
	a.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	h := SecureHandler(testStore(t), "http://localhost:5173", a, AuthOptions{})
	path := "/api/v1/organizations/" + team
	authRequest(t, h, "GET", path+"/members", "", token, "", 403)
	authRequest(t, h, "GET", path+"/access", "", token, "", 200)
	authRequest(t, h, "POST", path+"/leave", `{"expected_version":1}`, token, session.CSRF, 200)
	if a.role(member.ID, team) != "" || a.role(owner.ID, team) != "owner" {
		t.Fatal("leaving a restricted role changed ownership or retained access")
	}
}

func TestCustomRoleManualWriterCannotDispatchLegacyModels(t *testing.T) {
	a, err := OpenAuthStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	owner, _, _, err := a.login("manual-owner@example.test", "synthetic-password", true)
	if err != nil {
		t.Fatal(err)
	}
	member, token, session, err := a.login("manual-writer@example.test", "synthetic-password", true)
	if err != nil {
		t.Fatal(err)
	}
	team := testTeamSpace(t, a, owner.ID)
	roleID := "cccccccccccccccccccccccccccccccc"
	a.mu.Lock()
	d := a.clone()
	org := d.Organizations[team]
	org.Members[member.ID] = "editor"
	org.CustomRoles = map[string]identity.CustomRole{roleID: {ID: roleID, Name: "手动编剧", Base: "editor", Version: 1, Permissions: []string{"workspace.read", "workspace.write"}}}
	org.MemberRoles = map[string]string{member.ID: roleID}
	d.Organizations[team] = org
	err = a.commit(d)
	a.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	s := testStore(t)
	p, _, err := s.CreateProject(team, "manual-writer-project", ProjectInput{Title: "只能手动写作", Brief: "模型执行权限必须独立于手动编辑权限"})
	if err != nil {
		t.Fatal(err)
	}
	h := SecureHandler(s, "http://localhost:5173", a, AuthOptions{})
	authRequest(t, h, "POST", "/api/v1/projects/"+p.ID+"/analysis?space_id="+team, `{"expected_version":1}`, token, session.CSRF, 403)
	if !a.can(member.ID, team, "workspace.write") || len(s.creative.data.Jobs) != 0 {
		t.Fatal("model permission became a display-only label")
	}
	// Space management is an actual independent grant, not a cosmetic option
	// or an alias for access to the member directory.
	a.mu.Lock()
	next := a.clone()
	changed := next.Organizations[team]
	managerID := "dddddddddddddddddddddddddddddddd"
	changed.CustomRoles[managerID] = identity.CustomRole{ID: managerID, Name: "空间管理员", Base: "admin", Version: 1, Permissions: []string{"workspace.manage", "workspace.read", "workspace.write"}}
	changed.Members[member.ID] = "admin"
	changed.MemberRoles[member.ID] = managerID
	next.Organizations[team] = changed
	err = a.commit(next)
	a.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	authRequest(t, h, "PATCH", "/api/v1/organizations/"+team+"/settings", `{"expected_version":1,"name":"只管理空间名称"}`, token, session.CSRF, 200)
	authRequest(t, h, "GET", "/api/v1/organizations/"+team+"/members", "", token, "", 403)
}
