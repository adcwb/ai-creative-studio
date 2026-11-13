package application

import (
	"context"
	"errors"
	"github.com/adcwb/ai-creative-studio/backend/internal/modules/prompts"
	w "github.com/adcwb/ai-creative-studio/backend/internal/modules/workflows"
	"github.com/adcwb/ai-creative-studio/backend/internal/persistence/workflowrepo"
	"net/http"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
)

func workflowFixture(t *testing.T, s *FileStore) (Project, MediaState, *AuthStore, string) {
	t.Helper()
	var a *AuthStore
	var err error
	if s.pg != nil {
		a, err = OpenPostgresAuthStore(filepath.Dir(s.path), s.pg.db)
	} else {
		a, err = OpenAuthStore(filepath.Dir(s.path))
	}
	if err != nil {
		t.Fatal(err)
	}
	u, _, _, err := a.login("workflow-owner@example.test", "synthetic-password", true)
	if err != nil {
		t.Fatal(err)
	}
	space := testTeamSpace(t, a, u.ID)
	editor, _, _, err := a.login("workflow-editor@example.test", "synthetic-password", true)
	if err != nil {
		t.Fatal(err)
	}
	a.mu.Lock()
	data := a.clone()
	team := data.Organizations[space]
	team.Members[editor.ID] = "editor"
	team.Version++
	data.Organizations[space] = team
	err = a.commit(data)
	a.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	p, _, err := s.CreateProject(space, "workflow-project", ProjectInput{Title: "Creative flow", Brief: "Actual owned media through a human review step"})
	if err != nil {
		t.Fatal(err)
	}
	m, err := s.media.AddAsset(p.ID, space, "workflow-source", "Owned frame", "reference", "", "", "uploaded", syntheticPNG(0), "image/png")
	if err != nil {
		t.Fatal(err)
	}
	s.runtime = NewModelRuntime(a, nil)
	return p, m, a, editor.ID
}
func exerciseWorkflow(t *testing.T, s *FileStore) {
	t.Helper()
	ctx := context.Background()
	p, m, a, actor := workflowFixture(t, s)
	graph := w.Graph{Nodes: []w.Node{{ID: "source", Kind: "asset", AssetKind: "image", AssetID: m.Assets[0].ID}, {ID: "review", Kind: "review"}, {ID: "output", Kind: "output"}}, Edges: []w.Edge{{Source: "source", Target: "review", Input: "media"}, {Source: "review", Target: "output", Input: "media"}}}
	input := workflowSave{Name: "A reviewed result", Graph: graph}
	saved, err := s.saveWorkflow(ctx, p.SpaceID, p.ID, "", "workflow-create", actor, input)
	if err != nil {
		t.Fatal(err)
	}
	original := saved.Revisions[0]
	input.ExpectedVersion = saved.Version
	input.Name = "Renamed direction"
	input.Graph.Nodes = append([]w.Node(nil), graph.Nodes...)
	input.Graph.Nodes[0].X = 55
	current, err := s.saveWorkflow(ctx, p.SpaceID, p.ID, saved.ID, "workflow-next", actor, input)
	if err != nil {
		t.Fatal(err)
	}
	if current.Version != 2 || !reflect.DeepEqual(current.Revisions[0], original) {
		t.Fatal("immutable revisions")
	}
	replay, err := s.saveWorkflow(ctx, p.SpaceID, p.ID, "", "workflow-create", actor, workflowSave{Name: "A reviewed result", Graph: graph})
	if err != nil || replay.Version != 2 {
		t.Fatal("replay rewound graph", err)
	}
	if _, err = s.saveWorkflow(ctx, p.SpaceID, p.ID, saved.ID, "workflow-stale", actor, input); !errors.Is(err, w.ErrConflict) {
		t.Fatal("CAS", err)
	}
	run, err := s.startWorkflow(ctx, p.SpaceID, p.ID, saved.ID, "workflow-run", actor, p.Version, original.ID, map[string]workflowPin{})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.advanceWorkflow(ctx, p.SpaceID, p.ID); err != nil {
		t.Fatal(err)
	}
	d, err := s.workflows.Read(ctx, p.SpaceID, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	paused := d.Runs[run.ID]
	if paused.Status != "waiting_review" || paused.Nodes["output"].Status != "queued" || paused.Nodes["review"].Result.AssetID != m.Assets[0].ID {
		t.Fatal("review did not gate output", paused)
	}
	if scopes, err := s.workflows.Pending(ctx); err != nil || len(scopes) != 0 {
		t.Fatal("idle review busy loop", scopes, err)
	}
	if _, err = s.workflowDecision(ctx, p.SpaceID, p.ID, run.ID, "review", "decision-stale", run.Version, true); !errors.Is(err, w.ErrConflict) {
		t.Fatal("review CAS", err)
	}
	approved, err := s.workflowDecision(ctx, p.SpaceID, p.ID, run.ID, "review", "decision-ok", paused.Version, true)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.advanceWorkflow(ctx, p.SpaceID, p.ID); err != nil {
		t.Fatal(err)
	}
	d, err = s.workflows.Read(ctx, p.SpaceID, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	done := d.Runs[run.ID]
	if done.Status != "succeeded" || done.Nodes["output"].Result.AssetID != m.Assets[0].ID {
		t.Fatal("output", done)
	}
	replayRun, err := s.workflowDecision(ctx, p.SpaceID, p.ID, run.ID, "review", "decision-ok", paused.Version, true)
	if err != nil || replayRun.Version != done.Version {
		t.Fatal("decision replay", approved, err)
	}
	if _, err = s.workflowDecision(ctx, "other", p.ID, run.ID, "review", "foreign", done.Version, true); err == nil {
		t.Fatal("tenant leak")
	}
	second, err := s.startWorkflow(ctx, p.SpaceID, p.ID, saved.ID, "workflow-second", actor, p.Version, original.ID, map[string]workflowPin{})
	if err != nil {
		t.Fatal(err)
	}
	if s.pg != nil {
		fresh, openErr := OpenPostgresAuthStore(t.TempDir(), s.pg.db)
		if openErr != nil {
			t.Fatal(openErr)
		}
		s.runtime.auth = fresh
	}
	a.mu.Lock()
	data := a.clone()
	org := data.Organizations[p.SpaceID]
	delete(org.Members, actor)
	data.Organizations[p.SpaceID] = org
	org.Version++
	data.Organizations[p.SpaceID] = org
	err = a.commit(data)
	a.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if err = s.advanceWorkflow(ctx, p.SpaceID, p.ID); err != nil {
		t.Fatal(err)
	}
	d, _ = s.workflows.Read(ctx, p.SpaceID, p.ID)
	if d.Runs[second.ID].Status != "failed" {
		t.Fatal("revoked actor advanced")
	}
	before := s.media.Get(p.ID, p.SpaceID)
	if !reflect.DeepEqual(before, m) {
		t.Fatal("workflow changed source media")
	}

}
func TestFileWorkflowReviewOutput(t *testing.T) { exerciseWorkflow(t, testStore(t)) }
func TestPostgresWorkflowReviewOutput(t *testing.T) {
	exerciseWorkflow(t, postgresTestStore(t, postgresTestDB(t)))
}

func TestWorkflowHTTPCurrentRolesAndScope(t *testing.T) {
	s := testStore(t)
	p, m, a, actor := workflowFixture(t, s)
	owner, ot, osession, err := a.login("workflow-owner@example.test", "synthetic-password", false)
	if err != nil {
		t.Fatal(err)
	}
	_, et, esession, err := a.login("workflow-editor@example.test", "synthetic-password", false)
	if err != nil {
		t.Fatal(err)
	}
	viewer, vt, vs, err := a.login("workflow-viewer@example.test", "synthetic-password", true)
	if err != nil {
		t.Fatal(err)
	}
	_, ft, _, err := a.login("workflow-foreign@example.test", "synthetic-password", true)
	if err != nil {
		t.Fatal(err)
	}
	a.mu.Lock()
	data := a.clone()
	org := data.Organizations[p.SpaceID]
	org.Members[viewer.ID] = "viewer"
	org.Version++
	data.Organizations[p.SpaceID] = org
	err = a.commit(data)
	a.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	graph := w.Graph{Nodes: []w.Node{{ID: "source", Kind: "asset", AssetKind: "image", AssetID: m.Assets[0].ID}, {ID: "output", Kind: "output"}}, Edges: []w.Edge{{Source: "source", Target: "output", Input: "media"}}}
	saved, err := s.saveWorkflow(context.Background(), p.SpaceID, p.ID, "", "roles-save", actor, workflowSave{Name: "Current access", Graph: graph})
	if err != nil {
		t.Fatal(err)
	}
	h := SecureHandler(s, "http://localhost:5173", a, AuthOptions{})
	path := "/api/v1/projects/" + p.ID + "/workflows/" + saved.ID + "?space_id=" + p.SpaceID
	for _, token := range []string{ot, et, vt} {
		authRequest(t, h, http.MethodGet, path, "", token, "", 200)
	}
	authRequest(t, h, http.MethodGet, path, "", ft, "", 403)
	body := projectJSON(t, workflowSave{Name: "Changed", ExpectedVersion: saved.Version, Graph: graph})
	authRequest(t, h, http.MethodPut, path, body, vt, vs.CSRF, 403)
	authRequest(t, h, http.MethodPut, path, body, et, "", 403)
	authRequest(t, h, http.MethodPut, path, body, et, esession.CSRF, 200)
	// Owner and editor have the same workflow write permission; a fresh read
	// always sees the immutable revision created by the editor.
	if owner.ID == actor {
		t.Fatal("fixture must exercise distinct actors")
	}
	authRequest(t, h, http.MethodGet, path, "", ot, osession.CSRF, 200)
}

func TestPostgresWorkflowConcurrentControllersAndReview(t *testing.T) {
	db := postgresTestDB(t)
	s := postgresTestStore(t, db)
	p, m, _, actor := workflowFixture(t, s)
	other := postgresTestStore(t, db)
	otherAuth, err := OpenPostgresAuthStore(t.TempDir(), db)
	if err != nil {
		t.Fatal(err)
	}
	other.runtime = NewModelRuntime(otherAuth, nil)
	graph := w.Graph{Nodes: []w.Node{{ID: "source", Kind: "asset", AssetKind: "image", AssetID: m.Assets[0].ID}, {ID: "review", Kind: "review"}, {ID: "output", Kind: "output"}}, Edges: []w.Edge{{Source: "source", Target: "review", Input: "media"}, {Source: "review", Target: "output", Input: "media"}}}
	ctx := context.Background()
	saved, err := s.saveWorkflow(ctx, p.SpaceID, p.ID, "", "controllers-save", actor, workflowSave{Name: "Concurrent workers", Graph: graph})
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.startWorkflow(ctx, p.SpaceID, p.ID, saved.ID, "controllers-run", actor, p.Version, saved.Head, map[string]workflowPin{})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errorsOut := make(chan error, 2)
	for _, store := range []*FileStore{s, other} {
		wg.Add(1)
		go func(store *FileStore) { defer wg.Done(); errorsOut <- store.advanceWorkflow(ctx, p.SpaceID, p.ID) }(store)
	}
	wg.Wait()
	for i := 0; i < 2; i++ {
		err = <-errorsOut
		if err != nil && !errors.Is(err, errWorkflowIdle) {
			t.Fatal(err)
		}
	}
	d, err := s.workflows.Read(ctx, p.SpaceID, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	paused := d.Runs[run.ID]
	if paused.Status != "waiting_review" || paused.Version != 2 {
		t.Fatal("controllers advanced twice", paused)
	}
	for i, store := range []*FileStore{s, other} {
		wg.Add(1)
		go func(i int, store *FileStore) {
			defer wg.Done()
			key := "decision-one"
			if i == 1 {
				key = "decision-two"
			}
			_, e := store.workflowDecision(ctx, p.SpaceID, p.ID, run.ID, "review", key, paused.Version, true, actor)
			errorsOut <- e
		}(i, store)
	}
	wg.Wait()
	success, conflict := 0, 0
	for i := 0; i < 2; i++ {
		err = <-errorsOut
		if err == nil {
			success++
		} else if errors.Is(err, w.ErrConflict) {
			conflict++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatal("review did not CAS across instances", success, conflict)
	}
}

func TestPostgresWorkflowOfflineRoundTrip(t *testing.T) {
	ctx := context.Background()
	source := t.TempDir()
	file, err := OpenStore(source)
	if err != nil {
		t.Fatal(err)
	}
	p, m, _, actor := workflowFixture(t, file)
	graph := w.Graph{Nodes: []w.Node{{ID: "source", Kind: "asset", AssetKind: "image", AssetID: m.Assets[0].ID}, {ID: "review", Kind: "review"}, {ID: "output", Kind: "output"}}, Edges: []w.Edge{{Source: "source", Target: "review", Input: "media"}, {Source: "review", Target: "output", Input: "media"}}}
	saved, err := file.saveWorkflow(ctx, p.SpaceID, p.ID, "", "workflow-migrate-save", actor, workflowSave{Name: "Preserved flow", Graph: graph})
	if err != nil {
		t.Fatal(err)
	}
	run, err := file.startWorkflow(ctx, p.SpaceID, p.ID, saved.ID, "workflow-migrate-run", actor, p.Version, saved.Head, map[string]workflowPin{})
	if err != nil {
		t.Fatal(err)
	}
	if err = file.advanceWorkflow(ctx, p.SpaceID, p.ID); err != nil {
		t.Fatal(err)
	}
	before, err := file.workflows.Read(ctx, p.SpaceID, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = file.Close(); err != nil {
		t.Fatal(err)
	}
	db := postgresTestDB(t)
	report, err := ImportPostgresData(ctx, source, db)
	if err != nil || report.Records["workflows"] != 1 {
		t.Fatal(report, err)
	}
	pg := postgresTestStore(t, db)
	after, err := pg.workflows.Read(ctx, p.SpaceID, p.ID)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("import lost graph/run/private pins/receipts", err)
	}
	target := filepath.Join(t.TempDir(), "restored")
	report, err = ExportPostgresData(ctx, target, db)
	if err != nil || report.Records["workflows"] != 1 {
		t.Fatal(report, err)
	}
	restored, err := OpenStore(target)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	d, err := restored.workflows.Read(ctx, p.SpaceID, p.ID)
	if err != nil || !reflect.DeepEqual(before, d) || d.Runs[run.ID].Status != "waiting_review" {
		t.Fatal("export lost suspended human review", err)
	}
	replay, err := restored.startWorkflow(ctx, p.SpaceID, p.ID, saved.ID, "workflow-migrate-run", actor, p.Version, saved.Head, map[string]workflowPin{})
	if err != nil || replay.ID != run.ID || replay.Status != "waiting_review" {
		t.Fatal("original request not restored", err)
	}
}

func TestWorkflowReconcilesCommittedChildAfterConfigDisappears(t *testing.T) {
	s := testStore(t)
	p, m, _, actor := workflowFixture(t, s)
	ctx := context.Background()
	graph := w.Graph{Nodes: []w.Node{{ID: "text", Kind: "text", Text: "Synthetic original prompt"}, {ID: "image", Kind: "image", ModelID: "pinned-model", ModelVersion: 1}, {ID: "review", Kind: "review"}, {ID: "output", Kind: "output"}}, Edges: []w.Edge{{Source: "text", Target: "image", Input: "prompt"}, {Source: "image", Target: "review", Input: "media"}, {Source: "review", Target: "output", Input: "media"}}}
	saved, err := s.saveWorkflow(ctx, p.SpaceID, p.ID, "", "child-recovery-save", actor, workflowSave{Name: "Crash boundary", Graph: graph})
	if err != nil {
		t.Fatal(err)
	}
	selection := RuntimeSelection{TenantID: p.SpaceID, ActorID: actor, ModelID: "pinned-model", Version: 1, GatewayID: "gateway", GatewayVersion: 1, Capability: "image", FeatureKey: "image_generation"}
	pin := workflowPin{Selection: selection, Provider: "gateway-images", Model: "synthetic-image"}
	run, err := s.startWorkflow(ctx, p.SpaceID, p.ID, saved.ID, "child-recovery-run", actor, p.Version, saved.Head, map[string]workflowPin{"image": pin})
	if err != nil {
		t.Fatal(err)
	}
	// This is the actual boundary: child admission/completion committed, but
	// the controller document never acknowledged that child. No provider runs.
	input := CreativeJobInput{Kind: "image", ExpectedVersion: p.Version, Prompt: "Synthetic original prompt", ImageParameters: &prompts.Defaults{Kind: "general", Ratio: "1:1", Style: "natural"}, ModelRequest: ModelRequest{ModelConfigID: selection.ModelID, ModelConfigVersion: selection.Version, selection: &selection, selectedCaps: Capabilities{Provider: pin.Provider, Model: pin.Model, Mode: "real", Ready: true}}}
	child, _, err := s.createCreativeJob(p.SpaceID, p.ID, workflowJobKey(run.ID, "image"), input)
	if err != nil {
		t.Fatal(err)
	}
	child.Status = "succeeded"
	child.AssetID = m.Assets[0].ID
	if err = s.creative.updateJob(child); err != nil {
		t.Fatal(err)
	}
	// No model store exists, so any new runtime resolve must fail. Read-only
	// reconciliation of the exact admitted child must still recover its result.
	for i := 0; i < 2; i++ {
		if err = s.advanceWorkflow(ctx, p.SpaceID, p.ID); err != nil {
			t.Fatal(err)
		}
	}
	d, err := s.workflows.Read(ctx, p.SpaceID, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	paused := d.Runs[run.ID]
	if paused.Status != "waiting_review" || paused.Nodes["image"].JobID != child.ID || paused.Nodes["review"].Result.AssetID != child.AssetID {
		t.Fatal("lost acknowledged child", paused)
	}
	jobs, err := s.listCreativeJobs(p.SpaceID, p.ID)
	if err != nil || len(jobs) != 1 {
		t.Fatal("duplicate child", len(jobs), err)
	}
	rejected, err := s.workflowDecision(ctx, p.SpaceID, p.ID, run.ID, "review", "reject-original", paused.Version, false, actor)
	if err != nil || rejected.Status != "cancelled" || rejected.Nodes["output"].Status != "cancelled" {
		t.Fatal("rejection did not stop descendants", rejected, err)
	}
}

func TestWorkflowFailedParallelStepStopsAdmissionAndDrainsKnownChildren(t *testing.T) {
	s := testStore(t)
	p, m, _, actor := workflowFixture(t, s)
	ctx := context.Background()
	graph := w.Graph{Nodes: []w.Node{{ID: "text", Kind: "text", Text: "Synthetic parallel prompt"}}, Edges: []w.Edge{}}
	selection := RuntimeSelection{TenantID: p.SpaceID, ActorID: actor, ModelID: "pinned-model", Version: 1, GatewayID: "gateway", GatewayVersion: 1, Capability: "image", FeatureKey: "image_generation"}
	pin := workflowPin{Selection: selection, Provider: "gateway-images", Model: "synthetic-image"}
	pins := map[string]workflowPin{}
	for _, id := range []string{"a", "b", "c"} {
		graph.Nodes = append(graph.Nodes, w.Node{ID: id, Kind: "image", ModelID: selection.ModelID, ModelVersion: 1}, w.Node{ID: "out-" + id, Kind: "output"})
		graph.Edges = append(graph.Edges, w.Edge{Source: "text", Target: id, Input: "prompt"}, w.Edge{Source: id, Target: "out-" + id, Input: "media"})
		pins[id] = pin
	}
	saved, err := s.saveWorkflow(ctx, p.SpaceID, p.ID, "", "parallel-save", actor, workflowSave{Name: "Parallel failure", Graph: graph})
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.startWorkflow(ctx, p.SpaceID, p.ID, saved.ID, "parallel-run", actor, p.Version, saved.Head, pins)
	if err != nil {
		t.Fatal(err)
	}
	children := map[string]CreativeJob{}
	for _, id := range []string{"a", "b"} {
		input := CreativeJobInput{Kind: "image", ExpectedVersion: p.Version, Prompt: "Synthetic parallel prompt", ImageParameters: &prompts.Defaults{Kind: "general", Ratio: "1:1", Style: "natural"}, ModelRequest: ModelRequest{ModelConfigID: selection.ModelID, ModelConfigVersion: selection.Version, selection: &selection, selectedCaps: Capabilities{Provider: pin.Provider, Model: pin.Model, Mode: "real", Ready: true}}}
		child, _, e := s.createCreativeJob(p.SpaceID, p.ID, workflowJobKey(run.ID, id), input)
		if e != nil {
			t.Fatal(e)
		}
		children[id] = child
	}
	// Durable state after two admissions, before the independent workers finish.
	err = s.workflows.Update(ctx, p.SpaceID, p.ID, func(d *workflowrepo.Document) error {
		r := d.Runs[run.ID]
		r.Status = "running"
		r.Nodes["text"] = w.NodeRun{NodeID: "text", Status: "succeeded", Result: w.Result{Text: "Synthetic parallel prompt"}}
		for id, job := range children {
			r.Nodes[id] = w.NodeRun{NodeID: id, Status: "running", JobID: job.ID}
		}
		d.Runs[run.ID] = r
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	first := children["a"]
	first.Status = "failed"
	first.ErrorCode = "provider_rejected"
	if err = s.creative.updateJob(first); err != nil {
		t.Fatal(err)
	}
	if err = s.advanceWorkflow(ctx, p.SpaceID, p.ID); err != nil {
		t.Fatal(err)
	}
	d, err := s.workflows.Read(ctx, p.SpaceID, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	current := d.Runs[run.ID]
	if current.Status != "running" || current.Nodes["a"].Status != "failed" || current.Nodes["c"].Status != "queued" || current.Nodes["c"].JobID != "" {
		t.Fatal("failed branch admitted new work or lost running child", current)
	}
	second := children["b"]
	second.Status = "succeeded"
	second.AssetID = m.Assets[0].ID
	if err = s.creative.updateJob(second); err != nil {
		t.Fatal(err)
	}
	if err = s.advanceWorkflow(ctx, p.SpaceID, p.ID); err != nil {
		t.Fatal(err)
	}
	d, err = s.workflows.Read(ctx, p.SpaceID, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	current = d.Runs[run.ID]
	if current.Status != "failed" || current.Nodes["b"].Result.AssetID != second.AssetID || current.Nodes["c"].JobID != "" {
		t.Fatal("did not drain original result", current)
	}
	jobs, err := s.listCreativeJobs(p.SpaceID, p.ID)
	if err != nil || len(jobs) != 2 {
		t.Fatal("unexpected new child", len(jobs), err)
	}
}

func TestWorkflowTeamClosureStopsQueuedStepsForEveryController(t *testing.T) {
	for _, mode := range []string{"file", "postgres"} {
		t.Run(mode, func(t *testing.T) {
			s := testStore(t)
			if mode == "postgres" {
				s = postgresTestStore(t, postgresTestDB(t))
			}
			p, m, a, actor := workflowFixture(t, s)
			graph := w.Graph{Nodes: []w.Node{{ID: "source", Kind: "asset", AssetKind: "image", AssetID: m.Assets[0].ID}, {ID: "output", Kind: "output"}}, Edges: []w.Edge{{Source: "source", Target: "output", Input: "media"}}, Viewport: &w.Viewport{X: -10, Y: 20, Zoom: .75}}
			ctx := context.Background()
			saved, err := s.saveWorkflow(ctx, p.SpaceID, p.ID, "", "closed-save", actor, workflowSave{Name: "Paused before closure", Graph: graph})
			if err != nil {
				t.Fatal(err)
			}
			run, err := s.startWorkflow(ctx, p.SpaceID, p.ID, saved.ID, "closed-run", actor, p.Version, saved.Head, map[string]workflowPin{})
			if err != nil {
				t.Fatal(err)
			}
			owner, token, session, err := a.login("workflow-owner@example.test", "synthetic-password", false)
			if err != nil {
				t.Fatal(err)
			}
			if !a.can(owner.ID, p.SpaceID, "workspace.write") {
				t.Fatal("owner fixture")
			}
			a.mu.Lock()
			org := a.data.Organizations[p.SpaceID]
			a.mu.Unlock()
			h := SecureHandler(s, "http://localhost:5173", a, AuthOptions{})
			authRequest(t, h, "POST", "/api/v1/organizations/"+p.SpaceID+"/close", projectJSON(t, map[string]any{"expected_version": org.Version, "name": org.Name, "confirmed": true}), token, session.CSRF, 200)
			if err = s.advanceWorkflow(ctx, p.SpaceID, p.ID); err != nil {
				t.Fatal(err)
			}
			doc, err := s.workflows.Read(ctx, p.SpaceID, p.ID)
			if err != nil {
				t.Fatal(err)
			}
			result := doc.Runs[run.ID]
			if result.Status != "failed" || result.Nodes["source"].ErrorCode != "role_denied" || result.Nodes["output"].Result.AssetID != "" {
				t.Fatal("closed team advanced", result)
			}
			if doc.Workflows[saved.ID].Revisions[0].Graph.Viewport.Zoom != .75 {
				t.Fatal("viewport snapshot lost")
			}
		})
	}
}
