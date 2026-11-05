package application

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/library"
	"github.com/adcwb/ai-creative-studio/backend/internal/modules/media"
	"github.com/adcwb/ai-creative-studio/backend/internal/persistence/libraryrepo"
	"github.com/adcwb/ai-creative-studio/backend/internal/platform/jobqueue"
)

func alignmentLineageFixture(t *testing.T, s *FileStore) (Project, libraryrepo.Result, AlignmentInput) {
	t.Helper()
	p, card, _ := libraryDispatchFixture(t, s)
	if _, err := s.media.Demo(p.ID, p.SpaceID, "alignment-fixture", s.media.Get(p.ID, p.SpaceID).Version, nil); err != nil {
		t.Fatal(err)
	}
	// Simulate a trusted generation result with an immutable ancestor card.
	// No public API accepts this ancestry; the audio is synthesized locally.
	ctx := context.WithValue(context.Background(), libraryOutputSourcesKey{}, []media.LibrarySource{{AssetID: card.Asset.ID, VersionID: card.Version.ID}})
	state, err := s.media.addAssetContext(ctx, p.ID, p.SpaceID, "alignment-derived-audio", "合成配音", "voiceover", "", "", "openai-speech", syntheticWAV(2), "audio/wav")
	if err != nil {
		t.Fatal(err)
	}
	audio := state.Assets[len(state.Assets)-1].ID
	state.Timeline.Clips[0].AudioAssetID = audio
	state, err = s.media.SaveTimeline(p.ID, p.SpaceID, "alignment-audio-timeline", state.Version, state.Timeline)
	if err != nil {
		t.Fatal(err)
	}
	in := AlignmentInput{Mode: "asr", ClipID: state.Timeline.Clips[0].ID, AudioAssetID: audio, ExpectedVersion: state.Version, ExpectedProvider: "openai-asr", ExpectedModel: "whisper-1"}
	return p, card, in
}

func alignmentAnalysisGrant(t *testing.T, s *FileStore, card libraryrepo.Result, expected int64, key string) {
	t.Helper()
	in := library.AuthorizationInput{Expected: expected, Status: "granted", VersionIDs: []string{card.Version.ID}, Purposes: []string{"audio_analysis"}, Holder: "Synthetic creator", Basis: "own_work", Evidence: "Locally synthesized fixture", Attested: true}
	intentLibraryCall(t, s, "POST", "/api/v1/library/assets/"+card.Asset.ID+"/use-authorization?space_id="+card.Asset.SpaceID, key, in, 200)
}

// Hold file work before execution; PostgreSQL naturally waits for a leased
// worker. Both variants use the production persistence and execution paths.
func pauseAlignment(t *testing.T, s *FileStore) func(string) AlignmentTask {
	t.Helper()
	a := s.media.alignment
	var release func()
	if a.pg == nil {
		if a.jobs == nil {
			a.jobs = newLocalJobs(1)
		}
		gate := make(chan struct{})
		var once sync.Once
		release = func() { once.Do(func() { close(gate) }) }
		a.jobs.submit(func() { <-gate })
		t.Cleanup(release)
	}
	return func(id string) AlignmentTask {
		t.Helper()
		if release != nil {
			release()
			a.jobs.wait()
		} else {
			q := jobqueue.Queue{DB: a.pg.db}
			j, ok, err := q.Claim(context.Background(), "alignment")
			if err != nil || !ok || j.ID != id {
				t.Fatal("alignment lease", j, err)
			}
			if err = q.Dispatch(context.Background(), j); err != nil {
				t.Fatal(err)
			}
			if err = a.runTask(context.Background(), s.media, id, &j); err != nil {
				t.Fatal(err)
			}
			if _, ok, err = q.Claim(context.Background(), "alignment"); err != nil || ok {
				t.Fatal("completed task was queued again", ok, err)
			}
		}
		a.mu.Lock()
		defer a.mu.Unlock()
		return cloneAlignmentTask(a.data.Tasks[id])
	}
}

