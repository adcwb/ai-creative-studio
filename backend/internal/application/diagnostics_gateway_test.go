package application

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/adcwb/ai-creative-studio/backend/internal/platform/diagnostics"
)

// Exercise the real auth short-circuit, which never reaches the main route mux.
func TestDiagnosticGatewaySyncIncludesRouteAndUpstream(t *testing.T) {
	for _, spec := range []struct{ name, body, code string }{
		{"json", `{"code":"NotFound","request_id":"f63f4568-9026-4b82-b076-05f92f92f711","message":"sk-private-token private-prompt"}`, "NotFound"},
		{"unknown_code", `{"error":{"code":"sk-private-token"},"message":"private-prompt"}`, diagnostics.SafeUpstreamCode("sk-private-token")},
		{"html", `<html>private-prompt sk-private-token</html>`, ""},
		{"oversized", `{"code":"NotFound","message":"` + strings.Repeat("private-prompt", 6000) + `"}`, ""},
	} {
		t.Run(spec.name, func(t *testing.T) {
			logs := diagnosticLogs(t)
			calls := 0
			const upstreamID = "f63f4568-9026-4b82-b076-05f92f92f711"
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != "GET" || r.URL.Path != "/compatible-mode/v1/models" || r.URL.RawQuery != "" {
					t.Error("incorrect catalog request")
				}
				w.Header().Set("X-DashScope-Request-ID", upstreamID)
				w.WriteHeader(404)
				fmt.Fprint(w, spec.body)
			}))
			defer srv.Close()
			u, _ := url.Parse(srv.URL)
			t.Setenv("MODEL_GATEWAY_ALLOWED_HOSTS", u.Host)
			dir := t.TempDir()
			auth, err := OpenAuthStore(dir)
			if err != nil {
				t.Fatal(err)
			}
			auth.models, err = OpenModelConfigStore(dir, bytes.Repeat([]byte{11}, 32))
			if err != nil {
				t.Fatal(err)
			}
			user, token, session, err := auth.login("synthetic@example.test", "synthetic-password-A", true)
			if err != nil {
				t.Fatal(err)
			}
			space := testTeamSpace(t, auth, user.ID)
			report, err := auth.models.saveGateway(space, 0, AIGateway{Vendor: gatewayBailian, Name: "synthetic", BaseURL: srv.URL + "/api/v1", Enabled: true, TimeoutSeconds: 30}, ModelCredentialInput{Action: "set", Secret: "sk-private-token"})
			if err != nil {
				t.Fatal(err)
			}
			g := report.Gateways[0]
			h := SecureHandler(testStore(t), "http://localhost:5173", auth, AuthOptions{})
			w := authRequest(t, h, "POST", "/api/v1/ai-gateways/"+g.ID+"/sync-models?space_id="+space+"&secret=private-prompt", fmt.Sprintf(`{"expected_version":%d}`, g.Version), token, session.CSRF, 502)
			var body struct {
				Error struct {
					Code string `json:"code"`
					diagnostics.Detail
				} `json:"error"`
			}
			if json.Unmarshal(w.Body.Bytes(), &body) != nil || body.Error.Code != "gateway_catalog_unavailable" || body.Error.Upstream == nil || body.Error.Upstream.HTTPStatus != 404 || body.Error.Upstream.Code != spec.code || body.Error.Upstream.RequestID != upstreamID || body.Error.CauseKnown || calls != 1 {
				t.Fatal("missing bounded failure metadata", w.Body.String(), calls)
			}
			upstreamEvents, httpEvents := 0, 0
			for _, line := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
				var event map[string]any
				if json.Unmarshal([]byte(line), &event) != nil {
					t.Fatal("invalid log JSON")
				}
				if event["event"] != "upstream_error" && event["event"] != "http_error" {
					continue
				}
				if event["correlation_id"] != body.Error.CorrelationID || event["upstream_http_status"] != float64(404) || event["upstream_error_code"] != spec.code || event["upstream_request_id"] != upstreamID || event["cause_known"] != false {
					t.Fatal("inconsistent log correlation or upstream metadata", event)
				}
				if event["event"] == "http_error" {
					httpEvents++
					if event["route"] != "/api/v1/ai-gateways/{id}/sync-models" || event["http_method"] != "POST" || event["http_status"] != float64(502) || event["request_id"] != body.Error.RequestID {
						t.Fatal("auth route was lost", event)
					}
				} else {
					upstreamEvents++
				}
			}
			if upstreamEvents != 1 || httpEvents != 1 || body.Error.CorrelationID == "" {
				t.Fatal("missing or duplicated error events", logs.String())
			}
			noPrivateDiagnostics(t, logs.String(), w.Body.String())
			if strings.Contains(logs.String(), g.ID) || strings.Contains(logs.String(), space) {
				t.Fatal("route logged resource identity")
			}
		})
	}
}

func TestDiagnosticGatewayRoutesBeforeAuthentication(t *testing.T) {
	logs := diagnosticLogs(t)
	auth, err := OpenAuthStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := SecureHandler(testStore(t), "http://localhost:5173", auth, AuthOptions{})
	for path, want := range map[string]string{
		"/api/v1/ai-gateways/private-url/check":       "/api/v1/ai-gateways/{id}/check",
		"/api/v1/ai-gateways/private-url/sync-models": "/api/v1/ai-gateways/{id}/sync-models",
		"/api/v1/ai-gateways/private-url":             "/api/v1/ai-gateways/{id}",
		"/api/v1/gateway-models/private-url":          "/api/v1/gateway-models/{id}",
		"/api/v1/model-features/private-url":          "/api/v1/model-features/{key}",
		"/api/v1/model-usage/calls":                   "/api/v1/model-usage/calls",
		"/api/v1/ai-gateways/private-url/unknown":     "unmatched",
	} {
		logs.Reset()
		authRequest(t, h, "GET", path+"?secret=sk-private-token", "", "", "", 401)
		var event map[string]any
		if json.Unmarshal(bytes.TrimSpace(logs.Bytes()), &event) != nil || event["route"] != want {
			t.Fatal("incorrect safe route", path, logs.String())
		}
		noPrivateDiagnostics(t, logs.String())
	}
}

func TestDiagnosticCompatibleTextErrorEnvelope(t *testing.T) {
	for _, status := range []int{401, 403, 404, 429, 500} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			logs := diagnosticLogs(t)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
				fmt.Fprint(w, `{"error":{"code":"invalid_request_error","message":"private-prompt sk-private-token"},"request_id":"f63f4568-9026-4b82-b076-05f92f92f711"}`)
			}))
			defer srv.Close()
			h := WithDiagnostics(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				p := newCreativeProvider("synthetic", srv.URL, srv.Client())
				if _, err := p.request(r.Context(), "POST", "/chat/completions", map[string]string{"model": "synthetic"}, 1024); err == nil {
					t.Fatal("upstream failure accepted")
				}
				problem(w, 502, "provider_error", "上游失败")
			}))
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest("POST", "/private-url", nil))
			if !strings.Contains(w.Body.String(), `"error_code":"invalid_request_error"`) || !strings.Contains(logs.String(), `"upstream_request_id":"f63f4568-9026-4b82-b076-05f92f92f711"`) {
				t.Fatal("text failure envelope discarded", w.Body.String())
			}
			noPrivateDiagnostics(t, w.Body.String(), logs.String())
		})
	}
}
