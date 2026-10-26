package studio

import (
	"context"
	"encoding/base64"
	"errors"
	"github.com/adcwb/ai-creative-studio/backend/internal/tracing"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
)

// RuntimeSelection is persisted with the accepted task. ModelID identifies a
// tenant configuration, not a vendor model. ActorID must come from authentication.
type RuntimeSelection struct {
	RequestDigest string `json:"request_sha256,omitempty"`
	TenantID      string `json:"tenant_id"`
	ActorID       string `json:"actor_id"`
	ModelID       string `json:"model_config_id"`
	Version       int64  `json:"model_config_version"`
	Capability    string `json:"capability"`
	Voice         string `json:"voice,omitempty"`
	Currency      string `json:"budget_currency,omitempty"`
	MaximumMinor  int64  `json:"budget_maximum_minor,omitempty"`
}

type RuntimeProviders struct {
	Image     ImageProvider
	Creative  CreativeProvider
	Vision    VisionProvider
	Alignment AlignmentProvider
	Music     MusicProvider
}

// No environment lookup is performed by this runtime. Platform secret references
// are deliberately unavailable to tenant configurations: naming OPENAI_API_KEY
// must never grant a tenant use of the deployment operator's credential.
type ModelRuntime struct {
	auth          *AuthStore
	authorize     func(context.Context, RuntimeSelection) error
	secrets       *ApprovedSecretResolver
	musicEntitled func(string, int64, ModelConfigEntry) bool
}

func NewModelRuntime(auth *AuthStore, authorize func(context.Context, RuntimeSelection) error) *ModelRuntime {
	return &ModelRuntime{auth: auth, authorize: authorize}
}

// BindReadiness reports current governance wiring without implying live model
// validation. The callback must be nonblocking and must not acquire model locks.
func (r *ModelRuntime) BindReadiness(ready func() bool) {
	if r == nil || r.auth == nil || r.auth.models == nil {
		return
	}
	r.auth.models.mu.Lock()
	r.auth.models.runtimeReady = func() bool { return r.authorize != nil && ready != nil && ready() }
	r.auth.models.mu.Unlock()
}

func NewModelRuntimeWithSecrets(auth *AuthStore, authorize func(context.Context, RuntimeSelection) error, resolver *ApprovedSecretResolver) *ModelRuntime {
	r := NewModelRuntime(auth, authorize)
	r.secrets = resolver
	return r
}

var errRuntimeDenied = errors.New("model runtime authorization denied")
var errRuntimeUnavailable = errors.New("model runtime configuration unavailable")

func runtimeRoleAllowed(role string) bool {
	return role == "owner" || role == "admin" || role == "editor"
}

func (r *ModelRuntime) Resolve(ctx context.Context, tenant, actor, id, capability string) (RuntimeSelection, ModelConfigEntry, error) {
	selection := RuntimeSelection{TenantID: tenant, ActorID: actor, ModelID: id, Capability: capability}
	if ctx.Err() != nil || r == nil || r.auth == nil || r.auth.models == nil || !runtimeRoleAllowed(r.auth.role(actor, tenant)) {
		return selection, ModelConfigEntry{}, errRuntimeDenied
	}
	s := r.auth.models
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failed != nil {
		return selection, ModelConfigEntry{}, errRuntimeUnavailable
	}
	t := s.data.Tenants[tenant]
	if id == "" {
		for _, m := range t.Entries {
			if m.Entry.Capability == capability && m.Entry.IsDefault {
				if selection.ModelID != "" {
					return selection, ModelConfigEntry{}, errRuntimeUnavailable
				}
				selection.ModelID = m.Entry.ID
			}
		}
	}
	m, ok := t.Entries[selection.ModelID]
	if !ok || !r.entryAllowed(m.Entry, tenant, capability, t.Version) {
		return selection, ModelConfigEntry{}, errRuntimeUnavailable
	}
	selection.Version = t.Version
	entry := m.Entry
	entry.SecretMask = ""
	if entry.SecretStatus == "encrypted" {
		entry.SecretMask = "••••••••"
	}
	return selection, entry, nil
}

func (r *ModelRuntime) entryAllowed(e ModelConfigEntry, tenant, capability string, version int64) bool {
	a, err := validateModelInput(ModelConfigInput{ID: e.ID, Provider: e.Provider, Model: e.Model, Capability: e.Capability, Region: e.Region, Enabled: e.Enabled, IsDefault: e.IsDefault, Lifecycle: e.Lifecycle})
	if err != nil || a.Endpoint != e.Endpoint || a.Protocol != e.Protocol {
		return false
	}
	credentialReady := e.SecretStatus == "encrypted" || (e.SecretStatus == "reference_unverified" && r.secrets.approved(tenant, e))
	if !e.Enabled || e.Lifecycle != "active" || e.Capability != capability || !credentialReady {
		return false
	}
	switch e.Provider {
	case "minimax-music":
		return (e.Model == "music-3.0" || e.Model == "music-2.6") && r.musicEntitled != nil && r.musicEntitled(tenant, version, e)
	case "openai-speech":
		return false
	case "openai-asr":
		return e.Model == "whisper-1"
	}
	return true
}

