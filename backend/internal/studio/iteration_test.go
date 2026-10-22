package studio

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestInputContractAndDefaults(t *testing.T) {
	for _, kind := range []string{"poster", "portrait", "general"} {
		got, err := Normalize(Input{Prompt: "  三个字  ", Kind: kind})
		if err != nil || got != (Input{Prompt: "三个字", Kind: kind, Ratio: "4:5", Style: "natural"}) {
			t.Fatalf("legacy %s input: %#v, %v", kind, got, err)
		}
		for _, ratio := range []string{"1:1", "4:5", "16:9"} {
			for _, style := range []string{"natural", "studio", "illustration"} {
				in := Input{Prompt: "😀😀😀", Kind: kind, Ratio: ratio, Style: style}
				if got, err := Normalize(in); err != nil || got != in {
					t.Fatalf("valid input changed or rejected: %#v, %v", got, err)
				}
			}
		}
	}
	for _, in := range []Input{
		{Prompt: "😀😀", Kind: "poster"},
		{Prompt: strings.Repeat("字", 1001), Kind: "poster"},
		{Prompt: "valid", Kind: "movie"},
		{Prompt: "valid", Kind: "poster", Ratio: "3:4"},
		{Prompt: "valid", Kind: "poster", Ratio: " 4:5 "},
		{Prompt: "valid", Kind: "poster", Style: "cinematic"},
		{Prompt: "valid", Kind: "poster", Style: "Natural"},
	} {
		if _, err := Normalize(in); err == nil {
			t.Errorf("accepted invalid input: %#v", in)
		}
	}
	if _, err := Normalize(Input{Prompt: strings.Repeat("😀", 1000), Kind: "general"}); err != nil {
		t.Fatalf("1000 Unicode code points rejected: %v", err)
	}
}

type recordingProvider struct {
	inputs []Input
}

func (p *recordingProvider) Generate(_ context.Context, in Input) (string, error) {
	p.inputs = append(p.inputs, in)
	return "provider-observed-output", nil
}

func TestEinoPassesNormalizedParametersAndRejectsBeforeProvider(t *testing.T) {
	provider := &recordingProvider{}
	pipeline, err := NewPipeline(context.Background(), provider)
	if err != nil {
		t.Fatal(err)
	}
	for _, in := range []Input{
		{Prompt: "  legacy prompt  ", Kind: "poster"},
		{Prompt: "  selected parameters  ", Kind: "general", Ratio: "16:9", Style: "illustration"},
	} {
		got, err := pipeline.Invoke(context.Background(), in)
		if err != nil || got != "provider-observed-output" {
			t.Fatalf("Eino did not return provider result: %q, %v", got, err)
		}
		want, _ := Normalize(in)
		if provider.inputs[len(provider.inputs)-1] != want {
			t.Fatalf("provider did not receive normalized input: %#v", provider.inputs)
		}
	}
	for _, in := range []Input{
		{Prompt: "valid", Kind: "poster", Ratio: "3:4"},
		{Prompt: "valid", Kind: "poster", Style: "unknown"},
		{Prompt: "x", Kind: "general"},
	} {
		if _, err := pipeline.Invoke(context.Background(), in); err == nil {
			t.Fatalf("Eino accepted invalid input: %#v", in)
		}
	}
	if len(provider.inputs) != 2 {
		t.Fatalf("invalid requests reached provider: %#v", provider.inputs)
	}
}

