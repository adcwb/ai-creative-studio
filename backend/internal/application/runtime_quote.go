package application

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

type RuntimeQuoteInput struct {
	Operation          string          `json:"operation"`
	ProjectID          string          `json:"project_id,omitempty"`
	ModelConfigID      string          `json:"model_config_id"`
	ModelConfigVersion int64           `json:"model_config_version"`
	Voice              string          `json:"voice,omitempty"`
	Input              json.RawMessage `json:"input"`
}
type RuntimeRequestQuote struct {
	RuntimeQuotePreview
	RequestDigest      string `json:"request_sha256"`
	ModelConfigID      string `json:"model_config_id"`
	ModelConfigVersion int64  `json:"model_config_version"`
}

func decodeQuotePayload(raw json.RawMessage, target any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(target); err != nil {
		return ErrInvalidProjectInput
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return ErrInvalidProjectInput
	}
	return nil
}
func registerRuntimeQuote(mux *http.ServeMux, s *FileStore) {
	mux.HandleFunc("POST /api/v1/runtime-quote", func(w http.ResponseWriter, r *http.Request) {
		scope, ok := r.Context().Value(authScopeKey{}).(authScope)
		if !ok || !runtimeRoleAllowed(scope.Role) || s.runtime == nil {
			problem(w, 403, "role_denied", "费用预览需要当前组织的创作权限")
			return
		}
		var in RuntimeQuoteInput
		if !decodeProject(w, r, &in) {
			return
		}
		if in.ModelConfigID == "" || in.ModelConfigVersion < 1 {
			problem(w, 400, "invalid_input", "请选择模型配置及其当前版本")
			return
		}
		capability := in.Operation
		feature := operationFeature(in.Operation, nil)
		switch in.Operation {
		case "script", "storyboard", "analysis":
			capability = "text"
		case "creative":
			var job CreativeJobInput
			if decodeQuotePayload(in.Input, &job) != nil {
				problem(w, 400, "invalid_input", "创作请求格式无效")
				return
			}
			capability = creativeRuntimeCapability(job.Kind)
			feature = operationFeature("creative", &job)
		case "alignment":
			capability = "asr"
			var alignment AlignmentInput
			if decodeQuotePayload(in.Input, &alignment) != nil {
				problem(w, 400, "invalid_input", "转写请求格式无效")
				return
			}
			if alignment.Mode == "transcription" {
				feature = "speech_transcription"
			}
		case "image":
		default:
			problem(w, 400, "invalid_input", "不支持此执行类型的费用预览")
			return
		}
		selection, entry, err := s.runtime.Resolve(r.Context(), scope.SpaceID, scope.UserID, in.ModelConfigID, capability)
		if err != nil || selection.Version != in.ModelConfigVersion {
			problem(w, 409, "model_config_conflict", "模型配置不可用或已更新，请重新选择")
			return
		}
		if (capability == "speech" && !configToken.MatchString(in.Voice)) || (capability != "speech" && in.Voice != "") {
			problem(w, 400, "invalid_input", "声音标识无效")
			return
		}
		selection.Voice = in.Voice
		if selection.GatewayID != "" || feature != "" {
			if _, allowed := s.runtime.authorizeFeature(scope.SpaceID, feature, in.ModelConfigID); !allowed {
				problem(w, 403, "feature_model_denied", "此功能未绑定该模型或不允许切换")
				return
			}
			if selection.GatewayID != "" {
				selection.FeatureKey = feature
			}
		}
		if !s.execution.Ready() {
			problem(w, 503, "provider_not_ready", "费用与内容审核治理未就绪，不能执行远端模型")
			return
		}
		request, err := s.buildQuoteExecutionRequest(scope.SpaceID, in, selection, entry)
		if err != nil {
			storeError(w, err)
			return
		}
		quote, err := s.execution.Quoter.QuoteExecution(r.Context(), request)
		units, ce := CurrencyMinorUnits(quote.Currency)
		if err != nil || ce != nil || quote.MaximumMinor <= 0 || quote.MaximumMinor > 9007199254740991 || quote.Evidence == "" || !quote.ExpiresAt.After(time.Now()) {
			problem(w, 409, "quote_unavailable", "当前请求没有匹配且有效的可信报价，不能提交远端执行")
			return
		}
		respond(w, 200, RuntimeRequestQuote{RuntimeQuotePreview: RuntimeQuotePreview{Currency: quote.Currency, MaximumMinor: quote.MaximumMinor, MinorUnits: units, Source: quote.Evidence, ExpiresAt: quote.ExpiresAt, Contract: "studio-request-v1"}, RequestDigest: ExecutionRequestDigest(request), ModelConfigID: selection.ModelID, ModelConfigVersion: selection.Version})
	})
}
func (s *FileStore) buildQuoteExecutionRequest(tenant string, in RuntimeQuoteInput, selection RuntimeSelection, entry ModelConfigEntry) (ExecutionRequest, error) {
	if in.Operation == "image" {
		var input Input
		if decodeQuotePayload(in.Input, &input) != nil {
			return ExecutionRequest{}, ErrInvalidProjectInput
		}
		if input.ModelRequest != (ModelRequest{}) {
			return ExecutionRequest{}, ErrInvalidProjectInput
		}
		input, err := Normalize(input)
		if err != nil {
			return ExecutionRequest{}, ErrInvalidProjectInput
		}
		return imageExecutionRequest(Task{SpaceID: tenant, Provider: entry.Provider, Model: entry.Model, Input: input, RuntimeSelection: &selection}), nil
	}
	project, err := s.GetProject(tenant, in.ProjectID)
	if err != nil {
		return ExecutionRequest{}, err
	}
	if in.Operation == "script" || in.Operation == "storyboard" {
		var input ScriptGenerationInput
		if decodeQuotePayload(in.Input, &input) != nil || input.ModelRequest != (ModelRequest{}) {
			return ExecutionRequest{}, ErrInvalidProjectInput
		}
		if input.ExpectedVersion != project.Version {
			return ExecutionRequest{}, ErrVersionConflict
		}
		request := ExecutionRequest{Tenant: tenant, Provider: entry.Provider, Model: entry.Model, Capability: "text", Selection: &selection}
		if in.Operation == "script" {
			request.Text = project.Title + "\n" + project.Brief
		} else {
			if project.Script == nil {
				return ExecutionRequest{}, ErrScriptMissing
			}
			raw, e := json.Marshal(project.Script)
			if e != nil {
				return ExecutionRequest{}, e
			}
			request.Text = string(raw)
		}
		return request, nil
	}
	if in.Operation == "creative" || in.Operation == "analysis" {
		var input CreativeJobInput
		if in.Operation == "analysis" {
			var generation ScriptGenerationInput
			if decodeQuotePayload(in.Input, &generation) != nil || generation.ModelRequest != (ModelRequest{}) {
				return ExecutionRequest{}, ErrInvalidProjectInput
			}
			input = CreativeJobInput{Kind: "analysis", ExpectedVersion: generation.ExpectedVersion}
		} else if decodeQuotePayload(in.Input, &input) != nil || input.ModelRequest != (ModelRequest{}) {
			return ExecutionRequest{}, ErrInvalidProjectInput
		}
		if input.ExpectedVersion != project.Version {
			return ExecutionRequest{}, ErrVersionConflict
		}
		if input.Kind != "analysis" {
			input.Prompt, err = normalizeProjectText(input.Prompt, "创作提示", 3, 2000)
			if err != nil {
				return ExecutionRequest{}, err
			}
		}
		if err := s.validateCreativeControls(project.ID, tenant, input, Capabilities{Provider: entry.Provider, Model: entry.Model}); err != nil {
			return ExecutionRequest{}, err
		}
		job := CreativeJob{ReferenceAssetIDs: input.ReferenceAssetIDs, FirstFrameAssetID: input.FirstFrameAssetID, LastFrameAssetID: input.LastFrameAssetID, MotionReferenceAssetID: input.MotionReferenceAssetID, SpaceID: tenant, ProjectID: project.ID, Provider: entry.Provider, Model: entry.Model, Kind: input.Kind, Prompt: input.Prompt, ShotID: input.ShotID, Character: input.Character, ReferenceAssetID: input.ReferenceAssetID, ComparisonAssetID: input.ComparisonAssetID, MaskAssetID: input.MaskAssetID, AudioAssetID: input.AudioAssetID, Source: ProjectInput{Title: project.Title, Brief: project.Brief}, RuntimeSelection: &selection}
		return s.creativeExecutionRequest(job)
	}
	if in.Operation == "alignment" {
		var input AlignmentInput
		if decodeQuotePayload(in.Input, &input) != nil || input.EpisodeNumber < 0 || input.EpisodeNumber > 999 || input.ModelRequest != (ModelRequest{}) || !validRuntimeAlignmentMode(input.Mode, entry.Model, &selection) || strings.TrimSpace(input.Text) != "" {
			return ExecutionRequest{}, ErrInvalidProjectInput
		}
		media := s.media.Get(project.ID, tenant)
		if media.Version != input.ExpectedVersion {
			return ExecutionRequest{}, ErrVersionConflict
		}
		for _, clip := range timelineForEpisode(media, input.EpisodeNumber).Clips {
			if clip.ID != input.ClipID {
				continue
			}
			if input.AudioAssetID == "" || input.AudioAssetID != clip.AudioAssetID {
				return ExecutionRequest{}, ErrInvalidProjectInput
			}
			asset, ok := mediaAsset(media, clip.AudioAssetID)
			if !ok || asset.Kind != "audio" || (input.Mode == "transcription" && asset.Duration > 30) {
				return ExecutionRequest{}, ErrInvalidProjectInput
			}
			path, mime, e := s.media.file(project.ID, tenant, asset.ID, false, false)
			if e != nil {
				return ExecutionRequest{}, e
			}
			f, e := os.Open(path)
			if e != nil {
				return ExecutionRequest{}, e
			}
			data, e := io.ReadAll(io.LimitReader(f, (24<<20)+1))
			_ = f.Close()
			if e != nil || len(data) == 0 || len(data) > 24<<20 {
				return ExecutionRequest{}, ErrInvalidProjectInput
			}
			task := AlignmentTask{EpisodeNumber: input.EpisodeNumber, SpaceID: tenant, ProjectID: project.ID, Provider: entry.Provider, Model: entry.Model, Mode: input.Mode, ClipID: clip.ID, AudioAssetID: asset.ID, AudioDuration: asset.Duration, Duration: clip.Duration, TrimStart: clip.TrimStart, RuntimeSelection: &selection}
			return alignmentExecutionRequest(task, data, mime), nil
		}
		return ExecutionRequest{}, ErrNotFound
	}
	return ExecutionRequest{}, ErrInvalidProjectInput
}

