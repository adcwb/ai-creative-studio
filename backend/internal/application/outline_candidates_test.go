package application

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/story"
	"github.com/adcwb/ai-creative-studio/backend/internal/persistence/storyrepo"
	"github.com/adcwb/ai-creative-studio/backend/internal/platform/jobqueue"
)

func outlineFixture(t *testing.T, s *FileStore, endpoint string) (outlineSnapshot, ModelRequest) {
	t.Helper()
	snapshot, model := checkFixture(t, s, endpoint)
	ctx, err := s.series.Context(context.Background(), checkActor.Space, snapshot.EpisodeID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.runtime.auth.models.saveFeatureBinding(checkActor.Space, checkActor.User, 0, FeatureBinding{FeatureKey: "script_generation", ModelConfigID: model.ModelConfigID, Enabled: true, AllowOverride: true}); err != nil {
		t.Fatal(err)
	}
	model.selection.FeatureKey = "script_generation"
	source, err := s.prepareOutlineSnapshot(context.Background(), checkActor.Space, ctx.Season.ID, snapshot.EpisodeID)
	if err != nil {
		t.Fatal(err)
	}
	return source, model
}
func outlineCall(t *testing.T, s *FileStore, space, method, path, key string, in any, status int) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	if in != nil {
		_ = json.NewEncoder(&body).Encode(in)
	}
	r := httptest.NewRequest(method, "/api/v1"+path, &body)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Idempotency-Key", key)
	r = r.WithContext(context.WithValue(r.Context(), authScopeKey{}, authScope{SpaceID: space, UserID: checkActor.User, Role: "editor"}))
	w := httptest.NewRecorder()
	Handler(s, "").ServeHTTP(w, r)
	if w.Code != status {
		t.Fatal(w.Code, w.Body.String())
	}
	return w
}
func outlineUpstream(t *testing.T, calls *atomic.Int32, wrong *atomic.Bool) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer synthetic-gateway-key" {
			t.Error("wrong text protocol")
		}
		var request struct {
			Model    string
			Messages []struct{ Content string }
		}
		if json.NewDecoder(r.Body).Decode(&request) != nil || len(request.Messages) != 2 {
			t.Error("invalid request")
			return
		}
		var input struct {
			Source outlineSnapshot `json:"source"`
			Brief  string          `json:"creative_brief"`
		}
		if json.Unmarshal([]byte(request.Messages[1].Content), &input) != nil || input.Source.SHA256 == "" || len(input.Source.Material) != 1 || input.Source.Material[0].Kind != "bible" || strings.Contains(request.Messages[1].Content, "FUTURE_PRIVATE_MATERIAL") {
			t.Error("incorrect fixed planning context")
			return
		}
		if input.Brief != "角色先试探再作出选择" || len(input.Source.Episodes) != 2 {
			t.Error("missing brief or directory")
		}
		out := outlineCandidateResult{Title: "门外的选择", Outline: story.Outline{SeasonID: input.Source.SeasonID, EpisodeID: input.Source.EpisodeID, Goal: "确认门外同伴身份", Opening: "听到敲门", Conflict: "钥匙缺齿", Turn: "先询问再开门", Hook: "答案来自过去"}, Rationale: "让选择推动冲突，不把建议当作既成事实。"}
		if wrong.Load() {
			out.Outline.EpisodeID = "ffffffffffffffffffffffffffffffff"
		}
		raw, _ := json.Marshal(out)
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]string{"content": string(raw)}}}, "usage": map[string]int{"prompt_tokens": 30, "completion_tokens": 20}})
	}))
}
func TestOutlineCandidatesRealGatewayFixedSourcesAndExplicitCASAdoption(t *testing.T) {
	for _, storage := range []string{"file", "postgres"} {
		t.Run(storage, func(t *testing.T) {
			var calls atomic.Int32
			var wrong atomic.Bool
			upstream := outlineUpstream(t, &calls, &wrong)
			defer upstream.Close()
			u, _ := url.Parse(upstream.URL)
			t.Setenv("MODEL_GATEWAY_ALLOWED_HOSTS", u.Host)
			s := testStore(t)
			if storage == "postgres" {
				s = postgresTestStore(t, postgresTestDB(t))
			}
			source, model := outlineFixture(t, s, upstream.URL+"/v1")
			in := CreativeJobInput{Kind: "outline", ExpectedVersion: source.ProjectVersion, Prompt: "角色先试探再作出选择", OutlineScopeSHA256: source.SHA256, outlineSnapshot: &source, ModelRequest: model}
			job, created, err := s.createCreativeJob(source.Space, source.AnchorID, "outline-real-original", in)
			if err != nil || !created {
				t.Fatal(job, err)
			}
			// Detached caller data cannot mutate the stored source snapshot.
			job.OutlineContext.Material[0].Title = "tampered caller clone"
			jobs, _ := s.listCreativeJobs(source.Space, source.AnchorID)
			if jobs[0].OutlineContext.Material[0].Title == "tampered caller clone" {
				t.Fatal("snapshot alias")
			}
			job = jobs[0]
			if err = s.executeCreativeJob(context.Background(), &job); err != nil {
				t.Fatal(err)
			}
			if job.OutlineResult == nil || calls.Load() != 1 {
				t.Fatal("missing real candidate", job, calls.Load())
			}
			job.Status = "succeeded"
			if err = s.creative.updateJob(job); err != nil {
				t.Fatal(err)
			}
			id := story.OutlineID(source.SeriesID, source.SeasonID, source.EpisodeID)
			q := storyrepo.Scope{Space: source.Space, Series: source.SeriesID}
			if _, err = s.story.Get(context.Background(), q, id, ""); !errors.Is(err, story.ErrNotFound) {
				t.Fatal("generation silently became formal", err)
			}
			path := "/seasons/" + source.SeasonID + "/outline-candidates/" + job.ID + "/adopt?episode_id=" + source.EpisodeID
			formal := story.Input{Title: "作者修改后的集纲", Kind: "outline", Status: "active", Body: story.Body{Outline: &job.OutlineResult.Outline, Evidence: []story.Evidence{}}}
			formal.Body.Outline.Hook = "作者确认的新悬念"
			body := map[string]any{"anchor_project_id": source.AnchorID, "confirmed": false, "story": formal}
			outlineCall(t, s, source.Space, "POST", path, "outline-adopt-original", body, 400)
			body["confirmed"] = true
			var adopted story.Version
			_ = json.Unmarshal(outlineCall(t, s, source.Space, "POST", path, "outline-adopt-original", body, 200).Body.Bytes(), &adopted)
			if adopted.Number != 1 || adopted.Body.Outline.Hook != "作者确认的新悬念" {
				t.Fatal(adopted)
			}
			stored, _ := s.listCreativeJobs(source.Space, source.AnchorID)
			if stored[0].OutlineResult.Outline.Hook != "答案来自过去" {
				t.Fatal("edited adoption mutated the original model candidate")
			}
			later := formal
			copy := *formal.Body.Outline
			copy.Hook = "作者之后继续创作"
			later.Body.Outline = &copy
			later.ExpectedVersion = 1
			if _, err = s.SaveStory(context.Background(), checkActor, source.SeriesID, id, "outline-later-formal", later); err != nil {
				t.Fatal(err)
			}
			outlineCall(t, s, source.Space, "POST", path, "outline-adopt-original", body, 200)
			current, err := s.story.Get(context.Background(), q, id, "")
			if err != nil || current.Number != 2 || current.Body.Outline.Hook != "作者之后继续创作" {
				t.Fatal("replay overwrote later edits", current, err)
			}
			outlineCall(t, s, source.Space, "POST", path, "outline-adopt-new-key", body, 409)
			outlineCall(t, s, "foreign-tenant", "POST", path, "outline-foreign-key", body, 404)
			// Model removal and source edits do not redispatch an accepted receipt.
			s.runtime.auth.models.mu.Lock()
			tenant := s.runtime.auth.models.data.Tenants[source.Space]
			delete(tenant.Models, model.ModelConfigID)
			s.runtime.auth.models.data.Tenants[source.Space] = tenant
			s.runtime.auth.models.mu.Unlock()
			original := in
			original.outlineSnapshot = nil
			replayed, found, err := s.replayOutline(source.Space, source.SeasonID, source.EpisodeID, source.AnchorID, "outline-real-original", original)
			if err != nil || !found || replayed.ID != job.ID || calls.Load() != 1 {
				t.Fatal(replayed, found, err, calls.Load())
			}
			if storage == "file" {
				restored, err := openCreativeStore(filepath.Dir(s.creative.path))
				if err != nil || restored.data.Jobs[job.ID].OutlineResult == nil {
					t.Fatal("candidate not durable", err)
				}
			}
		})
	}
}
func TestOutlineCandidatesRejectForgedScopeStaleSourcesAndRevokedPermission(t *testing.T) {
	var calls atomic.Int32
	var wrong atomic.Bool
	upstream := outlineUpstream(t, &calls, &wrong)
	defer upstream.Close()
	u, _ := url.Parse(upstream.URL)
	t.Setenv("MODEL_GATEWAY_ALLOWED_HOSTS", u.Host)
	s := testStore(t)
	source, model := outlineFixture(t, s, upstream.URL+"/v1")
	in := CreativeJobInput{Kind: "outline", ExpectedVersion: source.ProjectVersion, Prompt: "角色先试探再作出选择", OutlineScopeSHA256: source.SHA256, ModelRequest: model}
	if _, _, err := s.createCreativeJob(source.Space, source.AnchorID, "outline-forged-input", in); err == nil {
		t.Fatal("public snapshot forgery accepted")
	}
	in.outlineSnapshot = &source
	job, _, err := s.createCreativeJob(source.Space, source.AnchorID, "outline-valid-job", in)
	if err != nil {
		t.Fatal(err)
	}
	wrong.Store(true)
	if err = s.executeCreativeJob(context.Background(), &job); err == nil || job.OutlineResult != nil {
		t.Fatal("foreign episode in model output accepted", err)
	}
	if code, _ := creativeFailure(err); code != "invalid_outline_output" {
		t.Fatal("wrong scope error classification", code)
	}
	wrong.Store(false)
	s.runtime.auth.mu.Lock()
	org := s.runtime.auth.data.Organizations[source.Space]
	org.Members[checkActor.User] = "viewer"
	s.runtime.auth.data.Organizations[source.Space] = org
	s.runtime.auth.mu.Unlock()
	if err = s.executeCreativeJob(context.Background(), &job); err == nil || calls.Load() != 1 {
		t.Fatal("revoked execution dispatched", err, calls.Load())
	}
	s.runtime.auth.mu.Lock()
	org.Members[checkActor.User] = "editor"
	s.runtime.auth.data.Organizations[source.Space] = org
	s.runtime.auth.mu.Unlock()
	bible := storyBible()
	bible.ExpectedVersion = 1
	bible.Body.Bible.Theme = "变化后的正式主题"
	if _, err = s.SaveStory(context.Background(), checkActor, source.SeriesID, storyrepo.BibleID(source.SeriesID), "outline-new-bible", bible); err != nil {
		t.Fatal(err)
	}
	if err = s.executeCreativeJob(context.Background(), &job); err == nil || calls.Load() != 1 {
		t.Fatal("stale source dispatched", err, calls.Load())
	}
}

