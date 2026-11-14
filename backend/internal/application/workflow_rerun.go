package application

import (
	"context"
	"encoding/json"
	"fmt"
	w "github.com/adcwb/ai-creative-studio/backend/internal/modules/workflows"
	"github.com/adcwb/ai-creative-studio/backend/internal/persistence/workflowrepo"
	"time"
)

var errWorkflowRerunUnsafe = fmt.Errorf("workflow rerun requires settled results: %w", w.ErrConflict)

func workflowResetNodes(r w.Run, node string) (map[string]bool, error) {
	if r.Status != "succeeded" && r.Status != "failed" && r.Status != "cancelled" {
		return nil, errWorkflowRerunUnsafe
	}
	if _, ok := r.Nodes[node]; !ok {
		return nil, w.ErrInput
	}
	reset := map[string]bool{node: true}
	for changed := true; changed; {
		changed = false
		for _, edge := range r.Graph.Edges {
			if reset[edge.Source] && !reset[edge.Target] {
				reset[edge.Target] = true
				changed = true
			}
		}
	}
	for id, n := range r.Nodes {
		if n.Status == "unknown" || !reset[id] && n.Status != "succeeded" {
			return nil, errWorkflowRerunUnsafe
		}
	}
	return reset, nil
}

func (s *FileStore) rerunWorkflow(ctx context.Context, space, project, from, node, key, actor string, expected int, pins map[string]workflowPin) (w.Run, error) {
	p, err := s.GetProject(space, project)
	if err != nil {
		return w.Run{}, err
	}
	var out w.Run
	digest := w.Digest([]any{from, node, expected})
	err = s.workflows.Update(ctx, space, project, func(d *workflowrepo.Document) error {
		if id, replay, e := workflowReceipt(d, key, digest, "rerun"); e != nil {
			return e
		} else if replay {
			out = d.Runs[id]
			return nil
		}
		source, ok := d.Runs[from]
		if !ok {
			return workflowrepo.ErrNotFound
		}
		if source.Version != expected || p.Version != source.ProjectVersion {
			return w.ErrConflict
		}
		reset, e := workflowResetNodes(source, node)
		if e != nil {
			return e
		}
		var original map[string]workflowPin
		if json.Unmarshal(d.Pins[from], &original) != nil {
			return workflowrepo.ErrStorage
		}
		for _, definition := range source.Graph.Nodes {
			if reset[definition.ID] && w.ModelKind(definition.Kind) {
				pin, ok := pins[definition.ID]
				if !ok || pin.Selection.ActorID != actor || pin.Selection.TenantID != space || pin.Selection.ModelID != definition.ModelID || pin.Selection.Version != definition.ModelVersion {
					return w.ErrInput
				}
				original[definition.ID] = pin
			}
			if !reset[definition.ID] && source.Nodes[definition.ID].Result.AssetID != "" {
				state, e := s.media.GetChecked(project, space)
				if e != nil {
					return e
				}
				if _, ok := mediaAsset(state, source.Nodes[definition.ID].Result.AssetID); !ok {
					return w.ErrInput
				}
			}
		}
		out = w.Run{ID: workflowKey(space, project, key, "run"), WorkflowID: source.WorkflowID, RevisionID: source.RevisionID, SHA256: source.SHA256, Graph: source.Graph, ProjectVersion: source.ProjectVersion, Inputs: source.Inputs, InputsSHA256: source.InputsSHA256, FromRunID: from, RestartNode: node, Actor: actor, Version: 1, Status: "queued", CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), Nodes: map[string]w.NodeRun{}}
		for id, n := range source.Nodes {
			if reset[id] {
				out.Nodes[id] = w.NodeRun{NodeID: id, Status: "queued"}
			} else {
				n.ReusedFrom = from
				out.Nodes[id] = n
			}
		}
		raw, e := json.Marshal(original)
		if e != nil {
			return e
		}
		d.Runs[out.ID] = out
		d.Pins[out.ID] = raw
		d.Receipts[key] = workflowrepo.Receipt{Kind: "rerun", Digest: digest, ID: out.ID}
		return nil
	})
	return out, err
}