func TestAlignmentLineagePinsRevocationAndFreshIntent(t *testing.T) {
	for _, mode := range []string{"file", "postgres"} {
		t.Run(mode, func(t *testing.T) {
			s := testStore(t)
			if mode == "postgres" {
				s = postgresTestStore(t, postgresTestDB(t))
			}
			p, card, in := alignmentLineageFixture(t, s)
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.URL.Path != "/audio/transcriptions" || r.Method != "POST" || r.ParseMultipartForm(25<<20) != nil {
					t.Error("ASR multipart route")
					w.WriteHeader(400)
					return
				}
				defer r.MultipartForm.RemoveAll()
				f, h, err := r.FormFile("file")
				if err != nil {
					t.Error(err)
					return
				}
				data, _ := io.ReadAll(f)
				f.Close()
				if !bytes.Equal(data, syntheticWAV(2)) || h.Header.Get("Content-Type") != "audio/wav" || r.FormValue("model") != "whisper-1" || r.FormValue("response_format") != "verbose_json" {
					t.Error("fixed audio/model omitted or changed")
				}
				_, _ = w.Write([]byte(`{"segments":[{"start":0,"end":1,"text":"合成对白"}]}`))
			}))
			defer server.Close()
			a := s.media.alignment
			a.provider = newOpenAIAlignmentProvider("synthetic", "whisper-1", server.URL+"/audio/transcriptions", server.Client())
			a.caps = Capabilities{Ready: true, Mode: "real", Provider: "openai-asr", Model: "whisper-1"}
			if _, _, err := s.media.CreateAlignment(p.ID, p.SpaceID, "asr-no-grant", in); err == nil || calls.Load() != 0 {
				t.Fatal("undeclared analysis accepted", err)
			}
			// Image/video rights do not silently grant analysis of descendant audio.
			setLibraryDispatchAuthorization(t, s, card, 0, "image-only", "granted")
			if _, _, err := s.media.CreateAlignment(p.ID, p.SpaceID, "asr-image-only", in); err == nil {
				t.Fatal("image grant broadened into audio analysis")
			}
			alignmentAnalysisGrant(t, s, card, 1, "analysis-grant")
			run := pauseAlignment(t, s)
			task, created, err := s.media.CreateAlignment(p.ID, p.SpaceID, "asr-revoked-queue", in)
			sha := sha256.Sum256(syntheticWAV(2))
			if err != nil || !created || task.AudioSHA256 != hex.EncodeToString(sha[:]) || len(task.LibraryMediaPins) != 1 || task.LibraryMediaPins[0].Uses[0].Revision != 2 || task.LibraryMediaPins[0].Uses[0].Purpose != "audio_analysis" {
				t.Fatal("missing durable snapshot", task, err)
			}
			original := cloneAlignmentTask(task)
			task.LibraryMediaPins[0].Uses[0].Revision = 500
			a.mu.Lock()
			stored := cloneAlignmentTask(a.data.Tasks[task.ID])
			a.mu.Unlock()
			if !reflect.DeepEqual(stored, original) {
				t.Fatal("response alias changed stored authority")
			}
			setLibraryDispatchAuthorization(t, s, card, 2, "analysis-revoke", "revoked")
			failed := run(task.ID)
			if failed.Status != "failed" || failed.ErrorCode != "library_use_denied" || calls.Load() != 0 {
				t.Fatal("revoked queue sent audio", failed, calls.Load())
			}
			replay, created, err := s.media.CreateAlignment(p.ID, p.SpaceID, "asr-revoked-queue", in)
			if err != nil || created || replay.ID != task.ID || !reflect.DeepEqual(replay, failed) {
				t.Fatal("accepted receipt lost after revoke", err)
			}
			alignmentAnalysisGrant(t, s, card, 3, "analysis-regrant")
			if err = checkLibraryDispatch(a.guardSource(context.Background(), original)); err == nil {
				t.Fatal("new grant revived old accepted revision")
			}
			run = pauseAlignment(t, s)
			fresh, _, err := s.media.CreateAlignment(p.ID, p.SpaceID, "asr-fresh-intent", in)
			if err != nil {
				t.Fatal(err)
			}
			result := run(fresh.ID)
			if result.Status != "succeeded" || len(result.Cues) != 1 || calls.Load() != 1 || result.LibraryMediaPins[0].Uses[0].Revision != 4 {
				t.Fatal("fresh explicit intent", result, calls.Load())
			}
			// Completed subtitle history remains usable after a later withdrawal.
			setLibraryDispatchAuthorization(t, s, card, 4, "analysis-revoke-after-result", "revoked")
			state := s.media.Get(p.ID, p.SpaceID)
			state.Timeline.Clips[0].SubtitleAlignmentID = result.ID
			state.Timeline.Clips[0].SubtitleSource = "asr"
			state.Timeline.Clips[0].SubtitleCues = result.Cues
			if _, err = s.media.SaveTimeline(p.ID, p.SpaceID, "apply-fixed-historical-cues", state.Version, state.Timeline); err != nil {
				t.Fatal("historical result was destroyed", err)
			}
			if _, created, err = s.media.CreateAlignment(p.ID, p.SpaceID, "asr-fresh-intent", in); err != nil || created || calls.Load() != 1 {
				t.Fatal("replayed success resubmitted", err, calls.Load())
			}
		})
	}
}

