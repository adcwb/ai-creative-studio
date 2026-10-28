package application

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func musicRuntimeFixture(t *testing.T) (*ModelRuntime, RuntimeSelection, ModelConfigInput, *ExecutionPolicy, *ExecutionGovernor) {
	t.Helper()
	r, old, in := runtimeFixture(t)
	in.Provider, in.Model, in.Capability = "minimax-music", "music-3.0", "music"
	report, err := r.auth.models.update(old.TenantID, old.Version, in, ModelCredentialInput{Action: "set", Secret: "synthetic-music-secret"})
	if err != nil {
		t.Fatal(err)
	}
	policy := &ExecutionPolicy{MusicEntitlements: []MusicEntitlement{{TenantID: old.TenantID, ModelConfigID: in.ID, ModelConfigVersion: report.Version, Model: in.Model, Contract: "minimax-existing-paid-account-v1", Source: "synthetic eligibility fixture", ExpiresAt: time.Now().Add(time.Hour)}}}
	if _, _, err = r.Resolve(context.Background(), old.TenantID, old.ActorID, in.ID, "music"); err == nil {
		t.Fatal("music enabled without operator eligibility")
	}
	r.musicEntitled = policy.MusicEntitled
	sel, _, err := r.Resolve(context.Background(), old.TenantID, old.ActorID, in.ID, "music")
	if err != nil {
		t.Fatal(err)
	}
	sel.Currency, sel.MaximumMinor = "USD", 30
	ledger, err := OpenBudgetLedger(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = ledger.SetLimit(sel.TenantID, "USD", 100); err != nil {
		t.Fatal(err)
	}
	g := &ExecutionGovernor{Ledger: ledger, Quoter: policy, Moderator: testExecutionModerator{true}}
	r.authorize = g.AuthorizeRuntime
	return r, sel, in, policy, g
}

func musicRequest(policy *ExecutionPolicy, sel *RuntimeSelection, id string) ExecutionRequest {
	req := ExecutionRequest{ID: id, Tenant: sel.TenantID, Provider: "minimax-music", Model: "music-3.0", Capability: "music", Text: "Synthetic instrumental score", Selection: sel}
	bindMusicQuote(policy, &req)
	return req
}
func bindMusicQuote(policy *ExecutionPolicy, req *ExecutionRequest) {
	digest := ExecutionRequestDigest(*req)
	req.Selection.RequestDigest = digest
	policy.Prices = []ExecutionPriceRule{{RequestDigest: digest, Provider: req.Provider, Model: req.Model, Capability: "music", Currency: "USD", MaximumMinor: 30, MaximumInputBytes: 8192, Source: "synthetic fixed request", ExpiresAt: time.Now().Add(time.Hour)}}
}
func replaceMusicTransport(p RuntimeProviders, f runtimeFixtureTransport) {
	client := p.Music.(*MiniMaxMusicProvider).transport.client
	transport := client.Transport.(runtimeTransport)
	transport.base = f
	client.Transport = transport
}
func musicFixtureResponse(data []byte) *http.Response {
	body, _ := json.Marshal(map[string]any{"data": map[string]any{"status": 2, "audio": hex.EncodeToString(data)}, "base_resp": map[string]int{"status_code": 0}})
	return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(body))}
}

func TestMusicEntitlementStrictBindingAndExpiry(t *testing.T) {
	r, sel, _, policy, _ := musicRuntimeFixture(t)
	_, entry, err := r.Resolve(context.Background(), sel.TenantID, sel.ActorID, sel.ModelID, "music")
	if err != nil {
		t.Fatal(err)
	}
	original := policy.MusicEntitlements[0]
	for _, change := range []func(*MusicEntitlement){func(e *MusicEntitlement) { e.TenantID = "other" }, func(e *MusicEntitlement) { e.ModelConfigID = "other" }, func(e *MusicEntitlement) { e.ModelConfigVersion++ }, func(e *MusicEntitlement) { e.Model = "music-2.6" }, func(e *MusicEntitlement) { e.Contract = "tenant-asserted" }, func(e *MusicEntitlement) { e.Source = "" }, func(e *MusicEntitlement) { e.ExpiresAt = time.Now().Add(-time.Second) }} {
		v := original
		change(&v)
		policy.MusicEntitlements = []MusicEntitlement{v}
		if policy.MusicEntitled(sel.TenantID, sel.Version, entry) {
			t.Fatal("invalid eligibility allowed")
		}
	}
	policy.MusicEntitlements = []MusicEntitlement{original}
	if validateMusicEntitlements(policy.MusicEntitlements) != nil {
		t.Fatal("valid eligibility rejected")
	}
	if validateMusicEntitlements([]MusicEntitlement{original, original}) == nil {
		t.Fatal("duplicate qualification accepted")
	}
	if _, _, err = r.Resolve(context.Background(), sel.TenantID, "other-actor", sel.ModelID, "music"); err == nil {
		t.Fatal("eligibility bypassed member authorization")
	}
	if _, _, err = r.Resolve(context.Background(), sel.TenantID, sel.ActorID, sel.ModelID, "speech"); err == nil {
		t.Fatal("eligibility bypassed capability")
	}
}

