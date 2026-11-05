package application

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/story"
	"github.com/adcwb/ai-creative-studio/backend/internal/persistence/storyrepo"
	"github.com/adcwb/ai-creative-studio/backend/internal/platform/jobqueue"
)

func TestStoryPendingIndexDoesNotReopenTerminalHistoryAndRepairsQueuedReceipts(t *testing.T) {
	var calls, mode atomic.Int32
	upstream := checkServer(t, &mode, &calls)
	defer upstream.Close()
	u, _ := url.Parse(upstream.URL)
	t.Setenv("MODEL_GATEWAY_ALLOWED_HOSTS", u.Host)
	s := testStore(t)
	snapshot, model := checkFixture(t, s, upstream.URL+"/v1")
	done := completedStoryCheck(t, s, snapshot, model, "check-terminal-history")
	source := filepath.Join(filepath.Dir(s.path), "story", story.TextHash(done.Space), done.SeriesID, ".checks", done.EpisodeID, done.ID+".json")
	if e := os.WriteFile(source, []byte(`{"broken":"terminal history"}`), 0600); e != nil {
		t.Fatal(e)
	}
	queued := createCheck(t, s, snapshot, model, "check-queued-after-history")
	index := filepath.Join(filepath.Dir(s.path), "story-check-queue", queued.ID+".json")
	if e := os.Remove(index); e != nil {
		t.Fatal(e)
	}
	// The original accepted queued request may repair a disposable scheduling
	// pointer. It cannot turn a running/unknown job into another model call.
	replay := createCheck(t, s, snapshot, model, "check-queued-after-history")
	if replay.ID != queued.ID {
		t.Fatal("receipt changed")
	}
	next, ok, e := s.story.NextFileCheck(context.Background())
	if e != nil || !ok || next.Check.ID != queued.ID {
		t.Fatal("scheduler scanned unrelated terminal history", e)
	}
	if calls.Load() != 1 {
		t.Fatal("queued pointer repair dispatched a model")
	}
}
func TestPostgresStoryCheckAndDecisionsOfflineRoundTrip(t *testing.T) {
	var calls, mode atomic.Int32
	mode.Store(6)
	upstream := checkServer(t, &mode, &calls)
	defer upstream.Close()
	u, _ := url.Parse(upstream.URL)
	t.Setenv("MODEL_GATEWAY_ALLOWED_HOSTS", u.Host)
	db := postgresTestDB(t)
	dir := t.TempDir()
	file, e := OpenStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	ctx := context.Background()
	snapshot, model := checkFixture(t, file, upstream.URL+"/v1")
	c := completedStoryCheck(t, file, snapshot, model, "check-offline-completed")
	var accepted story.Decision
	var original story.DecisionInput
	for i, action := range []string{"adopt", "intentional", "reject"} {
		f := c.Result.Findings[i]
		view, e := file.reviewStoryFinding(ctx, c.Space, c.EpisodeID, c.ID, f.ID)
		if e != nil {
			t.Fatal(e)
		}
		input := story.DecisionInput{Action: action, Reason: "离线往返保留创作者原判断", ReviewSHA256: view.ReviewSHA256}
		if action == "adopt" {
			body := humanFact()
			input.Proposal = &body
			original = input
		}
		d, e := file.decideStoryFinding(ctx, checkActor, c.EpisodeID, c.ID, f.ID, "offline-decision-"+action, input)
		if e != nil {
			t.Fatal(e)
		}
		if action == "adopt" {
			accepted = d
		}
	}
	fresh, e := file.CreateStoryContext(ctx, checkActor, c.EpisodeID, "check-offline-fresh", story.Selection{})
	if e != nil {
		t.Fatal(e)
	}
	queued := createCheck(t, file, fresh, model, "check-offline-queued")
	running := createCheck(t, file, fresh, model, "check-offline-running")
	q := storyrepo.Scope{Space: c.Space, Series: c.SeriesID}
	if _, e = file.story.TransitionCheck(ctx, q, c.EpisodeID, running.ID, "queued", "running", "", nil, nil); e != nil {
		t.Fatal(e)
	}
	if e = file.Close(); e != nil {
		t.Fatal(e)
	}
	before, e := os.ReadFile(filepath.Join(dir, "state.json"))
	if e != nil {
		t.Fatal(e)
	}
	report, e := ImportPostgresData(ctx, dir, db)
	if e != nil || report.Records["story_checks"] != 3 || report.Records["story_decisions"] != 3 {
		t.Fatal("check/decision import", e, report.Records)
	}
	after, e := os.ReadFile(filepath.Join(dir, "state.json"))
	if e != nil || string(before) != string(after) {
		t.Fatal("migration rewrote source")
	}
	pg := postgresTestStore(t, db)
	got, e := pg.story.GetDecision(ctx, q, c.EpisodeID, c.ID, c.Result.Findings[0].ID)
	if e != nil || !reflect.DeepEqual(got, accepted) {
		t.Fatal("decision changed in PG", e)
	}
	unknown, e := pg.story.GetCheck(ctx, q, c.EpisodeID, running.ID)
	if e != nil || unknown.Check.State != "unknown" {
		t.Fatal("offline running text POST was made retryable", e)
	}
	queue := jobqueue.Queue{DB: db}
	j, e := queue.Get(ctx, c.Space, queued.ID)
	if e != nil || j.State != "queued" || j.Kind != "story_check" {
		t.Fatal("queued intent not imported atomically", e)
	}
	if _, e = queue.Get(ctx, c.Space, running.ID); !errors.Is(e, sql.ErrNoRows) {
		t.Fatal("unknown text POST enqueued", e)
	}
	restoredDir := filepath.Join(t.TempDir(), "restored")
	report, e = ExportPostgresData(ctx, restoredDir, db)
	if e != nil || report.Records["story_checks"] != 3 || report.Records["story_decisions"] != 3 {
		t.Fatal("check/decision export", e, report.Records)
	}
	restored, e := OpenStore(restoredDir)
	if e != nil {
		t.Fatal(e)
	}
	defer restored.Close()
	replay, e := restored.decideStoryFinding(ctx, checkActor, c.EpisodeID, c.ID, c.Result.Findings[0].ID, "offline-decision-adopt", original)
	if e != nil || !reflect.DeepEqual(replay, accepted) {
		t.Fatal("offline transfer lost original adoption", e)
	}
	if e = restored.story.RecoverFileChecks(ctx); e != nil {
		t.Fatal(e)
	}
	next, ok, e := restored.story.NextFileCheck(ctx)
	if e != nil || !ok || next.Check.ID != queued.ID {
		t.Fatal("restored pending index did not resume the accepted queued job", e)
	}
	if _, ok, e = restored.story.NextFileCheck(ctx); e != nil || ok {
		t.Fatal("unknown task was automatically retried", e)
	}
	if calls.Load() != 1 {
		t.Fatal("offline transfer called a provider")
	}
}
