package application

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/story"
	"github.com/adcwb/ai-creative-studio/backend/internal/persistence/storyrepo"
)

func TestStoryDispatchedTextCancellationIsUnknownAndNeverResent(t *testing.T) {
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
	snapshot, model := checkFixture(t, s, upstream.URL+"/v1")
	c := createCheck(t, s, snapshot, model, "check-cancel-original")
	r, ok, e := s.story.NextFileCheck(context.Background())
	if e != nil || !ok {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type outcome struct{ state, code string }
	done := make(chan outcome, 1)
	go func() {
		_, state, code := s.executeStoryCheck(ctx, r, func(context.Context) error { return nil })
		done <- outcome{state, code}
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("text POST not dispatched")
	}
	cancel()
	var result outcome
	select {
	case result = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled text POST did not terminate")
	}
	if result.state != "unknown" || result.code != "execution_outcome_unknown" {
		t.Fatal("ambiguous POST was relabeled safe to retry", result)
	}
	if _, e = s.story.TransitionCheck(context.Background(), storyrepo.Scope{Space: c.Space, Series: c.SeriesID}, c.EpisodeID, c.ID, "running", result.state, result.code, nil, nil); e != nil {
		t.Fatal(e)
	}
	if replay := createCheck(t, s, snapshot, model, "check-cancel-original"); replay.State != "unknown" {
		t.Fatal("unknown accepted intent reset to queued")
	}
	if _, ok, e = s.story.NextFileCheck(context.Background()); e != nil || ok || calls.Load() != 1 {
		t.Fatal("unknown POST resubmitted", e, calls.Load())
	}
}
func TestStoryAdoptionCrashHelper(t *testing.T) {
	dir := os.Getenv("STUDIO_ADOPTION_CRASH_DIR")
	if dir == "" {
		return
	}
	s, e := OpenStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	ep, check, finding := os.Getenv("STUDIO_ADOPTION_CRASH_EP"), os.Getenv("STUDIO_ADOPTION_CRASH_CHECK"), os.Getenv("STUDIO_ADOPTION_CRASH_FINDING")
	view, e := s.reviewStoryFinding(context.Background(), checkActor.Space, ep, check, finding)
	if e != nil {
		t.Fatal(e)
	}
	body := humanFact()
	if _, e = s.decideStoryFinding(context.Background(), checkActor, ep, check, finding, "adoption-crash-original", story.DecisionInput{Action: "adopt", ReviewSHA256: view.ReviewSHA256, Proposal: &body}); e != nil {
		t.Fatal(e)
	}
	os.Stdout.WriteString("ADOPTION_DURABLE\n")
	for {
		time.Sleep(time.Second)
	}
}
func TestStorySIGKILLAdoptionRetainsDecisionAndExactlyOneFormalVersion(t *testing.T) {
	var calls, mode atomic.Int32
	mode.Store(6)
	upstream := checkServer(t, &mode, &calls)
	defer upstream.Close()
	u, _ := url.Parse(upstream.URL)
	t.Setenv("MODEL_GATEWAY_ALLOWED_HOSTS", u.Host)
	dir := t.TempDir()
	s, e := OpenStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	snapshot, model := checkFixture(t, s, upstream.URL+"/v1")
	c := completedStoryCheck(t, s, snapshot, model, "adoption-crash-check")
	f := c.Result.Findings[0]
	view, e := s.reviewStoryFinding(context.Background(), c.Space, c.EpisodeID, c.ID, f.ID)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Close(); e != nil {
		t.Fatal(e)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestStoryAdoptionCrashHelper$")
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "STUDIO_ADOPTION_CRASH_DIR=" + dir, "STUDIO_ADOPTION_CRASH_EP=" + c.EpisodeID, "STUDIO_ADOPTION_CRASH_CHECK=" + c.ID, "STUDIO_ADOPTION_CRASH_FINDING=" + f.ID}
	pipe, e := cmd.StdoutPipe()
	if e != nil {
		t.Fatal(e)
	}
	if e = cmd.Start(); e != nil {
		t.Fatal(e)
	}
	ready := make(chan bool, 1)
	go func() {
		scanner := bufio.NewScanner(pipe)
		for scanner.Scan() {
			if scanner.Text() == "ADOPTION_DURABLE" {
				ready <- true
				return
			}
		}
		ready <- false
	}()
	select {
	case ok := <-ready:
		if !ok {
			cmd.Process.Kill()
			cmd.Wait()
			t.Fatal("child did not commit adoption")
		}
	case <-time.After(10 * time.Second):
		cmd.Process.Kill()
		cmd.Wait()
		t.Fatal("child did not reach adoption boundary")
	}
	if e = cmd.Process.Kill(); e != nil {
		t.Fatal(e)
	}
	if cmd.Wait() == nil {
		t.Fatal("child not killed")
	}
	reopened, e := OpenStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer reopened.Close()
	body := humanFact()
	d, e := reopened.decideStoryFinding(context.Background(), checkActor, c.EpisodeID, c.ID, f.ID, "adoption-crash-original", story.DecisionInput{Action: "adopt", ReviewSHA256: view.ReviewSHA256, Proposal: &body})
	if e != nil {
		t.Fatal("formal edit lost its decision", e)
	}
	history, e := reopened.story.History(context.Background(), storyrepo.Scope{Space: c.Space, Series: c.SeriesID}, d.TargetRecordID, 0, 100)
	if e != nil || len(history.Items) != 1 || history.Items[0].VersionID != d.VersionID {
		t.Fatal("crash lost or duplicated accepted version", e)
	}
	if calls.Load() != 1 {
		t.Fatal("adoption recovery called a model")
	}
}
