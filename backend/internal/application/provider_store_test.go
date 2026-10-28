package application

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func realStore(t *testing.T, dir string) *FileStore {
	t.Helper()
	s, e := OpenStore(dir, realCapabilities())
	if e != nil {
		t.Fatal(e)
	}
	return s
}
func realResult(t *testing.T) GenerationResult {
	return GenerationResult{Data: pngFixture(t, 1024, 1024), MIMEType: "image/png", Width: 1024, Height: 1024}
}
func realInput() Input {
	return Input{Prompt: "Synthetic persistent PNG fixture", Kind: "general", Ratio: "1:1", Style: "natural"}
}
func TestPNGResultPersistenceOwnershipAndSafeFiles(t *testing.T) {
	dir := t.TempDir()
	s := realStore(t, dir)
	task, _, e := s.Create("personal-demo", "png-key", realInput())
	if e != nil {
		t.Fatal(e)
	}
	result := realResult(t)
	if e = s.Finish(task, result, nil); e != nil {
		t.Fatal(e)
	}
	if e = s.Finish(task, result, nil); !errors.Is(e, ErrConflict) {
		t.Fatal("terminal overwrite", e)
	}
	a, e := s.SaveAsset("personal-demo", task.ID)
	if e != nil || a.Model != OpenAIModel || a.ResultMIME != "image/png" || a.ResultWidth != 1024 || a.ResultHeight != 1024 {
		t.Fatal("metadata", a, e)
	}
	public, _ := s.Get("personal-demo", task.ID)
	b, _ := json.Marshal(public.Public())
	if bytes.Contains(b, []byte("result_path")) || bytes.Contains(b, []byte("idempotency_key")) {
		t.Fatal("private metadata exposed")
	}
	if _, e = s.ReadResult("team-demo", task.ID); !errors.Is(e, ErrNotFound) {
		t.Fatal("cross-space read")
	}
	if _, e = s.SaveAsset("team-demo", task.ID); !errors.Is(e, ErrNotFound) {
		t.Fatal("cross-space save")
	}
	path := filepath.Join(dir, "results", task.ID+".png")
	info, e := os.Stat(path)
	if e != nil || info.Mode().Perm() != 0600 {
		t.Fatal("permissions", e)
	}
	s.Close()
	s = realStore(t, dir)
	defer s.Close()
	restored, e := s.ReadResult("personal-demo", task.ID)
	if e != nil || !bytes.Equal(restored.Data, result.Data) || len(s.Assets("personal-demo")) != 1 {
		t.Fatal("restart lost PNG/asset", e)
	}
	rec := httptest.NewRecorder()
	Handler(s, "").ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/tasks/"+task.ID+"/result?space_id=personal-demo", nil))
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "image/png" || !bytes.Equal(rec.Body.Bytes(), result.Data) {
		t.Fatal("PNG HTTP", rec.Code)
	}
	outside := filepath.Join(t.TempDir(), "outside.png")
	os.WriteFile(outside, result.Data, 0600)
	os.Remove(path)
	if e = os.Symlink(outside, path); e != nil {
		t.Fatal(e)
	}
	if _, e = s.ReadResult("personal-demo", task.ID); e == nil {
		t.Fatal("followed symlink")
	}
	os.Remove(path)
	if e = syscall.Mkfifo(path, 0600); e != nil {
		t.Fatal(e)
	}
	done := make(chan error, 1)
	go func() { _, e := s.ReadResult("personal-demo", task.ID); done <- e }()
	select {
	case e = <-done:
		if e == nil {
			t.Fatal("FIFO accepted")
		}
	case <-time.After(time.Second):
		t.Fatal("FIFO blocked")
	}
	os.Remove(path)
	os.WriteFile(path, result.Data, 0600)
	s.mu.Lock()
	bad := s.data.Tasks[task.ID]
	bad.ResultPath = "../outside.png"
	s.data.Tasks[task.ID] = bad
	s.mu.Unlock()
	if _, e = s.ReadResult("personal-demo", task.ID); e == nil {
		t.Fatal("traversal accepted")
	}
}
func TestInvalidOrUncommittedPNGNeverPublishesSuccess(t *testing.T) {
	for _, scenario := range []string{"invalid-png", "wrong-mime", "closed-root", "commit-failure"} {
		t.Run(scenario, func(t *testing.T) {
			s := realStore(t, t.TempDir())
			defer s.Close()
			task, _, e := s.Create("personal-demo", "key", realInput())
			if e != nil {
				t.Fatal(e)
			}
			result := realResult(t)
			switch scenario {
			case "invalid-png":
				result.Data = []byte("not png")
			case "wrong-mime":
				result.MIMEType = "image/svg+xml"
			case "closed-root":
				s.results.Close()
			case "commit-failure":
				s.path = filepath.Join(t.TempDir(), "absent", "state.json")
			}
			e = s.Finish(task, result, nil)
			got, _ := s.Get("personal-demo", task.ID)
			if got.Status == "succeeded" || got.ResultURL != "" {
				t.Fatal("invalid/uncommitted result published")
			}
			if scenario == "invalid-png" || scenario == "wrong-mime" {
				if e != nil || got.Status != "failed" || got.ErrorCode != "invalid_provider_response" {
					t.Fatal(got, e)
				}
			} else if e == nil {
				t.Fatal("storage failure hidden")
			}
			if _, e = s.SaveAsset("personal-demo", task.ID); !errors.Is(e, ErrConflict) {
				t.Fatal("invalid result saved")
			}
		})
	}
}
func TestProviderRecoveryAndUnavailableIdempotency(t *testing.T) {
	dir := t.TempDir()
	s := realStore(t, dir)
	task, _, e := s.Create("personal-demo", "stable-key", realInput())
	if e != nil {
		t.Fatal(e)
	}
	if _, ok, e := s.Next(); !ok || e != nil {
		t.Fatal(e)
	}
	s.Close()
	_, caps := ConfigureProvider("openai-images", "")
	s, e = OpenStore(dir, caps)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	got, _ := s.Get("personal-demo", task.ID)
	if got.Status != "failed" || got.ErrorCode != "execution_interrupted" {
		t.Fatal("unsafe recovery", got)
	}
	replay, created, e := s.Create("personal-demo", "stable-key", realInput())
	if e != nil || created || replay.ID != task.ID || replay.Provider != "openai-images" {
		t.Fatal("rebound/rejected replay", e)
	}
	if _, _, e = s.Create("personal-demo", "new-key", realInput()); !errors.Is(e, ErrProviderNotReady) {
		t.Fatal("unready create")
	}
	if _, _, e = s.Retry("personal-demo", task.ID, "retry-key", realInput()); !errors.Is(e, ErrProviderNotReady) {
		t.Fatal("unready retry")
	}
	for _, path := range []string{"/api/v1/tasks?space_id=personal-demo", "/api/v1/tasks/" + task.ID + "/retry?space_id=personal-demo"} {
		req := httptest.NewRequest("POST", path, strings.NewReader(`{"prompt":"Synthetic persistent PNG fixture","kind":"general","ratio":"1:1","style":"natural"}`))
		req.Header.Set("Idempotency-Key", "http-new-key")
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		Handler(s, "").ServeHTTP(rec, req)
		if rec.Code != 503 || !strings.Contains(rec.Body.String(), "provider_not_ready") {
			t.Fatal("unready HTTP", rec.Code, rec.Body.String())
		}
	}
}
func TestWorkerPinnedProviderNeverCallsChangedConfiguration(t *testing.T) {
	for _, caps := range []Capabilities{mockCapabilities(), {Provider: "openai-images", Mode: "real", Ready: true, Model: "different-model"}} {
		t.Run(caps.Provider+caps.Model, func(t *testing.T) {
			dir := t.TempDir()
			s := realStore(t, dir)
			task, _, e := s.Create("personal-demo", "key", realInput())
			if e != nil {
				t.Fatal(e)
			}
			s.Close()
			s, e = OpenStore(dir, caps)
			if e != nil {
				t.Fatal(e)
			}
			defer s.Close()
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(500) }))
			defer server.Close()
			p, e := NewPipeline(context.Background(), newOpenAIProvider(syntheticAPIKey, server.URL, server.Client()), caps)
			if e != nil {
				t.Fatal(e)
			}
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() { done <- RunWorker(ctx, s, p) }()
			deadline := time.Now().Add(3 * time.Second)
			var got Task
			for time.Now().Before(deadline) {
				got, _ = s.Get("personal-demo", task.ID)
				if got.Status == "failed" {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			cancel()
			if e = <-done; e != nil {
				t.Fatal(e)
			}
			if got.ErrorCode != "provider_unavailable" || calls.Load() != 0 {
				t.Fatal("changed config invoked upstream", got, calls.Load())
			}
		})
	}
}

func TestIncompleteSnapshotRefusedWithoutOverwritingEvidence(t *testing.T) {
	for _, body := range []string{`{}`, `null`, `{"tasks":{},"assets":{}}`, `{"version":1,"assets":{}}`, `{"version":1,"tasks":{}}`, `{"version":1,"tasks":null,"assets":{}}`, `{"version":1,"tasks":{},"assets":null}`} {
		t.Run(body, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "state.json")
			original := []byte(body)
			if e := os.WriteFile(path, original, 0600); e != nil {
				t.Fatal(e)
			}
			s, e := OpenStore(dir, realCapabilities())
			if e == nil {
				s.Close()
				t.Fatal("incomplete existing snapshot was accepted")
			}
			after, e := os.ReadFile(path)
			if e != nil || !bytes.Equal(after, original) {
				t.Fatal("invalid snapshot evidence overwritten", e)
			}
		})
	}
}
