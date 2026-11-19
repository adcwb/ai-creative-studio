package application

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/prompts"
	w "github.com/adcwb/ai-creative-studio/backend/internal/modules/workflows"
	"github.com/adcwb/ai-creative-studio/backend/internal/persistence/workflowrepo"
)

var errWorkflowIdle = errors.New("no workflow change")

func workflowInputs(graph w.Graph, node string, r w.Run) (map[string]w.Result, bool) {
	out := map[string]w.Result{}
	for _, edge := range graph.Edges {
		if edge.Target == node {
			parent := r.Nodes[edge.Source]
			if parent.Status != "succeeded" {
				return out, false
			}
			out[edge.Input] = parent.Result
		}
	}
	return out, true
}
func workflowJobKey(run, node string) string { return "workflow-" + w.Digest([]string{run, node}) }

// advanceWorkflow creates only existing durable creative jobs. Its stable child
// key reconciles the case where job admission committed but this document did
// not. It never retries a failed/ambiguous provider submission automatically.
func (s *FileStore) advanceWorkflow(ctx context.Context, space, project string) error {
	return s.workflows.Update(ctx, space, project, func(d *workflowrepo.Document) error {
		changed := false
		ids := []string{}
		for id := range d.Runs {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			r := d.Runs[id]
			if r.Status != "queued" && r.Status != "running" {
				continue
			}
			if s.runtime == nil || s.runtime.auth == nil || !s.runtime.auth.can(r.Actor, space, "workspace.write") {
				r.Status = "failed"
				r.Version++
				for key, n := range r.Nodes {
					if !w.Terminal(n.Status) {
						n.Status = "failed"
						n.ErrorCode = "role_denied"
						r.Nodes[key] = n
					}
				}
				d.Runs[id] = r
				changed = true
				continue
			}
			var pins map[string]workflowPin
			if json.Unmarshal(d.Pins[id], &pins) != nil {
				return workflowrepo.ErrStorage
			}
			order, err := w.Validate(r.Graph, true)
			if err != nil {
				return err
			}
			nodes := map[string]w.Node{}
			for _, n := range r.Graph.Nodes {
				nodes[n.ID] = n
			}
			runChanged := r.Status == "queued"
			blocked := false
			for _, existing := range r.Nodes {
				if existing.Status == "failed" || existing.Status == "unknown" || existing.Status == "cancelled" {
					blocked = true
				}
			}
			for _, nodeID := range order {
				n := r.Nodes[nodeID]
				definition := nodes[nodeID]
				if n.Status == "running" && n.JobID != "" {
					next, err := s.readWorkflowJobResult(space, project, definition.Kind, n)
					if err != nil {
						return err
					}
					runChanged = runChanged || next.Status != n.Status
					n = next
					r.Nodes[nodeID] = n
				}
				if n.Status == "failed" || n.Status == "unknown" || n.Status == "cancelled" {
					blocked = true
				}
				if n.Status != "queued" || blocked {
					continue
				}
				inputs, ready := workflowInputs(r.Graph, nodeID, r)
				if !ready {
					continue
				}
				switch {
				case definition.Kind == "asset":
					state, err := s.media.GetChecked(project, space)
					if err != nil {
						return err
					}
					asset, ok := mediaAsset(state, definition.AssetID)
					if !ok || asset.Kind != definition.AssetKind {
						n.Status = "failed"
						n.ErrorCode = "workflow_asset_unavailable"
					} else {
						n.Status = "succeeded"
						n.Result = w.Result{AssetID: asset.ID, AssetKind: asset.Kind}
					}
				case w.ModelKind(definition.Kind):
					pin, ok := pins[nodeID]
					if !ok {
						return workflowrepo.ErrStorage
					}
					n = s.admitWorkflowJob(ctx, space, project, r, definition, n, inputs, pin)
				default:
					next, err := w.EvaluateLocalNode(definition, n, r.Inputs, inputs)
					if err != nil {
						return err
					}
					n = next
				}
				r.Nodes[nodeID] = n
				if n.Status == "failed" || n.Status == "unknown" {
					blocked = true
				}
				runChanged = true
			}
			if runChanged {
				r.Status = workflowStatus(r.Nodes)
				r.Version++
				d.Runs[id] = r
				changed = true
			}
		}
		if !changed {
			return errWorkflowIdle
		}
		return nil
	})
}

