package studio

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type referenceBridgeFunc func(context.Context, []byte, string) (string, error)

func (f referenceBridgeFunc) ReferenceURL(c context.Context, b []byte, m string) (string, error) {
	return f(c, b, m)
}

func TestReferenceBridgeEntitlementBindingAndConfiguration(t *testing.T) {
	e := ReferenceBridgeEntitlement{TenantID: "tenant-a", ModelConfigID: "model-a", ModelConfigVersion: 2, Model: "jimeng_seedream46_cvtob", Endpoint: "https://objects.example.test", Bucket: "reference-test", Region: "us-east-1", Source: "synthetic transfer/storage authorization", ExpiresAt: time.Now().Add(time.Hour)}
	p := &ExecutionPolicy{ReferenceBridgeEntitlements: []ReferenceBridgeEntitlement{e}}
	t.Setenv("REFERENCE_BRIDGE_ENABLED", "")
	if p.configureReferenceBridge() == nil {
		t.Fatal("grant without explicit storage configuration")
	}
	t.Setenv("REFERENCE_BRIDGE_ENABLED", "true")
	t.Setenv("REFERENCE_S3_ENDPOINT", e.Endpoint)
	t.Setenv("REFERENCE_S3_BUCKET", e.Bucket)
	t.Setenv("REFERENCE_S3_REGION", e.Region)
	t.Setenv("REFERENCE_S3_ACCESS_KEY", "synthetic-key")
	t.Setenv("REFERENCE_S3_SECRET_KEY", "synthetic-secret")
	t.Setenv("REFERENCE_S3_SESSION_TOKEN", "")
	if err := p.configureReferenceBridge(); err != nil {
		t.Fatal(err)
	}
	selection := RuntimeSelection{TenantID: e.TenantID, ModelID: e.ModelConfigID, Version: e.ModelConfigVersion}
	if p.referenceBridgeFor(selection, e.Model) == nil {
		t.Fatal("valid grant rejected")
	}
	for _, mutate := range []func(*ReferenceBridgeEntitlement){func(e *ReferenceBridgeEntitlement) { e.TenantID = "other" }, func(e *ReferenceBridgeEntitlement) { e.ModelConfigID = "other" }, func(e *ReferenceBridgeEntitlement) { e.ModelConfigVersion++ }, func(e *ReferenceBridgeEntitlement) { e.Model = "other" }, func(e *ReferenceBridgeEntitlement) { e.ExpiresAt = time.Now().Add(-time.Second) }} {
		changed := e
		mutate(&changed)
		p.ReferenceBridgeEntitlements = []ReferenceBridgeEntitlement{changed}
		if p.referenceBridgeFor(selection, e.Model) != nil {
			t.Fatal("mismatched grant accepted")
		}
	}
	p.ReferenceBridgeEntitlements = []ReferenceBridgeEntitlement{e, e}
	if p.configureReferenceBridge() == nil {
		t.Fatal("duplicate")
	}
	p.ReferenceBridgeEntitlements = []ReferenceBridgeEntitlement{e}
	t.Setenv("REFERENCE_S3_BUCKET", "other-bucket")
	if p.configureReferenceBridge() == nil {
		t.Fatal("destination silently changed")
	}
}

