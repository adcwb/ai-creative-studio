package application

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func projectHTTP(t *testing.T, s *FileStore, method, path, key, body string, want int) map[string]any {
	t.Helper()
	request := httptest.NewRequest(method, "/api/v1"+path, strings.NewReader(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	if key != "" {
		request.Header.Set("Idempotency-Key", key)
	}
	response := httptest.NewRecorder()
	Handler(s, "").ServeHTTP(response, request)
	if response.Code != want {
		t.Fatalf("%s %s expected %d, received %d: %s", method, path, want, response.Code, response.Body.String())
	}
	var value map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &value); err != nil {
		t.Fatal("invalid object response", err, response.Body.String())
	}
	return value
}
func projectJSON(t *testing.T, value any) string {
	t.Helper()
	b, e := json.Marshal(value)
	if e != nil {
		t.Fatal(e)
	}
	return string(b)
}
func projectErrorCode(t *testing.T, value map[string]any, want string) {
	t.Helper()
	problem, ok := value["error"].(map[string]any)
	if !ok || problem["code"] != want {
		t.Fatalf("expected %s, received %v", want, value)
	}
}

func TestProjectHTTPInputValidationAndSpaceBoundaries(t *testing.T) {
	s := testStore(t)
	for _, body := range []string{`{}`, `null`, `{"title":"Title"}`, `{"brief":"brief"}`, `{"title":null,"brief":"brief"}`, `{"title":"Title","brief":null}`, `{"title":3,"brief":"brief"}`, `{"title":" ","brief":"brief"}`, `{"title":"Title","brief":"😀😀"}`, `{"title":"Title","brief":"brief","extra":true}`, projectJSON(t, map[string]any{"title": strings.Repeat("名", 81), "brief": "brief"}), projectJSON(t, map[string]any{"title": "Title", "brief": strings.Repeat("😀", 2001)})} {
		response := projectHTTP(t, s, "POST", "/projects?space_id=personal-demo", "invalid-project-key", body, 400)
		want := "invalid_input"
		if strings.Contains(body, `"title":3`) || strings.Contains(body, `"extra"`) || strings.Contains(body, "null") {
			want = "invalid_json"
		}
		projectErrorCode(t, response, want)
	}
	response := projectHTTP(t, s, "POST", "/projects?space_id=personal-demo", "bad", "{\"title\":\"Title\",\"brief\":\"brief\"}", 400)
	projectErrorCode(t, response, "invalid_key")
	body := projectJSON(t, map[string]any{"title": "  合成短剧项目  ", "brief": "  一位修钟师在旧车站找回迟到的信件。  "})
	created := projectHTTP(t, s, "POST", "/projects?space_id=personal-demo", "project-create-key", body, 201)
	if created["title"] != "合成短剧项目" || created["brief"] != "一位修钟师在旧车站找回迟到的信件。" || created["version"] != float64(1) {
		t.Fatal("create normalization/version", created)
	}
	id := created["id"].(string)
	replay := projectHTTP(t, s, "POST", "/projects?space_id=personal-demo", "project-create-key", `{"title":"合成短剧项目","brief":"一位修钟师在旧车站找回迟到的信件。"}`, 200)
	if replay["id"] != id {
		t.Fatal("duplicate project created")
	}
	projectErrorCode(t, projectHTTP(t, s, "POST", "/projects?space_id=personal-demo", "project-create-key", `{"title":"不同名称","brief":"不同的创作需求"}`, 409), "idempotency_conflict")
	for _, path := range []string{"/projects/" + id + "?space_id=team-demo", "/projects/" + id + "/generations?space_id=team-demo"} {
		projectErrorCode(t, projectHTTP(t, s, "GET", path, "", "", 404), "not_found")
	}
	for _, item := range []struct{ method, path, body string }{{"PUT", "/projects/" + id, `{"title":"其他项目","brief":"跨空间覆盖尝试","expected_version":1}`}, {"POST", "/projects/" + id + "/generations", `{"expected_version":1}`}, {"PUT", "/projects/" + id + "/script", `{"script":{},"expected_version":1}`}} {
		// Script-body validation may run before ownership, so use a valid structured fixture.
		if strings.HasSuffix(item.path, "/script") {
			item.body = projectJSON(t, map[string]any{"script": projectScriptFixture(), "expected_version": 1})
		}
		projectErrorCode(t, projectHTTP(t, s, item.method, item.path+"?space_id=team-demo", "cross-space-write", item.body, 404), "not_found")
	}
	for _, path := range []string{"/projects?space_id=personal-demo", "/projects?space_id=team-demo"} {
		r := httptest.NewRecorder()
		Handler(s, "").ServeHTTP(r, httptest.NewRequest("GET", "/api/v1"+path, nil))
		var projects []map[string]any
		if e := json.Unmarshal(r.Body.Bytes(), &projects); e != nil || r.Code != 200 {
			t.Fatal("project list", r.Code, e)
		}
		want := 1
		if strings.Contains(path, "team-demo") {
			want = 0
		}
		if len(projects) != want {
			t.Fatal("invalid writes or cross-space listing", projects)
		}
	}
}

func projectScriptFixture() map[string]any {
	return map[string]any{
		"title": "车站来信", "logline": "修钟师与快递员合作，把迟到二十年的信送到收件人手中。",
		"characters": []any{map[string]any{"name": "林钟", "description": "认真且不善表达的修钟师。"}, map[string]any{"name": "阿雨", "description": "愿意倾听的年轻快递员。"}},
		"scenes": []any{
			map[string]any{"heading": "第一场：旧车站", "action": "林钟从停摆的钟后发现信件。", "dialogue": []any{map[string]any{"character": "林钟", "text": "这封信还在等人。"}}},
			map[string]any{"heading": "第二场：雨中街道", "action": "阿雨核对地址，二人沿旧地图寻找。", "dialogue": []any{map[string]any{"character": "阿雨", "text": "只要还有地址，就能找到。"}}},
			map[string]any{"heading": "第三场：钟楼门口", "action": "二人把信投入约好的信箱，钟声响起。", "dialogue": []any{map[string]any{"character": "林钟", "text": "这一次，没有迟到。"}}},
		},
	}
}

func TestProjectHTTPRejectsInvalidWriteShapesWithoutMutation(t *testing.T) {
	s := testStore(t)
	p := createProject(t, s)
	for _, extra := range []string{``, `,"expected_version":null`, `,"expected_version":0`, `,"expected_version":-1`, `,"expected_version":1.5`, `,"expected_version":"1"`, `,"expected_version":1,"unexpected":true`} {
		body := `{"title":"合法标题","brief":"合法创作简报"` + extra + `}`
		result := projectHTTP(t, s, "PUT", "/projects/"+p.ID+"?space_id=personal-demo", "invalid-update-key", body, 400)
		want := "invalid_input"
		if strings.Contains(extra, "null") || strings.Contains(extra, "1.5") || strings.Contains(extra, `"1"`) || strings.Contains(extra, "unexpected") {
			want = "invalid_json"
		}
		projectErrorCode(t, result, want)
	}
	for _, body := range []string{`null`, `{"title":"合法标题","title":null,"brief":"合法创作简报"}`, `{"title":"合法标题","brief":"合法创作简报"}` + strings.Repeat(" ", 1<<20)} {
		projectErrorCode(t, projectHTTP(t, s, "POST", "/projects?space_id=personal-demo", "invalid-shape-key", body, 400), "invalid_json")
	}
	script := projectScriptFixture()
	script["scenes"].([]any)[0].(map[string]any)["unexpected"] = "extra"
	projectErrorCode(t, projectHTTP(t, s, "PUT", "/projects/"+p.ID+"/script?space_id=personal-demo", "invalid-script-key", projectJSON(t, map[string]any{"expected_version": 1, "script": script}), 400), "invalid_json")
	manual, _, _ := s.CreateProject("personal-demo", "manual-script-project", projectInput())
	projectHTTP(t, s, "PUT", "/projects/"+manual.ID+"/script?space_id=personal-demo", "manual-script-save", projectJSON(t, map[string]any{"expected_version": 1, "script": projectScriptFixture()}), 200)
	got, _ := s.GetProject("personal-demo", p.ID)
	if got.Version != 1 || got.Script != nil || len(s.ListProjects("personal-demo")) != 2 {
		t.Fatal("invalid write mutated persistent project")
	}
}