func (s *FileStore) readWorkflowJobResult(space, project, kind string, node w.NodeRun) (w.NodeRun, error) {
	s.creative.mu.Lock()
	job, ok := s.creative.data.Jobs[node.JobID]
	job = cloneCreativeJob(job)
	s.creative.mu.Unlock()
	if !ok || job.SpaceID != space || job.ProjectID != project {
		return node, workflowrepo.ErrStorage
	}
	switch job.Status {
	case "succeeded":
		node.Status = "succeeded"
		if kind == "llm" {
			node.Result = w.Result{Text: job.TextResult}
			return node, nil
		}
		state, err := s.media.GetChecked(project, space)
		if err != nil {
			return node, err
		}
		asset, ok := mediaAsset(state, job.AssetID)
		if !ok {
			return node, workflowrepo.ErrStorage
		}
		node.Result = w.Result{AssetID: asset.ID, AssetKind: asset.Kind}
	case "failed", "cancelled":
		node.Status = "failed"
		switch job.ErrorCode {
		case "provider_timeout", "provider_error", "execution_interrupted":
			node.Status = "unknown"
		}
		node.ErrorCode = job.ErrorCode
	}
	return node, nil
}

func workflowJobInput(run w.Run, definition w.Node, inputs map[string]w.Result, pin workflowPin) CreativeJobInput {
	in := CreativeJobInput{
		Kind:             workflowCreativeKind(definition.Kind),
		ExpectedVersion:  run.ProjectVersion,
		Prompt:           inputs["prompt"].Text,
		ReferenceAssetID: inputs["reference"].AssetID,
		ModelRequest: ModelRequest{
			ModelConfigID:      pin.Selection.ModelID,
			ModelConfigVersion: pin.Selection.Version,
			Voice:              pin.Selection.Voice,
			selection:          cloneRuntimeSelection(&pin.Selection),
			selectedCaps: Capabilities{
				Provider: pin.Provider, Model: pin.Model, Mode: "real", Ready: true,
			},
		},
	}
	switch definition.Kind {
	case "llm":
		in.TextInstruction = definition.Text
	case "image":
		ratio := definition.Ratio
		if ratio == "" {
			ratio = "1:1"
		}
		in.ImageParameters = &prompts.Defaults{Kind: "general", Ratio: ratio, Style: "natural"}
	}
	return in
}

func (s *FileStore) admitWorkflowJob(ctx context.Context, space, project string, run w.Run, definition w.Node, node w.NodeRun, inputs map[string]w.Result, pin workflowPin) w.NodeRun {
	key := workflowJobKey(run.ID, definition.ID)
	// A committed child receipt can outlive a crash before this document
	// records its ID. Reconcile with the original fingerprint before checking
	// whether a new admission is still allowed.
	s.creative.mu.Lock()
	_, admitted := s.creative.data.Receipts[projectReceiptKey(space, key)]
	s.creative.mu.Unlock()
	if !admitted {
		// The existing child worker obtains the task-bound execution permit
		// immediately before dispatch.
		current, _, err := s.runtime.Resolve(ctx, space, run.Actor, pin.Selection.ModelID, pin.Selection.Capability)
		_, featureAllowed := s.runtime.authorizeFeature(space, pin.Selection.FeatureKey, pin.Selection.ModelID)
		if err != nil || current.Version != pin.Selection.Version || current.GatewayID != pin.Selection.GatewayID ||
			current.GatewayVersion != pin.Selection.GatewayVersion || !featureAllowed {
			node.Status = "failed"
			node.ErrorCode = "model_runtime_denied"
			return node
		}
	}
	job, _, err := s.createCreativeJob(space, project, key, workflowJobInput(run, definition, inputs, pin))
	switch {
	case err == nil:
		node.Status = "running"
		node.JobID = job.ID
	case errors.Is(err, ErrVersionConflict):
		node.Status = "failed"
		node.ErrorCode = "version_conflict"
	case errors.Is(err, ErrInvalidProjectInput), errors.Is(err, ErrProviderChanged), errors.Is(err, ErrProviderNotReady):
		node.Status = "failed"
		node.ErrorCode = "invalid_input"
	default:
		node.Status = "unknown"
		node.ErrorCode = "workflow_unknown"
	}
	return node
}

// RunWorkflowWorker can run in independent PostgreSQL workers or the file API.
// Project transactions and original child keys make concurrent controllers safe.
func RunWorkflowWorker(ctx context.Context, s *FileStore) (workerErr error) {
	defer recoverFileWorker(ctx, &workerErr)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			scopes, err := s.workflows.Pending(ctx)
			if err != nil {
				return err
			}
			for _, scope := range scopes {
				if err = s.advanceWorkflow(ctx, scope.Space, scope.Project); err != nil && !errors.Is(err, errWorkflowIdle) {
					return err
				}
			}
		}
	}
}