func TestOutlineCandidatesPostgresIndependentWorker(t *testing.T) {
	var calls atomic.Int32
	var wrong atomic.Bool
	upstream := outlineUpstream(t, &calls, &wrong)
	defer upstream.Close()
	u, _ := url.Parse(upstream.URL)
	t.Setenv("MODEL_GATEWAY_ALLOWED_HOSTS", u.Host)
	db := postgresTestDB(t)
	api := postgresTestStore(t, db)
	source, model := outlineFixture(t, api, upstream.URL+"/v1")
	worker := postgresTestStore(t, db)
	worker.execution = api.execution
	ConfigureManagedRuntime(worker, api.runtime.auth)
	in := CreativeJobInput{Kind: "outline", ExpectedVersion: source.ProjectVersion, Prompt: "角色先试探再作出选择", OutlineScopeSHA256: source.SHA256, outlineSnapshot: &source, ModelRequest: model}
	job, _, err := api.createCreativeJob(source.Space, source.AnchorID, "outline-independent-worker", in)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	q := jobqueue.Queue{DB: db}
	done := make(chan error, 1)
	go func() { done <- RunQueuedCreativeWorker(ctx, q, worker) }()
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil && !errors.Is(err, context.Canceled) {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("outline worker did not stop")
		}
	}()
	deadline := time.After(8 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("outline queue did not finish")
		default:
		}
		queued, err := q.Get(context.Background(), source.Space, job.ID)
		if err != nil {
			t.Fatal(err)
		}
		if queued.State == "succeeded" {
			break
		}
		if queued.State == "failed" || queued.State == "unknown" {
			t.Fatal("outline queue", queued.State)
		}
		time.Sleep(20 * time.Millisecond)
	}
	jobs, err := api.listCreativeJobs(source.Space, source.AnchorID)
	if err != nil || len(jobs) != 1 || jobs[0].Status != "succeeded" || jobs[0].OutlineResult == nil || calls.Load() != 1 {
		t.Fatal(jobs, err, calls.Load())
	}
	if _, _, err = api.createCreativeJob(source.Space, source.AnchorID, "outline-independent-worker", in); err != nil || calls.Load() != 1 {
		t.Fatal("queue receipt resubmitted", err)
	}
}

