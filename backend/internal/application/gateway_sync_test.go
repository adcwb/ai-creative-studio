package application

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestDeploymentKeyMatchesPersistedCiphertexts(t *testing.T) {
	runtime, _, _ := gatewayFixture(t, "https://synthetic.example/v1")
	s := runtime.auth.models
	if err := s.verifyDeploymentModelKey(); err != nil {
		t.Fatal("correct key rejected", err)
	}
	wrong, err := OpenModelConfigStore(filepath.Dir(s.path), bytes.Repeat([]byte{22}, 32))
	if err != nil {
		t.Fatal(err)
	}
	if err = wrong.verifyDeploymentModelKey(); !errors.Is(err, errModelKey) {
		t.Fatal("mismatched deployment key accepted", err)
	}
	missing, err := OpenModelConfigStore(filepath.Dir(s.path), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = missing.verifyDeploymentModelKey(); !errors.Is(err, errModelKey) {
		t.Fatal("missing deployment key accepted with ciphertext", err)
	}
}

func TestGatewayWithoutKeyAllowsMetadataButRejectsCredentials(t *testing.T) {
	s, err := OpenModelConfigStore(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.verifyDeploymentModelKey(); err != nil {
		t.Fatal("empty store should remain usable without credentials", err)
	}
	g := AIGateway{Name: "Metadata only", BaseURL: "https://synthetic.example/v1", Enabled: true, TimeoutSeconds: 30}
	if _, err := s.saveGateway("tenant-a", 0, g, ModelCredentialInput{Action: "set", Secret: "synthetic-secret"}); !errors.Is(err, errModelKey) {
		t.Fatal("credential stored without encryption key", err)
	}
	report, err := s.saveGateway("tenant-a", 0, g, ModelCredentialInput{Action: "keep"})
	if err != nil || report.SecretStorageAvailable || len(report.Gateways) != 1 || report.Gateways[0].SecretStatus != "not_configured" {
		t.Fatal("metadata-only mode broken", report, err)
	}
}

func TestGatewaySyncPreservesConfigurationAndDeduplicates(t *testing.T) {
	var fail atomic.Bool
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != "GET" || r.URL.Path != "/v1/models" || r.Header.Get("Authorization") != "Bearer synthetic-gateway-key" {
			t.Error("incorrect catalog request")
			w.WriteHeader(400)
			return
		}
		if fail.Load() {
			w.WriteHeader(401)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"vendor/shared-model"},{"id":"vendor/new-image"},{"id":"vendor/new-image"}]}`))
	}))
	defer upstream.Close()
	u, _ := url.Parse(upstream.URL)
	t.Setenv("MODEL_GATEWAY_ALLOWED_HOSTS", u.Host)
	runtime, g, original := gatewayFixture(t, upstream.URL+"/v1")
	s := runtime.auth.models
	result, err := s.syncGatewayModels(context.Background(), "tenant-a", g.ID, g.Version)
	if err != nil || result.Added != 1 || result.Existing != 1 || len(result.Report.Models) != 2 {
		t.Fatal("import failed", result, err)
	}
	for _, m := range result.Report.Models {
		if m.ID == original.ID && !reflect.DeepEqual(m, original) {
			t.Fatal("existing model changed")
		}
		if m.ID != original.ID && (!m.Pending || m.Enabled || len(m.Capabilities) != 0 || m.MaximumMinor != 0) {
			t.Fatal("unverified model became available")
		}
	}
	if result.Report.Gateways[0].Version != g.Version || result.Report.Gateways[0].LastSyncedAt == nil {
		t.Fatal("sync invalidated gateway version")
	}
	repeated, err := s.syncGatewayModels(context.Background(), "tenant-a", g.ID, g.Version)
	if err != nil || repeated.Added != 0 || len(repeated.Report.Models) != 2 {
		t.Fatal("duplicate import", err)
	}
	fail.Store(true)
	if _, err = s.syncGatewayModels(context.Background(), "tenant-a", g.ID, g.Version); !errors.Is(err, errGatewayCatalog) {
		t.Fatal("failure accepted", err)
	}
	report, _ := s.managementReport("tenant-a")
	if len(report.Models) != 2 || report.Gateways[0].SyncError == "" {
		t.Fatal("failed sync changed model set")
	}
	if _, err = s.syncGatewayModels(context.Background(), "another-tenant", g.ID, g.Version); !errors.Is(err, ErrNotFound) {
		t.Fatal("cross-tenant sync accepted")
	}
	g = report.Gateways[0]
	g.SyncIntervalMinutes = 5
	_, err = s.saveGateway("tenant-a", g.Version, g, ModelCredentialInput{Action: "keep"})
	if err != nil {
		t.Fatal(err)
	}
	before := calls.Load()
	s.syncDueGateways(context.Background(), time.Now().UTC())
	if calls.Load() != before {
		t.Fatal("scheduler ignored last attempt")
	}
	s.syncDueGateways(context.Background(), time.Now().UTC().Add(6*time.Minute))
	if calls.Load() != before+1 {
		t.Fatal("scheduler did not run due gateway")
	}
}

func TestGatewayCatalogRejectsMalformedAndPartialData(t *testing.T) {
	var body string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) }))
	defer upstream.Close()
	u, _ := url.Parse(upstream.URL)
	t.Setenv("MODEL_GATEWAY_ALLOWED_HOSTS", u.Host)
	runtime, g, _ := gatewayFixture(t, upstream.URL+"/v1")
	for _, raw := range []string{`{}`, `{"data":null}`, `{"data":[{"id":"valid"},{"id":"bad model"}]}`, `not-json`} {
		body = raw
		if _, err := runtime.auth.models.syncGatewayModels(context.Background(), "tenant-a", g.ID, g.Version); !errors.Is(err, errGatewayCatalog) {
			t.Fatal("invalid catalog imported", raw, err)
		}
	}
	report, _ := runtime.auth.models.managementReport("tenant-a")
	if len(report.Models) != 1 {
		t.Fatal("partial import committed")
	}
}

func TestGatewaySyncRecordsIncompleteAndOversizeCatalogFailures(t *testing.T) {
	for _, mode := range []string{"oversize", "incomplete"} {
		t.Run(mode, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if mode == "incomplete" {
					w.Header().Set("Content-Length", "1000")
					fmt.Fprint(w, `{"data":[{"id":"partial"}]}`)
					return
				}
				fmt.Fprint(w, strings.Repeat(" ", (1<<20)+1))
			}))
			defer upstream.Close()
			u, _ := url.Parse(upstream.URL)
			t.Setenv("MODEL_GATEWAY_ALLOWED_HOSTS", u.Host)
			runtime, g, original := gatewayFixture(t, upstream.URL+"/v1")
			s := runtime.auth.models
			if _, err := s.syncGatewayModels(context.Background(), "tenant-a", g.ID, g.Version); !errors.Is(err, errGatewayCatalog) {
				t.Fatal("catalog failure not reported", err)
			}
			report, _ := s.managementReport("tenant-a")
			if len(report.Models) != 1 || !reflect.DeepEqual(report.Models[0], original) || report.Gateways[0].SyncAttemptedAt == nil || report.Gateways[0].SyncError == "" {
				t.Fatal("failure changed model or lost retry state", report)
			}
			reopened, err := OpenModelConfigStore(filepath.Dir(s.path), nil)
			if err != nil {
				t.Fatal(err)
			}
			persisted, _ := reopened.managementReport("tenant-a")
			if !reflect.DeepEqual(report.Gateways, persisted.Gateways) || !reflect.DeepEqual(report.Models, persisted.Models) {
				t.Fatal("failure state not persisted")
			}
		})
	}
}

func TestGatewaySyncRejectsConcurrentGatewayEdit(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		fmt.Fprint(w, `{"data":[{"id":"new-model"}]}`)
	}))
	defer upstream.Close()
	defer close(release)
	u, _ := url.Parse(upstream.URL)
	t.Setenv("MODEL_GATEWAY_ALLOWED_HOSTS", u.Host)
	runtime, g, original := gatewayFixture(t, upstream.URL+"/v1")
	s := runtime.auth.models
	done := make(chan error, 1)
	go func() { _, err := s.syncGatewayModels(context.Background(), "tenant-a", g.ID, g.Version); done <- err }()
	<-entered
	g.Enabled = false
	if _, err := s.saveGateway("tenant-a", g.Version, g, ModelCredentialInput{Action: "keep"}); err != nil {
		t.Fatal(err)
	}
	release <- struct{}{}
	if err := <-done; !errors.Is(err, errModelConflict) {
		t.Fatal("stale catalog committed", err)
	}
	report, _ := s.managementReport("tenant-a")
	if len(report.Models) != 1 || !reflect.DeepEqual(report.Models[0], original) || report.Gateways[0].LastSyncedAt != nil {
		t.Fatal("stale sync mutated state")
	}
}

func TestGatewaySyncCommitFailureLeavesSnapshotUnchanged(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"data":[{"id":"new-model"}]}`) }))
	defer upstream.Close()
	u, _ := url.Parse(upstream.URL)
	t.Setenv("MODEL_GATEWAY_ALLOWED_HOSTS", u.Host)
	runtime, g, _ := gatewayFixture(t, upstream.URL+"/v1")
	s := runtime.auth.models
	before, _ := s.managementReport("tenant-a")
	originalPath := s.path
	s.path = filepath.Join(t.TempDir(), "directory")
	if err := os.Mkdir(s.path, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := s.syncGatewayModels(context.Background(), "tenant-a", g.ID, g.Version); err == nil {
		t.Fatal("failed persistence reported success")
	}
	s.path = originalPath
	after, _ := s.managementReport("tenant-a")
	if !reflect.DeepEqual(before, after) {
		t.Fatal("failed commit published partial state")
	}
}

func TestGatewaySyncCapacityFailureDoesNotImportPartialCatalog(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ids := make([]map[string]string, 200)
		for i := range ids {
			ids[i] = map[string]string{"id": fmt.Sprintf("vendor/new-%d", i)}
		}
		json.NewEncoder(w).Encode(map[string]any{"data": ids})
	}))
	defer upstream.Close()
	u, _ := url.Parse(upstream.URL)
	t.Setenv("MODEL_GATEWAY_ALLOWED_HOSTS", u.Host)
	runtime, g, original := gatewayFixture(t, upstream.URL+"/v1")
	s := runtime.auth.models
	if _, err := s.syncGatewayModels(context.Background(), "tenant-a", g.ID, g.Version); !errors.Is(err, errGatewayCapacity) {
		t.Fatal("capacity overflow accepted", err)
	}
	report, _ := s.managementReport("tenant-a")
	if len(report.Models) != 1 || !reflect.DeepEqual(report.Models[0], original) || report.Gateways[0].SyncError != "gateway_model_capacity" || report.Gateways[0].LastSyncedAt != nil {
		t.Fatal("capacity failure imported or removed models", report)
	}
}
