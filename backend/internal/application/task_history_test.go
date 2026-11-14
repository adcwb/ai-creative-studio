package application

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func exerciseTaskHistory(t *testing.T, s *FileStore) {
	t.Helper()
	in := Input{Prompt: "Synthetic history source", Kind: "general", Ratio: "4:5", Style: "natural"}
	create := func(space, key string) Task {
		task, _, err := s.Create(space, key, in)
		if err != nil {
			t.Fatal(err)
		}
		return task
	}
	done := create("personal-demo", "history-original")
	if err := s.Finish(done, svgResult(`<svg xmlns="http://www.w3.org/2000/svg" width="800" height="1000"></svg>`), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveAsset(done.SpaceID, done.ID); err != nil {
		t.Fatal(err)
	}
	failed := create(done.SpaceID, "history-failed")
	if err := s.Finish(failed, GenerationResult{}, providerFailure("provider_error")); err != nil {
		t.Fatal(err)
	}
	running := create(done.SpaceID, "history-running")
	if _, _, err := s.Next(); err != nil {
		t.Fatal(err)
	}
	queued := create(done.SpaceID, "history-queued")
	foreign := create("team-demo", "history-foreign")
	if err := s.Finish(foreign, GenerationResult{}, providerFailure("provider_error")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DeleteTaskHistory(done.SpaceID, foreign.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("cross-space deletion", err)
	}
	for _, task := range []Task{running, queued} {
		if _, err := s.DeleteTaskHistory(done.SpaceID, task.ID); !errors.Is(err, ErrConflict) {
			t.Fatal("active task deletion", err)
		}
	}
	if n, err := s.DeleteTaskHistory(done.SpaceID, done.ID); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	if n, err := s.DeleteTaskHistory(done.SpaceID, done.ID); err != nil || n != 0 {
		t.Fatal("duplicate deletion", n, err)
	}
	if len(s.List(done.SpaceID)) != 3 {
		t.Fatal("history not removed")
	}
	if result, err := s.ReadResult(done.SpaceID, done.ID); err != nil || len(result.Data) == 0 {
		t.Fatal("saved asset source lost", err)
	}
	if len(s.Assets(done.SpaceID)) != 1 {
		t.Fatal("saved asset removed")
	}
	replay, created, err := s.Create(done.SpaceID, "history-original", in)
	if err != nil || created || replay.ID != done.ID || len(s.List(done.SpaceID)) != 3 {
		t.Fatal("replayed submission restored history or called model", err)
	}
	public, _ := json.Marshal(replay.Public())
	if strings.Contains(string(public), "history_deleted") {
		t.Fatal("internal deletion flag exposed")
	}
	if n, err := s.ClearTaskHistory(done.SpaceID); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	if len(s.List(done.SpaceID)) != 2 || len(s.List(foreign.SpaceID)) != 1 {
		t.Fatal("clear crossed scope or removed active tasks")
	}
	if n, err := s.ClearTaskHistory(done.SpaceID); err != nil || n != 0 {
		t.Fatal(n, err)
	}
}

func TestTaskHistoryPermanentRemovalAndRestart(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "store")
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	exerciseTaskHistory(t, s)
	s.Close()
	s, err = OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if len(s.List("personal-demo")) != 2 || len(s.Assets("personal-demo")) != 1 {
		t.Fatal("restart restored deleted history or lost saved assets")
	}
}

func TestPostgresTaskHistoryRemoval(t *testing.T) {
	exerciseTaskHistory(t, postgresTestStore(t, postgresTestDB(t)))
}

func TestTaskHistoryHTTPAuthorizationAndCSRF(t *testing.T) {
	s := testStore(t)
	auth, err := OpenAuthStore(filepath.Join(t.TempDir(), "auth"))
	if err != nil {
		t.Fatal(err)
	}
	owner, token, session, err := auth.login("history-owner@example.test", "Synthetic-history-password!", true)
	if err != nil {
		t.Fatal(err)
	}
	_, viewerToken, viewerSession, err := auth.login("history-viewer@example.test", "Synthetic-history-password!", true)
	if err != nil {
		t.Fatal(err)
	}
	space := testTeamSpace(t, auth, owner.ID)
	h := SecureHandler(s, "http://localhost:5173", auth, AuthOptions{})
	authRequest(t, h, "PUT", "/api/v1/organizations/"+space+"/members", testMemberInput(t, auth, "history-viewer@example.test", "viewer"), token, session.CSRF, 200)
	task, _, err := s.Create(space, "history-http-source", Input{Prompt: "Synthetic history", Kind: "general"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Finish(task, GenerationResult{}, providerFailure("provider_error")); err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/tasks/" + task.ID + "/history?space_id=" + space
	authRequest(t, h, "DELETE", path, "", "", "", 401)
	authRequest(t, h, "DELETE", path, "", token, "", 403)
	authRequest(t, h, "DELETE", path, "", viewerToken, viewerSession.CSRF, 403)
	authRequest(t, h, "DELETE", path, "", token, session.CSRF, 200)
	authRequest(t, h, "DELETE", path, "", token, session.CSRF, 200)
	authRequest(t, h, "DELETE", "/api/v1/tasks/history?space_id="+space, "", viewerToken, viewerSession.CSRF, 403)
	if len(s.List(space)) != 0 {
		t.Fatal("authorized delete did not remove history")
	}
}
