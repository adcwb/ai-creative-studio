package application

import (
	"context"
	"errors"
	"net/url"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/story"
	"github.com/adcwb/ai-creative-studio/backend/internal/persistence/storyrepo"
	"github.com/adcwb/ai-creative-studio/backend/internal/platform/jobqueue"
)

func completedStoryCheck(t *testing.T, s *FileStore, snapshot story.ContextSnapshot, model ModelRequest, key string) story.Check {
	t.Helper()
	ctx := context.Background()
	c := createCheck(t, s, snapshot, model, key)
	q := storyrepo.Scope{Space: c.Space, Series: c.SeriesID}
	var r storyrepo.CheckRecord
	var lease *jobqueue.Job
	var e error
	dispatch := func(context.Context) error { return nil }
	if s.pg == nil {
		var ok bool
		r, ok, e = s.story.NextFileCheck(ctx)
		if !ok || e != nil {
			t.Fatal(e)
		}
	} else {
		queue := jobqueue.Queue{DB: s.pg.db}
		j, ok, err := queue.Claim(ctx, "story_check")
		if err != nil || !ok {
			t.Fatal(err)
		}
		lease = &j
		if _, e = s.story.TransitionCheck(ctx, q, c.EpisodeID, c.ID, "queued", "running", "", nil, lease); e != nil {
			t.Fatal(e)
		}
		r, e = s.story.GetCheck(ctx, q, c.EpisodeID, c.ID)
		dispatch = func(ctx context.Context) error { return queue.Dispatch(ctx, j) }
	}
	if e != nil {
		t.Fatal(e)
	}
	result, state, code := s.executeStoryCheck(ctx, r, dispatch)
	if state != "succeeded" {
		t.Fatal(state, code)
	}
	out, e := s.story.TransitionCheck(ctx, q, c.EpisodeID, c.ID, "running", state, "", &result, lease)
	if e != nil {
		t.Fatal(e)
	}
	return out
}
func humanFact() story.Input {
	return story.Input{Title: "作者核对后的认知", Kind: "fact", Status: "active", Body: story.Body{Fact: &story.Fact{Subject: "小满", Predicate: "尚不确定", Value: "来客身份", Perspective: "unconfirmed"}}}
}
func decisionContract(t *testing.T, s *FileStore) {
	t.Helper()
	var calls, mode atomic.Int32
	mode.Store(6)
	upstream := checkServer(t, &mode, &calls)
	defer upstream.Close()
	u, _ := url.Parse(upstream.URL)
	t.Setenv("MODEL_GATEWAY_ALLOWED_HOSTS", u.Host)
	snapshot, model := checkFixture(t, s, upstream.URL+"/v1")
	c := completedStoryCheck(t, s, snapshot, model, "decision-check-key")
	ctx := context.Background()
	q := storyrepo.Scope{Space: c.Space, Series: c.SeriesID}
	if len(c.Result.Findings) != 3 {
		t.Fatal("missing findings")
	}
	first := c.Result.Findings[0]
	review, e := s.reviewStoryFinding(ctx, c.Space, c.EpisodeID, c.ID, first.ID)
	if e != nil || review.SourcesChanged || review.CurrentTarget != nil || review.Decision != nil {
		t.Fatal("invalid initial review", e)
	}
	body := humanFact()
	input := story.DecisionInput{Action: "adopt", Reason: "由作者核对，保留待证信息而非客观真相", ReviewSHA256: review.ReviewSHA256, Proposal: &body}
	if _, e = s.story.Get(ctx, q, story.AdoptionTarget(c.ID, first), ""); !errors.Is(e, story.ErrNotFound) {
		t.Fatal("AI automatically created formal fact", e)
	}
	accepted, e := s.decideStoryFinding(ctx, checkActor, c.EpisodeID, c.ID, first.ID, "decision-original-key", input)
	if e != nil || accepted.Action != "adopt" {
		t.Fatal("explicit adoption failed", e)
	}
	version, e := s.story.Get(ctx, q, accepted.TargetRecordID, accepted.VersionID)
	if e != nil || version.Number != 1 || version.Body.Fact.Perspective != "unconfirmed" {
		t.Fatal("human-edited proposal lost", e)
	}
	laterInput := body
	laterInput.ExpectedVersion = 1
	laterInput.Title = "作者后来的正式修订"
	later, e := s.SaveStory(ctx, checkActor, c.SeriesID, version.ID, "decision-later-formal", laterInput)
	if e != nil || later.Number != 2 {
		t.Fatal(e)
	}
	replay, e := s.decideStoryFinding(ctx, checkActor, c.EpisodeID, c.ID, first.ID, "decision-original-key", input)
	if e != nil || !reflect.DeepEqual(replay, accepted) {
		t.Fatal("old request overwrote later formal draft", e)
	}
	head, e := s.story.Get(ctx, q, version.ID, "")
	if e != nil || head.Number != 2 || head.Title != laterInput.Title {
		t.Fatal("replay reverted human edits", e)
	}
	changed := input
	changed.Reason = "changed body on original key"
	if _, e = s.decideStoryFinding(ctx, checkActor, c.EpisodeID, c.ID, first.ID, "decision-original-key", changed); !errors.Is(e, storyrepo.ErrKey) {
		t.Fatal("changed decision body replayed", e)
	}
	viewer := checkActor
	viewer.Writable = false
	if _, e = s.decideStoryFinding(ctx, viewer, c.EpisodeID, c.ID, first.ID, "decision-original-key", input); !errors.Is(e, story.ErrDenied) {
		t.Fatal("revoked writer replayed", e)
	}
	for i, action := range []string{"intentional", "reject"} {
		f := c.Result.Findings[i+1]
		view, e := s.reviewStoryFinding(ctx, c.Space, c.EpisodeID, c.ID, f.ID)
		if e != nil || !view.SourcesChanged {
			t.Fatal("later findings did not compare local source changes", e)
		}
		d, e := s.decideStoryFinding(ctx, checkActor, c.EpisodeID, c.ID, f.ID, "decision-"+action, story.DecisionInput{Action: action, Reason: "这是作者明确保留的艺术判断。", ReviewSHA256: view.ReviewSHA256})
		if e != nil || d.Action != action || d.VersionID != "" {
			t.Fatal("non-adoption changed formal material", e)
		}
	}
	list, e := s.story.List(ctx, q, storyrepo.Query{Limit: 100})
	if e != nil || len(list.Items) != 3 {
		t.Fatal("unexpected formal writes", e, len(list.Items))
	}
	if calls.Load() != 1 {
		t.Fatal("local review required paid recheck", calls.Load())
	}
}
func TestFileStoryFindingExplicitDecisions(t *testing.T) { decisionContract(t, testStore(t)) }
func TestPostgresStoryFindingExplicitDecisions(t *testing.T) {
	decisionContract(t, postgresTestStore(t, postgresTestDB(t)))
}
func TestStoryFindingExistingTargetCASAndAcknowledgedSourceChanges(t *testing.T) {
	var calls, mode atomic.Int32
	mode.Store(5)
	upstream := checkServer(t, &mode, &calls)
	defer upstream.Close()
	u, _ := url.Parse(upstream.URL)
	t.Setenv("MODEL_GATEWAY_ALLOWED_HOSTS", u.Host)
	s := testStore(t)
	snapshot, model := checkFixture(t, s, upstream.URL+"/v1")
	c := completedStoryCheck(t, s, snapshot, model, "decision-existing-key")
	f := c.Result.Findings[0]
	ctx := context.Background()
	view, e := s.reviewStoryFinding(ctx, c.Space, c.EpisodeID, c.ID, f.ID)
	if e != nil || view.CurrentTarget == nil {
		t.Fatal(e)
	}
	proposal := *f.Proposal
	in := story.DecisionInput{Action: "adopt", ReviewSHA256: view.ReviewSHA256, Proposal: &proposal}
	newer := storyBible()
	newer.ExpectedVersion = 1
	newer.Title = "作者已更新的当前圣经"
	if _, e = s.SaveStory(ctx, checkActor, c.SeriesID, f.TargetRecordID, "decision-other-editor", newer); e != nil {
		t.Fatal(e)
	}
	if _, e = s.decideStoryFinding(ctx, checkActor, c.EpisodeID, c.ID, f.ID, "decision-stale-view", in); !errors.Is(e, story.ErrConflict) {
		t.Fatal("stale review basis overwritten", e)
	}
	view, e = s.reviewStoryFinding(ctx, c.Space, c.EpisodeID, c.ID, f.ID)
	if e != nil || !view.SourcesChanged || view.CurrentTarget.Number != 2 {
		t.Fatal(e)
	}
	in.ReviewSHA256 = view.ReviewSHA256
	if _, e = s.decideStoryFinding(ctx, checkActor, c.EpisodeID, c.ID, f.ID, "decision-no-ack", in); !errors.Is(e, story.ErrConflict) {
		t.Fatal("source changes auto acknowledged", e)
	}
	in.AcknowledgeChanged = true
	if _, e = s.decideStoryFinding(ctx, checkActor, c.EpisodeID, c.ID, f.ID, "decision-no-rebase", in); !errors.Is(e, story.ErrConflict) {
		t.Fatal("stale target version overwritten", e)
	}
	proposal.ExpectedVersion = 2
	proposal.Body.Bible.Theme = "作者比较新稿后编辑的主题"
	in.Proposal = &proposal
	d, e := s.decideStoryFinding(ctx, checkActor, c.EpisodeID, c.ID, f.ID, "decision-human-rebased", in)
	if e != nil {
		t.Fatal(e)
	}
	head, e := s.story.Get(ctx, storyrepo.Scope{Space: c.Space, Series: c.SeriesID}, d.TargetRecordID, "")
	if e != nil || head.Number != 3 || head.Body.Bible.Theme != proposal.Body.Bible.Theme {
		t.Fatal("explicit CAS edit not applied", e)
	}
}
func TestStoryFindingConcurrentAdoptionHasOneFormalVersion(t *testing.T) {
	var calls, mode atomic.Int32
	mode.Store(6)
	upstream := checkServer(t, &mode, &calls)
	defer upstream.Close()
	u, _ := url.Parse(upstream.URL)
	t.Setenv("MODEL_GATEWAY_ALLOWED_HOSTS", u.Host)
	s := testStore(t)
	snapshot, model := checkFixture(t, s, upstream.URL+"/v1")
	c := completedStoryCheck(t, s, snapshot, model, "decision-concurrency-check")
	f := c.Result.Findings[0]
	view, e := s.reviewStoryFinding(context.Background(), c.Space, c.EpisodeID, c.ID, f.ID)
	if e != nil {
		t.Fatal(e)
	}
	proposal := humanFact()
	in := story.DecisionInput{Action: "adopt", ReviewSHA256: view.ReviewSHA256, Proposal: &proposal}
	var wg sync.WaitGroup
	results := make([]story.Decision, 2)
	errs := make([]error, 2)
	for i := range 2 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = s.decideStoryFinding(context.Background(), checkActor, c.EpisodeID, c.ID, f.ID, "decision-concurrent-original", in)
		}(i)
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil && !errors.Is(err, story.ErrConflict) {
			t.Fatal(err)
		}
	}
	replay, e := s.decideStoryFinding(context.Background(), checkActor, c.EpisodeID, c.ID, f.ID, "decision-concurrent-original", in)
	if e != nil {
		t.Fatal(e)
	}
	history, e := s.story.History(context.Background(), storyrepo.Scope{Space: c.Space, Series: c.SeriesID}, replay.TargetRecordID, 0, 100)
	if e != nil || len(history.Items) != 1 {
		t.Fatal("concurrent accepted finding wrote two versions", e)
	}
}
