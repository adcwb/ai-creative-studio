package application

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	w "github.com/adcwb/ai-creative-studio/backend/internal/modules/workflows"
	"github.com/adcwb/ai-creative-studio/backend/internal/persistence/workflowrepo"
)

type workflowSave struct {
	Name            string  `json:"name"`
	ExpectedVersion int     `json:"expected_version"`
	Graph           w.Graph `json:"graph"`
}
type workflowPin struct {
	Selection RuntimeSelection `json:"selection"`
	Provider  string           `json:"provider"`
	Model     string           `json:"model"`
}

func workflowKey(space, project, key, kind string) string {
	return w.Digest([]string{space, project, key, kind})
}
func workflowReceipt(d *workflowrepo.Document, key, digest, kind string) (string, bool, error) {
	r, ok := d.Receipts[key]
	if !ok {
		return "", false, nil
	}
	if r.Digest != digest || r.Kind != kind {
		return "", false, workflowrepo.ErrKey
	}
	return r.ID, true, nil
}
func (s *FileStore) saveWorkflow(ctx context.Context, space, project, id, key, actor string, in workflowSave) (w.Workflow, error) {
	if _, err := s.GetProject(space, project); err != nil {
		return w.Workflow{}, err
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || utf8.RuneCountInString(in.Name) > 80 || strings.IndexFunc(in.Name, unicode.IsControl) >= 0 {
		return w.Workflow{}, w.ErrInput
	}
	if in.Graph.Edges == nil {
		in.Graph.Edges = []w.Edge{}
	}
	if _, err := w.Validate(in.Graph, false); err != nil {
		return w.Workflow{}, err
	}
	creating := id == ""
	if id != "" && !w.ID.MatchString(id) {
		return w.Workflow{}, w.ErrInput
	}
	if id == "" {
		id = workflowKey(space, project, key, "workflow")
	}
	var out w.Workflow
	digest := w.Digest([]any{id, in})
	err := s.workflows.Update(ctx, space, project, func(d *workflowrepo.Document) error {
		replayID, replay, err := workflowReceipt(d, key, digest, "save")
		if err != nil {
			return err
		}
		if replay {
			out = d.Workflows[replayID]
			return nil
		}
		current, exists := d.Workflows[id]
		if !creating && !exists {
			return workflowrepo.ErrNotFound
		}
		if exists && current.Version != in.ExpectedVersion || !exists && in.ExpectedVersion != 0 {
			return w.ErrConflict
		}
		current.ID = id
		current.Name = in.Name
		current.Version++
		rev := w.Revision{ID: workflowKey(space, project, key, "revision"), Number: current.Version, SHA256: w.Digest(in.Graph), Graph: in.Graph, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), Actor: actor}
		current.Head = rev.ID
		current.Revisions = append(current.Revisions, rev)
		d.Workflows[id] = current
		d.Receipts[key] = workflowrepo.Receipt{Digest: digest, ID: id, Kind: "save"}
		out = current
		return nil
	})
	return out, err
}
func (s *FileStore) startWorkflow(ctx context.Context, space, project, id, key, actor string, expected int, revision string, pins map[string]workflowPin, supplied ...map[string]string) (w.Run, error) {
	p, err := s.GetProject(space, project)
	if err != nil {
		return w.Run{}, err
	}
	var out w.Run
	var values map[string]string
	if len(supplied) > 0 {
		values = supplied[0]
	}
	digest := workflowRunDigest(id, expected, revision, values)
	runID := workflowKey(space, project, key, "run")
	err = s.workflows.Update(ctx, space, project, func(d *workflowrepo.Document) error {
		if original, replay, err := workflowReceipt(d, key, digest, "run"); err != nil {
			return err
		} else if replay {
			out = d.Runs[original]
			return nil
		}
		workflow, ok := d.Workflows[id]
		if !ok {
			return workflowrepo.ErrNotFound
		}
		var source *w.Revision
		for _, v := range workflow.Revisions {
			if v.ID == revision {
				v := v
				source = &v
				break
			}
		}
		if source == nil {
			return workflowrepo.ErrNotFound
		}
		if p.Version != expected {
			return ErrVersionConflict
		}
		if _, err := w.Validate(source.Graph, true); err != nil {
			return err
		}
		resolved, e := w.ResolveInputs(source.Graph, values)
		if e != nil {
			return e
		}
		out = w.Run{Inputs: resolved, InputsSHA256: w.Digest(resolved), ID: runID, WorkflowID: id, RevisionID: revision, SHA256: source.SHA256, ProjectVersion: p.Version, Actor: actor, Status: "queued", Version: 1, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), Nodes: map[string]w.NodeRun{}, Graph: source.Graph}
		for _, node := range source.Graph.Nodes {
			if w.ModelKind(node.Kind) {
				pin, ok := pins[node.ID]
				if !ok || pin.Selection.TenantID != space || pin.Selection.ActorID != actor || pin.Selection.ModelID != node.ModelID || pin.Selection.Version != node.ModelVersion || pin.Selection.GatewayID == "" {
					return w.ErrInput
				}
			}
			if node.Kind == "asset" {
				state, err := s.media.GetChecked(project, space)
				if err != nil {
					return err
				}
				a, ok := mediaAsset(state, node.AssetID)
				if !ok || a.Kind != node.AssetKind {
					return w.ErrInput
				}
			}
			out.Nodes[node.ID] = w.NodeRun{NodeID: node.ID, Status: "queued"}
		}
		raw, err := json.Marshal(pins)
		if err != nil {
			return err
		}
		d.Runs[runID] = out
		d.Pins[runID] = raw
		d.Receipts[key] = workflowrepo.Receipt{Digest: digest, ID: runID, Kind: "run"}
		return nil
	})
	return out, err
}
func (s *FileStore) workflowDecision(ctx context.Context, space, project, run, node, key string, expected int, approve bool, reviewer ...string) (w.Run, error) {
	if _, err := s.GetProject(space, project); err != nil {
		return w.Run{}, err
	}
	var out w.Run
	digest := w.Digest([]any{run, node, expected, approve})
	err := s.workflows.Update(ctx, space, project, func(d *workflowrepo.Document) error {
		if original, replay, err := workflowReceipt(d, key, digest, "decision"); err != nil {
			return err
		} else if replay {
			out = d.Runs[original]
			return nil
		}
		r, ok := d.Runs[run]
		if !ok {
			return workflowrepo.ErrNotFound
		}
		v, ok := r.Nodes[node]
		if !ok {
			return workflowrepo.ErrNotFound
		}
		if r.Version != expected || v.Status != "waiting_review" || w.Terminal(r.Status) {
			return w.ErrConflict
		}
		v.ReviewedBy = r.Actor
		if len(reviewer) > 0 {
			v.ReviewedBy = reviewer[0]
		}
		v.ReviewedAt = time.Now().UTC().Format(time.RFC3339Nano)
		if approve {
			v.Status = "succeeded"
			r.Status = "queued"
		} else {
			v.Status = "failed"
			v.ErrorCode = "workflow_review_rejected"
			r.Status = "cancelled"
			for id, other := range r.Nodes {
				if id != node && !w.Terminal(other.Status) {
					other.Status = "cancelled"
					r.Nodes[id] = other
				}
			}
		}
		r.Nodes[node] = v
		r.Version++
		d.Runs[run] = r
		d.Receipts[key] = workflowrepo.Receipt{Digest: digest, ID: run, Kind: "decision"}
		out = r
		return nil
	})
	if err == nil && !approve {
		err = s.cancelWorkflowChildren(space, project, out)
	}
	return out, err
}

