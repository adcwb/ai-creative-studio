package application

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestVisionWorkflowOwnershipModelGuardAndVersionSnapshot(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p := createProject(t, s)
	png := syntheticCreativePNG()
	media, err := s.media.AddAsset(p.ID, p.SpaceID, "vision-ref-upload", "reference.png", "reference", "", "", "uploaded", png, "image/png")
	if err != nil {
		t.Fatal(err)
	}
	in := CreativeJobInput{Kind: "vision", ExpectedVersion: p.Version, Prompt: "Describe this fictional scene", ReferenceAssetID: media.Assets[0].ID}
	if _, _, err = s.createCreativeJob(p.SpaceID, p.ID, "vision-mock-denied", in); !errors.Is(err, ErrProviderNotReady) {
		t.Fatalf("mock must not fabricate vision: %v", err)
	}
	if jobs, _ := s.listCreativeJobs(p.SpaceID, p.ID); len(jobs) != 0 {
		t.Fatal("mock rejection created job", jobs)
	}
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/chat/completions" {
			t.Error("wrong path")
		}
		var body struct {
			Model    string `json:"model"`
			Messages []struct {
				Content []struct {
					Type     string `json:"type"`
					ImageURL struct {
						URL string `json:"url"`
					} `json:"image_url"`
				} `json:"content"`
			} `json:"messages"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil || body.Model != "vision-bound" || len(body.Messages) != 1 || len(body.Messages[0].Content) != 2 || body.Messages[0].Content[1].ImageURL.URL != "data:image/png;base64,"+base64.StdEncoding.EncodeToString(png) {
			t.Error("actual saved image was not sent")
		}
		_ = json.NewEncoder(w).Encode(visionTestResponse("openai-text", visionValidJSON))
	}))
	defer upstream.Close()
	provider := newCreativeProvider("synthetic", upstream.URL, upstream.Client())
	provider.SetModels("vision-bound", "", "")
	caps := Capabilities{Provider: "openai-text", Model: "vision-bound", Mode: "real", Ready: true}
	if err = ConfigureCreative(s, provider, caps); err != nil {
		t.Fatal(err)
	}
	in.ExpectedProvider = caps.Provider
	for i, model := range []string{"", "wrong-model"} {
		in.ExpectedModel = model
		if _, _, err = s.createCreativeJob(p.SpaceID, p.ID, "vision-model-denied-"+strings.Repeat("x", i+1), in); !errors.Is(err, ErrProviderChanged) {
			t.Fatal("missing/wrong expected model accepted", err)
		}
	}
	in.ExpectedModel = caps.Model
	other, _, err := s.CreateProject(p.SpaceID, "vision-other-project", projectInput())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.createCreativeJob(other.SpaceID, other.ID, "vision-cross-project", in); err == nil {
		t.Fatal("reference from other project accepted")
	}
	if _, _, err = s.createCreativeJob("team-demo", p.ID, "vision-cross-space", in); !errors.Is(err, ErrNotFound) {
		t.Fatal("cross space reference accepted", err)
	}
	job, created, err := s.createCreativeJob(p.SpaceID, p.ID, "vision-valid-submit", in)
	if err != nil || !created {
		t.Fatal(job, err)
	}
	if replay, created, err := s.createCreativeJob(p.SpaceID, p.ID, "vision-valid-submit", in); err != nil || created || replay.ID != job.ID {
		t.Fatal("accepted replay failed", replay, err)
	}
	// Creative analysis is a snapshot, not a write to the project's latest brief.
	updated, err := s.UpdateProject(p.SpaceID, p.ID, "vision-brief-update", ProjectUpdate{ExpectedVersion: p.Version, Title: p.Title, Brief: "需求已更新，旧任务仍须标注原始版本。"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- RunCreativeWorker(ctx, s) }()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	deadline := time.Now().Add(3 * time.Second)
	for {
		jobs, e := s.listCreativeJobs(p.SpaceID, p.ID)
		if e != nil {
			t.Fatal(e)
		}
		if len(jobs) == 1 && jobs[0].Status == "succeeded" {
			job = jobs[0]
			break
		}
		if len(jobs) == 1 && jobs[0].Status == "failed" {
			t.Fatal(jobs[0])
		}
		if time.Now().After(deadline) {
			t.Fatal("vision did not finish", jobs)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if job.Analysis == nil || job.Analysis.SourceVersion != p.Version || job.SourceVersion != p.Version || job.Analysis.SourceVersion == updated.Version || job.Analysis.Provider != "openai-text" || job.Model != "vision-bound" || job.AssetID != "" || calls.Load() != 1 {
		t.Fatalf("wrong persisted analysis snapshot/call count: %+v calls=%d", job, calls.Load())
	}
	current, err := s.GetProject(p.SpaceID, p.ID)
	if err != nil || current.Version != updated.Version || current.Brief != updated.Brief {
		t.Fatal("vision overwrote project", current, err)
	}
	replay, created, err := s.createCreativeJob(p.SpaceID, p.ID, "vision-valid-submit", in)
	if err != nil || created || replay.Status != "succeeded" || replay.Analysis == nil || calls.Load() != 1 {
		t.Fatal("finished replay lost result or recharged", replay, err)
	}
}

func TestVisionWorkflowQueuedModelChangeNeverCallsUpstream(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p := createProject(t, s)
	media, err := s.media.AddAsset(p.ID, p.SpaceID, "vision-bound-ref", "reference.png", "reference", "", "", "uploaded", syntheticCreativePNG(), "image/png")
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer upstream.Close()
	provider := newCreativeProvider("synthetic", upstream.URL, upstream.Client())
	provider.SetModels("original-model", "", "")
	caps := Capabilities{Provider: "openai-text", Model: "original-model", Mode: "real", Ready: true}
	if err = ConfigureCreative(s, provider, caps); err != nil {
		t.Fatal(err)
	}
	in := CreativeJobInput{Kind: "vision", ExpectedVersion: p.Version, ExpectedProvider: caps.Provider, ExpectedModel: caps.Model, Prompt: "Describe this fictional scene", ReferenceAssetID: media.Assets[0].ID}
	job, _, err := s.createCreativeJob(p.SpaceID, p.ID, "vision-bound-job", in)
	if err != nil {
		t.Fatal(err)
	}
	caps.Model = "replacement-model"
	if err = ConfigureCreative(s, provider, caps); err != nil {
		t.Fatal(err)
	}
	if err = s.executeCreativeJob(context.Background(), &job); err == nil || calls.Load() != 0 || job.Analysis != nil {
		t.Fatal("changed-model queued vision was executed", err, calls.Load())
	}
}
