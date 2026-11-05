package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/series"
	"github.com/adcwb/ai-creative-studio/backend/internal/persistence/seriesrepo"
	"github.com/adcwb/ai-creative-studio/backend/internal/platform/jobqueue"
)

func seriesRouteContract(t *testing.T, s *FileStore) {
	t.Helper()
	q := "?space_id=personal-demo"
	create := func(path, key string, body any) series.Node {
		t.Helper()
		v := projectHTTP(t, s, "POST", path+q, key, projectJSON(t, body), 201)
		b, _ := json.Marshal(v)
		var n series.Node
		if err := json.Unmarshal(b, &n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	root := create("/series", "http-series-root", map[string]any{"title": "未来的夏天", "summary": "跨越四季的成长故事", "profile": series.Profile{Duration: 120, Resolution: "2160p", Aspect: "9:16"}})
	season := create("/series/"+root.ID+"/seasons", "http-series-season", map[string]any{})
	first := create("/seasons/"+season.ID+"/episodes", "http-series-episode", map[string]any{"title": "初次相遇"})
	again := create("/seasons/"+season.ID+"/episodes", "http-series-episode", map[string]any{"title": "初次相遇"})
	if again.ID != first.ID {
		t.Fatal("duplicate episode")
	}
	p, err := s.GetProject("personal-demo", first.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	projectHTTP(t, s, "PUT", "/projects/"+p.ID+q, "edit-episode-current", projectJSON(t, ProjectUpdate{Title: "用户细化后的标题", Brief: "当前简述必须由目录实时投影", ExpectedVersion: 1}), 200)
	entry := projectHTTP(t, s, "GET", "/episodes/"+first.ID+q, "", "", 200)
	if entry["episode"].(map[string]any)["title"] != "用户细化后的标题" {
		t.Fatal("stale creation title exposed")
	}
	page := projectHTTP(t, s, "GET", "/seasons/"+season.ID+"/episodes"+q, "", "", 200)
	if page["items"].([]any)[0].(map[string]any)["project_version"] != float64(2) {
		t.Fatal("current project version missing")
	}
	state, err := s.media.GetChecked(p.ID, "personal-demo")
	if err != nil {
		t.Fatal(err)
	}
	if state.Timeline.OutputResolution != "2160p" || state.Timeline.AspectRatio != "9:16" {
		t.Fatal("new timeline lost selected profile", state.Timeline)
	}
	if _, err = s.media.SaveTimeline(p.ID, "personal-demo", "wrong-local-episode", state.Version, MediaTimeline{EpisodeNumber: 7}); err == nil {
		t.Fatal("linked project accepted other local episode")
	}
	if _, err = s.media.StartEpisodeExport(p.ID, "personal-demo", "wrong-export-episode", state.Version, 7); err == nil {
		t.Fatal("linked project exported other local episode")
	}
	bad := scriptFixture(t)
	bad.Scenes[0].EpisodeNumber = 7
	projectHTTP(t, s, "PUT", "/projects/"+p.ID+"/script"+q, "wrong-script-episode", projectJSON(t, ScriptSaveInput{Script: bad, ExpectedVersion: 2}), 400)
	good := scriptFixture(t)
	saved, err := s.SaveProjectScript("personal-demo", p.ID, "valid-one-episode", ScriptSaveInput{Script: good, ExpectedVersion: 2})
	if err != nil {
		t.Fatal(err)
	}
	for _, scene := range saved.Script.Scenes {
		if scene.EpisodeNumber != 1 {
			t.Fatal("local episode not normalized")
		}
	}
	generation, _, err := s.CreateScriptGeneration("personal-demo", p.ID, "snapshot-target-duration", ScriptGenerationInput{ExpectedVersion: saved.Version})
	if err != nil {
		t.Fatal(err)
	}
	if generation.SourceEpisodeID != first.ID || generation.TargetDurationSeconds != 120 {
		t.Fatal("generation intent missing", generation)
	}
	if err = s.FinishScriptGeneration(generation, bad, nil); err != nil {
		t.Fatal(err)
	}
	current, _ := s.GetProject("personal-demo", p.ID)
	if current.Script == nil || current.LatestGeneration.Status != "failed" || current.Version != saved.Version {
		t.Fatal("multi-episode upstream replaced current single episode")
	}
	projectHTTP(t, s, "PATCH", "/episodes/"+first.ID+q, "forged-episode-title", `{"title":"","expected_version":1,"status":"planned","profile":{"duration_seconds":90,"output_resolution":"1080p","aspect_ratio":"9:16"}}`, 400)
	projectHTTP(t, s, "GET", "/episodes/"+first.ID+"?space_id=team-demo", "", "", 404)
	projectHTTP(t, s, "GET", "/series?space_id=personal-demo&limit=101", "", "", 400)
	projectHTTP(t, s, "GET", "/series?space_id=personal-demo&cursor=bad", "", "", 400)
	// Moving a foreign child must not target a different parent via the URL.
	other := create("/series/"+root.ID+"/seasons", "http-other-season", map[string]any{})
	projectHTTP(t, s, "POST", "/seasons/"+other.ID+"/episode-order"+q, "wrong-parent-order", projectJSON(t, map[string]any{"id": first.ID, "ordinal": 7, "expected_version": first.Version, "expected_parent_version": other.Version}), 404)
}
func TestFileSeriesRoutesCanonicalTitlesLocalEpisodeAndProfiles(t *testing.T) {
	seriesRouteContract(t, testStore(t))
}
func TestPostgresSeriesRoutesCanonicalTitlesLocalEpisodeAndProfiles(t *testing.T) {
	seriesRouteContract(t, postgresTestStore(t, postgresTestDB(t)))
}
func TestSeriesExportSnapshotsSurviveReordering(t *testing.T) {
	s := testStore(t)
	season := seriesFixture(t, s)
	n, p, err := s.CreateSeriesEpisode(context.Background(), seriesActor, "series-export", season.ID, series.Input{Title: "旧成片集号保留"})
	if err != nil {
		t.Fatal(err)
	}
	state, err := s.media.AddAsset(p.ID, p.SpaceID, "series-image", "fixture.png", "shot", "", "", "uploaded", syntheticPNG(1), "image/png")
	if err != nil {
		t.Fatal(err)
	}
	timeline := MediaTimeline{OutputResolution: "1080p", AspectRatio: "16:9", Clips: []MediaClip{{ID: "one-shot", AssetID: state.Assets[0].ID, Duration: .5}}}
	state, err = s.media.SaveTimeline(p.ID, p.SpaceID, "series-cut", state.Version, timeline)
	if err != nil {
		t.Fatal(err)
	}
	exp, err := s.media.StartExport(p.ID, p.SpaceID, "series-export-key", state.Version)
	if err != nil {
		t.Fatal(err)
	}
	if exp.SeriesEpisode == nil || exp.SeriesEpisode.EpisodeID != n.ID || exp.SeriesEpisode.Ordinal != 1 || exp.SeriesEpisode.ProjectLocalEpisode != 1 || exp.Timeline.OutputResolution != "1080p" {
		t.Fatal("export snapshot missing", exp)
	}
	parent, _ := s.series.Get(context.Background(), p.SpaceID, "season", season.ID)
	cmd := series.Command{Operation: "move", Kind: "episode", ID: n.ID, Expected: 1, ExpectedParent: parent.Version, Ordinal: 7}
	if _, err = s.series.Mutate(context.Background(), seriesActor, "move-after-export", cmd, nil); err != nil {
		t.Fatal(err)
	}
	replay, err := s.media.StartExport(p.ID, p.SpaceID, "series-export-key", state.Version)
	if err != nil || replay.SeriesEpisode.Ordinal != 1 {
		t.Fatal("reorder changed accepted export", err)
	}
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		current := s.media.Get(p.ID, p.SpaceID)
		if len(current.Exports) > 0 && current.Exports[0].Status == "succeeded" {
			return
		}
		if len(current.Exports) > 0 && current.Exports[0].Status == "failed" {
			t.Fatal(current.Exports[0].Error)
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("local export did not finish")
}
func TestSeriesRealTextRequestCarriesSingleEpisodeIntent(t *testing.T) {
	fixture := scriptFixture(t)
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var in struct {
			Messages []struct{ Role, Content string }
			Model    string
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			t.Error(err)
		}
		if r.URL.Path != "/chat/completions" || in.Model != "synthetic-text" {
			t.Error("wrong gateway request")
		}
		system := in.Messages[0].Content
		if calls == 1 && (!strings.Contains(system, "episode_number=1") || !strings.Contains(system, "120 seconds") || strings.Contains(system, "exactly 3 scenes")) {
			t.Error("missing per-episode direction", system)
		}
		if calls == 2 && (!strings.Contains(system, "supplied scenes") || strings.Contains(system, "scene_index 1..3")) {
			t.Error("storyboard assumes a fixed script size", system)
		}
		var out any = fixture
		if calls == 2 {
			out = map[string]any{"shots": []StoryboardShot{{ID: "fourth-scene", SceneIndex: 4, Description: "第四个场景中的镜头", ShotSize: "medium", CameraMovement: "static", DurationSeconds: 3}}}
		}
		raw, _ := json.Marshal(out)
		fmt.Fprintf(w, `{"choices":[{"finish_reason":"stop","message":{"content":%s}}]}`, mustJSONString(string(raw)))
	}))
	defer server.Close()
	provider := newCreativeProvider("synthetic", server.URL, server.Client())
	provider.textModel = "synthetic-text"
	if _, err := provider.Script(context.Background(), ProjectInput{Title: "单集创作", Brief: strings.Repeat("字", 2000), SingleEpisode: true, TargetDurationSeconds: 120}); err != nil {
		t.Fatal(err)
	}
	fourScenes := cloneScript(fixture)
	fourScenes.Scenes = append(fourScenes.Scenes, fixture.Scenes[0])
	shots, err := provider.Storyboard(context.Background(), fourScenes)
	if err != nil || len(shots) != 1 || shots[0].SceneIndex != 4 {
		t.Fatal("cannot plan beyond the third supplied scene", err)
	}
	if calls != 2 {
		t.Fatal("unexpected retry or fallback")
	}

}
func mustJSONString(v string) string { b, _ := json.Marshal(v); return string(b) }
func TestSeriesSecureRolesCSRFAndTenantIsolation(t *testing.T) {
	s := testStore(t)
	auth, err := OpenAuthStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	owner, token, session, err := auth.login("series-owner@example.invalid", "Synthetic-series-owner-password!", true)
	if err != nil {
		t.Fatal(err)
	}
	_, viewerToken, viewerSession, err := auth.login("series-viewer@example.invalid", "Synthetic-series-viewer-password!", true)
	if err != nil {
		t.Fatal(err)
	}
	space := auth.spaces(owner.ID)[0]["id"]
	q := "?space_id=" + space
	h := SecureHandler(s, "http://localhost:5173", auth, AuthOptions{AllowRegistration: true})
	body := `{"title":"权限测试系列","summary":"组织内部独立创作空间"}`
	authRequest(t, h, "POST", "/api/v1/series"+q, body, token, "", 403)
	w := authRequest(t, h, "POST", "/api/v1/series"+q, body, token, session.CSRF, 201)
	var root series.Node
	json.Unmarshal(w.Body.Bytes(), &root)
	authRequest(t, h, "GET", "/api/v1/series/"+root.ID+q, "", viewerToken, "", 403)
	authRequest(t, h, "PUT", "/api/v1/organizations/"+space+"/members", `{"email":"series-viewer@example.invalid","role":"viewer"}`, token, session.CSRF, 200)
	authRequest(t, h, "GET", "/api/v1/series/"+root.ID+q, "", viewerToken, "", 200)
	authRequest(t, h, "POST", "/api/v1/series/"+root.ID+"/seasons"+q, `{}`, viewerToken, viewerSession.CSRF, 403)
	authRequest(t, h, "PUT", "/api/v1/organizations/"+space+"/members", `{"email":"series-viewer@example.invalid","role":"remove"}`, token, session.CSRF, 200)
	authRequest(t, h, "GET", "/api/v1/series/"+root.ID+q, "", viewerToken, "", 403)
}

func TestSeriesMediaDirectoryReadFailureIsRecoverable(t *testing.T) {
	s := testStore(t)
	season := seriesFixture(t, s)
	_, p, err := s.CreateSeriesEpisode(context.Background(), seriesActor, "profile-read-recovery", season.ID, series.Input{Title: "可恢复的设置读取"})
	if err != nil {
		t.Fatal(err)
	}
	original := s.media.seriesProfile
	s.media.seriesProfile = func(string, string) (initialMediaProfile, error) {
		return initialMediaProfile{}, fmt.Errorf("synthetic temporary directory outage")
	}
	if _, err = s.media.GetChecked(p.ID, p.SpaceID); err == nil {
		t.Fatal("directory failure silently returned legacy defaults")
	}
	if _, err = s.media.AddAsset(p.ID, p.SpaceID, "profile-upload-retry", "fixture.png", "shot", "", "", "uploaded", syntheticPNG(1), "image/png"); err == nil {
		t.Fatal("published media without its selected defaults")
	}
	if err = s.media.Available(); err != nil {
		t.Fatal("temporary directory read permanently poisoned media", err)
	}
	s.media.seriesProfile = original
	state, err := s.media.AddAsset(p.ID, p.SpaceID, "profile-upload-retry", "fixture.png", "shot", "", "", "uploaded", syntheticPNG(1), "image/png")
	if err != nil {
		t.Fatal(err)
	}
	if state.Timeline.OutputResolution != "1080p" || state.Timeline.AspectRatio != "9:16" {
		t.Fatal("mutation returned a different profile from the persisted media")
	}
	if len(state.Assets) != 1 {
		t.Fatal("temporary failure caused duplicate assets")
	}
	saved, err := s.media.GetChecked(p.ID, p.SpaceID)
	if err != nil || saved.Timeline.OutputResolution != "1080p" || saved.Timeline.AspectRatio != "9:16" {
		t.Fatal("selected defaults not restored", err)
	}
}

func TestSeriesCursorRejectsChangedParentInsteadOfSkippingReorderedEpisodes(t *testing.T) {
	s := testStore(t)
	season := seriesFixture(t, s)
	ctx := context.Background()
	var first series.Node
	for i := 0; i < 21; i++ {
		n, _, err := s.CreateSeriesEpisode(ctx, seriesActor, fmt.Sprintf("cursor-episode-%d", i), season.ID, series.Input{Title: fmt.Sprintf("游标剧集%d", i)})
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = n
		}
	}
	path := "/seasons/" + season.ID + "/episodes?space_id=" + seriesActor.Space
	page := projectHTTP(t, s, "GET", path, "", "", 200)
	token := page["next_cursor"].(string)
	parent, _ := s.series.Get(ctx, seriesActor.Space, "season", season.ID)
	if _, err := s.series.Mutate(ctx, seriesActor, "cursor-reorder", series.Command{Operation: "move", Kind: "episode", ID: first.ID, Expected: 1, ExpectedParent: parent.Version, Ordinal: 21}, nil); err != nil {
		t.Fatal(err)
	}
	projectHTTP(t, s, "GET", path+"&cursor="+token, "", "", 409)
	fresh := projectHTTP(t, s, "GET", path, "", "", 200)
	if fresh["next_cursor"] == "" {
		t.Fatal("refresh lost remaining page")
	}
}