func (s *FileStore) workflowAction(ctx context.Context, space, project, run, key, action string, expected int) (w.Run, error) {
	if _, err := s.GetProject(space, project); err != nil {
		return w.Run{}, err
	}
	var out w.Run
	digest := w.Digest([]any{run, action, expected})
	err := s.workflows.Update(ctx, space, project, func(d *workflowrepo.Document) error {
		if original, replay, err := workflowReceipt(d, key, digest, action); err != nil {
			return err
		} else if replay {
			out = d.Runs[original]
			return nil
		}
		r, ok := d.Runs[run]
		if !ok {
			return workflowrepo.ErrNotFound
		}
		if r.Version != expected {
			return w.ErrConflict
		}
		switch action {
		case "cancel":
			if r.Status == "succeeded" {
				return w.ErrConflict
			}
			r.Status = "cancelled"
			for id, n := range r.Nodes {
				if !w.Terminal(n.Status) {
					n.Status = "cancelled"
					r.Nodes[id] = n
				}
			}
		case "reconcile":
			if r.Status != "unknown" {
				return w.ErrConflict
			}
			for id, n := range r.Nodes {
				if n.Status == "unknown" {
					if n.JobID == "" {
						n.Status = "queued"
					} else {
						s.creative.mu.Lock()
						job, ok := s.creative.data.Jobs[n.JobID]
						s.creative.mu.Unlock()
						if !ok || job.SpaceID != space || job.ProjectID != project {
							return workflowrepo.ErrStorage
						}
						if job.Status == "succeeded" || job.Status == "running" || job.Status == "queued" {
							n.Status = "running"
						} else if job.UpstreamID != "" && (job.Kind == "video" || job.Kind == "image" && job.Provider == "jimeng-images") {
							if _, err := s.resumeCreativeJob(space, project, job.ID, workflowJobKey(run, id)+"-resume"); err != nil {
								return err
							}
							n.Status = "running"
						}
					}
					r.Nodes[id] = n
				}
			}
			r.Status = "queued"
		default:
			return w.ErrInput
		}
		r.Version++
		d.Runs[run] = r
		d.Receipts[key] = workflowrepo.Receipt{Digest: digest, ID: run, Kind: action}
		out = r
		return nil
	})
	if err == nil && action == "cancel" {
		err = s.cancelWorkflowChildren(space, project, out)
	}
	return out, err
}