func validateModelPreview(w http.ResponseWriter, r *http.Request, repo any, model ModelRequest, operation, projectID string, body any) bool {
	if model.selection == nil {
		return true
	}
	s, ok := repo.(*FileStore)
	if !ok {
		problem(w, 503, "provider_not_ready", "运行时不可用")
		return false
	}
	switch v := body.(type) {
	case Input:
		v.ModelRequest = ModelRequest{}
		body = v
	case ScriptGenerationInput:
		v.ModelRequest = ModelRequest{}
		body = v
	case CreativeJobInput:
		v.ModelRequest = ModelRequest{}
		body = v
	case AlignmentInput:
		v.ModelRequest = ModelRequest{}
		body = v
	default:
		problem(w, 400, "invalid_input", "费用预览请求类型无效")
		return false
	}
	raw, err := json.Marshal(body)
	if err != nil {
		problem(w, 400, "invalid_input", "费用预览请求无效")
		return false
	}
	request, err := s.buildQuoteExecutionRequest(model.selection.TenantID, RuntimeQuoteInput{Operation: operation, ProjectID: projectID, Input: raw}, *model.selection, ModelConfigEntry{Provider: model.selectedCaps.Provider, Model: model.selectedCaps.Model})
	if err != nil {
		storeError(w, err)
		return false
	}
	if model.QuoteRequestDigest != ExecutionRequestDigest(request) {
		problem(w, 409, "quote_changed", "请求内容已改变，请重新获取并确认费用报价")
		return false
	}
	return true
}
