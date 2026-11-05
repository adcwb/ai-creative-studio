package application

import (
	"context"
	"encoding/json"
	"testing"

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
	space := auth.spaces(owner.ID)[0]["id"]
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
	other := createProject(t, s)
	authRequest(t, h, "GET", base+"/"+v.ID+"?space_id="+other.SpaceID, "", token, "", 403)
	authRequest(t, h, "PUT", members, `{"email":"story-viewer@example.invalid","role":"remove"}`, token, session.CSRF, 200)
	authRequest(t, h, "GET", base+q, "", viewerToken, "", 403)
	authRequest(t, h, "POST", base+q, projectJSON(t, storyBible()), viewerToken, viewerSession.CSRF, 403)
}
