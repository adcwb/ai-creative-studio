package studio

import (
	"errors"
	"net/http"
)

func modelConfigError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errModelConflict):
		problem(w, 409, "model_config_conflict", "配置已更新，请刷新后重试")
	case errors.Is(err, ErrNotFound):
		problem(w, 404, "not_found", "模型配置不存在")
	case errors.Is(err, errModelInput):
		problem(w, 400, "invalid_model_config", "模型、能力、区域、凭据动作或生命周期配置无效")
	case errors.Is(err, errModelKey):
		problem(w, 503, "model_key_unavailable", "部署未配置凭据加密密钥，不能保存明文凭据；可使用受限引用")
	default:
		problem(w, 503, "model_config_storage_error", "配置保存结果不确定，请刷新核对版本，勿重新提交凭据")
	}
}
func (s *ModelConfigStore) serve(w http.ResponseWriter, r *http.Request, scope authScope) {
	if scope.Role != "owner" && scope.Role != "admin" {
		problem(w, 403, "role_denied", "仅空间管理员可管理模型接入")
		return
	}
	if r.URL.Path == "/api/v1/model-config" && r.Method == "GET" {
		report, err := s.report(scope.SpaceID)
		if err != nil {
			modelConfigError(w, err)
			return
		}
		respond(w, 200, report)
		return
	}
	if r.URL.Path == "/api/v1/model-config" && r.Method == "PUT" {
		var in struct {
			ExpectedVersion int64                `json:"expected_version"`
			Entry           ModelConfigInput     `json:"entry"`
			Credential      ModelCredentialInput `json:"credential"`
		}
		if !decode(w, r, &in) {
			return
		}
		report, err := s.update(scope.SpaceID, in.ExpectedVersion, in.Entry, in.Credential)
		if err != nil {
			modelConfigError(w, err)
			return
		}
		respond(w, 200, report)
		return
	}
	if r.URL.Path == "/api/v1/model-config/validate" && r.Method == "POST" {
		var in struct {
			ID string `json:"id"`
		}
		if !decode(w, r, &in) {
			return
		}
		result, err := s.validate(scope.SpaceID, in.ID)
		if err != nil {
			modelConfigError(w, err)
			return
		}
		respond(w, 200, result)
		return
	}
	if r.URL.Path == "/api/v1/model-config/live" {
		problem(w, 403, "live_not_authorized", "真实验证需要独立凭据、模型、数据及预算授权；此接口不执行模型调用")
		return
	}
	problem(w, 405, "method_not_allowed", "方法不支持")
}
