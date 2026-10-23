package studio

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func jimengTestCaps() Capabilities {
	return Capabilities{Provider: "jimeng-images", Model: "synthetic", Mode: "real", Ready: true}
}
func jimengTestProvider(endpoint string) *JimengImageProvider {
	return newJimengImageProvider("synthetic-access", "synthetic-secret", "synthetic", endpoint, &http.Client{Timeout: 5 * time.Second})
}

// Spawned by the crash test with only synthetic keys and a loopback upstream.
func TestJimengCrashWorkerHelper(t *testing.T) {
	dir := os.Getenv("STUDIO_JIMENG_TEST_DIR")
	if dir == "" {
		t.Skip("subprocess helper")
	}
	store, err := OpenStore(dir, jimengTestCaps())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	provider := jimengTestProvider(os.Getenv("STUDIO_JIMENG_TEST_URL"))
	if os.Getenv("STUDIO_JIMENG_TEST_KIND") == "creative" {
		SetCreativeImage(store, provider, jimengTestCaps())
		err = RunCreativeWorker(context.Background(), store)
	} else {
		var pipeline *Pipeline
		pipeline, err = NewPipeline(context.Background(), provider, jimengTestCaps())
		if err == nil {
			err = RunWorker(context.Background(), store, pipeline)
		}
	}
	if err != nil {
		t.Fatal(err)
	}
}
func TestJimengKillRestartResumesKnownTaskWithoutSubmit(t *testing.T) {
	for _, kind := range []string{"task", "creative"} {
		for _, ack := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/ack=%t", kind, ack), func(t *testing.T) {
				dir := t.TempDir()
				store, err := OpenStore(dir, jimengTestCaps())
				if err != nil {
					t.Fatal(err)
				}
				var id, projectID string
				if kind == "task" {
					task, _, e := store.Create("personal-demo", "original-key", jimengInput(), Expectation{Provider: "jimeng-images", Model: "synthetic"})
					if e != nil {
						t.Fatal(e)
					}
					id = task.ID
				} else {
					p, _, e := store.CreateProject("personal-demo", "project-key", ProjectInput{Title: "重启协议测试", Brief: "仅合成素材，不调用真实模型"})
					if e != nil {
						t.Fatal(e)
					}
					projectID = p.ID
					SetCreativeImage(store, jimengTestProvider("http://127.0.0.1:1"), jimengTestCaps())
					job, _, e := store.createCreativeJob(p.SpaceID, p.ID, "original-key", CreativeJobInput{Kind: "image", Prompt: "合成创作恢复", ExpectedVersion: p.Version, ExpectedProvider: "jimeng-images", ExpectedModel: "synthetic"})
					if e != nil {
						t.Fatal(e)
					}
					id = job.ID
				}
				store.Close()
				var submits atomic.Int32
				var complete atomic.Bool
				observed := make(chan struct{})
				var once sync.Once
				png64 := base64.StdEncoding.EncodeToString(syntheticPNG(0))
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					_, _ = io.Copy(io.Discard, r.Body)
					if r.URL.Query().Get("Action") == "CVSync2AsyncSubmitTask" {
						submits.Add(1)
						if !ack {
							once.Do(func() { close(observed) })
							<-r.Context().Done()
							return
						}
						fmt.Fprint(w, `{"code":10000,"data":{"task_id":"known-task-id"}}`)
						return
					}
					filename := "state.json"
					if kind == "creative" {
						filename = "creative.json"
					}
					raw, e := os.ReadFile(filepath.Join(dir, filename))
					if e != nil || !bytes.Contains(raw, []byte(`"upstream_id"`)) || !bytes.Contains(raw, []byte(`known-task-id`)) {
						t.Error("query occurred before durable task ID")
					}
					once.Do(func() { close(observed) })
					if !complete.Load() {
						fmt.Fprint(w, `{"code":10000,"data":{"status":"generating"}}`)
						return
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"code": 10000, "data": map[string]any{"status": "done", "binary_data_base64": []string{png64}}})
				}))
				defer upstream.Close()
				cmd := exec.Command(os.Args[0], "-test.run=^TestJimengCrashWorkerHelper$")
				cmd.Env = append(os.Environ(), "STUDIO_JIMENG_TEST_DIR="+dir, "STUDIO_JIMENG_TEST_URL="+upstream.URL, "STUDIO_JIMENG_TEST_KIND="+kind)
				var output bytes.Buffer
				cmd.Stdout = &output
				cmd.Stderr = &output
				if err = cmd.Start(); err != nil {
					t.Fatal(err)
				}
				select {
				case <-observed:
				case <-time.After(8 * time.Second):
					_ = cmd.Process.Kill()
					_ = cmd.Wait()
					t.Fatalf("no upstream observation: %s", output.String())
				}
				if err = cmd.Process.Kill(); err != nil {
					t.Fatal(err)
				}
				_ = cmd.Wait()
				complete.Store(true)
				store, err = OpenStore(dir, jimengTestCaps())
				if err != nil {
					t.Fatal(err)
				}
				defer store.Close()
				provider := jimengTestProvider(upstream.URL)
				SetCreativeImage(store, provider, jimengTestCaps())
				if kind == "task" {
					task, e := store.Get("personal-demo", id)
					if e != nil {
						t.Fatal(e)
					}
					if ack && (task.Status != "queued" || task.UpstreamID != "known-task-id") {
						t.Fatal(task)
					}
					if !ack && (task.Status != "failed" || task.UpstreamID != "") {
						t.Fatal(task)
					}
					replay, created, e := store.Create("personal-demo", "original-key", jimengInput(), Expectation{Provider: "jimeng-images", Model: "synthetic"})
					if e != nil || created || replay.ID != id {
						t.Fatal("submission replay failed", e)
					}
				} else {
					jobs, e := store.listCreativeJobs("personal-demo", projectID)
					if e != nil || len(jobs) != 1 {
						t.Fatal(e)
					}
					if ack && (jobs[0].Status != "queued" || jobs[0].UpstreamID != "known-task-id") {
						t.Fatal(jobs[0])
					}
					if !ack && (jobs[0].Status != "failed" || jobs[0].UpstreamID != "") {
						t.Fatal(jobs[0])
					}
				}
				ctx, cancel := context.WithCancel(context.Background())
				done := make(chan error, 1)
				if kind == "task" {
					pipeline, e := NewPipeline(ctx, provider, jimengTestCaps())
					if e != nil {
						t.Fatal(e)
					}
					go func() { done <- RunWorker(ctx, store, pipeline) }()
				} else {
					go func() { done <- RunCreativeWorker(ctx, store) }()
				}
				if ack {
					deadline := time.Now().Add(6 * time.Second)
					for {
						status := ""
						if kind == "task" {
							task, _ := store.Get("personal-demo", id)
							status = task.Status
						} else {
							jobs, _ := store.listCreativeJobs("personal-demo", projectID)
							status = jobs[0].Status
						}
						if status == "succeeded" {
							break
						}
						if status == "failed" || time.Now().After(deadline) {
							cancel()
							<-done
							t.Fatalf("resume status %s", status)
						}
						time.Sleep(10 * time.Millisecond)
					}
				} else {
					time.Sleep(300 * time.Millisecond)
				}
				cancel()
				if e := <-done; e != nil {
					t.Fatal(e)
				}
				if submits.Load() != 1 {
					t.Fatalf("billable submit repeated: %d", submits.Load())
				}
			})
		}
	}
}