func TestMusicGovernanceNativePayloadUnknownAndNoReplay(t *testing.T) {
	r, sel, _, policy, g := musicRuntimeFixture(t)
	req := musicRequest(policy, &sel, "music:fixture")
	sends := 0
	var retained MusicProvider
	run := func(ctx context.Context, p RuntimeProviders) error {
		retained = p.Music
		replaceMusicTransport(p, func(request *http.Request) (*http.Response, error) {
			sends++
			if request.URL.String() != "https://api.minimax.io/v1/music_generation" || request.Method != "POST" || request.Header.Get("Authorization") != "Bearer synthetic-music-secret" {
				t.Fatal("incorrect endpoint/credential")
			}
			var payload map[string]any
			if json.NewDecoder(request.Body).Decode(&payload) != nil || payload["model"] != "music-3.0" || payload["is_instrumental"] != true || payload["stream"] != false || payload["output_format"] != "hex" {
				t.Fatal("unbounded or wrong music protocol")
			}
			if _, ok := payload["lyrics"]; ok {
				t.Fatal("instrumental request unexpectedly includes lyrics")
			}
			entries, _ := g.Ledger.Entries(sel.TenantID, "USD")
			if len(entries) != 1 || entries[0].State != "unknown" {
				t.Fatal("send preceded durable reservation")
			}
			return musicFixtureResponse([]byte("ID3synthetic fixture")), nil
		})
		data, err := p.Music.Music(ctx, req.Text)
		if err != nil {
			return err
		}
		return g.ReviewResult(ctx, ExecutionRequest{Tenant: req.Tenant, Capability: "music", Provider: req.Provider, Model: req.Model, HasMedia: true, Media: []ExecutionMedia{{Data: data, MIME: "audio/mpeg"}}})
	}
	if err := g.ExecuteRuntime(context.Background(), r, req, run); err != nil {
		t.Fatal(err)
	}
	entries, _ := g.Ledger.Entries(sel.TenantID, "USD")
	if len(entries) != 1 || entries[0].State != "unknown" || entries[0].Reserved != 30 {
		t.Fatal("successful transport incorrectly settled", entries)
	}
	if err := g.ExecuteRuntime(context.Background(), r, req, run); err == nil || sends != 1 {
		t.Fatal("duplicate paid request", err, sends)
	}
	if _, err := retained.Music(context.Background(), req.Text); err == nil || sends != 1 {
		t.Fatal("escaped runtime adapter")
	}
	other, _ := g.Ledger.Entries(sel.TenantID, "CNY")
	if len(other) != 0 {
		t.Fatal("currency leakage")
	}
}

func TestMusicGovernanceRejectsChangedQuoteCurrencyAndModeration(t *testing.T) {
	for _, mode := range []string{"prompt", "currency", "moderation", "budget", "eligibility"} {
		t.Run(mode, func(t *testing.T) {
			r, sel, _, policy, g := musicRuntimeFixture(t)
			req := musicRequest(policy, &sel, "music:denied")
			switch mode {
			case "prompt":
				req.Text += " changed"
			case "currency":
				sel.Currency = "CNY"
			case "moderation":
				g.Moderator = testExecutionModerator{}
			case "budget":
				sel.MaximumMinor = 29
			case "eligibility":
				policy.MusicEntitlements = nil
			}
			if err := g.ExecuteRuntime(context.Background(), r, req, func(context.Context, RuntimeProviders) error { t.Fatal("denied operation executed"); return nil }); err == nil {
				t.Fatal("governance bypass")
			}
			entries, _ := g.Ledger.Entries(sel.TenantID, "USD")
			if len(entries) != 0 {
				t.Fatal("rejected preflight consumed budget")
			}
		})
	}
}

