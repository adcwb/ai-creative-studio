package application

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/adcwb/ai-creative-studio/backend/internal/modules/prompts"
	"github.com/adcwb/ai-creative-studio/backend/internal/persistence/promptrepo"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
)

func promptCommand() promptrepo.Command {
	return promptrepo.Command{Operation: "create", Metadata: prompts.Metadata{Name: "Soft garden %_", Visibility: "private", Tags: []string{"garden"}}, Recipe: prompts.Recipe{Syntax: "plain", Body: "Soft garden with literal {{braces}} and 花朵", Defaults: prompts.Defaults{Kind: "poster", Ratio: "4:5", Style: "natural"}}}
}
func TestPromptSecureRolesHistoryAndRevocation(t *testing.T) {
	s := testStore(t)
	auth, e := OpenAuthStore(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	owner, token, session, e := auth.login("prompt-owner@example.invalid", "Synthetic-prompt-owner-password!", true)
	if e != nil {
		t.Fatal(e)
	}
	editor, et, es, e := auth.login("prompt-editor@example.invalid", "Synthetic-prompt-editor-password!", true)
	if e != nil {
		t.Fatal(e)
	}
	space := auth.spaces(owner.ID)[0]["id"]
	h := SecureHandler(s, "http://localhost:5173", auth, AuthOptions{AllowRegistration: true})
	base := "/api/v1/prompt-templates"
	q := "?space_id=" + space
	cmd := promptCommand()
	body, _ := json.Marshal(struct {
		prompts.Metadata
		Version prompts.Recipe `json:"version"`
	}{cmd.Metadata, cmd.Recipe})
	authRequest(t, h, "POST", base+q, string(body), token, "", 403)
	w := authRequest(t, h, "POST", base+q, string(body), token, session.CSRF, 201)
	var created prompts.Result
	if json.Unmarshal(w.Body.Bytes(), &created) != nil {
		t.Fatal("create")
	}
	path := base + "/" + created.Template.ID
	authRequest(t, h, "PUT", "/api/v1/organizations/"+space+"/members", `{"email":"prompt-editor@example.invalid","role":"editor"}`, token, session.CSRF, 200)
	for _, suffix := range []string{"", "/versions", "/versions/" + created.Version.ID} {
		authRequest(t, h, "GET", path+suffix+q, "", et, "", 404)
	}
	w = authRequest(t, h, "GET", base+q, "", et, "", 200)
	if bytes.Contains(w.Body.Bytes(), []byte(created.Version.Body)) {
		t.Fatal("list leaked private body")
	}
	meta := created.Template.Metadata
	meta.Visibility = "organization"
	patch, _ := json.Marshal(struct {
		prompts.Metadata
		Expected int64  `json:"expected_row_version"`
		Status   string `json:"status"`
	}{meta, created.Template.RowVersion, "active"})
	w = authRequest(t, h, "PATCH", path+q, string(patch), token, session.CSRF, 200)
	var shared prompts.Result
	json.Unmarshal(w.Body.Bytes(), &shared)
	authRequest(t, h, "GET", path+"/versions/"+created.Version.ID+q, "", et, "", 200)
	metadata := shared.Template.Metadata
	metadata.Visibility = "private"
	bad, _ := json.Marshal(map[string]any{"metadata": metadata, "expected_row_version": shared.Template.RowVersion, "expected_head_version_id": shared.Version.ID, "version": cmd.Recipe})
	authRequest(t, h, "POST", path+"/versions"+q, string(bad), et, es.CSRF, 403)
	newer := cmd.Recipe
	newer.Body = "New garden draft by organization editor"
	versionBody, _ := json.Marshal(map[string]any{"expected_head_version_id": shared.Version.ID, "version": newer})
	w = authRequest(t, h, "POST", path+"/versions"+q, string(versionBody), et, es.CSRF, 201)
	var updated prompts.Result
	json.Unmarshal(w.Body.Bytes(), &updated)
	authRequest(t, h, "POST", path+"/versions"+q, string(versionBody), et, es.CSRF, 201)
	different := newer
	different.Body = "Different same key body"
	diff, _ := json.Marshal(map[string]any{"expected_head_version_id": shared.Version.ID, "version": different})
	authRequest(t, h, "POST", path+"/versions"+q, string(diff), et, es.CSRF, 409)
	authRequest(t, h, "PUT", "/api/v1/organizations/"+space+"/members", `{"email":"prompt-editor@example.invalid","role":"viewer"}`, token, session.CSRF, 200)
	authRequest(t, h, "POST", path+"/versions"+q, string(versionBody), et, es.CSRF, 403)
	authRequest(t, h, "GET", path+q, "", et, "", 200)
	previewBody, _ := json.Marshal(map[string]any{"version_id": created.Version.ID})
	authRequest(t, h, "POST", path+"/preview"+q, string(previewBody), et, es.CSRF, 200)
	authRequest(t, h, "PUT", path+"/favorite"+q, `{"enabled":true}`, et, es.CSRF, 200)
	authRequest(t, h, "PUT", path+"/favorite"+q, `{"enabled":true}`, et, "", 403)

	authRequest(t, h, "PUT", "/api/v1/organizations/"+space+"/members", `{"email":"prompt-editor@example.invalid","role":"remove"}`, token, session.CSRF, 200)
	authRequest(t, h, "POST", path+"/versions"+q, string(versionBody), et, es.CSRF, 403)
	authRequest(t, h, "GET", path+q, "", et, "", 403)
	if len(s.List(space)) != 0 {
		t.Fatal("recipe operations generated tasks")
	}
	_ = editor
}
func TestPromptHTTPPinnedSourceReplayArchiveAndRestart(t *testing.T) {
	dir := t.TempDir()
	s, e := OpenStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	h := Handler(s, "")
	ctx := context.Background()
	actor := prompts.Actor{Space: "personal-demo", User: "demo-user", Writable: true}
	cmd := promptCommand()
	id := "22222222222222222222222222222222"
	first, e := s.prompts.Mutate(ctx, actor, id, "prompt-create", cmd)
	if e != nil {
		t.Fatal(e)
	}
	call := func(path, key string, body any) *httptest.ResponseRecorder {
		b, _ := json.Marshal(body)
		r := httptest.NewRequest("POST", path, bytes.NewReader(b))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Idempotency-Key", key)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	body := map[string]any{"prompt": first.Version.Body, "kind": "poster", "ratio": "4:5", "style": "natural", "prompt_source": map[string]any{"template_id": id, "version_id": first.Version.ID, "edited": false}}
	w := call("/api/v1/tasks?space_id=personal-demo", "image-pinned", body)
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	var task Task
	json.Unmarshal(w.Body.Bytes(), &task)
	if task.PromptSource == nil || task.PromptSource.EffectiveSHA256 != prompts.TextSHA(first.Version.Body) || task.PromptSource.Edited {
		t.Fatal("source missing or incorrect")
	}
	newer := cmd.Recipe
	newer.Body = "New head must not alter the old task"
	current, e := s.prompts.Mutate(ctx, actor, id, "new-version", promptrepo.Command{Operation: "version", Recipe: newer, ExpectedHead: first.Version.ID})
	if e != nil {
		t.Fatal(e)
	}
	if w = call("/api/v1/tasks?space_id=personal-demo", "image-pinned", body); w.Code != 200 {
		t.Fatal("replay after head move", w.Code, w.Body.String())
	}
	wrong := map[string]any{"prompt": "Edited user image prompt", "kind": "poster", "ratio": "4:5", "style": "natural", "prompt_source": map[string]any{"template_id": id, "version_id": first.Version.ID, "edited": false}}
	if w = call("/api/v1/tasks?space_id=personal-demo", "image-unmarked", wrong); w.Code != 400 {
		t.Fatal("unmarked modification", w.Code)
	}
	wrong["prompt_source"].(map[string]any)["edited"] = true
	w = call("/api/v1/tasks?space_id=personal-demo", "image-edited", wrong)
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	var edited Task
	json.Unmarshal(w.Body.Bytes(), &edited)
	if !edited.PromptSource.Edited || edited.PromptSource.CompiledSHA256 != prompts.TextSHA(first.Version.Body) {
		t.Fatal("edited attribution")
	}
	_, e = s.prompts.Mutate(ctx, actor, id, "archive-pinned", promptrepo.Command{Operation: "metadata", Metadata: current.Template.Metadata, ExpectedRow: current.Template.RowVersion, Status: "archived"})
	if e != nil {
		t.Fatal(e)
	}
	if w = call("/api/v1/tasks?space_id=personal-demo", "image-new-archived", body); w.Code != 409 {
		t.Fatal("archive new call", w.Code)
	}
	s.mu.Lock()
	d := s.clone()
	failed := d.Tasks[task.ID]
	failed.Status = "failed"
	d.Tasks[task.ID] = failed
	e = s.commit(d)
	s.mu.Unlock()
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Close(); e != nil {
		t.Fatal(e)
	}
	s, e = OpenStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	h = Handler(s, "")
	if w = call("/api/v1/tasks?space_id=personal-demo", "image-pinned", body); w.Code != 200 {
		t.Fatal("restart accepted-key replay", w.Code, w.Body.String())
	}
	w = call("/api/v1/tasks/"+task.ID+"/retry?space_id=personal-demo", "retry-pinned", body)
	if w.Code != 202 {
		t.Fatal("archived exact retry", w.Code, w.Body.String())
	}
	var retried Task
	json.Unmarshal(w.Body.Bytes(), &retried)
	if retried.PromptSource.VersionID != first.Version.ID || retried.Prompt != first.Version.Body {
		t.Fatal("retry followed latest head")
	}
}
func TestPromptPostgresTwoAPIsAndFileRoundTrip(t *testing.T) {
	db := postgresTestDB(t)
	ctx := context.Background()
	a, b := postgresTestStore(t, db), postgresTestStore(t, db)
	actor := prompts.Actor{Space: "tenant-prompt", User: "owner", Writable: true}
	id := "33333333333333333333333333333333"
	cmd := promptCommand()
	cmd.Metadata.Visibility = "organization"
	first, e := a.prompts.Mutate(ctx, actor, id, "create-original", cmd)
	if e != nil {
		t.Fatal(e)
	}
	ch := make(chan error, 2)
	var wg sync.WaitGroup
	for i, s := range []*FileStore{a, b} {
		wg.Add(1)
		go func(i int, s *FileStore) {
			defer wg.Done()
			c := promptrepo.Command{Operation: "version", Recipe: cmd.Recipe, ExpectedHead: first.Version.ID}
			c.Recipe.Body = fmt.Sprintf("New database prompt version%d", i)
			_, e := s.prompts.Mutate(ctx, actor, id, fmt.Sprintf("concurrent-key-%d", i), c)
			ch <- e
		}(i, s)
	}
	wg.Wait()
	close(ch)
	win, conflict := 0, 0
	for e := range ch {
		if e == nil {
			win++
		} else if errors.Is(e, prompts.ErrConflict) {
			conflict++
		} else {
			t.Fatal(e)
		}
	}
	if win != 1 || conflict != 1 {
		t.Fatal(win, conflict)
	}
	viewer := prompts.Actor{Space: actor.Space, User: "viewer"}
	if e = a.prompts.Favorite(ctx, viewer, id, true); e != nil {
		t.Fatal(e)
	}
	q := promptrepo.Query{Source: "organization", Status: "active", Text: "%_", Favorite: true, Limit: 20}
	items, e := a.prompts.List(ctx, viewer, q)
	if e != nil || len(items) != 1 {
		t.Fatal("SQL literal/role query", len(items), e)
	}
	dir := filepath.Join(t.TempDir(), "prompts")
	tx, e := db.BeginTx(ctx, nil)
	if e != nil {
		t.Fatal(e)
	}
	n, e := promptrepo.ExportFiles(ctx, tx, dir)
	if e != nil {
		tx.Rollback()
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil || n != 2 {
		t.Fatal("export", n, e)
	}
	file, e := promptrepo.Open(dir, nil)
	if e != nil {
		t.Fatal(e)
	}
	if v, e := file.Version(ctx, actor, id, first.Version.ID); e != nil || v.Body != first.Version.Body {
		t.Fatal("export history", e)
	}
	items, e = file.List(ctx, viewer, q)
	if e != nil || len(items) != 1 || !items[0].Favorite {
		t.Fatal("export favorite", e)
	}
	target := postgresTestDB(t)
	tx, e = target.BeginTx(ctx, nil)
	if e != nil {
		t.Fatal(e)
	}
	n, e = promptrepo.ImportFiles(ctx, tx, dir)
	if e != nil {
		tx.Rollback()
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil || n != 2 {
		t.Fatal("import", n, e)
	}
	repo, e := promptrepo.Open("", target)
	if e != nil {
		t.Fatal(e)
	}
	replay, e := repo.Mutate(ctx, actor, id, "create-original", cmd)
	if e != nil || replay.Version.ID != first.Version.ID {
		t.Fatal("roundtrip receipt", e)
	}
}

func TestPromptAdminArchiveDoesNotGrantPrivateRead(t *testing.T) {
	for _, mode := range []string{"file", "postgres"} {
		t.Run(mode, func(t *testing.T) {
			var s *FileStore
			if mode == "file" {
				s = testStore(t)
			} else {
				s = postgresTestStore(t, postgresTestDB(t))
			}
			ctx := context.Background()
			owner := prompts.Actor{Space: "private-space", User: "private-owner", Writable: true}
			admin := prompts.Actor{Space: owner.Space, User: "administrator", Writable: true, Manage: true}
			id := "55555555555555555555555555555555"
			created, e := s.prompts.Mutate(ctx, owner, id, "private-create", promptCommand())
			if e != nil {
				t.Fatal(e)
			}
			if _, e = s.prompts.Get(ctx, admin, id); !errors.Is(e, prompts.ErrNotFound) {
				t.Fatal("admin private read", e)
			}
			if _, e = s.prompts.Version(ctx, admin, id, created.Version.ID); !errors.Is(e, prompts.ErrNotFound) {
				t.Fatal("admin private history", e)
			}
			h := Handler(s, "")
			r := httptest.NewRequest("POST", "/api/v1/prompt-templates/"+id+"/admin-archive?space_id="+owner.Space, bytes.NewBufferString(`{"expected_row_version":1}`))
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Idempotency-Key", "administrative-archive")
			r = r.WithContext(context.WithValue(r.Context(), authScopeKey{}, authScope{SpaceID: admin.Space, UserID: admin.User, Role: "admin"}))
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != 200 {
				t.Fatal(w.Code, w.Body.String())
			}
			if bytes.Contains(w.Body.Bytes(), []byte(created.Version.Body)) || bytes.Contains(w.Body.Bytes(), []byte("compiled_sha256")) {
				t.Fatal("administrative response leaked recipe", w.Body.String())
			}
			current, e := s.prompts.Get(ctx, owner, id)
			if e != nil || current.Template.Status != "archived" {
				t.Fatal("archive", e)
			}
			if _, e = s.prompts.Get(ctx, admin, id); !errors.Is(e, prompts.ErrNotFound) {
				t.Fatal("archive granted body access", e)
			}
			if _, e = s.prompts.Mutate(ctx, prompts.Actor{Space: "other", User: admin.User, Writable: true, Manage: true}, id, "administrative-archive", promptrepo.Command{Operation: "admin_archive", ExpectedRow: 1}); !errors.Is(e, prompts.ErrNotFound) {
				t.Fatal("cross tenant admin archive", e)
			}
		})
	}
}
