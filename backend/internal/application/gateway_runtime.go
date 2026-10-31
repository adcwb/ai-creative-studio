package application

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func (r *ModelRuntime) executeGateway(ctx context.Context, q RuntimeSelection, dispatch func(context.Context, RuntimeProviders) error, onDispatch func()) (resultErr error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	s := r.auth.models
	s.mu.Lock()
	if s.failed != nil || !gatewaySelectionCurrent(s.data, q) {
		s.mu.Unlock()
		return errRuntimeUnavailable
	}
	t := s.data.Tenants[q.TenantID]
	g := t.Gateways[q.GatewayID]
	m := t.Models[q.ModelID]
	secret, e := s.gatewaySecretLocked(q.TenantID, g)
	if e != nil {
		s.mu.Unlock()
		return e
	}
	defer clear(secret)
	call := &runtimeCall{selection: q, cancel: cancel}
	if s.runtimeCancels == nil {
		s.runtimeCancels = map[*runtimeCall]struct{}{}
	}
	s.runtimeCancels[call] = struct{}{}
	s.mu.Unlock()
	defer func() { s.mu.Lock(); delete(s.runtimeCancels, call); s.mu.Unlock() }()
	remove := r.auth.registerRuntimeCall(call)
	defer remove()
	go r.watchAuthorization(ctx, q, cancel)
	taskID := ""
	if permit, ok := ctx.Value(executionPermitKey{}).(*executionPermit); ok {
		permit.mu.Lock()
		taskID = permit.id
		permit.mu.Unlock()
	}
	observer := &gatewayObservation{store: s.usage, call: ModelCall{ID: authRandom(16), TaskID: taskID, Tenant: q.TenantID, GatewayID: g.Gateway.ID, GatewayName: g.Gateway.Name, ModelConfigID: m.ID, ModelName: m.Name, Model: m.Model, FeatureKey: q.FeatureKey, Capability: q.Capability, State: "unknown", StartedAt: time.Now().UTC(), Currency: m.Currency, MaximumMinor: m.MaximumMinor}}
	defer func() {
		if e := observer.finish(resultErr); e != nil {
			resultErr = errRuntimeUnavailable
		}
	}()
	client := newGatewayClient(g.Gateway)
	origin, _ := url.Parse(g.Gateway.BaseURL)
	client.Transport = gatewayObservedTransport{base: client.Transport, origin: origin, observation: observer}
	guard := func(requestCtx context.Context) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return r.check(requestCtx, q)
	}
	guardRuntimeClient(client, ctx, guard, onDispatch)
	setRuntimeReadOnly(client, func(req *http.Request) bool {
		id := executionRecoveryUpstream(req.Context())
		return id != "" && gatewayVideoID.MatchString(id) && req.Method == http.MethodGet && req.URL.Host == origin.Host && req.URL.Scheme == origin.Scheme && req.URL.RawQuery == "" && (req.URL.Path == origin.Path+"/videos/"+id || req.URL.Path == origin.Path+"/videos/"+id+"/content")
	})
	api := newCreativeProvider(string(secret), g.Gateway.BaseURL, client)
	api.SetModels(m.Model, m.Model, m.Model)
	api.maxOutputTokens = m.MaxOutputTokens
	api.jsonOutput = &m.JSONOutput
	providers := RuntimeProviders{}
	switch q.Capability {
	case "text":
		providers.Creative = runtimeCreative{p: &gatewayCreativeProvider{OpenAICreativeProvider: api, model: m, voice: q.Voice}, capability: q.Capability}
	case "vision":
		providers.Vision = &gatewayCreativeProvider{OpenAICreativeProvider: api, model: m}
	case "image":
		providers.Image = &gatewayImageProvider{api: api, model: m, download: guardRuntimeDownload(ctx, safeMediaDownload, guard, onDispatch)}
	case "video", "speech":
		providers.Creative = runtimeCreative{p: &gatewayCreativeProvider{OpenAICreativeProvider: api, model: m, voice: q.Voice}, capability: q.Capability}
	case "asr":
		p := NewOpenAIAlignmentProvider(string(secret), m.Model)
		p.gateway = true
		p.plainTranscript = q.FeatureKey == "speech_transcription"
		p.client = client
		p.endpoint = g.Gateway.BaseURL + "/audio/transcriptions"
		providers.Alignment = p
	default:
		return errRuntimeUnavailable
	}
	if e := guard(ctx); e != nil {
		return e
	}
	return dispatch(ctx, providers)
}

