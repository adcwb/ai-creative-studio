package application

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	w "github.com/adcwb/ai-creative-studio/backend/internal/modules/workflows"
	"github.com/adcwb/ai-creative-studio/backend/internal/persistence/workflowrepo"
	"net/http"
	"sort"
	"strconv"
)

func workflowError(out http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errWorkflowRerunUnsafe):
		problem(out, 409, "workflow_version_conflict", "先完成或停止当前运行；结果未知时先核对原任务。有其他未完成分支时，请从共同上游重做。旧运行保留。")
	case errors.Is(err, w.ErrInput):
		problem(out, 400, "invalid_workflow", "请检查步骤、连线类型、模型选择和输出；流程不能包含循环或未使用的生成步骤")
	case errors.Is(err, w.ErrConflict), errors.Is(err, ErrVersionConflict):
		problem(out, 409, "workflow_version_conflict", "流程、项目或运行已更新，请保留输入并重新读取")
	case errors.Is(err, workflowrepo.ErrNotFound), errors.Is(err, ErrNotFound):
		problem(out, 404, "not_found", "流程、修订或运行不存在")
	case errors.Is(err, workflowrepo.ErrKey):
		problem(out, 409, "idempotency_conflict", "原请求键不能用于不同的流程操作")
	default:
		problem(out, 503, "workflow_storage_error", "流程操作结果未确认，请使用原请求核对")
	}
}

type workflowSummary struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Version int    `json:"version"`
	Head    string `json:"head_revision_id"`
}

