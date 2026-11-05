package application

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"reflect"
	"testing"
	"time"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/series"
	"github.com/adcwb/ai-creative-studio/backend/internal/persistence/storyrepo"
)

func TestStoryCommittedCrashHelper(t *testing.T) {
	dir := os.Getenv("STUDIO_STORY_CRASH_DIR")
	if dir == "" {
		return
	}
	s, e := OpenStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	root := os.Getenv("STUDIO_STORY_CRASH_ROOT")
	saveStory(t, s, root, storyrepo.BibleID(root), "story-committed-crash", storyBible())
	os.Stdout.WriteString("STORY_DURABLE\n")
	for {
		time.Sleep(time.Second)
	}
}
func TestStorySIGKILLAfterCommitReplaysOriginalVersion(t *testing.T) {
	dir := t.TempDir()
	s, e := OpenStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	root, _ := storyFixture(t, s)
	if e = s.Close(); e != nil {
		t.Fatal(e)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestStoryCommittedCrashHelper$")
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "STUDIO_STORY_CRASH_DIR=" + dir, "STUDIO_STORY_CRASH_ROOT=" + root}
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
			if scanner.Text() == "STORY_DURABLE" {
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
			t.Fatal("child did not commit")
		}
	case <-time.After(10 * time.Second):
		cmd.Process.Kill()
		cmd.Wait()
		t.Fatal("child did not reach durable boundary")
	}
	if e = cmd.Process.Kill(); e != nil {
		t.Fatal(e)
	}
	if cmd.Wait() == nil {
		t.Fatal("child not killed")
	}
	s, e = OpenStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	v := saveStory(t, s, root, storyrepo.BibleID(root), "story-committed-crash", storyBible())
	again := saveStory(t, s, root, v.ID, "story-committed-crash", storyBible())
	if !reflect.DeepEqual(v, again) || v.Number != 1 {
		t.Fatal("crash duplicated an authored version")
	}
	history, e := s.story.History(context.Background(), storyrepo.Scope{Space: storyActor.Space, Series: root}, v.ID, 0, 100)
	if e != nil || len(history.Items) != 1 {
		t.Fatal("crash lost or duplicated history", e)
	}
}
func TestStoryEpisodeOptionsPageNarrativeOrderAndRejectStaleCursor(t *testing.T) {
	s := testStore(t)
	root, eps := storyFixture(t, s)
	base := "/series/" + root + "/story-episodes?space_id=" + storyActor.Space + "&limit=1"
	page := projectHTTP(t, s, "GET", base, "", "", 200)
	items := page["items"].([]any)
	if items[0].(map[string]any)["id"] != eps[0].ID {
		t.Fatal("first episode not narrative first")
	}
	cursor := page["next_cursor"].(string)
	page = projectHTTP(t, s, "GET", base+"&cursor="+cursor, "", "", 200)
	if page["items"].([]any)[0].(map[string]any)["id"] != eps[1].ID {
		t.Fatal("page did not follow external episode numbering")
	}
	n, e := s.series.Get(context.Background(), storyActor.Space, "episode", eps[0].ID)
	if e != nil {
		t.Fatal(e)
	}
	parent, e := s.series.Get(context.Background(), storyActor.Space, "season", n.Parent)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.series.Mutate(context.Background(), seriesActor, "story-reorder-options", series.Command{Operation: "move", Kind: "episode", ID: n.ID, Expected: n.Version, ExpectedParent: parent.Version, Ordinal: 99}, nil); e != nil {
		t.Fatal(e)
	}
	projectHTTP(t, s, "GET", base+"&cursor="+cursor, "", "", 409)
	b, _ := json.Marshal(map[string]any{"expected_version": 0, "actor_id": "forged"})
	projectHTTP(t, s, "POST", "/series/"+root+"/story-records?space_id="+storyActor.Space, "story-forged-json", string(b), 400)
}