func TestJimengCreativeCancelAndResumeOnlyQueries(t *testing.T) {
	var submits atomic.Int32
	var finish atomic.Bool
	observed := make(chan struct{})
	var once sync.Once
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		if r.URL.Query().Get("Action") == "CVSync2AsyncSubmitTask" {
			submits.Add(1)
			fmt.Fprint(w, `{"code":10000,"data":{"task_id":"cancel-task"}}`)
			return
		}
		once.Do(func() { close(observed) })
		if !finish.Load() {
			<-r.Context().Done()
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 10000, "data": map[string]any{"status": "done", "binary_data_base64": []string{base64.StdEncoding.EncodeToString(syntheticPNG(1))}}})
	}))
	defer upstream.Close()
	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	SetCreativeImage(store, jimengTestProvider(upstream.URL), jimengTestCaps())
	p, _, _ := store.CreateProject("personal-demo", "project-key", ProjectInput{Title: "取消恢复", Brief: "测试取消后只查询"})
	input := CreativeJobInput{Kind: "image", ExpectedVersion: p.Version, ExpectedProvider: "jimeng-images", ExpectedModel: "synthetic", Prompt: "合成画面"}
	job, _, err := store.createCreativeJob(p.SpaceID, p.ID, "create-key", input)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- RunCreativeWorker(ctx, store) }()
	defer func() { cancel(); <-done }()
	select {
	case <-observed:
	case <-time.After(5 * time.Second):
		t.Fatal("no poll")
	}
	cancelled, err := store.cancelCreativeJob(p.SpaceID, p.ID, job.ID, "cancel-key")
	if err != nil || cancelled.UpstreamID != "cancel-task" {
		t.Fatal(cancelled, err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		store.creative.mu.Lock()
		_, active := store.creative.cancels[job.ID]
		store.creative.mu.Unlock()
		if !active {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("worker did not release")
		}
		time.Sleep(time.Millisecond)
	}
	finish.Store(true)
	resumed, err := store.resumeCreativeJob(p.SpaceID, p.ID, job.ID, "resume-key")
	if err != nil || resumed.Status != "queued" {
		t.Fatal(err)
	}
	if _, err = store.resumeCreativeJob(p.SpaceID, p.ID, job.ID, "resume-key"); err != nil {
		t.Fatal(err)
	}
	for {
		jobs, _ := store.listCreativeJobs(p.SpaceID, p.ID)
		if jobs[0].Status == "succeeded" {
			break
		}
		if jobs[0].Status == "failed" || time.Now().After(deadline) {
			t.Fatal(jobs[0])
		}
		time.Sleep(time.Millisecond)
	}
	replay, created, err := store.createCreativeJob(p.SpaceID, p.ID, "create-key", input)
	if err != nil || created || replay.ID != job.ID || replay.AssetID == "" || submits.Load() != 1 {
		t.Fatal(replay, err, submits.Load())
	}
}
func TestJimengImageTaskExplicitResumeGuards(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenStore(dir, jimengTestCaps())
	if err != nil {
		t.Fatal(err)
	}
	task, _, err := s.Create("personal-demo", "resume-task-create", jimengInput())
	if err != nil {
		t.Fatal(err)
	}
	task, _, err = s.Next()
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetImageUpstream(task, "persisted-id"); err != nil {
		t.Fatal(err)
	}
	if err = s.SetImageUpstream(task, "different-id"); err == nil {
		t.Fatal("ACK overwritten")
	}
	if err = s.Finish(task, GenerationResult{}, providerFailure("provider_timeout")); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ResumeImageTask("team-demo", task.ID, "wrong-space-key"); err == nil {
		t.Fatal("cross-space resume")
	}
	s.capabilities.Model = "other-model"
	if _, err = s.ResumeImageTask("personal-demo", task.ID, "changed-model-key"); err == nil {
		t.Fatal("changed model resumed")
	}
	s.capabilities = jimengTestCaps()
	resumed, err := s.ResumeImageTask("personal-demo", task.ID, "resume-task-key")
	if err != nil || resumed.UpstreamID != "persisted-id" || resumed.Status != "queued" {
		t.Fatal(resumed, err)
	}
	s.Close()
	s, err = OpenStore(dir, jimengTestCaps())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	resumed, err = s.ResumeImageTask("personal-demo", task.ID, "resume-task-key")
	if err != nil || resumed.ID != task.ID {
		t.Fatal(resumed, err)
	}
}