func TestAlignmentSourcePreparationRevalidatesVersionAndReceipt(t *testing.T) {
	s := testStore(t)
	p, card, in := alignmentLineageFixture(t, s)
	alignmentAnalysisGrant(t, s, card, 0, "analysis-source-race-grant")
	a := s.media.alignment
	a.provider = newOpenAIAlignmentProvider("synthetic", "whisper-1", "http://127.0.0.1:1", &http.Client{})
	a.caps = Capabilities{Ready: true, Provider: "openai-asr", Model: "whisper-1"}
	prepare := a.pinSource
	a.pinSource = func(ctx context.Context, project, space, audio string) (alignmentSource, error) {
		value, err := prepare(ctx, project, space, audio)
		state := s.media.Get(project, space)
		if _, saveErr := s.media.SaveTimeline(project, space, "concurrent-timeline-change", state.Version, state.Timeline); saveErr != nil {
			t.Fatal(saveErr)
		}
		return value, err
	}
	if _, _, err := s.media.CreateAlignment(p.ID, p.SpaceID, "asr-version-race", in); !errors.Is(err, errMediaVersion) || len(a.list(p.ID, p.SpaceID)) != 0 {
		t.Fatal("accepted stale source version", err)
	}
	a.pinSource = prepare
	in.ExpectedVersion = s.media.Get(p.ID, p.SpaceID).Version
	run := pauseAlignment(t, s)
	task, _, err := s.media.CreateAlignment(p.ID, p.SpaceID, "accepted-before-prepare-error", in)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate another request winning acceptance while this one was preparing.
	got, created, err := s.media.createAlignment(p.ID, p.SpaceID, "accepted-before-prepare-error", in, &alignmentSource{}, providerFailure("library_use_denied"))
	if err != nil || created || got.ID != task.ID {
		t.Fatal("concurrent original receipt masked by preparation error", err)
	}
	setLibraryDispatchAuthorization(t, s, card, 1, "race-final-revoke", "revoked")
	if failed := run(task.ID); failed.ErrorCode != "library_use_denied" {
		t.Fatal(failed)
	}
}

