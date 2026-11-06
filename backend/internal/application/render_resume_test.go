package application

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

func TestRenderResumeSecurityReplayAndOriginalSnapshot(t *testing.T) {
	s := testStore(t)
	auth, e := OpenAuthStore(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	a, token, session, e := auth.login("render-owner@example.test", "synthetic-render-password", true)
	if e != nil {
		t.Fatal(e)
	}
	b, otherToken, otherSession, e := auth.login("render-viewer@example.test", "synthetic-render-password-B", true)
	if e != nil {
		t.Fatal(e)
	}
	space := testTeamSpace(t, auth, a.ID)
	otherSpace := testTeamSpace(t, auth, b.ID)
	project, _, e := s.CreateProject(space, "project", projectInput())
	if e != nil {
		t.Fatal(e)
	}
	p, e := s.media.Demo(project.ID, space, "demo", 1, nil)
	if e != nil {
		t.Fatal(e)
	}
	s.media.jobs = newLocalJobs(0)
	original, e := s.media.StartExport(project.ID, space, "export", p.Version)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.media.CancelExport(project.ID, space, "cancel", original.ID); e != nil {
		t.Fatal(e)
	}
	h := SecureHandler(s, "http://localhost:5173", auth, AuthOptions{AllowRegistration: true})
	path := fmt.Sprintf("/api/v1/projects/%s/media/exports/%s/resume?space_id=%s", project.ID, original.ID, space)
	authRequest(t, h, "POST", path, "{}", "", "", 401)
	authRequest(t, h, "POST", path, "{}", token, "", 403)
	authRequest(t, h, "POST", path, "{}", otherToken, otherSession.CSRF, 403)
	foreign := fmt.Sprintf("/api/v1/projects/%s/media/exports/%s/resume?space_id=%s", project.ID, original.ID, otherSpace)
	authRequest(t, h, "POST", foreign, "{}", otherToken, otherSession.CSRF, 404)
	members := "/api/v1/organizations/" + space + "/members"
	authRequest(t, h, "PUT", members, `{"email":"render-viewer@example.test","role":"viewer"}`, token, session.CSRF, 200)
	authRequest(t, h, "POST", path, "{}", otherToken, otherSession.CSRF, 403)
	response := authRequest(t, h, "POST", path, "{}", token, session.CSRF, http.StatusAccepted)
	var resumed MediaExport
	if e = json.Unmarshal(response.Body.Bytes(), &resumed); e != nil || resumed.ID != original.ID || resumed.Status != "queued" || mediaDigest(resumed.Timeline) != mediaDigest(original.Timeline) {
		t.Fatal("resume replaced original export", resumed, e)
	}
	authRequest(t, h, "POST", path, "{}", token, session.CSRF, http.StatusAccepted)
	s.media.mu.Lock()
	pending := len(s.media.jobs.pending)
	exports := len(s.media.data.Projects[mediaKey(space, project.ID)].Exports)
	s.media.mu.Unlock()
	if pending != 2 || exports != 1 {
		t.Fatal("duplicate resume scheduled work", pending, exports)
	}
	authRequest(t, h, "PUT", members, `{"email":"render-viewer@example.test","role":"editor"}`, token, session.CSRF, 200)
	authRequest(t, h, "POST", path, "{}", otherToken, otherSession.CSRF, http.StatusAccepted)
	authRequest(t, h, "PUT", members, `{"email":"render-viewer@example.test","role":"remove"}`, token, session.CSRF, 200)
	authRequest(t, h, "POST", path, "{}", otherToken, otherSession.CSRF, 403)
}
