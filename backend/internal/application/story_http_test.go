package application

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/projects"
	"github.com/adcwb/ai-creative-studio/backend/internal/modules/series"
	"github.com/adcwb/ai-creative-studio/backend/internal/modules/story"
)

func TestStorySecureHTTPCurrentRolesCSRFVersionsAndRevocation(t *testing.T) {
	s := testStore(t)
	auth, e := OpenAuthStore(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	owner, token, session, e := auth.login("story-owner@example.invalid", "Synthetic-story-password!", true)
	if e != nil {
		t.Fatal(e)
	}
	_, viewerToken, viewerSession, e := auth.login("story-viewer@example.invalid", "Synthetic-story-password!", true)
	if e != nil {
		t.Fatal(e)
	}
	space := testTeamSpace(t, auth, owner.ID)
	a := series.Actor{Space: space, User: owner.ID, Writable: true}
	root, e := s.series.Mutate(context.Background(), a, "secure-story-root", series.Command{Operation: "create", Kind: "series", ID: series.NewID(a, "create", "secure-story-root"), Input: series.Input{Title: "真实组织中的正式资料", Summary: "正式资料由当前组织授权的人编辑", Profile: series.DefaultProfile()}}, nil)
	if e != nil {
		t.Fatal(e)
	}
	season, e := s.series.Mutate(context.Background(), a, "secure-story-season", series.Command{Operation: "create", Kind: "season", ID: series.NewID(a, "create", "secure-story-season"), Parent: root.ID}, nil)
	if e != nil {
		t.Fatal(e)
	}
	ep, _, e := s.CreateSeriesEpisode(context.Background(), a, "secure-story-episode", season.ID, series.Input{Title: "资料核对"})
	if e != nil {
		t.Fatal(e)
	}
	h := SecureHandler(s, "http://localhost:5173", auth, AuthOptions{AllowRegistration: true})
	q := "?space_id=" + space
	base := "/api/v1/series/" + root.ID + "/story-records"
	members := "/api/v1/organizations/" + space + "/members"
	authRequest(t, h, "PUT", members, `{"email":"story-viewer@example.invalid","role":"viewer"}`, token, session.CSRF, 200)
	authRequest(t, h, "GET", base+q, "", "", "", 401)
	authRequest(t, h, "POST", base+q, projectJSON(t, storyBible()), token, "", 403)
	authRequest(t, h, "POST", base+q, projectJSON(t, storyBible()), viewerToken, viewerSession.CSRF, 403)
	w := authRequest(t, h, "POST", base+q, projectJSON(t, storyBible()), token, session.CSRF, 201)
	var v story.Version
	if e = json.Unmarshal(w.Body.Bytes(), &v); e != nil {
		t.Fatal(e)
	}
	authRequest(t, h, "GET", base+"/"+v.ID+q, "", viewerToken, "", 200)
	authRequest(t, h, "GET", base+"/"+v.ID+"/versions/"+v.VersionID+q, "", viewerToken, "", 200)
	authRequest(t, h, "GET", base+"/"+v.ID+"/versions"+q, "", viewerToken, "", 200)
	script := scriptFixture(t)
	for i := range script.Scenes {
		script.Scenes[i].EpisodeNumber = 1
	}
	stable, _, e := projects.CompileStructure(script, space+":"+ep.ID, 1)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.SaveProjectScript(space, ep.ID, "secure-evidence-script", ScriptSaveInput{ExpectedVersion: 1, Script: stable}); e != nil {
		t.Fatal(e)
	}
	evidenceBase := "/api/v1/series/" + root.ID + "/story-evidence/episodes/" + ep.ID + "/scripts"
	for _, path := range []string{evidenceBase + q, evidenceBase + "/1" + q} {
		authRequest(t, h, "GET", path, "", "", "", 401)
		authRequest(t, h, "GET", path, "", viewerToken, "", 200)
	}
	authRequest(t, h, "GET", evidenceBase+q+"&limit=101", "", token, "", 400)
	authRequest(t, h, "GET", evidenceBase+q+"&cursor=-1", "", token, "", 400)
	authRequest(t, h, "GET", evidenceBase+"/999"+q, "", token, "", 404)
	authRequest(t, h, "GET", evidenceBase+"/no-version"+q, "", token, "", 400)
	w = authRequest(t, h, "GET", base+q, "", viewerToken, "", 200)
	var page map[string]any
	if e = json.Unmarshal(w.Body.Bytes(), &page); e != nil {
		t.Fatal(e)
	}
	if _, ok := page["items"].([]any)[0].(map[string]any)["body"]; ok {
		t.Fatal("list leaked full bodies")
	}
	ctxPath := "/api/v1/episodes/" + ep.ID + "/context-previews" + q
	authRequest(t, h, "POST", ctxPath, `{}`, viewerToken, viewerSession.CSRF, 403)
	authRequest(t, h, "POST", ctxPath, `{}`, token, session.CSRF, 200)
	authRequest(t, h, "POST", ctxPath, `{"actor_id":"owner"}`, token, session.CSRF, 400)
	snapshots := "/api/v1/episodes/" + ep.ID + "/context-snapshots"
	authRequest(t, h, "POST", snapshots+q, `{}`, token, "", 403)
	authRequest(t, h, "POST", snapshots+q, `{}`, viewerToken, viewerSession.CSRF, 403)
	authRequest(t, h, "POST", snapshots+q, `{"created_by":"forged"}`, token, session.CSRF, 400)
	sw := authRequest(t, h, "POST", snapshots+q, `{"query":"规则","record_ids":[]}`, token, session.CSRF, 201)
	var snapshot story.ContextSnapshot
	if e = json.Unmarshal(sw.Body.Bytes(), &snapshot); e != nil || !story.ValidSnapshot(snapshot) {
		t.Fatal("invalid HTTP snapshot", e, sw.Body.String())
	}
	authRequest(t, h, "GET", snapshots+q, "", viewerToken, "", 200)
	authRequest(t, h, "GET", snapshots+"/"+snapshot.ID+q, "", viewerToken, "", 200)
	other := createProject(t, s)
	authRequest(t, h, "GET", base+"/"+v.ID+"?space_id="+other.SpaceID, "", token, "", 403)
	authRequest(t, h, "PUT", members, `{"email":"story-viewer@example.invalid","role":"remove"}`, token, session.CSRF, 200)
	authRequest(t, h, "GET", snapshots+q, "", viewerToken, "", 403)
	authRequest(t, h, "GET", snapshots+"/"+snapshot.ID+q, "", viewerToken, "", 403)
	authRequest(t, h, "GET", base+q, "", viewerToken, "", 403)
	authRequest(t, h, "GET", evidenceBase+q, "", viewerToken, "", 403)
	authRequest(t, h, "GET", evidenceBase+"/1"+q, "", viewerToken, "", 403)
	authRequest(t, h, "POST", base+q, projectJSON(t, storyBible()), viewerToken, viewerSession.CSRF, 403)
}