func operationFeature(operation string, in *CreativeJobInput) string {
	switch operation {
	case "script":
		return "script_generation"
	case "storyboard":
		return "storyboard_planning"
	case "analysis":
		return "brief_analysis"
	case "image":
		return "image_generation"
	case "alignment":
		return "subtitle_alignment"
	}
	if in == nil {
		return ""
	}
	switch in.Kind {
	case "analysis":
		return "brief_analysis"
	case "image":
		if in.ReferenceAssetID != "" || in.MaskAssetID != "" || len(in.ReferenceAssetIDs) > 0 {
			return "image_edit"
		}
		if in.Character != "" {
			return "character_image"
		}
		if in.ShotID != "" {
			return "shot_image"
		}
		return "image_generation"
	case "video":
		return "shot_video"
	case "speech":
		return "speech_synthesis"
	case "vision":
		return "vision_analysis"
	case "consistency":
		return "consistency_check"
	}
	return ""
}
func (r *ModelRuntime) authorizeFeature(tenant, feature, id string) (FeatureBinding, bool) {
	s := r.auth.models
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failed != nil || s.pg != nil && s.pg.err != nil {
		return FeatureBinding{}, false
	}
	t := s.data.Tenants[tenant]
	b, ok := t.Bindings[feature]
	m, exists := t.Models[id]
	if !exists {
		return b, !ok || !b.Enabled
	}
	f, known := featureDefinition(feature)
	return b, ok && known && b.Enabled && modelHasCapability(m, f.Capability) && (b.ModelConfigID == id || b.AllowOverride)
}

type gatewayQuoter struct {
	models   *ModelConfigStore
	fallback ExecutionQuoter
}

func underlyingExecutionPolicy(q ExecutionQuoter) (*ExecutionPolicy, bool) {
	if wrapped, ok := q.(*gatewayQuoter); ok {
		q = wrapped.fallback
	}
	p, ok := q.(*ExecutionPolicy)
	return p, ok
}
func (q *gatewayQuoter) QuoteExecution(ctx context.Context, r ExecutionRequest) (ExecutionQuote, error) {
	if r.Selection == nil || r.Selection.GatewayID == "" {
		if q.fallback != nil {
			return q.fallback.QuoteExecution(ctx, r)
		}
		return ExecutionQuote{}, ErrExecutionGovernance
	}
	s := q.models
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failed != nil || !gatewaySelectionCurrent(s.data, *r.Selection) {
		return ExecutionQuote{}, ErrExecutionGovernance
	}
	m := s.data.Tenants[r.Tenant].Models[r.Selection.ModelID]
	if m.MaximumMinor <= 0 || strings.TrimSpace(m.PriceSource) == "" {
		return ExecutionQuote{}, ErrExecutionGovernance
	}
	return ExecutionQuote{Currency: m.Currency, MaximumMinor: m.MaximumMinor, Evidence: "admin-bound:" + m.PriceSource, ExpiresAt: time.Now().Add(5 * time.Minute)}, nil
}
func (q *gatewayQuoter) RecoveryExpiry(ctx context.Context, r ExecutionRequest) (time.Time, error) {
	// Existing provider recovery policy remains authoritative. A custom gateway
	// is not assumed to offer free polling solely because it uses GET.
	if r.Selection != nil && r.Selection.GatewayID != "" {
		return time.Time{}, ErrExecutionGovernance
	}
	if p, ok := q.fallback.(ExecutionRecoveryAuthorizer); ok {
		return p.RecoveryExpiry(ctx, r)
	}
	return time.Time{}, ErrExecutionGovernance
}
