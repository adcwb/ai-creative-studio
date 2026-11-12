package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// These assertions follow the public Token Plan and Wan 2.7 HTTP contracts.
// A loopback response proves serialization/routing, never account entitlement.
func TestBailianWanImageRequestContractAllRatios(t *testing.T) {
	for _, model := range []string{"wan2.7-image", "wan2.7-image-pro"} {
		for _, spec := range []struct{ ratio, size string }{{"1:1", "1024*1024"}, {"16:9", "1536*864"}, {"9:16", "864*1536"}, {"4:5", "1024*1280"}} {
			t.Run(model+"/"+spec.ratio, func(t *testing.T) {
				calls, downloads := 0, 0
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls++
					if r.Method != http.MethodPost || r.URL.Path != "/api/v1"+bailianMultimodal || r.Header.Get("Content-Type") != "application/json" || r.Header.Get("Authorization") != "Bearer synthetic" || r.Header.Get("X-DashScope-Async") != "" || r.ContentLength <= 0 {
						t.Error("incorrect synchronous native image request")
					}
					var body struct {
						Model string `json:"model"`
						Input struct {
							Messages []struct {
								Role    string `json:"role"`
								Content []struct {
									Text string `json:"text"`
								} `json:"content"`
							} `json:"messages"`
						} `json:"input"`
						Parameters struct {
							Size string `json:"size"`
							N    int    `json:"n"`
						} `json:"parameters"`
					}
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					if body.Model != model || body.Parameters.Size != spec.size || body.Parameters.N != 1 || len(body.Input.Messages) != 1 || body.Input.Messages[0].Role != "user" || len(body.Input.Messages[0].Content) != 1 || !strings.Contains(body.Input.Messages[0].Content[0].Text, "synthetic landscape") {
						t.Error("native image payload differs from the documented contract")
					}
					fmt.Fprint(w, `{"output":{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":[{"type":"image","image":"https://cdn.synthetic.invalid/result.png"}]}}],"finished":true},"request_id":"synthetic-wan-request"}`)
				}))
				defer srv.Close()
				p := bailianImageProvider{wire: &bailianAPI{api: newCreativeProvider("synthetic", srv.URL+"/api/v1", srv.Client()), model: GatewayModel{Model: model, Capabilities: []string{"image"}}}, images: gatewayImageProvider{downloadTo: func(_ context.Context, target string, out io.Writer) error {
					downloads++
					if target != "https://cdn.synthetic.invalid/result.png" {
						t.Error("unexpected result URL")
					}
					_, err := out.Write(syntheticPNG(0))
					return err
				}}}
				result, err := p.Generate(context.Background(), Input{Kind: "general", Prompt: "synthetic landscape", Ratio: spec.ratio})
				if err != nil || result.Width < 1 || calls != 1 || downloads != 1 {
					t.Fatal("single native generation/download contract failed", err, calls, downloads)
				}
			})
		}
	}
}