func workflowStatus(nodes map[string]w.NodeRun) string {
	unknown, failed, running, waiting, queued := false, false, false, false, false
	for _, n := range nodes {
		unknown = unknown || n.Status == "unknown"
		failed = failed || n.Status == "failed" || n.Status == "cancelled"
		running = running || n.Status == "running"
		waiting = waiting || n.Status == "waiting_review"
		queued = queued || n.Status == "queued"
	}
	// Drain already admitted children without admitting further nodes after a
	// failed/unknown step, so their final identities and results remain visible.
	if running {
		return "running"
	}
	if unknown {
		return "unknown"
	}
	if failed {
		return "failed"
	}
	if waiting {
		return "waiting_review"
	}
	if queued {
		return "queued"
	}
	return "succeeded"
}

func (s *FileStore) cancelWorkflowChildren(space, project string, run w.Run) error {
	for id, n := range run.Nodes {
		if n.ReusedFrom != "" {
			continue
		}
		jobID := n.JobID
		if jobID == "" {
			s.creative.mu.Lock()
			receipt := s.creative.data.Receipts[projectReceiptKey(space, workflowJobKey(run.ID, id))]
			job := s.creative.data.Jobs[receipt.JobID]
			if job.SpaceID == space && job.ProjectID == project {
				jobID = job.ID
			}
			s.creative.mu.Unlock()
		}
		if jobID != "" {
			_, err := s.cancelCreativeJob(space, project, jobID, workflowJobKey(run.ID, id)+"-cancel")
			if err != nil && !errors.Is(err, ErrConflict) && !errors.Is(err, ErrNotFound) {
				return err
			}
		}
	}
	return nil
}

func workflowRunDigest(id string, expected int, revision string, values map[string]string) string {
	if len(values) == 0 {
		return w.Digest([]any{id, expected, revision})
	}
	return w.Digest([]any{id, expected, revision, values})
}
