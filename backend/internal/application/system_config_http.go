package application

import (
	"errors"
	"github.com/adcwb/ai-creative-studio/backend/internal/platform/deployconfig"
	"net/http"
)

// BuildRevision is injected by the image build; local builds report unknown.
var BuildRevision = "unknown"

func serveSystemConfig(w http.ResponseWriter, r *http.Request, s *deployconfig.Store) {
	var out deployconfig.Report
	var err error
	switch r.Method {
	case "GET":
		out, err = s.Report()
	case "PATCH":
		var in struct {
			ExpectedVersion string             `json:"expected_version"`
			Changes         map[string]*string `json:"changes"`
		}
		if !decode(w, r, &in) {
			return
		}
		out, err = s.Save(in.ExpectedVersion, in.Changes)
	default:
		problem(w, 405, "method_not_allowed", "方法不支持")
		return
	}
	switch {
	case errors.Is(err, deployconfig.ErrConflict):
		problem(w, 409, "config_conflict", "配置已更新，请重新读取后核对")
	case errors.Is(err, deployconfig.ErrInput):
		problem(w, 400, "invalid_config", "配置项、值或操作无效")
	case err != nil:
		problem(w, 503, "config_storage_error", "配置保存未确认，请重新读取后核对")
	default:
		out.BuildRevision = BuildRevision
		respond(w, 200, out)
	}
}