func TestOutlineCandidatesCancelledTextIsUnknownAndNeverResubmitted(t *testing.T) {
	var calls atomic.Int32
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		entered <- struct{}{}
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer upstream.Close()
	defer close(release)
	u, _ := url.Parse(upstream.URL)
	t.Setenv("MODEL_GATEWAY_ALLOWED_HOSTS", u.Host)
	s := testStore(t)
	source, model := outlineFixture(t, s, upstream.URL+"/v1")
	in := CreativeJobInput{Kind: "outline", ExpectedVersion: source.ProjectVersion, Prompt: "角色先试探再作出选择", OutlineScopeSHA256: source.SHA256, outlineSnapshot: &source, ModelRequest: model}
	job, _, err := s.createCreativeJob(source.Space, source.AnchorID, "outline-cancel-original", in)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.executeCreativeJob(ctx, &job) }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("text not dispatched")
	}
	cancel()
	select {
	case err = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled text did not stop")
	}
	job.ErrorCode, job.Error = creativeFailure(err)
	job.Status = "failed"
	if job.ErrorCode != "execution_outcome_unknown" || job.OutlineResult != nil {
		t.Fatal("ambiguous result not preserved", job.ErrorCode, err)
	}
	if err = s.creative.updateJob(job); err != nil {
		t.Fatal(err)
	}
	replay, created, err := s.createCreativeJob(source.Space, source.AnchorID, "outline-cancel-original", in)
	if err != nil || created || replay.ID != job.ID || replay.ErrorCode != "execution_outcome_unknown" || calls.Load() != 1 {
		t.Fatal("ambiguous text resent", replay, created, err, calls.Load())
	}
	if _, err = s.resumeCreativeJob(source.Space, source.AnchorID, job.ID, "outline-no-resume"); err == nil || calls.Load() != 1 {
		t.Fatal("text resume dispatched another call", err, calls.Load())
	}
	restored, err := openCreativeStore(filepath.Dir(s.creative.path))
	if err != nil || restored.data.Jobs[job.ID].ErrorCode != "execution_outcome_unknown" {
		t.Fatal("unknown result not durable", err)
	}
}

