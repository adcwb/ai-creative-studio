package studio

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestCreativeOpenAIContractAndValidation(t *testing.T) {
	script, _ := MockScriptProvider{}.Generate(context.Background(), ProjectInput{Title: "测试片", Brief: "合成需求用于本地测试"})
	shots, _ := MockStoryboardProvider{}.Generate(context.Background(), script)
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/chat/completions" || r.Header.Get("Authorization") != "Bearer synthetic" {
			t.Error("wrong request")
		}
		var body map[string]json.RawMessage
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			t.Error("invalid request")
		}
		var model string
		_ = json.Unmarshal(body["model"], &model)
		if model != "test-model" {
			t.Errorf("model=%s", model)
		}
		var value any
		switch requests {
		case 1:
			value = script
		case 2:
			value = map[string]any{"shots": shots}
		case 3:
			value = map[string]any{"summary": "一个清晰的故事概要", "questions": []string{"目标观众是谁？"}, "suggestions": []string{"先确认结局。"}}
		default:
			value = map[string]any{"title": "结构不合法"}
		}
		content, _ := json.Marshal(value)
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]string{"content": string(content)}}}})
	}))
	defer server.Close()
	p := newCreativeProvider("synthetic", server.URL, server.Client())
	p.SetModels("test-model", "test-speech", "test-video")
	if _, err := p.Script(context.Background(), ProjectInput{Title: "测试片", Brief: "合成需求用于本地测试"}); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Storyboard(context.Background(), script); err != nil {
		t.Fatal(err)
	}
	if a, err := p.Analyze(context.Background(), ProjectInput{Title: "测试片", Brief: "合成需求用于本地测试"}); err != nil || a.Provider != "openai-text" {
		t.Fatal(a, err)
	}
	if _, err := p.Script(context.Background(), ProjectInput{Title: "测试片", Brief: "合成需求用于本地测试"}); err == nil {
		t.Fatal("invalid output accepted")
	}
}
func TestCreativeNoRetryRedirectOrSecretLeak(t *testing.T) {
	for _, status := range []int{302, 401, 429, 500} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var calls atomic.Int32
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Location", "http://127.0.0.1:9/secret")
				w.WriteHeader(status)
				_, _ = w.Write([]byte("secret body credential"))
			}))
			defer s.Close()
			p := newCreativeProvider("synthetic", s.URL, s.Client())
			_, err := p.Speech(context.Background(), "合成测试配音文本")
			if err == nil || strings.Contains(err.Error(), "secret") {
				t.Fatal(err)
			}
			if calls.Load() != 1 {
				t.Fatal("generation retried")
			}
		})
	}
}
func TestCreativeVideoAsyncContract(t *testing.T) {
	posts := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "POST /videos":
			posts++
			_ = json.NewEncoder(w).Encode(VideoJob{ID: "video_synthetic", Status: "queued"})
		case "GET /videos/video_synthetic":
			_ = json.NewEncoder(w).Encode(VideoJob{ID: "video_synthetic", Status: "completed", Progress: 100})
		case "GET /videos/video_synthetic/content":
			_, _ = w.Write([]byte("\x00\x00\x00\x18ftypisomsynthetic"))
		default:
			t.Error("unexpected path")
			w.WriteHeader(400)
		}
	}))
	defer s.Close()
	p := newCreativeProvider("synthetic", s.URL, s.Client())
	v, err := p.SubmitVideo(context.Background(), "合成视频提示词")
	if err != nil {
		t.Fatal(err)
	}
	v, err = p.PollVideo(context.Background(), v.ID)
	if err != nil || v.Status != "completed" {
		t.Fatal(v, err)
	}
	if _, err = p.DownloadVideo(context.Background(), v.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = p.PollVideo(context.Background(), "../../metadata"); err == nil {
		t.Fatal("invalid ID accepted")
	}
	if posts != 1 {
		t.Fatal(posts)
	}
}
func TestCreativePersistentAnalysisIdempotencyConflictAndCancel(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	p, _, err := s.CreateProject("personal-demo", "project-001", ProjectInput{Title: "创作项目", Brief: "雨夜里两位朋友重逢"})
	if err != nil {
		t.Fatal(err)
	}
	in := CreativeJobInput{Kind: "analysis", ExpectedVersion: p.Version}
	j, created, err := s.createCreativeJob(p.SpaceID, p.ID, "analysis-001", in)
	if err != nil || !created {
		t.Fatal(j, created, err)
	}
	if replay, created, err := s.createCreativeJob(p.SpaceID, p.ID, "analysis-001", in); err != nil || created || replay.ID != j.ID {
		t.Fatal(replay, created, err)
	}
	altered := in
	altered.Prompt = "变更"
	if _, _, err = s.createCreativeJob(p.SpaceID, p.ID, "analysis-001", altered); err != ErrIdempotencyConflict {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- RunCreativeWorker(ctx, s) }()
	deadline := time.Now().Add(3 * time.Second)
	for {
		jobs, _ := s.listCreativeJobs(p.SpaceID, p.ID)
		j = jobs[0]
		if j.Status == "succeeded" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal(j)
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	if j.Analysis == nil || j.Analysis.Provider != "local-rules" || len(j.Analysis.Questions) != 4 {
		t.Fatal(j)
	}
	next, _, err := s.createCreativeJob(p.SpaceID, p.ID, "analysis-002", in)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.cancelCreativeJob(p.SpaceID, p.ID, next.ID, "cancel-001"); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	replay, created, err := s.createCreativeJob(p.SpaceID, p.ID, "analysis-001", in)
	if err != nil || created || replay.Status != "succeeded" {
		t.Fatal(replay, err)
	}
	jobs, _ := s.listCreativeJobs(p.SpaceID, p.ID)
	if jobs[0].Status != "cancelled" {
		t.Fatal(jobs)
	}
}
func TestCreativeRecoveryNeverRepeatsUncertainPost(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	p, _, _ := s.CreateProject("personal-demo", "project-001", ProjectInput{Title: "创作项目", Brief: "合成测试内容"})
	j, _, err := s.createCreativeJob(p.SpaceID, p.ID, "analysis-001", CreativeJobInput{Kind: "analysis", ExpectedVersion: p.Version})
	if err != nil {
		t.Fatal(err)
	}
	j.Status = "running"
	j.Provider = "openai-text"
	if err = s.creative.updateJob(j); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	jobs, _ := s.listCreativeJobs(p.SpaceID, p.ID)
	if jobs[0].Status != "failed" || jobs[0].ErrorCode != "execution_interrupted" {
		t.Fatal(jobs)
	}
	// Invalid state is never silently replaced with an empty queue.
	if err = os.WriteFile(filepath.Join(dir, "creative.json"), []byte(`{"version":1,"jobs":null}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = openCreativeStore(dir); err == nil {
		t.Fatal("corrupt state accepted")
	}
}
func TestCreativeRealScriptBindingAndRecovery(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	p, _, _ := s.CreateProject("personal-demo", "project-001", ProjectInput{Title: "创作项目", Brief: "合成测试内容"})
	caps := Capabilities{Provider: "openai-text", Mode: "real", Ready: true, Model: "synthetic-model"}
	if err = ConfigureCreative(s, newCreativeProvider("synthetic", "http://127.0.0.1:9", &http.Client{}), caps); err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.CreateScriptGeneration(p.SpaceID, p.ID, "script-001", ScriptGenerationInput{ExpectedVersion: p.Version}); err != ErrProviderChanged {
		t.Fatal(err)
	}
	j, _, err := s.CreateScriptGeneration(p.SpaceID, p.ID, "script-001", ScriptGenerationInput{ExpectedVersion: p.Version, ExpectedProvider: "openai-text", ExpectedModel: "synthetic-model"})
	if err != nil || j.Provider != "openai-text" {
		t.Fatal(j, err)
	}
	if _, ok, err := s.NextScriptGeneration(); err != nil || !ok {
		t.Fatal(ok, err)
	}
	s.Close()
	s, err = OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	jobs, _ := s.ListScriptGenerations(p.SpaceID, p.ID)
	if jobs[0].Status != "failed" || jobs[0].ErrorCode != "execution_interrupted" {
		t.Fatal(jobs)
	}
}

func TestCreativeImageAssetPipeline(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p, _, err := s.CreateProject("personal-demo", "project-001", ProjectInput{Title: "分镜素材", Brief: "使用合成图像检验资产关联"})
	if err != nil {
		t.Fatal(err)
	}
	j, _, err := s.createCreativeJob(p.SpaceID, p.ID, "image-job-001", CreativeJobInput{Kind: "image", ExpectedVersion: p.Version, ExpectedProvider: "local-synthetic", Prompt: "仅合成渐变色卡非AI"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- RunCreativeWorker(ctx, s) }()
	defer func() { cancel(); <-done }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		jobs, _ := s.listCreativeJobs(p.SpaceID, p.ID)
		j = jobs[0]
		if j.Status == "succeeded" || j.Status == "failed" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal(j)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if j.Status != "succeeded" || j.AssetID == "" {
		t.Fatal(j)
	}
	s.media.mu.Lock()
	state := mediaState(s.media.data, mediaKey(p.SpaceID, p.ID))
	s.media.mu.Unlock()
	if len(state.Assets) != 1 || state.Assets[0].Source != "local-synthetic" || state.Assets[0].Kind != "image" {
		t.Fatal(state)
	}
}

func TestCreativeVideoResumeReadsWithoutResubmission(t *testing.T) {
	var posts atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			posts.Add(1)
			t.Error("resume generated a new video")
		}
		if strings.HasSuffix(r.URL.Path, "/content") {
			w.WriteHeader(503)
			return
		}
		_ = json.NewEncoder(w).Encode(VideoJob{ID: "video_existing", Status: "completed"})
	}))
	defer upstream.Close()
	dir := t.TempDir()
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p, _, _ := s.CreateProject("personal-demo", "project-001", ProjectInput{Title: "视频恢复", Brief: "仅合成测试视频任务"})
	provider := newCreativeProvider("synthetic", upstream.URL, upstream.Client())
	SetCreativeVideo(s, provider, Capabilities{Provider: "openai-video", Mode: "real", Ready: true, Model: "synthetic-model"})
	j, _, err := s.createCreativeJob(p.SpaceID, p.ID, "video-job-001", CreativeJobInput{Kind: "video", Prompt: "合成测试", ExpectedVersion: p.Version, ExpectedProvider: "openai-video", ExpectedModel: "synthetic-model"})
	if err != nil {
		t.Fatal(err)
	}
	j.Status = "failed"
	j.UpstreamID = "video_existing"
	if err = s.creative.updateJob(j); err != nil {
		t.Fatal(err)
	}
	j, err = s.resumeCreativeJob(p.SpaceID, p.ID, j.ID, "resume-job-001")
	if err != nil || j.Status != "queued" {
		t.Fatal(j, err)
	}
	if _, err = s.resumeCreativeJob(p.SpaceID, p.ID, j.ID, "resume-job-001"); err != nil {
		t.Fatal("idempotent resume", err)
	}
	if err = s.executeCreativeJob(context.Background(), &j); err == nil {
		t.Fatal("expected content failure")
	}
	if posts.Load() != 0 {
		t.Fatal(posts.Load())
	}
}
func TestCreativeSpeechBinaryIngest(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/audio/speech" {
			t.Error(r.URL.Path)
		}
		w.Header().Set("Content-Type", "audio/wav")
		_, _ = w.Write(syntheticWAV(1))
	}))
	defer upstream.Close()
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p, _, _ := s.CreateProject("personal-demo", "project-001", ProjectInput{Title: "合成语音", Brief: "仅用正弦波验证非真实配音"})
	provider := newCreativeProvider("synthetic", upstream.URL, upstream.Client())
	SetCreativeSpeech(s, provider, Capabilities{Provider: "openai-speech", Mode: "real", Ready: true, Model: "synthetic-model"})
	j, _, err := s.createCreativeJob(p.SpaceID, p.ID, "speech-job-001", CreativeJobInput{Kind: "speech", Prompt: "合成测试非语音", ExpectedVersion: p.Version, ExpectedProvider: "openai-speech", ExpectedModel: "synthetic-model"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.executeCreativeJob(context.Background(), &j); err != nil {
		t.Fatal(err)
	}
	if j.AssetID == "" {
		t.Fatal(j)
	}
}

func TestCreativeModelChangesAndMissingCredentialsFailClosed(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(500) }))
	defer upstream.Close()
	for _, scenario := range []string{"changed_model", "missing_model", "missing_credentials"} {
		t.Run(scenario, func(t *testing.T) {
			s, err := OpenStore(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			p, _, _ := s.CreateProject("personal-demo", "project-001", ProjectInput{Title: "模型绑定", Brief: "仅合成输入检验队列模型绑定"})
			provider := newCreativeProvider("synthetic", upstream.URL, upstream.Client())
			caps := Capabilities{Provider: "openai-text", Mode: "real", Ready: true, Model: "model-a"}
			if err = ConfigureCreative(s, provider, caps); err != nil {
				t.Fatal(err)
			}
			in := CreativeJobInput{Kind: "analysis", ExpectedVersion: p.Version, ExpectedProvider: "openai-text", ExpectedModel: "model-a"}
			j, _, err := s.createCreativeJob(p.SpaceID, p.ID, "analysis-001", in)
			if err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "changed_model":
				s.creative.caps.Model = "model-b"
			case "missing_model":
				j.Model = ""
			case "missing_credentials":
				s.creative.caps.Ready = false
				s.creative.provider = nil
			}
			if err = s.executeCreativeJob(context.Background(), &j); err == nil || j.Analysis != nil {
				t.Fatal("real task incorrectly succeeded or fell back", j, err)
			}
		})
	}
	if calls.Load() != 0 {
		t.Fatal("stale model dispatched upstream", calls.Load())
	}
}
func TestCreativeTextQueuesBindModelsAcrossRestart(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(500) }))
	defer upstream.Close()
	for _, kind := range []string{"script", "storyboard"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			s, err := OpenStore(dir)
			if err != nil {
				t.Fatal(err)
			}
			p, _, _ := s.CreateProject("personal-demo", "project-001", ProjectInput{Title: "持久模型绑定", Brief: "只使用合成输入测试"})
			if kind == "storyboard" {
				_, _, err = s.CreateScriptGeneration(p.SpaceID, p.ID, "mock-script-001", ScriptGenerationInput{ExpectedVersion: p.Version})
				if err != nil {
					t.Fatal(err)
				}
				g, _, _ := s.NextScriptGeneration()
				script, _ := MockScriptProvider{}.Generate(context.Background(), ProjectInput{Title: p.Title, Brief: p.Brief})
				if err = s.FinishScriptGeneration(g, script, nil); err != nil {
					t.Fatal(err)
				}
				p, _ = s.GetProject(p.SpaceID, p.ID)
			}
			provider := newCreativeProvider("synthetic", upstream.URL, upstream.Client())
			provider.SetModels("model-a", "", "")
			caps := Capabilities{Provider: "openai-text", Mode: "real", Ready: true, Model: "model-a"}
			if err = ConfigureCreative(s, provider, caps); err != nil {
				t.Fatal(err)
			}
			in := ScriptGenerationInput{ExpectedVersion: p.Version, ExpectedProvider: "openai-text", ExpectedModel: "wrong-model"}
			if kind == "script" {
				if _, _, err = s.CreateScriptGeneration(p.SpaceID, p.ID, "real-gen-001", in); err != ErrProviderChanged {
					t.Fatal(err)
				}
			} else {
				if _, _, err = s.CreateStoryboardGeneration(p.SpaceID, p.ID, "real-gen-001", in); err != ErrProviderChanged {
					t.Fatal(err)
				}
			}
			in.ExpectedModel = "model-a"
			if kind == "script" {
				_, _, err = s.CreateScriptGeneration(p.SpaceID, p.ID, "real-gen-001", in)
			} else {
				_, _, err = s.CreateStoryboardGeneration(p.SpaceID, p.ID, "real-gen-001", in)
			}
			if err != nil {
				t.Fatal(err)
			}
			s.Close()
			s, err = OpenStore(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			provider.SetModels("model-b", "", "")
			caps.Model = "model-b"
			if err = ConfigureCreative(s, provider, caps); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			if kind == "script" {
				pipeline, e := NewScriptPipeline(ctx, CreativeScriptProvider(provider))
				if e != nil {
					t.Fatal(e)
				}
				go func() { done <- RunScriptWorker(ctx, s, pipeline) }()
			} else {
				pipeline, e := NewStoryboardPipeline(ctx, CreativeStoryboardProvider(provider))
				if e != nil {
					t.Fatal(e)
				}
				go func() { done <- RunStoryboardWorker(ctx, s, pipeline) }()
			}
			deadline := time.Now().Add(3 * time.Second)
			for {
				p, _ = s.GetProject(p.SpaceID, p.ID)
				status := ""
				if kind == "script" {
					status = p.LatestGeneration.Status
				} else {
					status = p.LatestStoryboardGeneration.Status
				}
				if status == "failed" {
					break
				}
				if time.Now().After(deadline) {
					cancel()
					<-done
					t.Fatal("queue did not fail closed")
				}
				time.Sleep(20 * time.Millisecond)
			}
			cancel()
			if err = <-done; err != nil {
				t.Fatal(err)
			}
		})
	}
	if calls.Load() != 0 {
		t.Fatal("old queue used new model", calls.Load())
	}
}
func TestCreativeCancelResumeWaitsForOldWorker(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p, _, _ := s.CreateProject("personal-demo", "project-001", ProjectInput{Title: "取消恢复", Brief: "合成任务用于竞争检查"})
	provider := newCreativeProvider("synthetic", "http://127.0.0.1:9", &http.Client{})
	SetCreativeVideo(s, provider, Capabilities{Provider: "openai-video", Mode: "real", Ready: true, Model: "model-a"})
	j, _, err := s.createCreativeJob(p.SpaceID, p.ID, "video-001", CreativeJobInput{Kind: "video", Prompt: "合成测试", ExpectedVersion: p.Version, ExpectedProvider: "openai-video", ExpectedModel: "model-a"})
	if err != nil {
		t.Fatal(err)
	}
	j.Status = "running"
	j.UpstreamID = "video_existing"
	if err = s.creative.updateJob(j); err != nil {
		t.Fatal(err)
	}
	s.creative.cancels[j.ID] = func() {}
	if _, err = s.cancelCreativeJob(p.SpaceID, p.ID, j.ID, "cancel-001"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.resumeCreativeJob(p.SpaceID, p.ID, j.ID, "resume-001"); err != ErrGenerationActive {
		t.Fatal("resumed while old worker still running", err)
	}
	delete(s.creative.cancels, j.ID)
	if _, err = s.resumeCreativeJob(p.SpaceID, p.ID, j.ID, "resume-001"); err != nil {
		t.Fatal(err)
	}
}
func TestCreativeInvalidImageConfigDoesNotFallBack(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	SetCreativeImage(s, unavailableProvider{}, Capabilities{Provider: "unavailable", Mode: "unavailable", Ready: false, ReasonCode: "invalid_provider"})
	caps := s.creative.mediaCaps()["image"]
	if caps.Ready || caps.Provider == "local-synthetic" {
		t.Fatal(caps)
	}
}
