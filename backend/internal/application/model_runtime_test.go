package application

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type runtimeFixtureTransport func(*http.Request) (*http.Response, error)

func runtimeRecoveryContext(upstream string) context.Context {
	p := &executionPermit{upstream: upstream, active: true, expires: time.Now().Add(time.Minute)}
	return context.WithValue(context.WithValue(context.Background(), executionPermitKey{}, p), executionReadOnlyKey{}, true)
}

func (f runtimeFixtureTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func runtimeFixture(t *testing.T) (*ModelRuntime, RuntimeSelection, ModelConfigInput) {
	t.Helper()
	s, err := OpenModelConfigStore(t.TempDir(), bytes.Repeat([]byte{3}, 32))
	if err != nil {
		t.Fatal(err)
	}
	in := syntheticModel()
	report, err := s.update("tenant-a", 0, in, ModelCredentialInput{Action: "set", Secret: "synthetic-tenant-a-secret"})
	if err != nil {
		t.Fatal(err)
	}
	in.ID = report.Entries[0].ID
	auth := &AuthStore{models: s, data: authSnapshot{Organizations: map[string]AuthOrganization{"tenant-a": {Members: map[string]string{"actor-a": "editor"}}}}}
	r := NewModelRuntime(auth, func(context.Context, RuntimeSelection) error { return nil })
	selection, _, err := r.Resolve(context.Background(), "tenant-a", "actor-a", "", "text")
	if err != nil {
		t.Fatal(err)
	}
	return r, selection, in
}

func runtimeTestClient(p RuntimeProviders) *http.Client {
	return p.Creative.(runtimeCreative).p.(*OpenAICreativeProvider).client
}
func runtimeReplaceTransport(p RuntimeProviders, f runtimeFixtureTransport) {
	c := runtimeTestClient(p)
	transport := c.Transport.(runtimeTransport)
	transport.base = f
	c.Transport = transport
}

func TestModelRuntimeSelectionAndActualAdapterPayload(t *testing.T) {
	r, sel, _ := runtimeFixture(t)
	var calls int
	var retained CreativeProvider
	err := r.Execute(context.Background(), sel, func(ctx context.Context, p RuntimeProviders) error {
		retained = p.Creative
		runtimeReplaceTransport(p, func(req *http.Request) (*http.Response, error) {
			calls++
			if req.URL.String() != "https://api.openai.com/v1/chat/completions" || req.Header.Get("Authorization") != "Bearer synthetic-tenant-a-secret" {
				t.Fatal("wrong tenant credential or endpoint")
			}
			var body map[string]any
			if json.NewDecoder(req.Body).Decode(&body) != nil || body["model"] != "synthetic-model" {
				t.Fatal("saved model not applied")
			}
			content := `{"summary":"Synthetic test summary","questions":[],"suggestions":[]}`
			data, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]string{"content": content}}}})
			return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(data)), Header: http.Header{}}, nil
		})
		if _, err := p.Creative.Speech(ctx, "must not use text permission for speech"); !errors.Is(err, errRuntimeDenied) {
			t.Fatal("capability escalation")
		}
		_, err := p.Creative.Analyze(ctx, ProjectInput{Title: "Synthetic title", Brief: "Synthetic local-only description"})
		return err
	})
	if err != nil || calls != 1 {
		t.Fatal("configured provider execution failed", err, calls)
	}
	if _, err = retained.Analyze(context.Background(), ProjectInput{Title: "Synthetic title", Brief: "Synthetic local-only description"}); err == nil || calls != 1 {
		t.Fatal("retained adapter reused after permit lifetime")
	}
}

