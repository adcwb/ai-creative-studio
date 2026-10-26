package studio

import (
	"context"
	"errors"
	"github.com/adcwb/ai-creative-studio/backend/internal/tracing"
	"net/http"
	"regexp"
)

var executionDigestPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

// ModelRequest contains only an explicit, reviewable model choice. Identity and
// effective provider settings are derived on the server, never from JSON.
// An omitted choice retains the existing explicitly configured mock/local mode;
// merely saving a default model must not silently start spending money.
type ModelRequest struct {
	traceParent        string
	QuoteRequestDigest string `json:"quote_request_sha256,omitempty"`
	ModelConfigID      string `json:"model_config_id,omitempty"`
	ModelConfigVersion int64  `json:"model_config_version,omitempty"`
	Voice              string `json:"voice,omitempty"`
	BudgetCurrency     string `json:"budget_currency,omitempty"`
	BudgetMaximumMinor int64  `json:"budget_maximum_minor,omitempty"`
	selection          *RuntimeSelection
	selectedCaps       Capabilities
}

func cloneRuntimeSelection(s *RuntimeSelection) *RuntimeSelection {
	if s == nil {
		return nil
	}
	c := *s
	return &c
}

func creativeRuntimeCapability(kind string) string {
	switch kind {
	case "analysis":
		return "text"
	case "consistency":
		return "vision"
	default:
		return kind
	}
}

func resolveModelRequest(w http.ResponseWriter, r *http.Request, repo any, in *ModelRequest, capability string) bool {
	in.traceParent = tracing.Parent(r.Context())
	if in.ModelConfigID == "" && in.ModelConfigVersion == 0 && in.Voice == "" && in.BudgetCurrency == "" && in.BudgetMaximumMinor == 0 && in.QuoteRequestDigest == "" {
		return true
	}
	if in.ModelConfigID == "" || in.ModelConfigVersion < 1 || (capability == "speech" && !configToken.MatchString(in.Voice)) || len(in.Voice) > 128 || (in.Voice != "" && capability != "speech") {
		problem(w, 400, "invalid_input", "请选择模型配置及其当前版本；声音仅适用于语音生成")
		return false
	}
	s, ok := repo.(*FileStore)
	scope, authenticated := r.Context().Value(authScopeKey{}).(authScope)
	if !ok || !authenticated || scope.UserID == "" || scope.SpaceID == "" || s.runtime == nil {
		problem(w, 403, "role_denied", "模型执行需要已授权的组织身份")
		return false
	}
	selection, entry, err := s.runtime.Resolve(r.Context(), scope.SpaceID, scope.UserID, in.ModelConfigID, capability)
	if err != nil {
		if errors.Is(err, errRuntimeDenied) {
			problem(w, 403, "role_denied", "模型执行权限不足")
		} else {
			problem(w, 409, "model_config_conflict", "模型配置不可用，请刷新后选择")
		}
		return false
	}
	if selection.Version != in.ModelConfigVersion {
		problem(w, 409, "model_config_conflict", "模型配置已更新，请刷新后确认")
		return false
	}
	if s.execution == nil || !s.execution.Ready() {
		problem(w, 503, "provider_not_ready", "费用与内容审核治理未就绪，不能执行远端模型")
		return false
	}
	if _, err := CurrencyMinorUnits(in.BudgetCurrency); err != nil || in.BudgetMaximumMinor <= 0 || in.BudgetMaximumMinor > 9007199254740991 {
		problem(w, 400, "invalid_input", "必须确认本次执行的币种与整数最小单位费用上限")
		return false
	}
	if !executionDigestPattern.MatchString(in.QuoteRequestDigest) {
		problem(w, 400, "invalid_input", "请先获取并确认当前请求的费用报价")
		return false
	}
	selection.RequestDigest = in.QuoteRequestDigest
	selection.Currency, selection.MaximumMinor = in.BudgetCurrency, in.BudgetMaximumMinor
	selection.Voice = in.Voice
	in.selection = &selection
	in.selectedCaps = Capabilities{Provider: entry.Provider, Model: entry.Model, Mode: "real", Ready: true}
	return true
}

func (s *FileStore) guardModelRequestLocked(in ModelRequest, provider, model string) error {
	if in.selection == nil {
		return s.guardCreativeLocked(provider, model)
	}
	// The model configuration ID and version are already an explicit confirmation;
	// legacy provider/model confirmations, when present, must still agree.
	if (provider != "" && provider != in.selectedCaps.Provider) || (model != "" && model != in.selectedCaps.Model) {
		return ErrProviderChanged
	}
	return s.guardCreativeLocked(in.selectedCaps.Provider, in.selectedCaps.Model, in.selectedCaps)
}

func runtimeImageReferenceSupported(provider string) bool {
	switch provider {
	case "openai-images", "gemini-images", "gemini-interactions-images", "ark-images", "minimax-images":
		return true
	default:
		return false
	}
}

// ConfigureManagedRuntime installs the same fresh authorization boundary in API
// and independent worker processes. It performs no credential lookup or calls.
func ConfigureManagedRuntime(s *FileStore, auth *AuthStore) {
	s.runtime = NewModelRuntime(auth, func(ctx context.Context, selection RuntimeSelection) error {
		if s.execution == nil {
			return errRuntimeDenied
		}
		return s.execution.AuthorizeRuntime(ctx, selection)
	})
	s.runtime.musicEntitled = func(tenant string, version int64, entry ModelConfigEntry) bool {
		if s.execution == nil {
			return false
		}
		policy, ok := s.execution.Quoter.(*ExecutionPolicy)
		return ok && policy.MusicEntitled(tenant, version, entry)
	}
	s.runtime.referenceBridge = func(selection RuntimeSelection, model string) ReferenceObjectBridge {
		if s.execution == nil {
			return nil
		}
		policy, ok := s.execution.Quoter.(*ExecutionPolicy)
		if !ok {
			return nil
		}
		return policy.referenceBridgeFor(selection, model)
	}
	s.runtime.BindReadiness(func() bool { return s.execution != nil && s.execution.Ready() })
	if s.media != nil && s.media.alignment != nil {
		s.media.alignment.runtime = s.runtime
	}
}
