package application

import (
	"context"
	"errors"
	w "github.com/adcwb/ai-creative-studio/backend/internal/modules/workflows"
	"github.com/adcwb/ai-creative-studio/backend/internal/persistence/workflowrepo"
	"testing"
)

func TestWorkflowInputsSnapshotAndSelectiveRerun(t *testing.T) {
	for _, mode := range []string{"file", "postgres"} {
		t.Run(mode, func(t *testing.T) {
			s := testStore(t)
			if mode == "postgres" {
				s = postgresTestStore(t, postgresTestDB(t))
			}
			p, _, _, actor := workflowFixture(t, s)
			ctx := context.Background()
			graph := w.Graph{Variables: []w.Variable{{Name: "subject", Label: "主体", Required: true}, {Name: "scene", Label: "场景", Default: "garden"}}, Nodes: []w.Node{{ID: "subject", Kind: "variable", Variable: "subject"}, {ID: "template", Kind: "template", Text: "{{input}} at {{scene}}"}, {ID: "review", Kind: "review"}, {ID: "output", Kind: "output"}}, Edges: []w.Edge{{Source: "subject", Target: "template", Input: "prompt"}, {Source: "template", Target: "review", Input: "media"}, {Source: "review", Target: "output", Input: "media"}}}
			saved, err := s.saveWorkflow(ctx, p.SpaceID, p.ID, "", "inputs-save", actor, workflowSave{Name: "Variable copy", Graph: graph})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.startWorkflow(ctx, p.SpaceID, p.ID, saved.ID, "missing", actor, p.Version, saved.Head, nil); err == nil {
				t.Fatal("missing required accepted")
			}
			run, err := s.startWorkflow(ctx, p.SpaceID, p.ID, saved.ID, "inputs-run", actor, p.Version, saved.Head, nil, map[string]string{"subject": "{{literal}}"})
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
			if paused.Nodes["review"].Result.Text != "{{literal}} at garden" || paused.Inputs["scene"] != "garden" || paused.InputsSHA256 == "" {
				t.Fatal(paused)
			}
			if _, err = s.rerunWorkflow(ctx, p.SpaceID, p.ID, run.ID, "template", "premature", actor, paused.Version, nil); !errors.Is(err, w.ErrConflict) {
				t.Fatal("restarted waiting review", err)
			}
			if _, err = s.workflowDecision(ctx, p.SpaceID, p.ID, run.ID, "review", "approve", paused.Version, true, actor); err != nil {
				t.Fatal(err)
			}
			if err = s.advanceWorkflow(ctx, p.SpaceID, p.ID); err != nil {
				t.Fatal(err)
			}
			d, _ = s.workflows.Read(ctx, p.SpaceID, p.ID)
			done := d.Runs[run.ID]
			next, err := s.rerunWorkflow(ctx, p.SpaceID, p.ID, run.ID, "template", "partial", actor, done.Version, nil)
			if err != nil {
				t.Fatal(err)
			}
			if next.ID == run.ID || next.FromRunID != run.ID || next.Nodes["subject"].ReusedFrom != run.ID || next.Nodes["review"].Status != "queued" || next.Nodes["template"].JobID != "" {
				t.Fatal(next)
			}
			if _, err = s.rerunWorkflow(ctx, p.SpaceID, p.ID, run.ID, "output", "partial", actor, done.Version, nil); !errors.Is(err, workflowrepo.ErrKey) {
				t.Fatal("changed original intent", err)
			}
			if err = s.advanceWorkflow(ctx, p.SpaceID, p.ID); err != nil {
				t.Fatal(err)
			}
			replay, err := s.rerunWorkflow(ctx, p.SpaceID, p.ID, run.ID, "template", "partial", actor, done.Version, nil)
			if err != nil || replay.ID != next.ID || replay.Status != "waiting_review" {
				t.Fatal("replay rewound", replay, err)
			}
			d, _ = s.workflows.Read(ctx, p.SpaceID, p.ID)
			if d.Runs[run.ID].Status != "succeeded" || d.Runs[run.ID].Version != done.Version {
				t.Fatal("changed original")
			}
			err = s.workflows.Update(ctx, p.SpaceID, p.ID, func(d *workflowrepo.Document) error {
				r := d.Runs[run.ID]
				r.Status = "unknown"
				n := r.Nodes["template"]
				n.Status = "unknown"
				r.Nodes["template"] = n
				d.Runs[run.ID] = r
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.rerunWorkflow(ctx, p.SpaceID, p.ID, run.ID, "template", "unsafe", actor, done.Version, nil); !errors.Is(err, w.ErrConflict) {
				t.Fatal("unknown resubmitted", err)
			}
		})
	}
}