func TestManagedJimengReferenceBridgeAndRevocation(t *testing.T) {
	r, old, in := runtimeFixture(t)
	in.Provider, in.Model, in.Capability = "jimeng-images", "jimeng_seedream46_cvtob", "image"
	in.Region = "cn-north-1"
	report, err := r.auth.models.update(old.TenantID, old.Version, in, ModelCredentialInput{Action: "set", Secret: `{"access_key":"synthetic-ak","secret_key":"synthetic-sk"}`})
	if err != nil {
		t.Fatal(err)
	}
	sel, entry, err := r.Resolve(context.Background(), old.TenantID, old.ActorID, in.ID, "image")
	if err != nil {
		t.Fatal(err)
	}
	if r.imageReferenceSupported(sel, entry.Provider, entry.Model) {
		t.Fatal("default bridge open")
	}
	bridge := &fixtureBridge{}
	policy := &ExecutionPolicy{referenceBridge: bridge, ReferenceBridgeEntitlements: []ReferenceBridgeEntitlement{{TenantID: old.TenantID, ModelConfigID: in.ID, ModelConfigVersion: report.Version, Model: in.Model, Source: "synthetic consent", ExpiresAt: time.Now().Add(time.Hour)}}}
	r.referenceBridge = policy.referenceBridgeFor
	if !r.imageReferenceSupported(sel, entry.Provider, entry.Model) {
		t.Fatal("eligible reference rejected")
	}
	var retained ReferenceURLBridge
	calls := 0
	err = r.Execute(context.Background(), sel, func(ctx context.Context, providers RuntimeProviders) error {
		p := providers.Image.(runtimeJimengImage).p
		retained = p.bridge
		transport := p.client.Transport.(runtimeTransport)
		transport.base = runtimeFixtureTransport(func(req *http.Request) (*http.Response, error) {
			calls++
			raw, _ := io.ReadAll(req.Body)
			if !strings.Contains(string(raw), "image_urls") {
				t.Fatal("reference omitted")
			}
			return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"code":10000,"data":{"task_id":"synthetic-task"}}`))}, nil
		})
		p.client.Transport = transport
		input := Input{Kind: "general", Prompt: "synthetic scene", Ratio: "16:9", Style: "natural"}
		_, err := p.SubmitImageWithReference(ctx, input, syntheticCreativePNG(), "image/png")
		if err != nil {
			return err
		}
		grant := policy.ReferenceBridgeEntitlements[0]
		policy.ReferenceBridgeEntitlements[0].ExpiresAt = time.Now().Add(-time.Second)
		if _, denied := p.SubmitImage(ctx, input); denied == nil {
			t.Fatal("supplier send escaped expired transfer grant")
		}
		policy.ReferenceBridgeEntitlements[0] = grant
		return nil
	})
	if err != nil || calls != 1 || bridge.calls != 1 {
		t.Fatal(err, calls, bridge.calls)
	}
	if _, err = retained.ReferenceURL(context.Background(), nil, "image/png"); err == nil || bridge.calls != 1 {
		t.Fatal("retained bridge escaped lifetime", err)
	}
	policy.referenceBridge = referenceBridgeFunc(func(context.Context, []byte, string) (string, error) {
		policy.ReferenceBridgeEntitlements = nil
		return "https://objects.example.test/reference", nil
	})
	err = r.Execute(context.Background(), sel, func(ctx context.Context, p RuntimeProviders) error {
		_, err := p.Image.(ReferenceImageProvider).GenerateWithReference(ctx, Input{Kind: "general", Prompt: "synthetic scene", Ratio: "16:9", Style: "natural"}, syntheticCreativePNG(), "image/png")
		return err
	})
	var noDispatch *RuntimeNoDispatchError
	if err == nil || errors.As(err, &noDispatch) || calls != 1 {
		t.Fatal("revocation or uncertain storage attempt lost", err, calls)
	}
}
func TestGuardedReferenceBridgeDenyBeforeStorageAndCancel(t *testing.T) {
	calls, attempts := 0, 0
	b := guardedReferenceBridge{lifetime: context.Background(), guard: func(context.Context) error { return errRuntimeDenied }, attempted: func() { attempts++ }, bridge: func() ReferenceObjectBridge {
		return referenceBridgeFunc(func(context.Context, []byte, string) (string, error) { calls++; return "", nil })
	}}
	if _, err := b.ReferenceURL(context.Background(), nil, ""); err == nil || calls != 0 || attempts != 0 {
		t.Fatal("unauthorized storage", err)
	}
	b.guard = func(context.Context) error { return nil }
	if _, err := b.ReferenceURL(runtimeRecoveryContext("job"), nil, ""); err == nil || calls != 0 {
		t.Fatal("recovery upload")
	}
	ctx, cancel := context.WithCancel(context.Background())
	b.lifetime = ctx
	b.bridge = func() ReferenceObjectBridge {
		return referenceBridgeFunc(func(ctx context.Context, _ []byte, _ string) (string, error) {
			cancel()
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(time.Second):
				t.Fatal("not cancelled")
				return "", nil
			}
		})
	}
	if _, err := b.ReferenceURL(context.Background(), nil, ""); err == nil {
		t.Fatal("cancelled upload succeeded")
	}
}