func summary(v w.Workflow) workflowSummary { return workflowSummary{v.ID, v.Name, v.Version, v.Head} }
func workflowPage(r *http.Request, space, project, kind string) (string, error) {
	raw := r.URL.Query().Get("cursor")
	if raw == "" {
		return "", nil
	}
	if len(raw) > 2048 {
		return "", w.ErrInput
	}
	b, e := base64.RawURLEncoding.DecodeString(raw)
	var parts []string
	if e != nil || json.Unmarshal(b, &parts) != nil || len(parts) != 4 || parts[0] != space || parts[1] != project || parts[2] != kind || !w.ID.MatchString(parts[3]) {
		return "", w.ErrInput
	}
	return parts[3], nil
}
func workflowNext(space, project, kind, id string) string {
	b, _ := json.Marshal([]string{space, project, kind, id})
	return base64.RawURLEncoding.EncodeToString(b)
}
func registerWorkflowRoutes(mux *http.ServeMux, s *FileStore) {
	base := "/api/v1/projects/{id}/workflows"
	mux.HandleFunc("POST /api/v1/projects/{id}/workflow-runs/{run_id}/reruns", func(out http.ResponseWriter, r *http.Request) {
		space, key, ok := projectWriteScope(out, r)
		if !ok {
			return
		}
		actor, ok := r.Context().Value(authScopeKey{}).(authScope)
		if !ok || actor.UserID == "" {
			problem(out, 403, "role_denied", "流程重做需要安全登录身份")
			return
		}
		if _, e := s.GetProject(space, r.PathValue("id")); e != nil {
			workflowError(out, e)
			return
		}
		var in struct {
			Node      string `json:"node_id"`
			Expected  int    `json:"expected_version"`
			Confirmed bool   `json:"confirmed"`
		}
		if !decodeProject(out, r, &in) {
			return
		}
		if !in.Confirmed {
			workflowError(out, w.ErrInput)
			return
		}
		d, e := s.workflows.Read(r.Context(), space, r.PathValue("id"))
		if e != nil {
			workflowError(out, e)
			return
		}
		digest := w.Digest([]any{r.PathValue("run_id"), in.Node, in.Expected})
		if id, replay, e := workflowReceipt(&d, key, digest, "rerun"); e != nil {
			workflowError(out, e)
			return
		} else if replay {
			respond(out, 200, d.Runs[id])
			return
		}
		source, ok := d.Runs[r.PathValue("run_id")]
		if !ok {
			workflowError(out, workflowrepo.ErrNotFound)
			return
		}
		reset, e := workflowResetNodes(source, in.Node)
		if e != nil {
			workflowError(out, e)
			return
		}
		pins, ok := resolveWorkflowPins(out, r, s, source.Graph, reset)
		if !ok {
			return
		}
		result, e := s.rerunWorkflow(r.Context(), space, r.PathValue("id"), source.ID, in.Node, key, actor.UserID, in.Expected, pins)
		if e != nil {
			workflowError(out, e)
			return
		}
		respond(out, 200, result)
	})

	scope := func(out http.ResponseWriter, r *http.Request, write bool) (string, string, bool) {
		var space, key string
		var ok bool
		if write {
			space, key, ok = projectWriteScope(out, r)
		} else {
			space, ok = spaceID(out, r)
		}
		if !ok {
			return "", "", false
		}
		if _, err := s.GetProject(space, r.PathValue("id")); err != nil {
			workflowError(out, err)
			return "", "", false
		}
		return space, key, true
	}
	mux.HandleFunc("GET "+base, func(out http.ResponseWriter, r *http.Request) {
		space, _, ok := scope(out, r, false)
		if !ok {
			return
		}
		after, err := workflowPage(r, space, r.PathValue("id"), "workflows")
		if err != nil {
			workflowError(out, err)
			return
		}
		doc, err := s.workflows.Read(r.Context(), space, r.PathValue("id"))
		if err != nil {
			workflowError(out, err)
			return
		}
		ids := []string{}
		for id := range doc.Workflows {
			if id > after {
				ids = append(ids, id)
			}
		}
		sort.Strings(ids)
		items := []workflowSummary{}
		next := ""
		for _, id := range ids {
			if len(items) == 20 {
				next = workflowNext(space, r.PathValue("id"), "workflows", items[len(items)-1].ID)
				break
			}
			items = append(items, summary(doc.Workflows[id]))
		}
		respond(out, 200, map[string]any{"items": items, "next_cursor": next})
	})
	save := func(out http.ResponseWriter, r *http.Request) {
		space, key, ok := scope(out, r, true)
		if !ok {
			return
		}
		var in workflowSave
		if !decodeProject(out, r, &in) {
			return
		}
		a := promptActor(r, space)
		result, err := s.saveWorkflow(r.Context(), space, r.PathValue("id"), r.PathValue("workflow_id"), key, a.User, in)
		if err != nil {
			workflowError(out, err)
			return
		}
		respond(out, 200, summary(result))
	}
	mux.HandleFunc("POST "+base, save)
	mux.HandleFunc("PUT "+base+"/{workflow_id}", save)
	mux.HandleFunc("GET "+base+"/{workflow_id}", func(out http.ResponseWriter, r *http.Request) {
		space, _, ok := scope(out, r, false)
		if !ok {
			return
		}
		d, err := s.workflows.Read(r.Context(), space, r.PathValue("id"))
		if err != nil {
			workflowError(out, err)
			return
		}
		v, ok := d.Workflows[r.PathValue("workflow_id")]
		if !ok {
			workflowError(out, workflowrepo.ErrNotFound)
			return
		}
		respond(out, 200, struct {
			workflowSummary
			Revision      w.Revision `json:"revision"`
			RevisionCount int        `json:"revision_count"`
		}{summary(v), v.Revisions[len(v.Revisions)-1], len(v.Revisions)})
	})
	mux.HandleFunc("GET "+base+"/{workflow_id}/revisions", func(out http.ResponseWriter, r *http.Request) {
		space, _, ok := scope(out, r, false)
		if !ok {
			return
		}
		d, err := s.workflows.Read(r.Context(), space, r.PathValue("id"))
		if err != nil {
			workflowError(out, err)
			return
		}
		v, ok := d.Workflows[r.PathValue("workflow_id")]
		if !ok {
			workflowError(out, workflowrepo.ErrNotFound)
			return
		}
		after, err := workflowPage(r, space, r.PathValue("id"), "revisions:"+v.ID)
		if err != nil {
			workflowError(out, err)
			return
		}
		number := len(v.Revisions)
		if after != "" {
			number, err = strconv.Atoi(after)
			if err != nil || number < 1 || number > len(v.Revisions) {
				workflowError(out, w.ErrInput)
				return
			}
		}
		items := []map[string]any{}
		next := ""
		for i := number - 1; i >= 0; i-- {
			if len(items) == 20 {
				next = workflowNext(space, r.PathValue("id"), "revisions:"+v.ID, strconv.Itoa(i+1))
				break
			}
			rev := v.Revisions[i]
			items = append(items, map[string]any{"id": rev.ID, "number": rev.Number, "sha256": rev.SHA256, "created_at": rev.CreatedAt, "created_by": rev.Actor})
		}
		respond(out, 200, map[string]any{"items": items, "next_cursor": next})
	})
	mux.HandleFunc("GET "+base+"/{workflow_id}/revisions/{revision_id}", func(out http.ResponseWriter, r *http.Request) {
		space, _, ok := scope(out, r, false)
		if !ok {
			return
		}
		d, err := s.workflows.Read(r.Context(), space, r.PathValue("id"))
		if err != nil {
			workflowError(out, err)
			return
		}
		v, ok := d.Workflows[r.PathValue("workflow_id")]
		if ok {
			for _, rev := range v.Revisions {
				if rev.ID == r.PathValue("revision_id") {
					respond(out, 200, rev)
					return
				}
			}
		}
		workflowError(out, workflowrepo.ErrNotFound)
	})
	mux.HandleFunc("POST "+base+"/{workflow_id}/runs", func(out http.ResponseWriter, r *http.Request) {
		space, key, ok := scope(out, r, true)
		if !ok {
			return
		}
		a, authenticated := r.Context().Value(authScopeKey{}).(authScope)
		if !authenticated || a.UserID == "" {
			problem(out, 403, "role_denied", "流程执行需要安全登录身份")
			return
		}
		var in struct {
			Values   map[string]string `json:"inputs,omitempty"`
			Revision string            `json:"revision_id"`
			Expected int               `json:"expected_project_version"`
		}
		if !decodeProject(out, r, &in) {
			return
		}
		d, err := s.workflows.Read(r.Context(), space, r.PathValue("id"))
		if err != nil {
			workflowError(out, err)
			return
		}
		digest := workflowRunDigest(r.PathValue("workflow_id"), in.Expected, in.Revision, in.Values)
		if id, replay, err := workflowReceipt(&d, key, digest, "run"); err != nil {
			workflowError(out, err)
			return
		} else if replay {
			respond(out, 200, d.Runs[id])
			return
		}
		v, ok := d.Workflows[r.PathValue("workflow_id")]
		var source *w.Revision
		if ok {
			for _, rev := range v.Revisions {
				if rev.ID == in.Revision {
					rev := rev
					source = &rev
					break
				}
			}
		}
		if source == nil {
			workflowError(out, workflowrepo.ErrNotFound)
			return
		}
		if _, err = w.Validate(source.Graph, true); err != nil {
			workflowError(out, err)
			return
		}
		pins, ok := resolveWorkflowPins(out, r, s, source.Graph, nil)
		if !ok {
			return
		}
		run, err := s.startWorkflow(r.Context(), space, r.PathValue("id"), v.ID, key, a.UserID, in.Expected, in.Revision, pins, in.Values)
		if err != nil {
			workflowError(out, err)
			return
		}
		respond(out, 200, run)
	})
	runBase := "/api/v1/projects/{id}/workflow-runs"
	mux.HandleFunc("GET "+runBase, func(out http.ResponseWriter, r *http.Request) {
		space, _, ok := scope(out, r, false)
		if !ok {
			return
		}
		after, err := workflowPage(r, space, r.PathValue("id"), "runs")
		if err != nil {
			workflowError(out, err)
			return
		}
		d, err := s.workflows.Read(r.Context(), space, r.PathValue("id"))
		if err != nil {
			workflowError(out, err)
			return
		}
		ids := []string{}
		for id := range d.Runs {
			if id > after {
				ids = append(ids, id)
			}
		}
		sort.Strings(ids)
		items := []map[string]any{}
		next := ""
		for _, id := range ids {
			if len(items) == 20 {
				next = workflowNext(space, r.PathValue("id"), "runs", items[len(items)-1]["id"].(string))
				break
			}
			v := d.Runs[id]
			items = append(items, map[string]any{"id": v.ID, "workflow_id": v.WorkflowID, "revision_id": v.RevisionID, "status": v.Status, "version": v.Version, "created_at": v.CreatedAt})
		}
		respond(out, 200, map[string]any{"items": items, "next_cursor": next})
	})
	mux.HandleFunc("GET "+runBase+"/{run_id}", func(out http.ResponseWriter, r *http.Request) {
		space, _, ok := scope(out, r, false)
		if !ok {
			return
		}
		d, err := s.workflows.Read(r.Context(), space, r.PathValue("id"))
		if err != nil {
			workflowError(out, err)
			return
		}
		v, ok := d.Runs[r.PathValue("run_id")]
		if !ok {
			workflowError(out, workflowrepo.ErrNotFound)
			return
		}
		respond(out, 200, v)
	})
	mux.HandleFunc("POST "+runBase+"/{run_id}/nodes/{node_id}/decision", func(out http.ResponseWriter, r *http.Request) {
		space, key, ok := scope(out, r, true)
		if !ok {
			return
		}
		var in struct {
			Expected int   `json:"expected_version"`
			Approved *bool `json:"approved"`
		}
		if !decodeProject(out, r, &in) {
			return
		}
		if in.Approved == nil {
			workflowError(out, w.ErrInput)
			return
		}
		v, err := s.workflowDecision(r.Context(), space, r.PathValue("id"), r.PathValue("run_id"), r.PathValue("node_id"), key, in.Expected, *in.Approved, promptActor(r, space).User)
		if err != nil {
			workflowError(out, err)
			return
		}
		respond(out, 200, v)
	})
	for _, action := range []string{"cancel", "reconcile"} {
		action := action
		mux.HandleFunc("POST "+runBase+"/{run_id}/"+action, func(out http.ResponseWriter, r *http.Request) {
			space, key, ok := scope(out, r, true)
			if !ok {
				return
			}
			var in struct {
				Expected int `json:"expected_version"`
			}
			if !decodeProject(out, r, &in) {
				return
			}
			v, err := s.workflowAction(r.Context(), space, r.PathValue("id"), r.PathValue("run_id"), key, action, in.Expected)
			if err != nil {
				workflowError(out, err)
				return
			}
			respond(out, 200, v)
		})
	}

}