func TestOutlineCandidatesSeasonArcHasItsOwnStableScope(t *testing.T) {
	var calls atomic.Int32
	var wrong atomic.Bool
	upstream := outlineUpstream(t, &calls, &wrong)
	defer upstream.Close()
	u, _ := url.Parse(upstream.URL)
	t.Setenv("MODEL_GATEWAY_ALLOWED_HOSTS", u.Host)
	s := testStore(t)
	episode, model := outlineFixture(t, s, upstream.URL+"/v1")
	source, err := s.prepareOutlineSnapshot(context.Background(), episode.Space, episode.SeasonID, "")
	if err != nil {
		t.Fatal(err)
	}
	if source.EpisodeID != "" || source.AnchorID == "" {
		t.Fatal("season conflated with episode", source)
	}
	in := CreativeJobInput{Kind: "outline", ExpectedVersion: source.ProjectVersion, Prompt: "角色先试探再作出选择", OutlineScopeSHA256: source.SHA256, outlineSnapshot: &source, ModelRequest: model}
	job, _, err := s.createCreativeJob(source.Space, source.AnchorID, "season-outline-original", in)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.executeCreativeJob(context.Background(), &job); err != nil {
		t.Fatal(err)
	}
	job.Status = "succeeded"
	if err = s.creative.updateJob(job); err != nil {
		t.Fatal(err)
	}
	input := story.Input{Title: job.OutlineResult.Title, Kind: "outline", Status: "active", Body: story.Body{Outline: &job.OutlineResult.Outline, Evidence: []story.Evidence{}}}
	var formal story.Version
	_ = json.Unmarshal(outlineCall(t, s, source.Space, "POST", "/seasons/"+source.SeasonID+"/outline-candidates/"+job.ID+"/adopt", "season-outline-adopt", map[string]any{"anchor_project_id": source.AnchorID, "confirmed": true, "story": input}, 200).Body.Bytes(), &formal)
	if formal.ID != story.OutlineID(source.SeriesID, source.SeasonID, "") || formal.Body.Outline.EpisodeID != "" {
		t.Fatal("wrong season record", formal)
	}
	if _, err = s.story.Get(context.Background(), storyrepo.Scope{Space: source.Space, Series: source.SeriesID}, story.OutlineID(source.SeriesID, source.SeasonID, episode.EpisodeID), ""); !errors.Is(err, story.ErrNotFound) {
		t.Fatal("season adoption rewrote episode", err)
	}
}