func TestMusicRevocationBeforeSendReleasesOnlyLocalNoDispatch(t *testing.T) {
	r, sel, in, policy, g := musicRuntimeFixture(t)
	req := musicRequest(policy, &sel, "music:revoke")
	err := g.ExecuteRuntime(context.Background(), r, req, func(ctx context.Context, p RuntimeProviders) error {
		replaceMusicTransport(p, func(*http.Request) (*http.Response, error) { t.Fatal("revoked music sent"); return nil, nil })
		if _, e := r.auth.models.update(sel.TenantID, sel.Version, in, ModelCredentialInput{Action: "revoke"}); e != nil {
			t.Fatal(e)
		}
		_, e := p.Music.Music(ctx, req.Text)
		return e
	})
	entries, _ := g.Ledger.Entries(sel.TenantID, "USD")
	if err == nil || len(entries) != 1 || entries[0].State != "released" {
		t.Fatal("proven local failure not released", err, entries)
	}
}

func TestMusicEligibilityWithdrawnDuringSendCancelsAndKeepsUnknown(t *testing.T) {
	r, sel, _, policy, g := musicRuntimeFixture(t)
	req := musicRequest(policy, &sel, "music:cancel")
	var entitled atomic.Bool
	entitled.Store(true)
	r.musicEntitled = func(tenant string, version int64, entry ModelConfigEntry) bool {
		return entitled.Load() && policy.MusicEntitled(tenant, version, entry)
	}
	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- g.ExecuteRuntime(context.Background(), r, req, func(ctx context.Context, p RuntimeProviders) error {
			replaceMusicTransport(p, func(req *http.Request) (*http.Response, error) {
				close(started)
				<-req.Context().Done()
				return nil, req.Context().Err()
			})
			_, err := p.Music.Music(ctx, req.Text)
			return err
		})
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("synthetic transport never started")
	}
	entitled.Store(false)
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("withdrawal ignored")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("withdrawn eligibility did not cancel")
	}
	entries, _ := g.Ledger.Entries(sel.TenantID, "USD")
	if len(entries) != 1 || entries[0].State != "unknown" {
		t.Fatal("attempted music incorrectly refunded", entries)
	}
}

type musicOutputReview struct{ denyOutput bool }

func (m musicOutputReview) ReviewExecution(_ context.Context, req ExecutionRequest) (ModerationDecision, error) {
	if req.Stage == "output" && (!req.HasMedia || len(req.Media) != 1 || req.Media[0].MIME != "audio/mpeg" || len(req.Media[0].Data) == 0) {
		return ModerationDecision{}, errors.New("missing audio review")
	}
	status := "allow"
	if req.Stage == "output" && m.denyOutput {
		status = "review"
	}
	return ModerationDecision{Provider: "fixture", Model: "synthetic", Status: status, CheckedAt: time.Now()}, nil
}

func TestMusicWorkerPublishesOnlyReviewedAudio(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg unavailable")
	}
	path := filepath.Join(t.TempDir(), "synthetic.mp3")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := ffmpeg(ctx, "-f", "lavfi", "-i", "sine=frequency=330:duration=0.5", "-c:a", "libmp3lame", "-f", "mp3", path); err != nil {
		t.Fatal(err)
	}
	mp3, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, deny := range []bool{false, true} {
		t.Run(map[bool]string{false: "allow", true: "deny"}[deny], func(t *testing.T) {
			r, sel, _, policy, g := musicRuntimeFixture(t)
			g.Moderator = musicOutputReview{deny}
			s := testStore(t)
			ConfigureExecutionGovernance(s, g)
			s.runtime = r
			project, _, err := s.CreateProject(sel.TenantID, "music-project", ProjectInput{Title: "Synthetic music", Brief: "Local synthesized tone only"})
			if err != nil {
				t.Fatal(err)
			}
			job := CreativeJob{ID: "synthetic-music-job", ProjectID: project.ID, SpaceID: sel.TenantID, Kind: "music", Provider: "minimax-music", Model: "music-3.0", Prompt: "Synthetic instrumental score", Source: ProjectInput{Title: project.Title, Brief: project.Brief}, RuntimeSelection: &sel}
			req, err := s.creativeExecutionRequest(job)
			if err != nil {
				t.Fatal(err)
			}
			bindMusicQuote(policy, &req)
			err = g.ExecuteRuntime(context.Background(), r, req, func(ctx context.Context, p RuntimeProviders) error {
				replaceMusicTransport(p, func(*http.Request) (*http.Response, error) { return musicFixtureResponse(mp3), nil })
				return s.executeCreativeJobWith(ctx, &job, &p)
			})
			if deny {
				if err == nil || job.AssetID != "" {
					t.Fatal("unreviewed audio published")
				}
			} else {
				if err != nil || job.AssetID == "" {
					t.Fatal("reviewed audio not ingested", err)
				}
				asset, e := s.media.AssetForReceipt(project.ID, sel.TenantID, "creative-"+job.ID)
				if e != nil || asset.Kind != "audio" || asset.Role != "music" {
					t.Fatal("wrong music asset", asset, e)
				}
			}
			entries, _ := g.Ledger.Entries(sel.TenantID, "USD")
			if len(entries) != 1 || entries[0].State != "unknown" {
				t.Fatal("output status replaced billing evidence")
			}
		})
	}
}