func TestAlignmentProviderChecksAtPOSTAndLegacyNeverGainsNewAuthority(t *testing.T) {
	s := testStore(t)
	p, card, in := alignmentLineageFixture(t, s)
	alignmentAnalysisGrant(t, s, card, 0, "analysis-boundary-grant")
	a := s.media.alignment
	source, err := a.pinSource(context.Background(), p.ID, p.SpaceID, in.AudioAssetID)
	if err != nil {
		t.Fatal(err)
	}
	task := AlignmentTask{ProjectID: p.ID, SpaceID: p.SpaceID, AudioAssetID: in.AudioAssetID, AudioMIME: source.MIME, AudioSHA256: source.SHA256, LibraryMediaPins: source.Pins}
	ctx := a.guardSource(context.Background(), task)
	if err = checkLibraryDispatch(ctx); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(500) }))
	defer server.Close()
	provider := newOpenAIAlignmentProvider("synthetic", "whisper-1", server.URL, server.Client())
	setLibraryDispatchAuthorization(t, s, card, 1, "analysis-boundary-revoke", "revoked")
	_, err = provider.Transcribe(ctx, syntheticWAV(2), "audio/wav", 2)
	var failure *ProviderError
	if !errors.As(err, &failure) || failure.Code != "library_use_denied" || calls.Load() != 0 {
		t.Fatal("direct POST bypassed grant check", err, calls.Load())
	}
	// Managed ASR adds runtime authorization but retains the same source guard.
	managed, err := buildRuntimeProviders(ctx, ModelConfigEntry{Provider: "openai-asr", Model: "whisper-1", Capability: "asr"}, "synthetic", "", func(context.Context) error { return nil }, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = managed.Alignment.Transcribe(ctx, syntheticWAV(2), "audio/wav", 2)
	if !errors.As(err, &failure) || failure.Code != "library_use_denied" {
		t.Fatal("managed ASR discarded source boundary", err)
	}
	// Local forced alignment also checks immediately before starting a command.
	local := &LocalTransform{command: "/nonexistent-synthetic-command", model: "synthetic"}
	_, err = local.Align(ctx, syntheticWAV(2), "audio/wav", 2, "合成对白")
	if !errors.As(err, &failure) || failure.Code != "library_use_denied" {
		t.Fatal("local command started without current authority", err)
	}
	task.AudioSHA256 = ""
	if err = checkLibraryDispatch(a.guardSource(context.Background(), task)); !errors.As(err, &failure) || failure.Code != "alignment_source_unpinned" {
		t.Fatal("invented old task authority", err)
	}
	// Ordinary uploaded files need no library grant; changes still fail closed.
	state, err := s.media.AddAsset(p.ID, p.SpaceID, "ordinary-asr-audio", "自有配音", "voiceover", "", "", "uploaded", syntheticWAV(2), "audio/wav")
	if err != nil {
		t.Fatal(err)
	}
	id := state.Assets[len(state.Assets)-1].ID
	source, err = a.pinSource(context.Background(), p.ID, p.SpaceID, id)
	if err != nil || len(source.Pins) != 0 {
		t.Fatal("ordinary upload required library declaration", err)
	}
	task.AudioAssetID, task.AudioSHA256, task.AudioMIME, task.LibraryMediaPins = id, source.SHA256, source.MIME, source.Pins
	ctx = a.guardSource(context.Background(), task)
	if err = checkLibraryDispatch(ctx); err != nil {
		t.Fatal(err)
	}
	path, _, err := s.media.file(p.ID, p.SpaceID, id, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, syntheticWAV(1), 0600); err != nil {
		t.Fatal(err)
	}
	if err = checkLibraryDispatch(ctx); !errors.As(err, &failure) || failure.Code != "alignment_source_changed" || calls.Load() != 0 {
		t.Fatal("changed bytes accepted", err)
	}
	// Pins serialize and clone independently; no mutable authority aliases.
	b, _ := json.Marshal(task)
	var restored AlignmentTask
	if json.Unmarshal(b, &restored) != nil || restored.AudioSHA256 != task.AudioSHA256 {
		t.Fatal("source snapshot not persistent")
	}
}

func TestAlignmentAnalysisPermissionDoesNotBroadenVoiceUse(t *testing.T) {
	now := time.Now().UTC()
	for _, kind := range []string{"character", "scene", "product", "style", "voice"} {
		auth, err := library.ApplyAuthorization(library.Asset{Kind: kind, Status: "active"}, library.AuthorizationInput{Status: "granted", VersionIDs: []string{"v1"}, Purposes: []string{"audio_analysis"}, Holder: "Creator", Basis: "own_work", Evidence: "Synthetic", Attested: true}, "creator", now)
		if err != nil || !auth.Allows("v1", "audio_analysis", now) || auth.Allows("v1", "voice_reference", now) || auth.Allows("v1", "speech_preset", now) {
			t.Fatal("analysis broadened into synthesis", kind, err)
		}
	}
}

