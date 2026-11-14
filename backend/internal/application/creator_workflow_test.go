package application

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/library"
	"github.com/adcwb/ai-creative-studio/backend/internal/modules/series"
	"github.com/adcwb/ai-creative-studio/backend/internal/persistence/libraryrepo"
)

func exerciseTeamSpaceActions(t *testing.T, a *AuthStore) {
	t.Helper()
	owner, token, session, err := a.login("team-owner@example.test", "synthetic-password", true)
	if err != nil {
		t.Fatal(err)
	}
	member, memberToken, memberSession, err := a.login("team-viewer@example.test", "synthetic-password", true)
	if err != nil {
		t.Fatal(err)
	}
	h := SecureHandler(testStore(t), "http://localhost:5173", a, AuthOptions{})
	team := testTeamSpace(t, a, owner.ID)
	path := "/api/v1/organizations/" + team
	authRequest(t, h, "PATCH", path+"/settings", `{"name":"New","expected_version":1}`, token, "", 403)
	authRequest(t, h, "PATCH", path+"/settings", `{"name":"New","expected_version":1}`, memberToken, memberSession.CSRF, 403)
	authRequest(t, h, "PUT", path+"/members", testMemberInput(t, a, "team-viewer@example.test", "viewer", 1), token, session.CSRF, 200)
	authRequest(t, h, "PATCH", path+"/settings", `{"name":"New","expected_version":2}`, memberToken, memberSession.CSRF, 403)
	authRequest(t, h, "PATCH", path+"/settings", `{"name":"New","expected_version":1}`, token, session.CSRF, 409)
	authRequest(t, h, "PATCH", path+"/settings", `{"name":"New","expected_version":2}`, token, session.CSRF, 200)
	if got := a.sessionSpaces(owner.ID); len(got) != 2 {
		t.Fatal("personal/team spaces", got)
	}
	authRequest(t, h, "POST", path+"/leave", `{"expected_version":3}`, token, session.CSRF, 403)
	authRequest(t, h, "POST", path+"/leave", `{"expected_version":2}`, memberToken, memberSession.CSRF, 409)
	authRequest(t, h, "POST", path+"/leave", `{"expected_version":3}`, memberToken, memberSession.CSRF, 200)
	if a.can(member.ID, team, "workspace.read") {
		t.Fatal("left member retains access")
	}
	authRequest(t, h, "GET", path+"/access", "", memberToken, "", 403)
	authRequest(t, h, "POST", path+"/leave", `{"expected_version":3}`, memberToken, memberSession.CSRF, 403)
	personal := a.spaces(owner.ID)[0]["id"]
	// Resolve the personal space explicitly; map iteration must not select a team.
	for _, v := range a.spaces(owner.ID) {
		if v["type"] == "personal" {
			personal = v["id"]
		}
	}
	authRequest(t, h, "PATCH", "/api/v1/organizations/"+personal+"/settings", `{"name":"Shared","expected_version":1}`, token, session.CSRF, 403)
	authRequest(t, h, "POST", "/api/v1/organizations/"+personal+"/leave", `{"expected_version":1}`, token, session.CSRF, 403)
}
func TestFileTeamSpaceActions(t *testing.T) {
	a, e := OpenAuthStore(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	exerciseTeamSpaceActions(t, a)
}
func TestPostgresTeamSpaceActions(t *testing.T) {
	a, e := OpenPostgresAuthStore(t.TempDir(), postgresTestDB(t))
	if e != nil {
		t.Fatal(e)
	}
	exerciseTeamSpaceActions(t, a)
}

func TestBindingVersionChecksAreScopedReadOnly(t *testing.T) {
	s := testStore(t)
	p, _, err := s.CreateProject("personal-demo", "binding-check-project", ProjectInput{Title: "Reference project", Brief: "Pinned references"})
	if err != nil {
		t.Fatal(err)
	}
	in := libraryWrite{Kind: "character", Card: library.Card{Name: "First", Character: &library.Character{}, ProhibitedChanges: []string{}}, References: []libraryReferenceInput{}}
	create := projectHTTP(t, s, "POST", "/library/assets?space_id=personal-demo", "check-library-create", projectJSON(t, in), 201)
	raw, _ := json.Marshal(create)
	var original libraryrepo.Result
	if err = json.Unmarshal(raw, &original); err != nil {
		t.Fatal(err)
	}
	state := s.media.Get(p.ID, p.SpaceID)
	projectHTTP(t, s, "POST", "/projects/"+p.ID+"/library-bindings?space_id=personal-demo", "check-library-bind", projectJSON(t, libraryBindingInput{ExpectedVersion: state.Version, AssetID: original.Asset.ID, VersionID: original.Version.ID, Usage: "character"}), 201)
	before := s.media.Get(p.ID, p.SpaceID)
	checks, _, err := s.checkBindingVersions(context.Background(), p.SpaceID, p.ID)
	if err != nil || len(checks) != 1 || checks[0].Status != "current" {
		t.Fatal(checks, err)
	}
	in.ExpectedVersion = original.Version.ID
	in.Card.Name = "New reference"
	projectHTTP(t, s, "POST", "/library/assets/"+original.Asset.ID+"/versions?space_id=personal-demo", "check-library-next", projectJSON(t, in), 201)
	checks, _, err = s.checkBindingVersions(context.Background(), p.SpaceID, p.ID)
	if err != nil || checks[0].Status != "outdated" || checks[0].Pinned != original.Version.ID || checks[0].Current == original.Version.ID {
		t.Fatal(checks, err)
	}
	if !reflect.DeepEqual(before, s.media.Get(p.ID, p.SpaceID)) {
		t.Fatal("check upgraded project assets")
	}
	if _, _, err = s.checkBindingVersions(context.Background(), "foreign-space", p.ID); err == nil {
		t.Fatal("tenant leak")
	}
	projectHTTP(t, s, "GET", "/projects/"+p.ID+"/library-bindings/check?space_id=personal-demo", "", "", 200)
	projectHTTP(t, s, "POST", "/library/assets/"+original.Asset.ID+"/archive?space_id=personal-demo", "check-library-archive", projectJSON(t, map[string]any{"expected_asset_version_id": checks[0].Current}), 200)
	checks, _, err = s.checkBindingVersions(context.Background(), p.SpaceID, p.ID)
	if err != nil || checks[0].Status != "archived" || !reflect.DeepEqual(before, s.media.Get(p.ID, p.SpaceID)) {
		t.Fatal("archive check changed history", checks, err)
	}

}

func TestSeasonProductionFactsDoNotPublishOrDispatch(t *testing.T) {
	s := testStore(t)
	season := seriesFixture(t, s)
	_, p, err := s.CreateSeriesEpisode(context.Background(), seriesActor, "production-summary-episode", season.ID, series.Input{Title: "First episode"})
	if err != nil {
		t.Fatal(err)
	}
	progress, err := s.episodeProduction(p.SpaceID, p.ID)
	if err != nil || progress.Script || progress.Storyboard || progress.ExportID != "" || progress.Shots != 0 || progress.ProjectVersion != p.Version {
		t.Fatal(progress, err)
	}
	p, err = s.SaveProjectScript(p.SpaceID, p.ID, "summary-script", ScriptSaveInput{ExpectedVersion: p.Version, Script: scriptFixture(t)})
	if err != nil {
		t.Fatal(err)
	}
	progress, err = s.episodeProduction(p.SpaceID, p.ID)
	if err != nil || !progress.Script || progress.Storyboard || progress.ExportID != "" {
		t.Fatal(progress, err)
	}
	page := projectHTTP(t, s, "GET", "/seasons/"+season.ID+"/episodes?space_id=personal-demo&include_production=true", "", "", 200)
	fact := page["items"].([]any)[0].(map[string]any)["production"].(map[string]any)
	if fact["script"] != true || fact["export_id"] != nil {
		t.Fatal(fact)
	}
	if len(s.data.ScriptGenerations) != 0 || len(s.data.Tasks) != 0 {
		t.Fatal("overview dispatched model")
	}
	projectHTTP(t, s, "GET", "/seasons/"+season.ID+"/episodes?space_id=team-demo&include_production=true", "", "", 404)
}

func TestDeliveryPackageFixedBytesAndRejections(t *testing.T) {
	s := testStore(t)
	p, state, _ := revisionFixture(t, s)
	id := "abcdefabcdefabcdefabcdefabcdefab"
	timeline := state.Timeline
	export := MediaExport{ID: id, Status: "succeeded", Timeline: timeline, Duration: 2}
	// Install immutable completed render files; no paid upstream or fake production claim.
	m := s.media
	m.mu.Lock()
	d := m.clone()
	state.Exports = []MediaExport{export}
	d.Projects[mediaKey(p.SpaceID, p.ID)] = state
	err := m.commit(d)
	m.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	for ext, data := range map[string]string{"mp4": "synthetic export bytes", "srt": "1\n00:00:00,000 --> 00:00:02,000\nHello\n", "vtt": "WEBVTT\n\n00:00.000 --> 00:02.000\nHello\n"} {
		if err = os.WriteFile(filepath.Join(m.dir, id+"."+ext), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		if err = m.storeBlobContext(context.Background(), p.SpaceID, p.ID, id+"."+ext); err != nil {
			t.Fatal(err)
		}
	}
	later := state.Timeline
	later.Clips = append([]MediaClip(nil), state.Timeline.Clips...)
	later.Clips[0].Subtitle = "This later edit must not enter the delivery"
	state, err = m.SaveTimeline(p.ID, p.SpaceID, "delivery-later-edit", state.Version, later)
	if err != nil {
		t.Fatal(err)
	}
	h := Handler(s, "")
	url := "/api/v1/projects/" + p.ID + "/media/exports/" + id + "/delivery.zip?space_id=" + p.SpaceID
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", url, nil))
	if w.Code != 200 || w.Header().Get("Content-Type") != "application/zip" {
		t.Fatal(w.Code, w.Body.String())
	}
	archive, err := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{}
	for _, f := range archive.File {
		r, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		files[f.Name], err = io.ReadAll(r)
		_ = r.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	var manifest deliveryManifest
	if err = json.Unmarshal(files["manifest.json"], &manifest); err != nil {
		t.Fatal(err)
	}
	if len(files) != 5 || len(manifest.Files) != 3 || manifest.ExportID != id || !reflect.DeepEqual(manifest.Timeline, timeline) || bytes.Contains(files["manifest.json"], []byte(m.dir)) {
		t.Fatal("manifest", manifest)
	}
	for _, f := range manifest.Files {
		sum := sha256.Sum256(files[f.Name])
		if f.SHA256 != hex.EncodeToString(sum[:]) || f.Size != int64(len(files[f.Name])) {
			t.Fatal("hash mismatch", f)
		}
	}
	if !reflect.DeepEqual(state, s.media.Get(p.ID, p.SpaceID)) {
		t.Fatal("download changed project")
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/projects/"+p.ID+"/media/exports/"+id+"/delivery.zip?space_id=team-demo", nil))
	if w.Code != 404 {
		t.Fatal("tenant", w.Code)
	}
	m.mu.Lock()
	d = m.clone()
	v := d.Projects[mediaKey(p.SpaceID, p.ID)]
	v.Exports[0].Purpose = "animatic"
	d.Projects[mediaKey(p.SpaceID, p.ID)] = v
	err = m.commit(d)
	m.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, url, nil))
	if w.Code != 400 {
		t.Fatal("preview packaged as final", w.Code)
	}
	// Cancellation leaves an invalid archive rather than a success manifest.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var out bytes.Buffer
	f, err := os.Open(filepath.Join(m.dir, id+".mp4"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err = writeDelivery(ctx, &out, p.ID, export, []deliveryInput{{"episode.mp4", f}}); err == nil {
		t.Fatal("cancel ignored")
	}
}

func TestCreatorWorkflowCurrentRoleAndTenantBoundaries(t *testing.T) {
	s := testStore(t)
	a, err := OpenAuthStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	owner, token, session, err := a.login("creator-owner@example.test", "synthetic-password", true)
	if err != nil {
		t.Fatal(err)
	}
	team := testTeamSpace(t, a, owner.ID)
	h := SecureHandler(s, "http://localhost:5173", a, AuthOptions{})
	type login struct{ token, csrf string }
	users := map[string]login{"owner": {token, session.CSRF}}
	for _, role := range []string{"admin", "editor", "viewer", "outsider"} {
		_, token, session, err := a.login(role+"@example.test", "synthetic-password", true)
		if err != nil {
			t.Fatal(err)
		}
		users[role] = login{token, session.CSRF}
		if role != "outsider" {
			authRequest(t, h, "PUT", "/api/v1/organizations/"+team+"/members", testMemberInput(t, a, role+"@example.test", role), users["owner"].token, users["owner"].csrf, 200)
		}
	}
	p, _, err := s.CreateProject(team, "rbac-restore-project", ProjectInput{Title: "Scoped project", Brief: "Only current team permissions authorize restoration"})
	if err != nil {
		t.Fatal(err)
	}
	state, err := s.media.Demo(p.ID, team, "rbac-restore-media", 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := s.CaptureProductionRevision(context.Background(), team, p.ID, "main", owner.ID, "rbac-restore-source", revisionCaptureInput{Label: "Original", ExpectedProjectVersion: p.Version, ExpectedMediaVersion: state.Version}, nil)
	if err != nil {
		t.Fatal(err)
	}
	base := "/api/v1/projects/" + p.ID + "/revisions/" + snapshot.Revision.ID
	body := projectJSON(t, revisionRestoreInput{ExpectedProjectVersion: p.Version, ExpectedMediaVersion: state.Version, Component: "timeline", Episode: 1})
	for role, u := range users {
		read, write := 200, 200
		if role == "viewer" {
			write = 403
		}
		if role == "outsider" {
			read = 403
			write = 403
		}
		authRequest(t, h, "GET", base+"/restore-plan?space_id="+team+"&component=timeline", "", u.token, "", read)
		authRequest(t, h, "POST", base+"/restore-timeline?space_id="+team, body, u.token, u.csrf, write)
		authRequest(t, h, "GET", "/api/v1/projects/"+p.ID+"/library-bindings/check?space_id="+team, "", u.token, "", read)
	}
	authRequest(t, h, "PUT", "/api/v1/organizations/"+team+"/members", testMemberInput(t, a, "editor@example.test", "viewer"), token, session.CSRF, 200)
	authRequest(t, h, "POST", base+"/restore-timeline?space_id="+team, body, users["editor"].token, users["editor"].csrf, 403)
	foreign, _, err := s.CreateProject(team, "rbac-other-project", ProjectInput{Title: "Other", Brief: "Other project"})
	if err != nil {
		t.Fatal(err)
	}
	authRequest(t, h, "GET", "/api/v1/projects/"+foreign.ID+"/revisions/"+snapshot.Revision.ID+"/restore-plan?space_id="+team+"&component=timeline", "", token, "", 404)
}

func TestPostgresTeamLeaveInvalidatesOtherAPI(t *testing.T) {
	db := postgresTestDB(t)
	a, err := OpenPostgresAuthStore(t.TempDir(), db)
	if err != nil {
		t.Fatal(err)
	}
	b, err := OpenPostgresAuthStore(t.TempDir(), db)
	if err != nil {
		t.Fatal(err)
	}
	owner, token, session, err := a.login("leave-owner@example.test", "synthetic-password", true)
	if err != nil {
		t.Fatal(err)
	}
	member, memberToken, memberSession, err := a.login("leave-member@example.test", "synthetic-password", true)
	if err != nil {
		t.Fatal(err)
	}
	team := testTeamSpace(t, a, owner.ID)
	s := testStore(t)
	ha := SecureHandler(s, "http://localhost:5173", a, AuthOptions{})
	hb := SecureHandler(s, "http://localhost:5173", b, AuthOptions{})
	authRequest(t, ha, "PUT", "/api/v1/organizations/"+team+"/members", testMemberInput(t, a, "leave-member@example.test", "editor", 1), token, session.CSRF, 200)
	if !b.can(member.ID, team, "workspace.write") {
		t.Fatal("second API failed to see membership")
	}
	authRequest(t, hb, "GET", "/api/v1/organizations/"+team+"/access", "", memberToken, "", 200)
	authRequest(t, ha, "POST", "/api/v1/organizations/"+team+"/leave", `{"expected_version":2}`, memberToken, memberSession.CSRF, 200)
	if b.can(member.ID, team, "workspace.read") {
		t.Fatal("second API retained revoked membership")
	}
	authRequest(t, hb, "GET", "/api/v1/organizations/"+team+"/access", "", memberToken, "", 403)
	var count int
	if err = db.QueryRow(`SELECT count(*) FROM studio_space_memberships WHERE space_id=$1 AND user_id=$2`, team, member.ID).Scan(&count); err != nil || count != 0 {
		t.Fatal("relational membership retained", count, err)
	}
}