func (r *ModelRuntime) check(ctx context.Context, selection RuntimeSelection) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if r == nil || r.auth == nil || r.auth.models == nil || r.authorize == nil || !runtimeRoleAllowed(r.auth.role(selection.ActorID, selection.TenantID)) {
		return errRuntimeDenied
	}
	s := r.auth.models
	s.mu.Lock()
	t := s.data.Tenants[selection.TenantID]
	m, ok := t.Entries[selection.ModelID]
	valid := s.failed == nil && ok && t.Version == selection.Version && r.entryAllowed(m.Entry, selection.TenantID, selection.Capability, t.Version)
	s.mu.Unlock()
	if !valid {
		return errRuntimeUnavailable
	}
	return r.authorize(ctx, selection)
}

// Execute only constructs adapters after the governance layer grants its
// task-bound permit. A retained adapter cannot be used after this callback ends.
// Every HTTP request rechecks that permit, current membership and config version.
func (r *ModelRuntime) Execute(ctx context.Context, selection RuntimeSelection, dispatch func(context.Context, RuntimeProviders) error) (resultErr error) {
	var attempts atomic.Int64
	defer func() {
		if resultErr != nil && attempts.Load() == 0 {
			resultErr = &RuntimeNoDispatchError{cause: resultErr}
		} else if resultErr != nil {
			// A nested callback's no-dispatch marker cannot erase this invocation's
			// observed transport attempt.
			resultErr = &runtimeAttemptedError{cause: resultErr}
		}
	}()
	if dispatch == nil {
		return errRuntimeDenied
	}
	if err := r.check(ctx, selection); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	s := r.auth.models
	s.mu.Lock()
	t := s.data.Tenants[selection.TenantID]
	m, ok := t.Entries[selection.ModelID]
	if s.failed != nil || !ok || t.Version != selection.Version || !r.entryAllowed(m.Entry, selection.TenantID, selection.Capability, t.Version) || (m.Entry.SecretStatus == "encrypted" && s.aead == nil) {
		s.mu.Unlock()
		return errRuntimeUnavailable
	}
	var err error
	var plain []byte
	if m.Entry.SecretStatus == "encrypted" {
		raw, decodeErr := base64.StdEncoding.DecodeString(m.Ciphertext)
		if decodeErr == nil && len(raw) >= s.aead.NonceSize() {
			plain, err = s.aead.Open(nil, raw[:s.aead.NonceSize()], raw[s.aead.NonceSize():], []byte(selection.TenantID+"\x00"+selection.ModelID+"\x00"+m.Entry.Provider))
		} else {
			err = errRuntimeUnavailable
		}
	}
	if err != nil {
		s.mu.Unlock()
		return errRuntimeUnavailable
	}
	if s.runtimeCancels == nil {
		s.runtimeCancels = map[*runtimeCall]struct{}{}
	}
	call := &runtimeCall{selection: selection, cancel: cancel}
	s.runtimeCancels[call] = struct{}{}
	s.mu.Unlock()
	defer func() {
		for i := range plain {
			plain[i] = 0
		}
		s.mu.Lock()
		delete(s.runtimeCancels, call)
		s.mu.Unlock()
	}()
	removeAuth := r.auth.registerRuntimeCall(call)
	defer removeAuth()
	go r.watchAuthorization(ctx, selection, cancel)
	if m.Entry.SecretStatus == "reference_unverified" {
		plain, err = r.secrets.resolve(ctx, selection.TenantID, m.Entry)
		if err != nil {
			return errRuntimeUnavailable
		}
	}
	guard := func(requestCtx context.Context) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return r.check(requestCtx, selection)
	}
	providers, err := buildRuntimeProviders(ctx, m.Entry, string(plain), selection.Voice, guard, func() { attempts.Add(1) })
	if err != nil {
		return err
	}
	if err = guard(ctx); err != nil {
		return err
	}
	return dispatch(ctx, providers)
}

type runtimeCall struct {
	selection RuntimeSelection
	cancel    context.CancelFunc
}

