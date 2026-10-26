package studio

import (
	"context"
	"strings"
	"sync/atomic"
	"time"
)

// ReferenceBridgeEntitlement authorizes transfer to one operator-controlled S3
// destination. It is never accepted from a tenant or inferred from a model key.
type ReferenceBridgeEntitlement struct {
	TenantID           string    `json:"tenant_id"`
	ModelConfigID      string    `json:"model_config_id"`
	ModelConfigVersion int64     `json:"model_config_version"`
	Model              string    `json:"model"`
	Endpoint           string    `json:"endpoint"`
	Bucket             string    `json:"bucket"`
	Region             string    `json:"region"`
	Source             string    `json:"source"`
	ExpiresAt          time.Time `json:"expires_at"`
}

func (e ReferenceBridgeEntitlement) valid(now time.Time) bool {
	return e.TenantID != "" && len(e.TenantID) <= 128 && !strings.ContainsRune(e.TenantID, 0) && e.ModelConfigID != "" && len(e.ModelConfigID) <= 128 && !strings.ContainsRune(e.ModelConfigID, 0) && e.ModelConfigVersion > 0 && nativeModelPattern.MatchString(e.Model) && strings.TrimSpace(e.Source) != "" && len(e.Source) <= 256 && e.ExpiresAt.After(now)
}
func (p *ExecutionPolicy) configureReferenceBridge() error {
	p.referenceBridge = nil
	if len(p.ReferenceBridgeEntitlements) == 0 {
		return nil
	}
	if len(p.ReferenceBridgeEntitlements) > 1000 {
		return ErrExecutionGovernance
	}
	seen := map[string]bool{}
	for _, e := range p.ReferenceBridgeEntitlements {
		key := e.TenantID + "\x00" + e.ModelConfigID
		if !e.valid(time.Now()) || seen[key] {
			return ErrExecutionGovernance
		}
		seen[key] = true
	}
	bridge, err := ConfigureReferenceBridgeFromEnv()
	if err != nil || bridge == nil {
		return ErrExecutionGovernance
	}
	s3, ok := bridge.(*S3ReferenceBridge)
	if !ok {
		return ErrExecutionGovernance
	}
	for _, e := range p.ReferenceBridgeEntitlements {
		if e.Endpoint != s3.config.Endpoint || e.Bucket != s3.config.Bucket || e.Region != s3.config.Region {
			return ErrExecutionGovernance
		}
	}
	p.referenceBridge = bridge
	return nil
}
func (p *ExecutionPolicy) referenceBridgeFor(s RuntimeSelection, model string) ReferenceObjectBridge {
	if p == nil || p.referenceBridge == nil {
		return nil
	}
	for _, e := range p.ReferenceBridgeEntitlements {
		if e.TenantID == s.TenantID && e.ModelConfigID == s.ModelID && e.ModelConfigVersion == s.Version && e.Model == model && e.valid(time.Now()) {
			return p.referenceBridge
		}
	}
	return nil
}

type guardedReferenceBridge struct {
	lifetime  context.Context
	guard     func(context.Context) error
	bridge    func() ReferenceObjectBridge
	attempted func()
}

func (b guardedReferenceBridge) ReferenceURL(ctx context.Context, data []byte, mime string) (string, error) {
	if executionReadOnly(ctx) || executionReadOnly(b.lifetime) {
		return "", errRuntimeDenied
	}
	if err := b.guard(ctx); err != nil {
		return "", err
	}
	bridge := b.bridge()
	if bridge == nil {
		return "", errRuntimeUnavailable
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(b.lifetime, cancel)
	defer stop()
	// Storage may have accepted a request even if its result is unknown. Never
	// mark the enclosing operation as safely unattempted after this point.
	b.attempted()
	url, err := bridge.ReferenceURL(ctx, data, mime)
	if err != nil {
		return "", err
	}
	if err = b.guard(ctx); err != nil {
		return "", err
	}
	if b.bridge() == nil {
		return "", errRuntimeUnavailable
	}
	return url, nil
}
func (r *ModelRuntime) attachReferenceBridge(providers RuntimeProviders, ctx context.Context, selection RuntimeSelection, entry ModelConfigEntry, guard func(context.Context) error, attempted func()) {
	p, ok := providers.Image.(runtimeJimengImage)
	if !ok || r.referenceBridge == nil {
		return
	}
	var referenceStarted atomic.Bool
	transport := p.p.client.Transport.(runtimeTransport)
	transport.guard = func(requestCtx context.Context) error {
		if err := guard(requestCtx); err != nil {
			return err
		}
		if referenceStarted.Load() && r.referenceBridge(selection, entry.Model) == nil {
			return errRuntimeUnavailable
		}
		return nil
	}
	p.p.client.Transport = transport
	p.p.SetReferenceBridge(guardedReferenceBridge{lifetime: ctx, guard: guard, attempted: func() { referenceStarted.Store(true); attempted() }, bridge: func() ReferenceObjectBridge { return r.referenceBridge(selection, entry.Model) }})
}
func (r *ModelRuntime) imageReferenceSupported(selection RuntimeSelection, provider, model string) bool {
	if provider != "jimeng-images" {
		return runtimeImageReferenceSupported(provider)
	}
	return r != nil && r.referenceBridge != nil && r.referenceBridge(selection, model) != nil
}