func TestAlignmentHTTPDenialIsActionableAndEstimateDoesNotReadAudio(t *testing.T) {
	s := testStore(t)
	p, _, in := alignmentLineageFixture(t, s)
	a := s.media.alignment
	a.provider = newOpenAIAlignmentProvider("synthetic", "whisper-1", "http://127.0.0.1:1", &http.Client{})
	a.caps = Capabilities{Ready: true, Provider: "openai-asr", Model: "whisper-1"}
	path := "/api/v1/projects/" + p.ID + "/media/alignments?space_id=" + p.SpaceID
	body := intentLibraryCall(t, s, "POST", path, "denied-audio-http", in, 409)
	var problemBody struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &problemBody) != nil || problemBody.Error.Code != "library_use_denied" {
		t.Fatal("analysis denial reported as uncertain write", string(body))
	}
	// Immutable capture fields are never accepted from a client.
	injected := map[string]any{"mode": "asr", "clip_id": in.ClipID, "audio_asset_id": in.AudioAssetID, "expected_version": in.ExpectedVersion, "audio_sha256": "forged"}
	intentLibraryCall(t, s, "POST", path, "forged-audio-pin", injected, 400)
	file, _, err := s.media.file(p.ID, p.SpaceID, in.AudioAssetID, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(file); err != nil {
		t.Fatal(err)
	}
	estimate := AlignmentInput{Mode: "estimate", ClipID: in.ClipID, AudioAssetID: in.AudioAssetID, Text: "只按文本安排时长。", ExpectedVersion: in.ExpectedVersion}
	var task AlignmentTask
	if json.Unmarshal(intentLibraryCall(t, s, "POST", path, "estimate-without-audio-read", estimate, 202), &task) != nil {
		t.Fatal("estimate response")
	}
	a.jobs.wait()
	for _, got := range a.list(p.ID, p.SpaceID) {
		if got.ID == task.ID && (got.Status != "succeeded" || got.AudioSHA256 != "" || len(got.LibraryMediaPins) != 0 || got.TimingBasis != "text-estimate") {
			t.Fatal("estimate read audio or claimed ASR", got)
		}
	}
}

func TestAlignmentRestartKeepsFixedSourcesAndNeverResubmits(t *testing.T) {
	s := testStore(t)
	p, card, in := alignmentLineageFixture(t, s)
	alignmentAnalysisGrant(t, s, card, 0, "analysis-before-interruption")
	a := s.media.alignment
	a.provider = newOpenAIAlignmentProvider("synthetic", "whisper-1", "http://127.0.0.1:1", &http.Client{})
	a.caps = Capabilities{Ready: true, Provider: "openai-asr", Model: "whisper-1"}
	// No worker can consume this queue before the simulated process exit.
	a.jobs = newLocalJobs(0)
	queued, _, err := s.media.CreateAlignment(p.ID, p.SpaceID, "interrupted-source-pin", in)
	if err != nil {
		t.Fatal(err)
	}
	a.close()
	reopened, err := openAlignmentStore(s.media.dir)
	if err != nil {
		t.Fatal(err)
	}
	s.media.alignment = reopened
	s.configureAlignmentLineage()
	reopened.pinSource = func(context.Context, string, string, string) (alignmentSource, error) {
		t.Error("replaying history attempted new source preparation")
		return alignmentSource{}, errors.New("unexpected preparation")
	}
	got, created, err := s.media.CreateAlignment(p.ID, p.SpaceID, "interrupted-source-pin", in)
	if err != nil || created || got.ID != queued.ID || got.Status != "failed" || got.ErrorCode != "execution_interrupted" || got.AudioSHA256 != queued.AudioSHA256 || !reflect.DeepEqual(got.LibraryMediaPins, queued.LibraryMediaPins) {
		t.Fatal("restart changed history or resubmitted", got, err)
	}
}