// A separate registry keeps AuthStore's persisted schema unchanged.
var authRuntimeCalls = struct {
	sync.Mutex
	calls map[*AuthStore]map[*runtimeCall]struct{}
}{calls: map[*AuthStore]map[*runtimeCall]struct{}{}}

func (s *AuthStore) registerRuntimeCall(call *runtimeCall) func() {
	authRuntimeCalls.Lock()
	if authRuntimeCalls.calls[s] == nil {
		authRuntimeCalls.calls[s] = map[*runtimeCall]struct{}{}
	}
	authRuntimeCalls.calls[s][call] = struct{}{}
	authRuntimeCalls.Unlock()
	return func() {
		authRuntimeCalls.Lock()
		delete(authRuntimeCalls.calls[s], call)
		if len(authRuntimeCalls.calls[s]) == 0 {
			delete(authRuntimeCalls.calls, s)
		}
		authRuntimeCalls.Unlock()
	}
}

// Called while AuthStore.mu is held, before publishing committed membership.
func (s *AuthStore) cancelChangedRuntimeLocked(before, after authSnapshot) {
	authRuntimeCalls.Lock()
	defer authRuntimeCalls.Unlock()
	for call := range authRuntimeCalls.calls[s] {
		q := call.selection
		if before.Organizations[q.TenantID].Members[q.ActorID] != after.Organizations[q.TenantID].Members[q.ActorID] {
			call.cancel()
		}
	}
}

type runtimeTransport struct {
	allowReadOnly func(*http.Request) bool
	lifetime      context.Context
	base          http.RoundTripper
	guard         func(context.Context) error
	onDispatch    func()
}

func (t runtimeTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if executionReadOnly(req.Context()) && (t.allowReadOnly == nil || !t.allowReadOnly(req)) {
		return nil, errRuntimeDenied
	}
	if err := t.guard(req.Context()); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(req.Context())
	stop := context.AfterFunc(t.lifetime, cancel)
	if t.onDispatch != nil {
		t.onDispatch()
	}
	ctx, span := tracing.Start(ctx, "provider.request", tracing.Attributes{Method: req.Method})
	resp, err := t.base.RoundTrip(req.Clone(ctx))
	if err != nil {
		span.End("unknown")
		stop()
		cancel()
		return nil, err
	}
	span.HTTPStatus(resp.StatusCode)
	outcome := "ok"
	if resp.StatusCode >= 400 {
		outcome = "unknown"
	}
	resp.Body = &runtimeResponseBody{span: span, outcome: outcome, ReadCloser: resp.Body, stop: stop, cancel: cancel}
	return resp, nil
}

type runtimeResponseBody struct {
	span    *tracing.Span
	outcome string
	io.ReadCloser
	stop   func() bool
	cancel context.CancelFunc
}

func (b *runtimeResponseBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err != nil && err != io.EOF {
		b.span.End("unknown")
	}
	return n, err
}

func (b *runtimeResponseBody) Close() error {
	defer b.stop()
	defer b.cancel()
	err := b.ReadCloser.Close()
	if err != nil {
		b.span.End("unknown")
	} else {
		b.span.End(b.outcome)
	}
	return err
}

func guardRuntimeClient(c *http.Client, lifetime context.Context, guard func(context.Context) error, onDispatch func()) {
	c.Transport = runtimeTransport{base: c.Transport, guard: guard, lifetime: lifetime, onDispatch: onDispatch}
}