func seriesTwoSeasonCapacityContract(t *testing.T, a, b *FileStore) {
	t.Helper()
	ctx := context.Background()
	firstSeason := seriesFixture(t, a)
	c := series.Command{Operation: "create", Kind: "season", ID: series.NewID(seriesActor, "create", "capacity-season-two"), Parent: firstSeason.SeriesID}
	secondSeason, err := a.series.Mutate(ctx, seriesActor, "capacity-season-two", c, nil)
	if err != nil {
		t.Fatal(err)
	}
	nodes := make([]series.Node, 0, 201)
	for i := 0; i < 200; i++ {
		parent := firstSeason.ID
		if i >= 100 {
			parent = secondSeason.ID
		}
		n, _, err := a.CreateSeriesEpisode(ctx, seriesActor, fmt.Sprintf("capacity-episode-%03d", i), parent, series.Input{Title: fmt.Sprintf("第%d份独立故事", i+1)})
		if err != nil {
			t.Fatal(err)
		}
		nodes = append(nodes, n)
	}
	var wg sync.WaitGroup
	errorsCh := make(chan error, 2)
	for i, s := range []*FileStore{a, b} {
		wg.Add(1)
		go func(i int, s *FileStore) {
			defer wg.Done()
			_, err := s.UpdateProject(seriesActor.Space, nodes[i*100].ProjectID, fmt.Sprintf("independent-edit-%d", i), ProjectUpdate{Title: fmt.Sprintf("同时编辑第%d季", i+1), Brief: "不同集的草稿和版本不能互相覆盖。", ExpectedVersion: 1})
			errorsCh <- err
		}(i, s)
	}
	wg.Wait()
	close(errorsCh)
	for err := range errorsCh {
		if err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 2; i++ {
		p, err := a.GetProject(seriesActor.Space, nodes[i*100].ProjectID)
		if err != nil || p.Version != 2 || p.Title != fmt.Sprintf("同时编辑第%d季", i+1) {
			t.Fatal("independent project overwritten", err)
		}
	}
	inserted, _, err := a.CreateSeriesEpisode(ctx, seriesActor, "capacity-episode-201", secondSeason.ID, series.Input{Title: "插入中段的新一集"})
	if err != nil {
		t.Fatal(err)
	}
	parent, _ := a.series.Get(ctx, seriesActor.Space, "season", secondSeason.ID)
	move := series.Command{Operation: "move", Kind: "episode", ID: inserted.ID, Expected: 1, ExpectedParent: parent.Version, Ordinal: 7, Mode: "insert"}
	if _, err = a.series.Mutate(ctx, seriesActor, "insert-episode-seven", move, nil); err != nil {
		t.Fatal(err)
	}
	for i, n := range nodes[100:] {
		current, err := a.series.Get(ctx, seriesActor.Space, "episode", n.ID)
		want := int64(i + 1)
		if want >= 7 {
			want++
		}
		if err != nil || current.Ordinal != want || current.ProjectID != n.ProjectID {
			t.Fatalf("insertion changed identity or ordering: %d %+v %v", want, current, err)
		}
	}
	current, err := a.series.Get(ctx, seriesActor.Space, "episode", inserted.ID)
	if err != nil || current.Ordinal != 7 || current.ProjectID != inserted.ProjectID {
		t.Fatal("201st episode could not be inserted", err)
	}
	parent, _ = a.series.Get(ctx, seriesActor.Space, "season", secondSeason.ID)
	moving, _ := a.series.Get(ctx, seriesActor.Space, "episode", nodes[101].ID)
	if _, err = a.series.Mutate(ctx, seriesActor, "insert-later-direction", series.Command{Operation: "move", Kind: "episode", ID: moving.ID, Expected: moving.Version, ExpectedParent: parent.Version, Ordinal: 9, Mode: "insert"}, nil); err != nil {
		t.Fatal(err)
	}
	page, err := a.series.List(ctx, seriesActor.Space, seriesrepo.Query{Kind: "episode", Parent: secondSeason.ID, Limit: 100})
	if err != nil || len(page.Items) != 100 || page.Next == "" {
		t.Fatal("first capacity page", err)
	}
	tail, err := a.series.List(ctx, seriesActor.Space, seriesrepo.Query{Kind: "episode", Parent: secondSeason.ID, Limit: 100, After: page.Next})
	if err != nil || len(tail.Items) != 1 || tail.Items[0].Ordinal != 101 {
		t.Fatal("last capacity page", err)
	}
	if len(a.data.Tasks) != 0 || len(a.data.ScriptGenerations) != 0 {
		t.Fatal("directory capacity test dispatched generation")
	}
}
func TestFileTwoSeason201CapacityAndInsert(t *testing.T) {
	s := testStore(t)
	seriesTwoSeasonCapacityContract(t, s, s)
}
func TestPostgresTwoSeason201CapacityAndInsert(t *testing.T) {
	db := postgresTestDB(t)
	seriesTwoSeasonCapacityContract(t, postgresTestStore(t, db), postgresTestStore(t, db))
}

type seriesCaptureScriptProvider struct {
	Input  chan ProjectInput
	Script Script
}

func (p seriesCaptureScriptProvider) Generate(ctx context.Context, in ProjectInput) (Script, error) {
	select {
	case p.Input <- in:
	case <-ctx.Done():
		return Script{}, ctx.Err()
	}
	return p.Script, nil
}
func TestPostgresSeriesQueuedWorkerKeepsEpisodeIntent(t *testing.T) {
	db := postgresTestDB(t)
	api, worker := postgresTestStore(t, db), postgresTestStore(t, db)
	season := seriesFixture(t, api)
	episode, project, err := api.CreateSeriesEpisode(context.Background(), seriesActor, "queue-single-episode", season.ID, series.Input{Title: "跨实例单集创作", Profile: series.Profile{Duration: 120, Resolution: "2160p", Aspect: "9:16"}})
	if err != nil {
		t.Fatal(err)
	}
	g, _, err := api.CreateScriptGeneration(seriesActor.Space, project.ID, "queue-episode-script", ScriptGenerationInput{ExpectedVersion: 1})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	capture := make(chan ProjectInput, 1)
	pipeline, err := NewScriptPipeline(ctx, seriesCaptureScriptProvider{Input: capture, Script: scriptFixture(t)})
	if err != nil {
		t.Fatal(err)
	}
	q := jobqueue.Queue{DB: db}
	done := make(chan error, 1)
	go func() { done <- RunQueuedScriptWorker(ctx, q, worker, pipeline) }()
	defer func() {
		cancel()
		if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
			t.Error(err)
		}
	}()
	select {
	case in := <-capture:
		if !in.SingleEpisode || in.TargetDurationSeconds != 120 || in.Title != project.Title {
			t.Fatal("worker lost persisted episode context", in)
		}
	case <-ctx.Done():
		t.Fatal("worker did not receive accepted series generation")
	}
	for ctx.Err() == nil {
		result, err := q.Get(ctx, seriesActor.Space, g.ID)
		if err != nil {
			t.Fatal(err)
		}
		if result.State == "succeeded" {
			p, err := api.GetProject(seriesActor.Space, project.ID)
			if err != nil || p.LatestGeneration.SourceEpisodeID != episode.ID || p.LatestGeneration.TargetDurationSeconds != 120 {
				t.Fatal("queued result lost intent", err)
			}
			for _, scene := range p.Script.Scenes {
				if scene.EpisodeNumber != 1 {
					t.Fatal("queued result crossed episode boundary")
				}
			}
			return
		}
		if result.State == "failed" || result.State == "unknown" {
			t.Fatal("queued series generation failed", result.State)
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("queued series generation did not finish")
}
