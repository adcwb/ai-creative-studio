package application

import (
	"net/http"
	"strings"
)

func gatewayManagementPath(path string) bool {
	for _, p := range []string{"/api/v1/model-management", "/api/v1/ai-gateways", "/api/v1/gateway-models", "/api/v1/model-features", "/api/v1/model-usage"} {
		if path == p || strings.HasPrefix(path, p+"/") {
			return true
		}
	}
	return false
}
func (s *ModelConfigStore) serveGatewayManagement(w http.ResponseWriter, r *http.Request, scope authScope) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/")
	if path == "model-usage" || path == "model-usage/calls" {
		if r.Method != "GET" {
			problem(w, 405, "method_not_allowed", "方法不支持")
			return
		}
		out, e := s.usage.report(scope.SpaceID, r)
		if e != nil {
			modelConfigError(w, e)
			return
		}
		respond(w, 200, out)
		return
	}
	if scope.Role != "owner" && scope.Role != "admin" {
		problem(w, 403, "role_denied", "仅空间管理员可管理模型")
		return
	}
	if r.Method == "GET" && (path == "model-management" || path == "model-features") {
		out, e := s.managementReport(scope.SpaceID)
		if e != nil {
			modelConfigError(w, e)
			return
		}
		respond(w, 200, out)
		return
	}
	var out ModelManagementReport
	var e error
	switch {
	case path == "ai-gateways" && r.Method == "POST" || strings.HasPrefix(path, "ai-gateways/") && r.Method == "PATCH":
		var in struct {
			ExpectedVersion int64                `json:"expected_version"`
			Gateway         AIGateway            `json:"gateway"`
			Credential      ModelCredentialInput `json:"credential"`
		}
		if !decode(w, r, &in) {
			return
		}
		if r.Method == "PATCH" {
			id := strings.TrimPrefix(path, "ai-gateways/")
			if strings.Contains(id, "/") || in.Gateway.ID != "" && in.Gateway.ID != id {
				modelConfigError(w, errModelInput)
				return
			}
			in.Gateway.ID = id
		} else if in.Gateway.ID != "" {
			modelConfigError(w, errModelInput)
			return
		}
		out, e = s.saveGateway(scope.SpaceID, in.ExpectedVersion, in.Gateway, in.Credential)
	case strings.HasSuffix(path, "/check") && strings.HasPrefix(path, "ai-gateways/") && r.Method == "POST":
		id := strings.TrimSuffix(strings.TrimPrefix(path, "ai-gateways/"), "/check")
		var in struct {
			ExpectedVersion int64 `json:"expected_version"`
		}
		if !decode(w, r, &in) {
			return
		}
		result, e := s.checkGateway(r.Context(), scope.SpaceID, id, in.ExpectedVersion)
		if e != nil {
			modelConfigError(w, e)
			return
		}
		respond(w, 200, result)
		return
	case path == "gateway-models" && r.Method == "POST" || strings.HasPrefix(path, "gateway-models/") && r.Method == "PATCH":
		var in struct {
			ExpectedVersion int64        `json:"expected_version"`
			Model           GatewayModel `json:"model"`
		}
		if !decode(w, r, &in) {
			return
		}
		if r.Method == "PATCH" {
			id := strings.TrimPrefix(path, "gateway-models/")
			if strings.Contains(id, "/") || in.Model.ID != "" && in.Model.ID != id {
				modelConfigError(w, errModelInput)
				return
			}
			in.Model.ID = id
		} else if in.Model.ID != "" {
			modelConfigError(w, errModelInput)
			return
		}
		out, e = s.saveGatewayModel(scope.SpaceID, in.ExpectedVersion, in.Model)
	case strings.HasPrefix(path, "model-features/") && r.Method == "PUT":
		var in struct {
			ExpectedVersion int64          `json:"expected_version"`
			Binding         FeatureBinding `json:"binding"`
		}
		if !decode(w, r, &in) {
			return
		}
		key := strings.TrimPrefix(path, "model-features/")
		if in.Binding.FeatureKey != "" && in.Binding.FeatureKey != key {
			modelConfigError(w, errModelInput)
			return
		}
		in.Binding.FeatureKey = key
		out, e = s.saveFeatureBinding(scope.SpaceID, scope.UserID, in.ExpectedVersion, in.Binding)
	default:
		problem(w, 405, "method_not_allowed", "方法不支持")
		return
	}
	if e != nil {
		modelConfigError(w, e)
		return
	}
	respond(w, 200, out)
}
