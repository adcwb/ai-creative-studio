package studio

import (
	"fmt"
	"net/http/httptest"
	"testing"
)

func TestReviewImageResumeReceiptLimitPreservesReplay(t *testing.T) {
	s, err := OpenStore(t.TempDir(), jimengTestCaps())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	task, _, err := s.Create("personal-demo", "review-create", jimengInput())
	if err != nil {
		t.Fatal(err)
	}
	task, _, err = s.Next()
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetImageUpstream(task, "known-review-task"); err != nil {
		t.Fatal(err)
	}
	if err = s.Finish(task, GenerationResult{}, providerFailure("provider_timeout")); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ResumeImageTask(task.SpaceID, task.ID, "accepted-resume"); err != nil {
		t.Fatal(err)
	}
	task, _, err = s.Next()
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Finish(task, GenerationResult{}, providerFailure("provider_timeout")); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	d := s.clone()
	for i := len(d.ImageResumeReceipts); i < 1000; i++ {
		d.ImageResumeReceipts[fmt.Sprintf("synthetic-limit-%d", i)] = task.ID
	}
	err = s.commit(d)
	s.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ResumeImageTask(task.SpaceID, task.ID, "new-over-limit"); err != ErrProviderNotReady {
		t.Fatal("unbounded receipt acceptance", err)
	}
	replay, err := s.ResumeImageTask(task.SpaceID, task.ID, "accepted-resume")
	if err != nil || replay.ID != task.ID || replay.Status != "failed" {
		t.Fatal("old receipt did not replay current result", replay, err)
	}
}
func TestReviewAlignmentReadSurfacesPersistenceFailure(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p, _, err := s.CreateProject("personal-demo", "review-project", ProjectInput{Title: "读取失败", Brief: "合成对齐存储故障"})
	if err != nil {
		t.Fatal(err)
	}
	s.media.alignment.mu.Lock()
	s.media.alignment.failure = fmt.Errorf("synthetic disk failure")
	s.media.alignment.mu.Unlock()
	req := httptest.NewRequest("GET", "/api/v1/projects/"+p.ID+"/media/alignments?space_id=personal-demo", nil)
	rec := httptest.NewRecorder()
	Handler(s, "http://localhost:5173").ServeHTTP(rec, req)
	if rec.Code != 500 {
		t.Fatalf("stopped worker presented as healthy: %d %s", rec.Code, rec.Body.String())
	}
}
