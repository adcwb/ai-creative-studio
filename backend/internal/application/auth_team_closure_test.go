package application

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"
)

func TestTeamClosureRevokesMembersAndExecutionPreservingOwnerHistory(t *testing.T) {
	for _, mode := range []string{"file", "postgres"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			a, e := OpenAuthStore(dir)
			if e != nil {
				t.Fatal(e)
			}
			var remote *AuthStore
			if mode == "postgres" {
				db := postgresTestDB(t)
				a, e = OpenPostgresAuthStore(dir, db)
				if e != nil {
					t.Fatal(e)
				}
				remote, e = OpenPostgresAuthStore(t.TempDir(), db)
				if e != nil {
					t.Fatal(e)
				}
			}
			owner, token, session, e := a.login("closure-owner@example.test", "synthetic-closure-password", true)
			if e != nil {
				t.Fatal(e)
			}
			member, memberToken, memberSession, e := a.login("closure-member@example.test", "synthetic-closure-password", true)
			if e != nil {
				t.Fatal(e)
			}
			space := testTeamSpace(t, a, owner.ID)
			s := testStore(t)
			h := SecureHandler(s, "http://localhost:5173", a, AuthOptions{})
			path := "/api/v1/organizations/" + space
			authRequest(t, h, "PUT", path+"/members", testMemberInput(t, a, member.Email, "editor", 1), token, session.CSRF, 200)
			p, _, e := s.CreateProject(space, "retained-closed-work", ProjectInput{Title: "团队历史保留", Brief: "关闭前准备，关闭后所有者仍可读。"})
			if e != nil {
				t.Fatal(e)
			}
			body := `{"expected_version":2,"name":"Synthetic test team","confirmed":true}`
			authRequest(t, h, "POST", path+"/close", body, memberToken, memberSession.CSRF, 403)
			authRequest(t, h, "POST", path+"/close", body, token, "", 403)
			authRequest(t, h, "POST", path+"/close", `{"expected_version":2,"name":"wrong","confirmed":true}`, token, session.CSRF, 400)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			unregister := a.registerRuntimeCall(&runtimeCall{selection: RuntimeSelection{ActorID: owner.ID, TenantID: space}, cancel: cancel})
			defer unregister()
			authRequest(t, h, "POST", path+"/close", body, token, session.CSRF, 200)
			select {
			case <-ctx.Done():
			case <-time.After(time.Second):
				t.Fatal("closure did not cancel execution")
			}
			authRequest(t, h, "POST", path+"/close", body, token, session.CSRF, 200)
			if a.can(owner.ID, space, "workspace.write") || a.can(owner.ID, space, "models.execute") || a.can(member.ID, space, "workspace.read") || !a.can(owner.ID, space, "workspace.read") {
				t.Fatal("closed grants")
			}
			if remote != nil && (remote.can(owner.ID, space, "models.execute") || remote.can(member.ID, space, "workspace.read")) {
				t.Fatal("other instance retained closed authority")
			}
			authRequest(t, h, "GET", "/api/v1/projects/"+p.ID+"?space_id="+space, "", token, session.CSRF, 200)
			authRequest(t, h, "PUT", "/api/v1/projects/"+p.ID+"?space_id="+space, `{"expected_version":1,"title":"must fail","brief":"closed team"}`, token, session.CSRF, 403)
			authRequest(t, h, "GET", "/api/v1/projects/"+p.ID+"?space_id="+space, "", memberToken, memberSession.CSRF, 403)
			if mode == "postgres" {
				target := filepath.Join(t.TempDir(), "closed-export")
				if _, e = ExportPostgresData(context.Background(), target, a.pg.db); e != nil {
					t.Fatal(e)
				}
				restored, e := OpenAuthStore(target)
				if e != nil || restored.can(owner.ID, space, "models.execute") || restored.can(member.ID, space, "workspace.read") {
					t.Fatal("closed file export", e)
				}
				db := postgresTestDB(t)
				if _, e = ImportPostgresData(context.Background(), target, db); e != nil {
					t.Fatal(e)
				}
				roundtrip, e := OpenPostgresAuthStore(t.TempDir(), db)
				if e != nil || roundtrip.can(owner.ID, space, "workspace.write") {
					t.Fatal("closed PG restore", e)
				}
			} else {
				restored, e := OpenAuthStore(dir)
				if e != nil || restored.can(owner.ID, space, "workspace.write") {
					t.Fatal("closed restart", e)
				}
			}
			var access struct {
				Version int64 `json:"version"`
				Closed  bool  `json:"closed"`
			}
			if json.Unmarshal(authRequest(t, h, "GET", path+"/access", "", token, session.CSRF, 200).Body.Bytes(), &access) != nil || !access.Closed {
				t.Fatal(access)
			}
			authRequest(t, h, "POST", path+"/reopen", `{"expected_version":3,"name":"Synthetic test team","confirmed":true}`, token, session.CSRF, 200)
			if !a.can(owner.ID, space, "workspace.write") || a.can(member.ID, space, "workspace.read") {
				t.Fatal("reopen restored former members")
			}
			// An old accepted close must not close a subsequently reopened team.
			authRequest(t, h, "POST", path+"/close", body, token, session.CSRF, 409)
		})
	}
}