func TestMockSVGUsesSelectedCanvasAndVisualParameters(t *testing.T) {
	pipeline, err := NewPipeline(context.Background(), MockProvider{})
	if err != nil {
		t.Fatal(err)
	}
	outputs := map[string]bool{}
	for _, kind := range []string{"poster", "portrait", "general"} {
		for _, ratio := range []string{"1:1", "4:5", "16:9"} {
			for _, style := range []string{"natural", "studio", "illustration"} {
				svg, err := pipeline.Invoke(context.Background(), Input{Prompt: "<script>synthetic</script>", Kind: kind, Ratio: ratio, Style: style})
				if err != nil {
					t.Fatal(err)
				}
				var root struct {
					XMLName xml.Name
					Width   float64 `xml:"width,attr"`
					Height  float64 `xml:"height,attr"`
					ViewBox string  `xml:"viewBox,attr"`
				}
				if err := xml.Unmarshal([]byte(svg), &root); err != nil || root.XMLName.Local != "svg" {
					t.Fatalf("invalid SVG: %v", err)
				}
				parts := strings.Split(ratio, ":")
				width, _ := strconv.ParseFloat(parts[0], 64)
				height, _ := strconv.ParseFloat(parts[1], 64)
				if root.Height <= 0 || math.Abs(root.Width/root.Height-width/height) > 0.001 {
					t.Fatalf("%s canvas is %gx%g", ratio, root.Width, root.Height)
				}
				var x, y, viewWidth, viewHeight float64
				if _, err := fmt.Sscan(root.ViewBox, &x, &y, &viewWidth, &viewHeight); err != nil || viewWidth != root.Width || viewHeight != root.Height {
					t.Fatalf("canvas/viewBox mismatch: %#v", root)
				}
				if strings.Contains(svg, "<script>") || !strings.Contains(svg, "非 AI 生成") {
					t.Fatal("SVG lost escaping or mock label")
				}
				// Exclude text and data-* metadata: labels alone do not prove a visual change.
				decoder := xml.NewDecoder(strings.NewReader(svg))
				var graphics []xml.StartElement
				for {
					token, err := decoder.Token()
					if err == io.EOF {
						break
					}
					if err != nil {
						t.Fatal(err)
					}
					if element, ok := token.(xml.StartElement); ok {
						switch element.Name.Local {
						case "path", "rect", "circle", "ellipse", "polygon", "line":
							graphics = append(graphics, element)
						}
					}
				}
				fingerprint, err := json.Marshal(graphics)
				if err != nil || len(graphics) == 0 {
					t.Fatalf("SVG has no valid graphics: %v", err)
				}
				key := ratio + string(fingerprint)
				if outputs[key] {
					t.Fatalf("selected template/style did not change actual graphics: %s/%s/%s", kind, ratio, style)
				}
				outputs[key] = true
			}
		}
	}
}

func TestNormalizedIdempotencyIncludesEveryParameter(t *testing.T) {
	s := testStore(t)
	original, created, err := s.Create("personal-demo", "normalize-key", Input{Prompt: "  legacy prompt  ", Kind: "poster"})
	if err != nil || !created {
		t.Fatalf("create: %v", err)
	}
	canonical := Input{Prompt: "legacy prompt", Kind: "poster", Ratio: "4:5", Style: "natural"}
	if original.Input != canonical {
		t.Fatalf("store did not normalize input: %#v", original.Input)
	}
	again, created, err := s.Create("personal-demo", "normalize-key", canonical)
	if err != nil || created || again.ID != original.ID {
		t.Fatalf("omitted and explicit defaults not idempotent: %v", err)
	}
	for _, changed := range []Input{
		{Prompt: "new prompt", Kind: "poster", Ratio: "4:5", Style: "natural"},
		{Prompt: "legacy prompt", Kind: "general", Ratio: "4:5", Style: "natural"},
		{Prompt: "legacy prompt", Kind: "poster", Ratio: "1:1", Style: "natural"},
		{Prompt: "legacy prompt", Kind: "poster", Ratio: "4:5", Style: "studio"},
	} {
		if _, _, err := s.Create("personal-demo", "normalize-key", changed); !errors.Is(err, ErrConflict) {
			t.Errorf("changed input did not conflict: %#v, %v", changed, err)
		}
	}
	other, created, err := s.Create("team-demo", "normalize-key", canonical)
	if err != nil || !created || other.ID == original.ID {
		t.Fatalf("key must be scoped to space: %v", err)
	}
}

