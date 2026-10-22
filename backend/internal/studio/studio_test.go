package studio

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func testStore(t *testing.T) *FileStore {
	t.Helper()
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
func TestPipeline(t *testing.T) {
	p, err := NewPipeline(context.Background(), MockProvider{})
	if err != nil {
		t.Fatal(err)
	}
	svg, err := p.Invoke(context.Background(), Input{Prompt: "  <script>危险</script>  ", Kind: "poster"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(svg, "<script>") || !strings.Contains(svg, "非 AI 生成") {
		t.Fatal("unescaped or unlabeled SVG")
	}
	if _, err = p.Invoke(context.Background(), Input{Prompt: "x", Kind: "poster"}); err == nil {
		t.Fatal("expected normalization error")
	}
	if _, err = p.Invoke(context.Background(), Input{Prompt: "[mock-fail] test", Kind: "poster"}); err == nil {
		t.Fatal("expected provider error")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = p.Invoke(ctx, Input{Prompt: "cancel", Kind: "portrait"}); err == nil {
		t.Fatal("expected cancellation")
	}
}
func TestRecoveryAndOwnership(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = OpenStore(dir); err == nil {
		t.Fatal("second writer accepted")
	}
	task, _, err := s.Create("personal-demo", "test-key", Input{Prompt: "test image", Kind: "poster"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Get("team-demo", task.ID); err != ErrNotFound {
		t.Fatal("cross-space read")
	}
	if _, err = s.SaveAsset("team-demo", task.ID); err != ErrNotFound {
		t.Fatal("cross-space save")
	}
	if _, err = s.SaveAsset("personal-demo", task.ID); err != ErrConflict {
		t.Fatal("unfinished asset")
	}
	if _, ok, err := s.Next(); !ok || err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	recovered, err := s.Get("personal-demo", task.ID)
	if err != nil || recovered.Status != "queued" {
		t.Fatal("not recovered", err)
	}
	if err = s.Finish(recovered, "<svg/>", nil); err != nil {
		t.Fatal(err)
	}
	a, err := s.SaveAsset("personal-demo", task.ID)
	if err != nil {
		t.Fatal(err)
	}
	a2, err := s.SaveAsset("personal-demo", task.ID)
	if err != nil || a.ID != a2.ID || len(s.Assets("personal-demo")) != 1 {
		t.Fatal("asset save not idempotent")
	}
	s.Close()
	s, err = OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	restored, err := s.Get("personal-demo", task.ID)
	if err != nil || restored.SVG != "<svg/>" || len(s.Assets("personal-demo")) != 1 {
		t.Fatal("result/asset not persistent")
	}
	again, created, err := s.Create("personal-demo", "test-key", task.Input)
	if err != nil || created || again.ID != task.ID {
		t.Fatal("idempotency not persistent")
	}
}
func TestCorruptStateRefused(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenStore(dir); err == nil {
		t.Fatal("silently reset corrupt data")
	}
}
func TestConcurrentIdempotency(t *testing.T) {
	s := testStore(t)
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, _, err := s.Create("personal-demo", "same-key", Input{Prompt: "test", Kind: "poster"}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if len(s.List("personal-demo")) != 1 {
		t.Fatal("duplicate tasks")
	}
	if _, _, err := s.Create("personal-demo", "same-key", Input{Prompt: "different", Kind: "poster"}); err != ErrConflict {
		t.Fatal("key reuse not rejected")
	}
}
func TestHTTPWorkflow(t *testing.T) {
	s := testStore(t)
	h := Handler(s, "http://localhost:5173")
	call := func(method, path, body, key string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Idempotency-Key", key)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	path := "/api/v1/tasks?space_id=personal-demo"
	body := `{"prompt":"春日海报","kind":"poster"}`
	w := call("POST", path, body, "workflow-key")
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	var task Task
	if err := json.Unmarshal(w.Body.Bytes(), &task); err != nil {
		t.Fatal(err)
	}
	if task.Key != "" {
		t.Fatal("internal key exposed")
	}
	if w = call("POST", path, body, "workflow-key"); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if w = call("POST", path, `{"prompt":"不同描述","kind":"poster"}`, "workflow-key"); w.Code != 409 {
		t.Fatal(w.Code)
	}
	for _, invalid := range []string{`{"prompt":"x","kind":"poster"}`, `{"prompt":"valid","kind":"movie"}`, body + body, `{"prompt":"valid","kind":"poster","extra":1}`} {
		if w = call("POST", path, invalid, "invalid-key"); w.Code != 400 {
			t.Fatal(w.Code)
		}
	}
	if w = call("GET", "/api/v1/tasks/"+task.ID+"?space_id=team-demo", "", ""); w.Code != 404 {
		t.Fatal(w.Code)
	}
	p, err := NewPipeline(context.Background(), MockProvider{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- RunWorker(ctx, s, p) }()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	deadline := time.Now().Add(3 * time.Second)
	for {
		task, err = s.Get("personal-demo", task.ID)
		if err != nil {
			t.Fatal(err)
		}
		if task.Status == "succeeded" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("worker timed out")
		}
		time.Sleep(20 * time.Millisecond)
	}
	w = call("GET", task.ResultURL, "", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "非 AI 生成") {
		t.Fatal(w.Code)
	}
	w = call("POST", "/api/v1/assets?space_id=personal-demo", `{"task_id":"`+task.ID+`"}`, "")
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	if len(s.Assets("personal-demo")) != 1 || len(s.Assets("team-demo")) != 0 {
		t.Fatal("asset ownership")
	}
	r := httptest.NewRequest(http.MethodGet, path, nil)
	r.Header.Set("Origin", "https://evil.example")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("unexpected CORS acceptance")
	}
}
func TestWorkerFailure(t *testing.T) {
	s := testStore(t)
	task, _, err := s.Create("personal-demo", "fail-key", Input{Prompt: "[mock-fail] test", Kind: "portrait"})
	if err != nil {
		t.Fatal(err)
	}
	p, err := NewPipeline(context.Background(), MockProvider{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	if err = RunWorker(ctx, s, p); err != nil {
		t.Fatal(err)
	}
	task, err = s.Get("personal-demo", task.ID)
	if err != nil || task.Status != "failed" || task.ResultURL != "" {
		t.Fatal("invalid failed task", err)
	}
}

func TestFailedCommitDoesNotPublishTask(t *testing.T) {
	s := testStore(t)
	// Force rename to fail after the temporary file was written.
	s.path = filepath.Dir(s.path)
	_, _, err := s.Create("personal-demo", "failure-key", Input{Prompt: "failure test", Kind: "poster"})
	if err == nil {
		t.Fatal("expected persistence failure")
	}
	if len(s.List("personal-demo")) != 0 {
		t.Fatal("uncommitted task visible")
	}
}