func TestPostgresMusicAcceptanceReservesAndQueuesAtomically(t *testing.T) {
	db := postgresTestDB(t)
	s := postgresTestStore(t, db)
	auth, err := OpenPostgresAuthStore(t.TempDir(), db)
	if err != nil {
		t.Fatal(err)
	}
	user, _, _, err := auth.login("music-atomic@example.test", "synthetic-music-password", true)
	if err != nil {
		t.Fatal(err)
	}
	tenant := auth.spaces(user.ID)[0]["id"]
	auth.models, err = OpenPostgresModelConfigStore(t.TempDir(), db, bytes.Repeat([]byte{41}, 32))
	if err != nil {
		t.Fatal(err)
	}
	input := ModelConfigInput{Provider: "minimax-music", Model: "music-3.0", Capability: "music", Region: "global", Enabled: true, Lifecycle: "active"}
	report, err := auth.models.update(tenant, 0, input, ModelCredentialInput{Action: "set", Secret: "synthetic-music-secret"})
	if err != nil {
		t.Fatal(err)
	}
	entry := report.Entries[0]
	policy := &ExecutionPolicy{MusicEntitlements: []MusicEntitlement{{TenantID: tenant, ModelConfigID: entry.ID, ModelConfigVersion: report.Version, Model: entry.Model, Contract: "minimax-existing-paid-account-v1", Source: "synthetic account fixture", ExpiresAt: time.Now().Add(time.Hour)}}}
	ledger, err := OpenPostgresBudgetLedger(db)
	if err != nil {
		t.Fatal(err)
	}
	if err = ledger.SetLimit(tenant, "USD", 30); err != nil {
		t.Fatal(err)
	}
	governor := &ExecutionGovernor{Ledger: ledger, Quoter: policy, Moderator: testExecutionModerator{true}}
	ConfigureExecutionGovernance(s, governor)
	ConfigureManagedRuntime(s, auth)
	sel, _, err := s.runtime.Resolve(context.Background(), tenant, user.ID, entry.ID, "music")
	if err != nil {
		t.Fatal(err)
	}
	sel.Currency, sel.MaximumMinor = "USD", 20
	project, _, err := s.CreateProject(tenant, "music-pg-project", ProjectInput{Title: "Synthetic music", Brief: "Local fixture only"})
	if err != nil {
		t.Fatal(err)
	}
	in := CreativeJobInput{Kind: "music", Prompt: "Synthetic instrumental score", ExpectedVersion: project.Version, ModelRequest: ModelRequest{selection: &sel, selectedCaps: Capabilities{Provider: entry.Provider, Model: entry.Model, Mode: "real", Ready: true}}}
	job, created, err := s.createCreativeJob(tenant, project.ID, "music-pg-accept", in)
	if err != nil || !created {
		t.Fatal(err)
	}
	replay, created, err := s.createCreativeJob(tenant, project.ID, "music-pg-accept", in)
	if err != nil || created || replay.ID != job.ID {
		t.Fatal("music idempotency failed", err)
	}
	if _, _, err = s.createCreativeJob(tenant, project.ID, "music-pg-overbudget", in); err == nil {
		t.Fatal("music exceeded original-currency budget")
	}
	var count int
	if err = db.QueryRow("SELECT count(*) FROM execution_jobs WHERE tenant_id=$1 AND kind='creative'", tenant).Scan(&count); err != nil || count != 1 {
		t.Fatal("budget rejection left queue job", count, err)
	}
	entries, err := ledger.Entries(tenant, "USD")
	if err != nil || len(entries) != 1 || entries[0].Reserved != 20 || entries[0].State != "reserved" {
		t.Fatal(entries, err)
	}
	other, err := ledger.Entries(tenant, "CNY")
	if err != nil || len(other) != 0 {
		t.Fatal("cross-currency reservation", other, err)
	}
	if _, err = s.cancelCreativeJob(tenant, project.ID, job.ID, "music-pg-cancel"); err != nil {
		t.Fatal(err)
	}
	entries, err = ledger.Entries(tenant, "USD")
	if err != nil || entries[0].State != "released" {
		t.Fatal("undispatched cancellation held budget", entries, err)
	}
}