func TestLegacySnapshotUpgradePreservesHistoryAndKeys(t *testing.T) {
	dir := t.TempDir()
	legacy := `{"version":1,"tasks":{
	 "legacy-success":{"id":"legacy-success","space_id":"personal-demo","prompt":"legacy image","kind":"poster","status":"succeeded","progress":100,"provider":"mock-svg","result_url":"/api/v1/tasks/legacy-success/result?space_id=personal-demo","result_svg":"<svg>original bytes</svg>","idempotency_key":"legacy-key","created_at":"2026-10-20T10:00:00Z","updated_at":"2026-10-20T10:00:01Z"},
	 "legacy-running":{"id":"legacy-running","space_id":"personal-demo","prompt":"pending portrait","kind":"portrait","status":"running","progress":35,"provider":"mock-svg","idempotency_key":"running-key","created_at":"2026-10-20T10:00:02Z","updated_at":"2026-10-20T10:00:03Z"}
	},"assets":{"legacy-success":{"id":"legacy-success","task_id":"legacy-success","space_id":"personal-demo","prompt":"legacy image","kind":"poster","provider":"mock-svg","result_url":"/api/v1/tasks/legacy-success/result?space_id=personal-demo","created_at":"2026-10-20T10:00:04Z"}}}`
	if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte(legacy), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close() }()
	task, err := s.Get("personal-demo", "legacy-success")
	if err != nil || task.Ratio != "4:5" || task.Style != "natural" || task.SVG != "<svg>original bytes</svg>" || task.Key != "legacy-key" || task.CreatedAt.Format("2006-01-02T15:04:05Z") != "2026-10-20T10:00:00Z" {
		t.Fatalf("legacy history changed: %#v, %v", task, err)
	}
	asset := s.Assets("personal-demo")[0]
	if asset.Ratio != "4:5" || asset.Style != "natural" || asset.ID != "legacy-success" || asset.ResultURL != task.ResultURL {
		t.Fatalf("legacy asset not migrated: %#v", asset)
	}
	recovered, err := s.Get("personal-demo", "legacy-running")
	if err != nil || recovered.Status != "queued" || recovered.Progress != 0 || recovered.Ratio != "4:5" || recovered.Style != "natural" {
		t.Fatalf("legacy running recovery: %#v, %v", recovered, err)
	}
	for _, input := range []Input{{Prompt: task.Prompt, Kind: task.Kind}, task.Input} {
		again, created, err := s.Create("personal-demo", "legacy-key", input)
		if err != nil || created || again.ID != task.ID {
			t.Fatalf("legacy key no longer idempotent: %v", err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := s.Get("personal-demo", task.ID)
	if err != nil || reloaded.Input != task.Input || reloaded.SVG != task.SVG || s.Assets("personal-demo")[0] != asset {
		t.Fatalf("migration did not persist unchanged history: %#v, %v", reloaded, err)
	}
}

func TestHTTPParametersAndExplicitRetryPreserveFailedTask(t *testing.T) {
	s := testStore(t)
	h := Handler(s, "http://localhost:5173")
	call := func(method, path, body, key string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Idempotency-Key", key)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	path := "/api/v1/tasks?space_id=personal-demo"
	body := `{"prompt":"[mock-fail] synthetic portrait","kind":"portrait","ratio":"16:9","style":"studio"}`
	response := call("POST", path, body, "original-failure-key")
	if response.Code != 202 {
		t.Fatal(response.Code, response.Body.String())
	}
	var failed Task
	if err := json.Unmarshal(response.Body.Bytes(), &failed); err != nil {
		t.Fatal(err)
	}
	if failed.Ratio != "16:9" || failed.Style != "studio" {
		t.Fatal("task response lost parameters")
	}
	stored, _ := s.Get("personal-demo", failed.ID)
	if err := s.Finish(stored, "", errors.New("synthetic failure")); err != nil {
		t.Fatal(err)
	}
	response = call("POST", path, body, "original-failure-key")
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"status":"failed"`) {
		t.Fatal("network retry changed failed task", response.Code, response.Body.String())
	}
	retryPath := "/api/v1/tasks/" + failed.ID + "/retry?space_id=personal-demo"
	retryBody := `{"prompt":"recovered synthetic portrait","kind":"portrait","ratio":"16:9","style":"studio"}`
	response = call("POST", retryPath, retryBody, "explicit-retry-new-key")
	var retry Task
	if err := json.Unmarshal(response.Body.Bytes(), &retry); err != nil || response.Code != 202 || retry.ID == failed.ID || retry.RetryOf != failed.ID {
		t.Fatal("explicit retry did not create new task", response.Code, err)
	}
	if replay := call("POST", retryPath, retryBody, "explicit-retry-new-key"); replay.Code != 200 || !strings.Contains(replay.Body.String(), retry.ID) {
		t.Fatal("retry replay did not return child", replay.Code, replay.Body.String())
	}
	if replay := call("POST", retryPath, body, "explicit-retry-new-key"); replay.Code != 409 {
		t.Fatal("retry key allowed a changed body", replay.Code)
	}
	if replay := call("POST", path, retryBody, "explicit-retry-new-key"); replay.Code != 409 {
		t.Fatal("retry key reused as ordinary create", replay.Code)
	}
	if replay := call("POST", retryPath, body, "original-failure-key"); replay.Code != 409 {
		t.Fatal("ordinary create key reused for retry", replay.Code)
	}
	if replay := call("POST", "/api/v1/tasks/"+failed.ID+"/retry?space_id=team-demo", retryBody, "cross-space-retry"); replay.Code != 404 {
		t.Fatal("cross-space retry exposed parent", replay.Code)
	}
	if replay := call("POST", "/api/v1/tasks/missing/retry?space_id=personal-demo", retryBody, "missing-parent-key"); replay.Code != 404 {
		t.Fatal("missing parent retry accepted", replay.Code)
	}
	if replay := call("POST", "/api/v1/tasks/"+retry.ID+"/retry?space_id=personal-demo", retryBody, "queued-parent-key"); replay.Code != 409 {
		t.Fatal("queued task accepted as retry parent", replay.Code)
	}
	other, _, err := s.Create("personal-demo", "other-parent-key", Input{Prompt: "another synthetic failure", Kind: "general"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Finish(other, "", errors.New("synthetic failure")); err != nil {
		t.Fatal(err)
	}
	if replay := call("POST", "/api/v1/tasks/"+other.ID+"/retry?space_id=personal-demo", retryBody, "explicit-retry-new-key"); replay.Code != 409 {
		t.Fatal("retry key reused for different parent", replay.Code)
	}
	original, _ := s.Get("personal-demo", failed.ID)
	if original.Status != "failed" || original.Input != failed.Input || len(s.List("personal-demo")) != 3 {
		t.Fatal("explicit retry mutated history")
	}
	for _, invalid := range []string{
		`{"prompt":"valid","kind":"poster","ratio":"3:4"}`,
		`{"prompt":"valid","kind":"poster","style":"photographic"}`,
		`{"prompt":"valid","kind":"poster","ratio":12}`,
		`{"prompt":"valid","kind":"poster","ratio":null}`,
		`{"prompt":"valid","kind":"poster","style":null}`,
		`{"prompt":"valid","kind":"poster","style":12}`,
		`{"prompt":"valid","kind":"poster","ratio":{}}`,
		`{"prompt":"valid","kind":"poster","style":[]}`,
	} {
		if response = call("POST", path, invalid, "invalid-parameters"); response.Code != 400 {
			t.Fatalf("invalid parameters accepted: %d %s", response.Code, response.Body.String())
		}
		if response = call("POST", retryPath, invalid, "invalid-retry-parameters"); response.Code != 400 {
			t.Fatalf("invalid retry parameters accepted: %d %s", response.Code, response.Body.String())
		}
	}
	if len(s.List("personal-demo")) != 3 {
		t.Fatal("invalid create/retry parameters were queued")
	}
	pipeline, err := NewPipeline(context.Background(), MockProvider{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- RunWorker(ctx, s, pipeline) }()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	deadline := time.Now().Add(3 * time.Second)
	for {
		stored, err = s.Get("personal-demo", retry.ID)
		if err != nil {
			t.Fatal(err)
		}
		if stored.Status == "succeeded" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("retry was not scheduled and executed by Eino worker")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if stored.RetryOf != failed.ID || !strings.Contains(stored.SVG, "非 AI 生成") {
		t.Fatal("worker lost retry relation or mock result")
	}
	if replay := call("POST", retryPath, retryBody, "explicit-retry-new-key"); replay.Code != 200 || !strings.Contains(replay.Body.String(), `"status":"succeeded"`) {
		t.Fatal("completed retry replay changed result", replay.Code, replay.Body.String())
	}
	if replay := call("POST", "/api/v1/tasks/"+retry.ID+"/retry?space_id=personal-demo", retryBody, "succeeded-parent-key"); replay.Code != 409 {
		t.Fatal("completed task accepted as retry parent", replay.Code)
	}
	response = call("POST", "/api/v1/assets?space_id=personal-demo", fmt.Sprintf(`{"task_id":%q}`, retry.ID), "")
	var asset Asset
	if err := json.Unmarshal(response.Body.Bytes(), &asset); err != nil || response.Code != 200 || asset.Ratio != "16:9" || asset.Style != "studio" {
		t.Fatalf("saved asset lost parameters: %#v, %v", asset, err)
	}
	response = call("GET", path, "", "")
	if strings.Contains(response.Body.String(), "idempotency_key") || strings.Contains(response.Body.String(), "result_svg") {
		t.Fatal("task listing leaked internal persistence fields")
	}
}

func TestRetryConcurrencyAndRestartPersistence(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close() }()
	parent, _, err := s.Create("personal-demo", "persist-parent-key", Input{Prompt: "synthetic failure", Kind: "general"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Finish(parent, "", errors.New("synthetic failure")); err != nil {
		t.Fatal(err)
	}
	input := Input{Prompt: " retry synthetic image ", Kind: "general", Ratio: "1:1", Style: "illustration"}
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, _, err := s.Retry("personal-demo", parent.ID, "persist-retry-key", input); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if len(s.List("personal-demo")) != 2 {
		t.Fatal("concurrent retry created duplicate children")
	}
	child, created, err := s.Retry("personal-demo", parent.ID, "persist-retry-key", input)
	if err != nil || created || child.RetryOf != parent.ID {
		t.Fatalf("retry replay: %#v, %v", child, err)
	}
	if err := s.Finish(child, "<svg>persisted retry image</svg>", nil); err != nil {
		t.Fatal(err)
	}
	asset, err := s.SaveAsset("personal-demo", child.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	restored, created, err := s.Retry("personal-demo", parent.ID, "persist-retry-key", input)
	if err != nil || created || restored.ID != child.ID || restored.RetryOf != parent.ID || restored.Status != "succeeded" || restored.SVG != "<svg>persisted retry image</svg>" {
		t.Fatalf("retry identity/result did not survive restart: %#v, %v", restored, err)
	}
	original, err := s.Get("personal-demo", parent.ID)
	if err != nil || original.Status != "failed" || original.Input != parent.Input || original.RetryOf != "" {
		t.Fatalf("retry replaced original failure: %#v, %v", original, err)
	}
	assets := s.Assets("personal-demo")
	if len(assets) != 1 || assets[0] != asset || asset.Ratio != "1:1" || asset.Style != "illustration" {
		t.Fatalf("retry asset parameters not persisted: %#v", assets)
	}
}

func TestHTTPEmptyAndOmittedParametersShareDefaults(t *testing.T) {
	s := testStore(t)
	parent, _, err := s.Create("personal-demo", "default-parent-key", Input{Prompt: "synthetic default parent", Kind: "poster"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Finish(parent, "", errors.New("synthetic failure")); err != nil {
		t.Fatal(err)
	}
	h := Handler(s, "http://localhost:5173")
	for index, path := range []string{"/api/v1/tasks?space_id=personal-demo", "/api/v1/tasks/" + parent.ID + "/retry?space_id=personal-demo"} {
		var originalID string
		for attempt, body := range []string{
			`{"prompt":"default synthetic image","kind":"poster"}`,
			`{"prompt":" default synthetic image ","kind":"poster","ratio":"","style":""}`,
			`{"prompt":"default synthetic image","kind":"poster","ratio":"4:5","style":"natural"}`,
		} {
			r := httptest.NewRequest("POST", path, strings.NewReader(body))
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Idempotency-Key", fmt.Sprintf("default-request-%d", index))
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			wantCode := 200
			if attempt == 0 {
				wantCode = 202
			}
			var task Task
			if err := json.Unmarshal(w.Body.Bytes(), &task); err != nil || w.Code != wantCode || task.Ratio != "4:5" || task.Style != "natural" {
				t.Fatalf("default request %d/%d: %d %s (%v)", index, attempt, w.Code, w.Body.String(), err)
			}
			if attempt == 0 {
				originalID = task.ID
			} else if task.ID != originalID {
				t.Fatal("equivalent parameter defaults created duplicate tasks")
			}
		}
	}
}

func TestTemplateCatalogAndServerSideAssetSearch(t *testing.T) {
	s := testStore(t)
	h := Handler(s, "http://localhost:5173")
	get := func(path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		return w
	}
	response := get("/api/v1/templates")
	var catalog struct {
		Templates []struct {
			ID          string `json:"id"`
			Name        string `json:"name"`
			Description string `json:"description"`
			Input
		} `json:"templates"`
		Ratios []struct{ Value, Label string } `json:"ratios"`
		Styles []struct{ Value, Label string } `json:"styles"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &catalog); err != nil || response.Code != 200 {
		t.Fatalf("catalog unavailable: %d, %v", response.Code, err)
	}
	if len(catalog.Templates) != 3 || len(catalog.Ratios) != 3 || len(catalog.Styles) != 3 {
		t.Fatalf("unexpected backend catalog: %#v", catalog)
	}
	wantTemplates := map[string][2]string{"poster": {"4:5", "illustration"}, "portrait": {"4:5", "studio"}, "general": {"1:1", "natural"}}
	seen := map[string]bool{}
	for _, item := range catalog.Templates {
		want, ok := wantTemplates[item.Kind]
		if !ok || seen[item.Kind] || item.ID == "" || item.Name == "" || item.Description == "" || item.Ratio != want[0] || item.Style != want[1] {
			t.Fatalf("invalid template: %#v", item)
		}
		seen[item.Kind] = true
		if _, err := Normalize(item.Input); err != nil {
			t.Fatalf("server catalog cannot be submitted: %v", err)
		}
	}
	for _, ratio := range catalog.Ratios {
		if ratio.Label == "" {
			t.Fatal("missing ratio label")
		}
		if _, err := Normalize(Input{Prompt: "valid", Kind: "general", Ratio: ratio.Value}); err != nil {
			t.Fatal(err)
		}
	}
	for _, style := range catalog.Styles {
		if style.Label == "" {
			t.Fatal("missing style label")
		}
		if _, err := Normalize(Input{Prompt: "valid", Kind: "general", Style: style.Value}); err != nil {
			t.Fatal(err)
		}
	}
	for index, fixture := range []struct{ space, prompt, kind string }{
		{"personal-demo", "Synthetic CAFE 春日海报", "poster"},
		{"personal-demo", "Synthetic cafe 肖像", "portrait"},
		{"personal-demo", "其他通用构图", "general"},
		{"team-demo", "Synthetic CAFE 团队空间", "poster"},
	} {
		task, _, err := s.Create(fixture.space, fmt.Sprintf("search-fixture-%d", index), Input{Prompt: fixture.prompt, Kind: fixture.kind, Ratio: "1:1", Style: "natural"})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Finish(task, "<svg/>", nil); err != nil {
			t.Fatal(err)
		}
		if _, err := s.SaveAsset(fixture.space, task.ID); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		query, kind string
		count       int
	}{
		{"", "", 3}, {"  cAfE  ", "", 2}, {"cafe", "poster", 1}, {"cafe", "general", 0},
		{"春日", "poster", 1}, {"", "portrait", 1}, {"unmatched", "", 0},
	} {
		response = get("/api/v1/assets?space_id=personal-demo&q=" + url.QueryEscape(tc.query) + "&kind=" + tc.kind)
		var assets []Asset
		if err := json.Unmarshal(response.Body.Bytes(), &assets); err != nil || response.Code != 200 || len(assets) != tc.count {
			t.Fatalf("query %#v: %d %s (%v)", tc, response.Code, response.Body.String(), err)
		}
		for _, asset := range assets {
			if asset.SpaceID != "personal-demo" || asset.Ratio == "" || asset.Style == "" {
				t.Fatal("search leaked space or lost parameters")
			}
		}
	}
	for _, query := range []string{"kind=movie", "q=" + url.QueryEscape(strings.Repeat("😀", 101))} {
		response = get("/api/v1/assets?space_id=personal-demo&" + query)
		if response.Code != 400 || !strings.Contains(response.Body.String(), `"code":"invalid_input"`) {
			t.Fatalf("invalid search accepted: %d %s", response.Code, response.Body.String())
		}
	}
	response = get("/api/v1/assets?space_id=personal-demo&q=" + url.QueryEscape("  "+strings.Repeat("😀", 100)+"  "))
	if response.Code != 200 {
		t.Fatal("100 Unicode code points should be accepted after trim", response.Code)
	}
}