func TestAlignmentPostgresIndependentWorkerRechecksRevocation(t *testing.T) {
	db := postgresTestDB(t)
	api := postgresTestStore(t, db)
	remote, err := NewFileBlobStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = configureBlobStorage(api, remote); err != nil {
		t.Fatal(err)
	}
	p, card, in := alignmentLineageFixture(t, api)
	alignmentAnalysisGrant(t, api, card, 0, "analysis-api-process")
	a := api.media.alignment
	a.provider = newOpenAIAlignmentProvider("synthetic", "whisper-1", "http://127.0.0.1:1", &http.Client{})
	a.caps = Capabilities{Ready: true, Provider: "openai-asr", Model: "whisper-1"}
	task, _, err := api.media.CreateAlignment(p.ID, p.SpaceID, "independent-worker-task", in)
	if err != nil {
		t.Fatal(err)
	}
	worker := postgresTestStore(t, db)
	if err = configureBlobStorage(worker, remote); err != nil {
		t.Fatal(err)
	}
	worker.media.alignment.provider, worker.media.alignment.caps = a.provider, a.caps
	// The other handle must fetch current authority, not the grant it loaded.
	setLibraryDispatchAuthorization(t, api, card, 1, "analysis-other-process-revoke", "revoked")
	result := pauseAlignment(t, worker)(task.ID)
	if result.Status != "failed" || result.ErrorCode != "library_use_denied" || !reflect.DeepEqual(result.LibraryMediaPins, task.LibraryMediaPins) {
		t.Fatal("worker used stale in-memory authority", result)
	}
}

func TestAlignmentAllAudioModesUsePinnedAuthority(t *testing.T) {
	for _, mode := range []string{"asr", "forced", "transcription"} {
		t.Run(mode, func(t *testing.T) {
			s := testStore(t)
			p, card, in := alignmentLineageFixture(t, s)
			alignmentAnalysisGrant(t, s, card, 0, "analysis-mode-grant")
			a := s.media.alignment
			in.Mode = mode
			if mode == "forced" {
				in.Text = "合成对白"
				in.ExpectedProvider, in.ExpectedModel = "local-forced-alignment", "synthetic-aligner"
				a.forced = &LocalTransform{command: "/nonexistent-synthetic-command", model: in.ExpectedModel}
				a.forcedCaps = Capabilities{Ready: true, Provider: in.ExpectedProvider, Model: in.ExpectedModel}
			} else if mode == "transcription" {
				in.ExpectedProvider, in.ExpectedModel = "gateway-asr", "synthetic-transcript"
				in.selection = &RuntimeSelection{GatewayID: "synthetic-gateway", FeatureKey: "speech_transcription", Capability: "asr"}
				in.selectedCaps = Capabilities{Ready: true, Provider: in.ExpectedProvider, Model: in.ExpectedModel}
			} else {
				a.provider = newOpenAIAlignmentProvider("synthetic", "whisper-1", "http://127.0.0.1:1", &http.Client{})
				a.caps = Capabilities{Ready: true, Provider: in.ExpectedProvider, Model: in.ExpectedModel}
			}
			run := pauseAlignment(t, s)
			task, _, err := s.media.CreateAlignment(p.ID, p.SpaceID, "audio-mode-task", in)
			if err != nil || task.AudioSHA256 == "" || len(task.LibraryMediaPins) != 1 {
				t.Fatal("mode failed to pin source", mode, task, err)
			}
			setLibraryDispatchAuthorization(t, s, card, 1, "analysis-mode-revoke", "revoked")
			got := run(task.ID)
			if got.Status != "failed" || got.ErrorCode != "library_use_denied" || len(got.Cues) != 0 || got.Transcript != "" {
				t.Fatal("mode bypassed source guard or invented a result", mode, got)
			}
		})
	}
}