func TestBailianNativeRejectionRetainsStatusAndRequestIDWithoutPrivateBody(t *testing.T) {
	for _, spec := range []struct {
		status int
		code   string
	}{{200, "provider_rejected"}, {400, "provider_rejected"}, {404, "provider_rejected"}, {413, "provider_rejected"}, {422, "provider_rejected"}, {401, "provider_auth_failed"}, {403, "provider_auth_failed"}, {429, "provider_rate_limited"}, {500, "provider_error"}} {
		t.Run(fmt.Sprint(spec.status), func(t *testing.T) {
			calls := 0
			const privateBody = "synthetic-private-prompt-and-credential-echo"
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != http.MethodPost || r.URL.Path != "/api/v1"+bailianMultimodal {
					t.Error("runtime used an incompatible or fallback route")
				}
				io.Copy(io.Discard, r.Body)
				w.WriteHeader(spec.status)
				fmt.Fprintf(w, `{"code":"InvalidParameter","message":%q,"request_id":"synthetic-rejection-id"}`, privateBody)
			}))
			defer srv.Close()
			u, _ := url.Parse(srv.URL)
			t.Setenv("MODEL_GATEWAY_ALLOWED_HOSTS", u.Host)
			runtime, g, m := gatewayFixture(t, srv.URL+"/api/v1")
			g.Vendor = gatewayBailian
			if _, err := runtime.auth.models.saveGateway("tenant-a", g.Version, g, ModelCredentialInput{Action: "keep"}); err != nil {
				t.Fatal(err)
			}
			m.Model, m.Capabilities = "wan2.7-image", []string{"image"}
			if _, err := runtime.auth.models.saveGatewayModel("tenant-a", m.Version, m); err != nil {
				t.Fatal(err)
			}
			q, _, err := runtime.Resolve(context.Background(), "tenant-a", "actor-a", m.ID, "image")
			if err != nil {
				t.Fatal(err)
			}
			err = runtime.Execute(context.Background(), q, func(ctx context.Context, providers RuntimeProviders) error {
				_, err := providers.Image.Generate(ctx, Input{Kind: "general", Prompt: "synthetic image", Ratio: "1:1"})
				return err
			})
			var failure *ProviderError
			if !errors.As(err, &failure) || failure.Code != spec.code || strings.Contains(err.Error(), privateBody) || calls != 1 {
				t.Fatal("incorrect failure mapping or a retry", err, calls)
			}
			report, err := runtime.auth.models.usage.report("tenant-a", httptest.NewRequest(http.MethodGet, "/model-usage/calls", nil))
			if err != nil || len(report.Items) != 1 {
				t.Fatal("missing diagnostic call", err)
			}
			call := report.Items[0]
			if call.HTTPStatus != spec.status || call.RequestID != "synthetic-rejection-id" || call.ErrorCode != spec.code || call.HTTPCalls != 1 || call.State != "failed" {
				t.Fatal("lost bounded rejection diagnostics", call)
			}
			raw, _ := json.Marshal(report)
			if strings.Contains(string(raw), privateBody) || strings.Contains(string(raw), "synthetic-gateway-key") {
				t.Fatal("private provider content leaked into usage")
			}
			other, err := runtime.auth.models.usage.report("tenant-b", httptest.NewRequest(http.MethodGet, "/model-usage/calls", nil))
			if err != nil || len(other.Items) != 0 {
				t.Fatal("cross-tenant diagnostic disclosure", err)
			}
		})
	}
}

func TestBailianCompatibleVendorMisconfigurationReproducesRejectedImageRoute(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/api/v1/images/generations" {
			t.Error("unexpected route for the OpenAI-compatible vendor", r.URL.Path)
		}
		io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"code":"NotFound","request_id":"synthetic-wrong-route"}`)
	}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	t.Setenv("MODEL_GATEWAY_ALLOWED_HOSTS", u.Host)
	runtime, _, m := gatewayFixture(t, srv.URL+"/api/v1")
	// A gateway name or a model name never changes the stored vendor protocol.
	m.Model, m.Capabilities = "wan2.7-image", []string{"image"}
	if _, err := runtime.auth.models.saveGatewayModel("tenant-a", m.Version, m); err != nil {
		t.Fatal(err)
	}
	q, _, err := runtime.Resolve(context.Background(), "tenant-a", "actor-a", m.ID, "image")
	if err != nil {
		t.Fatal(err)
	}
	err = runtime.Execute(context.Background(), q, func(ctx context.Context, providers RuntimeProviders) error {
		_, err := providers.Image.Generate(ctx, Input{Kind: "general", Prompt: "synthetic image", Ratio: "1:1"})
		return err
	})
	var failure *ProviderError
	if !errors.As(err, &failure) || failure.Code != "provider_rejected" || calls != 1 {
		t.Fatal("incompatible route did not reproduce the reported label", err, calls)
	}
}
