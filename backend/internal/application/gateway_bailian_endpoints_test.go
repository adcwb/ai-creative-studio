package application

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// Go through saved vendor selection and runtime transports, not just adapter
// constructors. Synthetic rejections verify dispatch without generating media.
func TestBailianRuntimeEndpointMatrix(t *testing.T) {
	for _, prefix := range []string{"", "/proxy"} {
		for _, spec := range []struct {
			name, capability, protocol, method, path string
			count                                    bool
		}{
			{"chat", "text", "chat_completions", "POST", "/compatible-mode/v1/chat/completions", false},
			{"responses", "text", "responses", "POST", "/compatible-mode/v1/responses", false},
			{"explicit_input_count", "text", "responses", "POST", "/compatible-mode/v1/responses/input_tokens", true},
			{"vision", "vision", "", "POST", "/api/v1/services/aigc/multimodal-generation/generation", false},
			{"image", "image", "", "POST", "/api/v1/services/aigc/multimodal-generation/generation", false},
			{"speech", "speech", "", "POST", "/api/v1/services/audio/tts/SpeechSynthesizer", false},
			{"asr", "asr", "", "POST", "/api/v1/services/aigc/multimodal-generation/generation", false},
			{"video_submit", "video", "", "POST", "/api/v1/services/aigc/video-generation/video-synthesis", false},
			{"video_poll", "video", "", "GET", "/api/v1/tasks/synthetic-task", false},
		} {
			t.Run(prefix+"/"+spec.name, func(t *testing.T) {
				calls := 0
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls++
					if r.Method != spec.method || r.URL.Path != prefix+spec.path || r.URL.RawQuery != "" || r.Header.Get("Authorization") != "Bearer synthetic-gateway-key" {
						t.Error("incorrect vendor endpoint", r.Method, r.URL)
					}
					async := ""
					if spec.name == "video_submit" {
						async = "enable"
					}
					if r.Header.Get("X-DashScope-Async") != async {
						t.Error("async header used for wrong operation")
					}
					io.Copy(io.Discard, r.Body)
					w.WriteHeader(400)
					fmt.Fprint(w, `{"code":"InvalidParameter"}`)
				}))
				defer srv.Close()
				u, _ := url.Parse(srv.URL)
				t.Setenv("MODEL_GATEWAY_ALLOWED_HOSTS", u.Host)
				runtime, g, m := gatewayFixture(t, srv.URL+prefix+"/api/v1")
				g.Vendor = gatewayBailian
				if _, err := runtime.auth.models.saveGateway("tenant-a", g.Version, g, ModelCredentialInput{Action: "keep"}); err != nil {
					t.Fatal(err)
				}
				m.Capabilities = []string{spec.capability}
				m.TextProtocol, m.NativeInputCounting = spec.protocol, spec.count
				m.VideoSize, m.VideoSeconds = "1280x720", 4
				if spec.count {
					m.ContextWindowTokens = 8192
				}
				if _, err := runtime.auth.models.saveGatewayModel("tenant-a", m.Version, m); err != nil {
					t.Fatal(err)
				}
				q, _, err := runtime.Resolve(context.Background(), "tenant-a", "actor-a", m.ID, spec.capability)
				if err != nil {
					t.Fatal(err)
				}
				q.FeatureKey = "speech_transcription"
				err = runtime.Execute(context.Background(), q, func(ctx context.Context, p RuntimeProviders) error {
					switch spec.capability {
					case "text":
						_, err = p.Creative.Analyze(ctx, ProjectInput{Title: "Synthetic", Brief: "Synthetic test description"})
					case "vision":
						_, err = p.Vision.DescribeImage(ctx, "describe", syntheticPNG(0), "image/png")
					case "image":
						_, err = p.Image.Generate(ctx, Input{Kind: "general", Prompt: "synthetic landscape", Ratio: "1:1"})
					case "speech":
						_, err = p.Creative.Speech(ctx, "synthetic speech")
					case "asr":
						_, err = p.Alignment.Transcribe(ctx, []byte("synthetic-audio"), "audio/wav", 1)
					case "video":
						if spec.name == "video_poll" {
							_, err = p.Creative.PollVideo(ctx, "synthetic-task")
						} else {
							_, err = p.Creative.SubmitVideo(ctx, "synthetic video")
						}
					}
					return err
				})
				wantCode := "provider_rejected"
				if spec.count {
					wantCode = "provider_input_count_unavailable"
				}
				var failure *ProviderError
				if !errors.As(err, &failure) || failure.Code != wantCode || calls != 1 {
					t.Fatal("dispatch failed or retried", err, calls)
				}
			})
		}
	}
}

func TestBailianVideoRecoveryUsesOnlyAcknowledgedNativeTask(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "GET" || r.URL.Path != "/proxy/api/v1/tasks/synthetic-task" {
			t.Error("recovery escaped acknowledged task", r.Method, r.URL)
		}
		fmt.Fprint(w, `{"output":{"task_id":"synthetic-task","task_status":"RUNNING"}}`)
	}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	t.Setenv("MODEL_GATEWAY_ALLOWED_HOSTS", u.Host)
	runtime, g, m := gatewayFixture(t, srv.URL+"/proxy/api/v1")
	g.Vendor = gatewayBailian
	if _, err := runtime.auth.models.saveGateway("tenant-a", g.Version, g, ModelCredentialInput{Action: "keep"}); err != nil {
		t.Fatal(err)
	}
	m.Capabilities, m.VideoSize, m.VideoSeconds = []string{"video"}, "1280x720", 4
	if _, err := runtime.auth.models.saveGatewayModel("tenant-a", m.Version, m); err != nil {
		t.Fatal(err)
	}
	ctx := runtimeRecoveryContext("synthetic-task")
	q, _, err := runtime.Resolve(ctx, "tenant-a", "actor-a", m.ID, "video")
	if err != nil {
		t.Fatal(err)
	}
	err = runtime.Execute(ctx, q, func(ctx context.Context, p RuntimeProviders) error {
		job, err := p.Creative.PollVideo(ctx, "synthetic-task")
		if err != nil || job.Status != "in_progress" {
			t.Fatal("native recovery blocked", job, err)
		}
		if _, err := p.Creative.PollVideo(ctx, "another-task"); err == nil {
			t.Fatal("recovery queried another task")
		}
		if _, err := p.Creative.SubmitVideo(ctx, "synthetic video"); err == nil {
			t.Fatal("recovery created another generation")
		}
		api := p.Creative.(runtimeCreative).p.(*bailianCreativeProvider).wire.api
		for _, path := range []string{"/videos/synthetic-task", "/tasks/another-task", "/tasks/synthetic-task?extra=1"} {
			req, _ := http.NewRequestWithContext(ctx, "GET", api.endpoint+path, nil)
			if res, err := api.client.Do(req); err == nil {
				res.Body.Close()
				t.Fatal("transport recovery grant too broad", path)
			}
		}
		return nil
	})
	if err != nil || calls != 1 {
		t.Fatal("recovery failed or sent extra requests", err, calls)
	}
}