func TestModelRuntimeAuthorizationBeforeDecryption(t *testing.T) {
	r, sel, in := runtimeFixture(t)
	for _, mutate := range []func(*RuntimeSelection){func(q *RuntimeSelection) { q.ActorID = "stranger" }, func(q *RuntimeSelection) { q.TenantID = "tenant-b" }, func(q *RuntimeSelection) { q.Version++ }, func(q *RuntimeSelection) { q.Capability = "speech" }, func(q *RuntimeSelection) { q.ModelID = "other" }} {
		q := sel
		mutate(&q)
		if err := r.Execute(context.Background(), q, func(context.Context, RuntimeProviders) error { t.Fatal("unauthorized callback"); return nil }); err == nil {
			t.Fatal("invalid selection allowed")
		}
	}
	r.authorize = nil
	if err := r.Execute(context.Background(), sel, func(context.Context, RuntimeProviders) error { t.Fatal("missing governance allowed"); return nil }); err == nil {
		t.Fatal("missing governance allowed")
	}
	r.authorize = func(context.Context, RuntimeSelection) error { return nil }
	// A platform env name is never permission to borrow the platform's credential.
	if _, err := r.auth.models.update(sel.TenantID, sel.Version, in, ModelCredentialInput{Action: "reference", Reference: "OPENAI_API_KEY"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.Resolve(context.Background(), sel.TenantID, sel.ActorID, sel.ModelID, "text"); err == nil {
		t.Fatal("platform secret reference applied to tenant")
	}
}

func TestModelRuntimeRevocationCancelsInFlightAndRejectsLaterSend(t *testing.T) {
	for _, change := range []string{"credential", "membership"} {
		t.Run(change, func(t *testing.T) {
			r, sel, in := runtimeFixture(t)
			started := make(chan struct{})
			finished := make(chan error, 1)
			go func() {
				finished <- r.Execute(context.Background(), sel, func(ctx context.Context, p RuntimeProviders) error {
					runtimeReplaceTransport(p, func(req *http.Request) (*http.Response, error) {
						close(started)
						<-req.Context().Done()
						return nil, req.Context().Err()
					})
					_, err := p.Creative.Analyze(context.Background(), ProjectInput{Title: "Synthetic title", Brief: "Synthetic local-only description"})
					return err
				})
			}()
			select {
			case <-started:
			case <-time.After(time.Second):
				t.Fatal("request not started")
			}
			if change == "credential" {
				if _, err := r.auth.models.update(sel.TenantID, sel.Version, in, ModelCredentialInput{Action: "revoke"}); err != nil {
					t.Fatal(err)
				}
			} else {
				r.auth.mu.Lock()
				next := authSnapshot{Organizations: map[string]AuthOrganization{"tenant-a": {Members: map[string]string{"actor-a": "viewer"}}}}
				r.auth.cancelChangedRuntimeLocked(r.auth.data, next)
				r.auth.data = next
				r.auth.mu.Unlock()
			}
			select {
			case err := <-finished:
				if err == nil {
					t.Fatal("cancel not propagated")
				}
			case <-time.After(time.Second):
				t.Fatal("revocation did not cancel request")
			}
			if err := r.Execute(context.Background(), sel, func(context.Context, RuntimeProviders) error { t.Fatal("revoked credential used"); return nil }); err == nil {
				t.Fatal("revoked execution accepted")
			}
		})
	}
}

func TestModelRuntimePermitRecheckedAndSecretNeverInError(t *testing.T) {
	r, sel, _ := runtimeFixture(t)
	var allowed atomic.Bool
	allowed.Store(true)
	r.authorize = func(context.Context, RuntimeSelection) error {
		if !allowed.Load() {
			return errRuntimeDenied
		}
		return nil
	}
	err := r.Execute(context.Background(), sel, func(ctx context.Context, p RuntimeProviders) error {
		runtimeReplaceTransport(p, func(*http.Request) (*http.Response, error) {
			t.Fatal("network dispatched without current permit")
			return nil, nil
		})
		allowed.Store(false)
		_, err := p.Creative.Analyze(ctx, ProjectInput{Title: "Synthetic title", Brief: "Synthetic local-only description"})
		return err
	})
	if err == nil || strings.Contains(err.Error(), "synthetic-tenant") {
		t.Fatal("permit missing or error leaked credential")
	}
}

func TestModelRuntimeCredentialAADAndWrongKeyFailClosed(t *testing.T) {
	r, sel, _ := runtimeFixture(t)
	s := r.auth.models
	s.mu.Lock()
	tenant := s.data.Tenants[sel.TenantID]
	m := tenant.Entries[sel.ModelID]
	m.Ciphertext = "invalid"
	tenant.Entries[sel.ModelID] = m
	s.data.Tenants[sel.TenantID] = tenant
	s.mu.Unlock()
	if err := r.Execute(context.Background(), sel, func(context.Context, RuntimeProviders) error { t.Fatal("invalid ciphertext allowed"); return nil }); !errors.Is(err, errRuntimeUnavailable) {
		t.Fatal(err)
	}
}

func TestModelRuntimeFactoryHasNoFallbackAndUsesSavedModel(t *testing.T) {
	for _, a := range ModelAdapters() {
		for _, capability := range a.Capabilities {
			t.Run(a.Provider+"/"+capability, func(t *testing.T) {
				model := "synthetic-model"
				if a.Provider == "openai-asr" {
					model = "whisper-1"
				}
				entry := ModelConfigEntry{Provider: a.Provider, Capability: capability, Model: model}
				secret := "synthetic-secret"
				if a.Provider == "jimeng-images" {
					secret = `{"access_key":"synthetic-ak","secret_key":"synthetic-sk"}`
				}
				p, err := buildRuntimeProviders(context.Background(), entry, secret, "synthetic-voice", func(context.Context) error { return errRuntimeDenied }, nil)
				unsupported := a.Provider == "minimax-music" || a.Provider == "openai-speech"
				if unsupported {
					if err == nil {
						t.Fatal("unsupported configuration silently enabled")
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				switch capability {
				case "image":
					if p.Image == nil || p.Creative != nil {
						t.Fatal("incorrect capability")
					}
				case "vision":
					if p.Vision == nil || p.Creative != nil {
						t.Fatal("incorrect capability")
					}
				case "asr":
					if p.Alignment == nil {
						t.Fatal("missing ASR")
					}
				default:
					if p.Creative == nil {
						t.Fatal("missing creative adapter")
					}
				}
			})
		}
	}
}

func TestModelRuntimeApprovedSecretResolverIsolation(t *testing.T) {
	r, sel, in := runtimeFixture(t)
	report, err := r.auth.models.update(sel.TenantID, sel.Version, in, ModelCredentialInput{Action: "reference", Reference: "OPENAI_API_KEY"})
	if err != nil {
		t.Fatal(err)
	}
	var fetches int
	resolver, err := NewApprovedSecretResolver([]SecretBinding{{TenantID: sel.TenantID, ModelConfigID: sel.ModelID, Provider: in.Provider, Reference: "OPENAI_API_KEY", ExternalID: "synthetic-vault-entry"}}, func(ctx context.Context, id string) ([]byte, error) {
		fetches++
		if id != "synthetic-vault-entry" {
			t.Fatal("incorrect binding")
		}
		return []byte("synthetic-external-secret"), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	r = NewModelRuntimeWithSecrets(r.auth, func(context.Context, RuntimeSelection) error { return nil }, resolver)
	sel.Version = report.Version
	if err = r.Execute(context.Background(), sel, func(ctx context.Context, p RuntimeProviders) error {
		core := p.Creative.(runtimeCreative).p.(*OpenAICreativeProvider)
		if core.key != "synthetic-external-secret" {
			t.Fatal("resolver not used")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if fetches != 1 {
		t.Fatal(fetches)
	}
	entry := report.Entries[0]
	for _, mutation := range []func(*ModelConfigEntry){func(e *ModelConfigEntry) { e.ID = "other-id" }, func(e *ModelConfigEntry) { e.Provider = "ark-text" }, func(e *ModelConfigEntry) { e.SecretReference = "ARK_API_KEY" }} {
		e := entry
		mutation(&e)
		if _, err = resolver.resolve(context.Background(), sel.TenantID, e); err == nil {
			t.Fatal("reference scope widened")
		}
	}
	if _, err = resolver.resolve(context.Background(), "other-tenant", entry); err == nil || fetches != 1 {
		t.Fatal("cross tenant fetch")
	}
}

func TestModelRuntimeNoDispatchEvidenceAndDynamicReadiness(t *testing.T) {
	r, sel, _ := runtimeFixture(t)
	var ready atomic.Bool
	r.BindReadiness(ready.Load)
	for _, want := range []bool{false, true, false} {
		ready.Store(want)
		report, err := r.auth.models.report(sel.TenantID)
		if err != nil || report.RuntimeApplied != want || report.LiveEnabled {
			t.Fatal("incorrect dynamic readiness", err)
		}
	}
	if err := r.Execute(context.Background(), sel, func(context.Context, RuntimeProviders) error { return errRuntimeUnavailable }); !RuntimeNoDispatch(err) {
		t.Fatal("missing known local failure evidence")
	}
	err := r.Execute(context.Background(), sel, func(ctx context.Context, p RuntimeProviders) error {
		runtimeReplaceTransport(p, func(*http.Request) (*http.Response, error) { return nil, errors.New("synthetic connection failure") })
		_, err := p.Creative.Analyze(ctx, ProjectInput{Title: "Synthetic title", Brief: "Synthetic local-only description"})
		return err
	})
	if err == nil || RuntimeNoDispatch(err) {
		t.Fatal("attempted dispatch incorrectly released")
	}
	err = r.Execute(context.Background(), sel, func(ctx context.Context, p RuntimeProviders) error {
		runtimeReplaceTransport(p, func(*http.Request) (*http.Response, error) { return nil, errors.New("synthetic network error") })
		_, _ = p.Creative.Analyze(ctx, ProjectInput{Title: "Synthetic title", Brief: "Synthetic local-only description"})
		return &RuntimeNoDispatchError{cause: errRuntimeUnavailable}
	})
	if RuntimeNoDispatch(err) {
		t.Fatal("nested no-dispatch marker erased observed attempt")
	}
}

func TestModelRuntimeRecoveryTransportRejectsCreation(t *testing.T) {
	ctx := runtimeRecoveryContext("synthetic-job")
	var attempts int
	transport := runtimeTransport{allowReadOnly: videoReadOnlyRequest("gemini-video"), lifetime: ctx, guard: func(context.Context) error { return nil }, onDispatch: func() { attempts++ }, base: runtimeFixtureTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("{}"))}, nil
	})}
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		req, _ := http.NewRequestWithContext(ctx, method, "https://generativelanguage.googleapis.com/v1beta/files/synthetic-job", nil)
		if _, err := transport.RoundTrip(req); !errors.Is(err, errRuntimeDenied) {
			t.Fatal("read-only permit accepted mutation", method)
		}
	}
	if attempts != 0 {
		t.Fatal("recovery sent a mutation")
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://generativelanguage.googleapis.com/v1beta/files/synthetic-job", nil)
	resp, err := transport.RoundTrip(req)
	if err != nil || attempts != 1 {
		t.Fatal("recovery GET denied", err)
	}
	resp.Body.Close()
}

func TestModelRuntimeJimengReadOnlyRecoveryIsStrict(t *testing.T) {
	ctx := runtimeRecoveryContext("synthetic-job")
	providers, err := buildRuntimeProviders(ctx, ModelConfigEntry{Provider: "jimeng-images", Model: "jimeng_seedream46_cvtob", Capability: "image"}, `{"access_key":"synthetic-ak","secret_key":"synthetic-sk"}`, "", func(context.Context) error { return nil }, nil)
	if err != nil {
		t.Fatal(err)
	}
	p := providers.Image.(runtimeJimengImage).p
	if _, err := providers.Image.Generate(ctx, Input{}); !errors.Is(err, errRuntimeDenied) {
		t.Fatal("recovery may generate")
	}
	if _, err := providers.Image.(ReferenceImageProvider).GenerateWithReference(ctx, Input{}, nil, "image/png"); !errors.Is(err, errRuntimeDenied) {
		t.Fatal("recovery may upload a reference before submit")
	}
	transport := p.client.Transport.(runtimeTransport)
	calls := 0
	transport.base = runtimeFixtureTransport(func(req *http.Request) (*http.Response, error) {
		calls++
		if !strings.HasPrefix(req.Header.Get("Authorization"), "HMAC-SHA256 Credential=synthetic-ak/") {
			t.Fatal("missing signed adapter")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"code":10000,"data":{"task_id":"synthetic-job","status":"done"}}`)), Header: http.Header{}}, nil
	})
	p.client.Transport = transport
	if _, err = p.PollImage(ctx, "synthetic-job"); err != nil || calls != 1 {
		t.Fatal("documented query failed", err, calls)
	}
	if _, err = p.PollImage(ctx, "other-tenant-job"); err == nil || calls != 1 {
		t.Fatal("same credential queried unrelated upstream")
	}
	if _, err = p.request(ctx, "CVSync2AsyncSubmitTask", map[string]string{"req_key": p.reqKey, "prompt": "synthetic"}); err == nil || calls != 1 {
		t.Fatal("read-only permit submitted new work")
	}
	baseURL := "https://visual.volcengineapi.com/?Action=CVSync2AsyncGetResult&Version=2022-08-31"
	body := `{"req_key":"jimeng_seedream46_cvtob","task_id":"synthetic-job","req_json":"{\"return_url\":false,\"logo_info\":{\"add_logo\":true}}"}`
	for _, tc := range []struct{ url, body string }{
		{strings.Replace(baseURL, "visual.volcengineapi.com", "evil.example", 1), body},
		{baseURL + "&Action=CVSync2AsyncSubmitTask", body},
		{strings.Replace(baseURL, "2022-08-31", "other", 1), body},
		{baseURL, strings.Replace(body, "jimeng_seedream46_cvtob", "other-model", 1)},
		{baseURL, strings.TrimSuffix(body, "}") + `,"prompt":"create"}`},
	} {
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, tc.url, strings.NewReader(tc.body))
		if jimengReadOnlyRequest(p.reqKey)(req) {
			t.Fatal("unsafe query accepted")
		}
	}
}

func TestModelRuntimeJimengCredentialPairEncryptedAndValidated(t *testing.T) {
	r, sel, _ := runtimeFixture(t)
	in := ModelConfigInput{Provider: "jimeng-images", Model: "jimeng_seedream46_cvtob", Capability: "image", Region: "cn-north-1", Enabled: true, IsDefault: true, Lifecycle: "active"}
	if _, err := r.auth.models.update(sel.TenantID, sel.Version, in, ModelCredentialInput{Action: "set", Secret: "synthetic-single-key"}); !errors.Is(err, errModelInput) {
		t.Fatal("single key accepted as credential pair")
	}
	report, err := r.auth.models.update(sel.TenantID, sel.Version, in, ModelCredentialInput{Action: "set", Secret: `{"access_key":"synthetic-pair-ak","secret_key":"synthetic-pair-sk"}`})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(report)
	if strings.Contains(string(raw), "synthetic-pair") {
		t.Fatal("credential pair leaked")
	}
	selection, _, err := r.Resolve(context.Background(), sel.TenantID, sel.ActorID, "", "image")
	if err != nil {
		t.Fatal(err)
	}
	if err = r.Execute(context.Background(), selection, func(ctx context.Context, p RuntimeProviders) error {
		wrapped, ok := p.Image.(runtimeJimengImage)
		jimeng := wrapped.p
		if !ok || jimeng.accessKey != "synthetic-pair-ak" || jimeng.secretKey != "synthetic-pair-sk" {
			t.Fatal("encrypted credential pair not applied")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestModelRuntimeRecoveryVideoUpstreamBinding(t *testing.T) {
	for _, tc := range []struct{ provider, id, url string }{
		{"ark-video", "cgt-known", "https://ark.cn-beijing.volces.com/api/v3/contents/generations/tasks/cgt-known"},
		{"minimax-video", "12345", "https://api.minimax.io/v2/query/video_generation/12345"},
		{"gemini-video", "known-file", "https://generativelanguage.googleapis.com/v1beta/files/known-file"},
	} {
		t.Run(tc.provider, func(t *testing.T) {
			ctx := runtimeRecoveryContext(tc.id)
			predicate := videoReadOnlyRequest(tc.provider)
			req, _ := http.NewRequestWithContext(ctx, http.MethodGet, tc.url, nil)
			if !predicate(req) {
				t.Fatal("bound query denied")
			}
			for _, target := range []string{tc.url + "-other", tc.url + "?task_id=other", strings.Replace(tc.url, "https://", "https://evil.example/", 1)} {
				req, _ := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
				if predicate(req) {
					t.Fatal("unbound task/origin accepted")
				}
			}
			req, _ = http.NewRequestWithContext(runtimeRecoveryContext("other-upstream"), http.MethodGet, tc.url, nil)
			if predicate(req) {
				t.Fatal("same adapter credential used for other task")
			}
			providers, err := buildRuntimeProviders(ctx, ModelConfigEntry{Provider: tc.provider, Model: "synthetic-model", Capability: "video"}, "synthetic-secret", "", func(context.Context) error { return nil }, nil)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = providers.Creative.PollVideo(ctx, "other-upstream"); !errors.Is(err, errRuntimeDenied) {
				t.Fatal("poll wrapper ignored binding", err)
			}
			if _, err = providers.Creative.DownloadVideo(ctx, "other-upstream"); !errors.Is(err, errRuntimeDenied) {
				t.Fatal("download wrapper ignored binding", err)
			}
		})
	}
}