func TestJimengAcknowledgementSurvivesCancellationWindow(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	SetCreativeImage(s, jimengTestProvider("http://127.0.0.1:1"), jimengTestCaps())
	p, _, _ := s.CreateProject("personal-demo", "project-window", ProjectInput{Title: "ACK窗口", Brief: "取消与确认窗口测试"})
	job, _, err := s.createCreativeJob(p.SpaceID, p.ID, "window-job", CreativeJobInput{Kind: "image", Prompt: "合成图片", ExpectedVersion: p.Version, ExpectedProvider: "jimeng-images", ExpectedModel: "synthetic"})
	if err != nil {
		t.Fatal(err)
	}
	job.Status = "running"
	if err = s.creative.updateJob(job); err != nil {
		t.Fatal(err)
	}
	if _, err = s.cancelCreativeJob(p.SpaceID, p.ID, job.ID, "window-cancel"); err != nil {
		t.Fatal(err)
	}
	if err = s.creative.recordCreativeUpstream(&job, "ack-after-cancel"); err != context.Canceled {
		t.Fatal(err)
	}
	s.Close()
	s, err = OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	SetCreativeImage(s, jimengTestProvider("http://127.0.0.1:1"), jimengTestCaps())
	jobs, _ := s.listCreativeJobs(p.SpaceID, p.ID)
	if jobs[0].Status != "cancelled" || jobs[0].UpstreamID != "ack-after-cancel" {
		t.Fatal(jobs[0])
	}
	if _, err = s.resumeCreativeJob(p.SpaceID, p.ID, job.ID, "window-resume"); err != nil {
		t.Fatal(err)
	}
}

type ackFailureRepo struct{ *FileStore }

func (ackFailureRepo) SetImageUpstream(Task, string) error {
	return fmt.Errorf("synthetic durable ACK write failure")
}

type ackProbeProvider struct{ submits, polls atomic.Int32 }

func (p *ackProbeProvider) Generate(context.Context, Input) (GenerationResult, error) {
	return GenerationResult{}, fmt.Errorf("durable worker used monolithic generate")
}
func (p *ackProbeProvider) SubmitImage(context.Context, Input) (AsyncImageJob, error) {
	p.submits.Add(1)
	return AsyncImageJob{ID: "ack-failure-task", Status: "queued"}, nil
}
func (p *ackProbeProvider) PollImage(context.Context, string) (AsyncImageJob, error) {
	p.polls.Add(1)
	return AsyncImageJob{}, fmt.Errorf("unexpected query")
}
func (p *ackProbeProvider) DownloadImage(context.Context, string) (GenerationResult, error) {
	return GenerationResult{}, fmt.Errorf("unexpected download")
}
func TestJimengNoPollingWhenDurableAcknowledgementFails(t *testing.T) {
	s, err := OpenStore(t.TempDir(), jimengTestCaps())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_, _, err = s.Create("personal-demo", "ack-failure", jimengInput())
	if err != nil {
		t.Fatal(err)
	}
	provider := &ackProbeProvider{}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	pipeline, err := NewPipeline(ctx, provider, jimengTestCaps())
	if err != nil {
		t.Fatal(err)
	}
	err = RunWorker(ctx, ackFailureRepo{s}, pipeline)
	if err == nil || provider.submits.Load() != 1 || provider.polls.Load() != 0 {
		t.Fatal(err, provider.submits.Load(), provider.polls.Load())
	}
}