func buildRuntimeProviders(lifetime context.Context, e ModelConfigEntry, secret, voice string, guard func(context.Context) error, onDispatch func()) (RuntimeProviders, error) {
	var out RuntimeProviders
	if !validModelSecret(e.Provider, secret) {
		return out, errRuntimeUnavailable
	}
	switch e.Provider {
	case "openai-text", "anthropic-text", "gemini-text", "ark-text", "minimax-text":
		p, caps := configureCreative(e.Provider, secret, e.Model, "", "")
		if !caps.Ready {
			return out, errRuntimeUnavailable
		}
		switch p := p.(type) {
		case *OpenAICreativeProvider:
			guardRuntimeClient(p.client, lifetime, guard, onDispatch)
		case *NativeTextProvider:
			guardRuntimeClient(p.client, lifetime, guard, onDispatch)
		case *arkTextProvider:
			guardRuntimeClient(p.client, lifetime, guard, onDispatch)
		default:
			return out, errRuntimeUnavailable
		}
		if e.Capability == "vision" {
			out.Vision, _ = p.(VisionProvider)
		} else {
			out.Creative = p
		}
	case "openai-images":
		p := NewOpenAIProvider(secret)
		p.model = e.Model
		guardRuntimeClient(p.client, lifetime, guard, onDispatch)
		out.Image = p
	case "gemini-images", "ark-images":
		p := NewGeminiImageProvider(secret, e.Model)
		if e.Provider == "ark-images" {
			p = NewArkImageProvider(secret, e.Model)
		}
		guardRuntimeClient(p.client, lifetime, guard, onDispatch)
		out.Image = p
	case "gemini-interactions-images":
		p := NewGoogleInteractionsImageProvider(secret, e.Model)
		guardRuntimeClient(p.api.client, lifetime, guard, onDispatch)
		out.Image = p
	case "minimax-images":
		p := NewMiniMaxImageProvider(secret, e.Model)
		guardRuntimeClient(p.api.client, lifetime, guard, onDispatch)
		out.Image = p
	case "jimeng-images":
		pair, err := decodeJimengCredential(secret)
		if err != nil {
			return out, errRuntimeUnavailable
		}
		p := NewJimengImageProvider(pair.AccessKey, pair.SecretKey, e.Model)
		guardRuntimeClient(p.client, lifetime, guard, onDispatch)
		transport := p.client.Transport.(runtimeTransport)
		transport.allowReadOnly = jimengReadOnlyRequest(e.Model)
		p.client.Transport = transport
		out.Image = runtimeJimengImage{p: p}
	case "gemini-video":
		p := NewGoogleVideoProvider(secret, e.Model)
		guardRuntimeClient(p.client, lifetime, guard, onDispatch)
		setRuntimeReadOnly(p.client, videoReadOnlyRequest(e.Provider))
		p.download = guardRuntimeDownload(lifetime, p.download, guard, onDispatch)
		out.Creative = p
	case "ark-video":
		p := NewArkVideoProvider(secret, e.Model)
		guardRuntimeClient(p.api.client, lifetime, guard, onDispatch)
		setRuntimeReadOnly(p.api.client, videoReadOnlyRequest(e.Provider))
		p.download = guardRuntimeDownload(lifetime, p.download, guard, onDispatch)
		out.Creative = p
	case "minimax-video":
		p := NewMiniMaxVideoProvider(secret, e.Model)
		guardRuntimeClient(p.api.client, lifetime, guard, onDispatch)
		setRuntimeReadOnly(p.api.client, videoReadOnlyRequest(e.Provider))
		p.download = guardRuntimeDownload(lifetime, p.download, guard, onDispatch)
		out.Creative = p
	case "openai-asr":
		if e.Model != "whisper-1" {
			return out, errRuntimeUnavailable
		}
		p := NewOpenAIAlignmentProvider(secret, e.Model)
		guardRuntimeClient(p.client, lifetime, guard, onDispatch)
		out.Alignment = p
	case "minimax-speech":
		if !configToken.MatchString(voice) {
			return out, errRuntimeUnavailable
		}
		p := NewMiniMaxSpeechProvider(secret, e.Model, voice)
		guardRuntimeClient(p.transport.client, lifetime, guard, onDispatch)
		out.Creative = p
	case "gemini-speech":
		if !configToken.MatchString(voice) {
			return out, errRuntimeUnavailable
		}
		p := NewGoogleSpeechProvider(secret, e.Model, voice)
		guardRuntimeClient(p.client, lifetime, guard, onDispatch)
		out.Creative = p
	case "bytedance-speech":
		if !configToken.MatchString(voice) {
			return out, errRuntimeUnavailable
		}
		p := NewByteDanceSpeechProvider(secret, e.Model, voice)
		guardRuntimeClient(p.client, lifetime, guard, onDispatch)
		out.Creative = p
	case "minimax-music":
		if e.Capability != "music" || (e.Model != "music-3.0" && e.Model != "music-2.6") {
			return out, errRuntimeUnavailable
		}
		p := NewMiniMaxMusicProvider(secret, e.Model)
		guardRuntimeClient(p.transport.client, lifetime, guard, onDispatch)
		out.Music = p
	default:
		// Deprecated OpenAI speech stays disabled.
		return out, errRuntimeUnavailable
	}
	if out.Creative != nil {
		out.Creative = runtimeCreative{p: out.Creative, capability: e.Capability}
	}
	return out, nil
}

func guardRuntimeDownload(lifetime context.Context, download func(context.Context, string) ([]byte, error), guard func(context.Context) error, onDispatch func()) func(context.Context, string) ([]byte, error) {
	return func(ctx context.Context, url string) ([]byte, error) {
		if err := guard(ctx); err != nil {
			return nil, err
		}
		ctx, cancel := context.WithCancel(ctx)
		stop := context.AfterFunc(lifetime, cancel)
		defer stop()
		defer cancel()
		if onDispatch != nil {
			onDispatch()
		}
		return download(ctx, url)
	}
}