func resolveWorkflowPins(out http.ResponseWriter, r *http.Request, s *FileStore, graph w.Graph, reset map[string]bool) (map[string]workflowPin, bool) {
	pins := map[string]workflowPin{}
	for _, node := range graph.Nodes {
		if !w.ModelKind(node.Kind) || reset != nil && !reset[node.ID] {
			continue
		}
		job := CreativeJobInput{Kind: workflowCreativeKind(node.Kind), ModelRequest: ModelRequest{ModelConfigID: node.ModelID, ModelConfigVersion: node.ModelVersion, Voice: node.Voice}}
		for _, edge := range graph.Edges {
			if edge.Target == node.ID && edge.Input == "reference" {
				job.ReferenceAssetID = "workflow-reference"
			}
		}
		if !resolveModelRequest(out, r, s, &job.ModelRequest, creativeRuntimeCapability(workflowCreativeKind(node.Kind)), operationFeature("creative", &job)) {
			return nil, false
		}
		if job.selection == nil || job.selection.GatewayID == "" {
			problem(out, 409, "gateway_model_required", "流程生成步骤必须选择真实网关模型")
			return nil, false
		}
		pins[node.ID] = workflowPin{*job.selection, job.selectedCaps.Provider, job.selectedCaps.Model}
	}
	return pins, true
}
