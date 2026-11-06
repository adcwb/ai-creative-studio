package application

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/library"
	"github.com/adcwb/ai-creative-studio/backend/internal/modules/media"
	"github.com/adcwb/ai-creative-studio/backend/internal/persistence/libraryrepo"
)

func inputLibraryFixture(t *testing.T, s *FileStore, kind string, card library.Card) (Project, libraryrepo.Result, string, []string) {
	t.Helper()
	p, _, err := s.CreateProject("personal-demo", "input-project", ProjectInput{Title: "固定创作资产", Brief: "仅合成测试，不调用真实付费服务"})
	if err != nil {
		t.Fatal(err)
	}
	refs := []libraryReferenceInput{}
	if kind != "voice" {
		state, e := s.media.AddAsset(p.ID, p.SpaceID, "input-source-image", "合成参考", "reference", "", "", "uploaded", syntheticCreativePNG(), "image/png")
		if e != nil {
			t.Fatal(e)
		}
		refs = append(refs, libraryReferenceInput{ProjectID: p.ID, AssetID: state.Assets[0].ID, Role: "reference", ViewAngle: "front"})
	}
	var result libraryrepo.Result
	_ = json.Unmarshal(intentLibraryCall(t, s, "POST", "/api/v1/library/assets?space_id="+p.SpaceID, "input-card", libraryWrite{Kind: kind, Card: card, References: refs}, 201), &result)
	state := s.media.Get(p.ID, p.SpaceID)
	var bound MediaState
	_ = json.Unmarshal(intentLibraryCall(t, s, "POST", "/api/v1/projects/"+p.ID+"/library-bindings?space_id="+p.SpaceID, "input-bind", libraryBindingInput{ExpectedVersion: state.Version, AssetID: result.Asset.ID, VersionID: result.Version.ID, Usage: kind}, 201), &bound)
	b := bound.LibraryBindings[len(bound.LibraryBindings)-1]
	return p, result, b.ID, b.MediaAssetIDs
}
func inputLibraryGrant(t *testing.T, s *FileStore, a libraryrepo.Result, revision int64, key string, purposes ...string) {
	t.Helper()
	intentLibraryCall(t, s, "POST", "/api/v1/library/assets/"+a.Asset.ID+"/use-authorization?space_id="+a.Asset.SpaceID, key, library.AuthorizationInput{Expected: revision, Status: "granted", VersionIDs: []string{a.Version.ID}, Purposes: purposes, Holder: "Synthetic creator", Basis: "own_work", Evidence: "Only synthetic local test assets", Attested: true}, 200)
}
func TestLibraryInputsActualReferencePromptAndImmutableHistory(t *testing.T) {
	for _, storage := range []string{"file", "postgres"} {
		t.Run(storage, func(t *testing.T) {
			s := testStore(t)
			if storage == "postgres" {
				s = postgresTestStore(t, postgresTestDB(t))
			}
			// Complete authored direction can exceed the old raw-brief limit; it must be
			// transmitted separately and intact, never trimmed or read as voice dialogue.
			appearance := strings.Repeat("青灰色外观，", 220)
			p, a, binding, ids := inputLibraryFixture(t, s, "character", library.Card{Name: "小满", Description: "合成角色", Character: &library.Character{Appearance: appearance, Costume: "雨夜外套", VoiceNotes: "NEVER_SPEAK_OR_SEND_VOICE_NOTE"}, ProhibitedChanges: []string{"保留蓝色衣领"}})
			inputLibraryGrant(t, s, a, 0, "input-grant", "prompt_context", "image_reference", "audio_analysis")
			var calls atomic.Int32
			output := pngFixture(t, 1536, 864)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				var prompt string
				if r.URL.Path == "/images/edits" {
					if err := r.ParseMultipartForm(8 << 20); err != nil {
						t.Error(err)
						return
					}
					defer r.MultipartForm.RemoveAll()
					f, _, err := r.FormFile("image[]")
					if err != nil {
						t.Error(err)
						return
					}
					raw, _ := io.ReadAll(f)
					f.Close()
					if !bytes.Equal(raw, syntheticCreativePNG()) {
						t.Error("exact copied reference omitted")
					}
					prompt = r.FormValue("prompt")
				} else if r.URL.Path == "/images/generations" {
					var b map[string]any
					_ = json.NewDecoder(r.Body).Decode(&b)
					prompt, _ = b["prompt"].(string)
				} else {
					t.Error("unexpected generation route", r.URL.Path)
				}
				if !strings.Contains(prompt, appearance) || !strings.Contains(prompt, "保留蓝色衣领") || strings.Contains(prompt, "NEVER_SPEAK") {
					t.Error("authored fields truncated, omitted or voice note leaked")
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]string{"b64_json": base64.StdEncoding.EncodeToString(output)}}})
			}))
			defer upstream.Close()
			provider := newOpenAIProvider("synthetic", upstream.URL+"/images/generations", upstream.Client())
			SetCreativeImage(s, provider, Capabilities{Ready: true, Mode: "real", Provider: "openai-images", Model: provider.model})
			in := CreativeJobInput{Kind: "image", Prompt: "门口的雨夜场景", ExpectedVersion: p.Version, ExpectedProvider: "openai-images", ExpectedModel: provider.model, LibraryInputs: []libraryInputChoice{{BindingID: binding, Mode: "reference", MediaAssetIDs: ids}}}
			job, _, err := s.createCreativeJob(p.SpaceID, p.ID, "input-reference", in)
			if err != nil || len(job.LibraryInputs) != 1 || len(job.LibraryInputs[0].Uses) != 2 || job.ReferenceAssetID != ids[0] || job.Prompt != in.Prompt || len(job.LibraryInputContext) < 1000 || !reflect.DeepEqual(job.LibraryInputs[0].OmittedFields, []string{"voice_notes"}) {
				t.Fatal("incomplete fixed input", job, err)
			}
			if operationFeature("creative", &in) != "image_edit" {
				t.Fatal("reference card bypassed edit feature binding")
			}
			if err = s.executeCreativeJob(context.Background(), &job); err != nil {
				t.Fatal(err)
			}
			result, err := s.media.AssetForReceipt(p.ID, p.SpaceID, "creative-"+job.ID)
			if err != nil || len(result.LibrarySources) != 1 || result.LibrarySources[0].VersionID != a.Version.ID {
				t.Fatal("reference lineage lost", result, err)
			}
			promptOnly := in
			promptOnly.LibraryInputs = []libraryInputChoice{{BindingID: binding, Mode: "prompt_only"}}
			textJob, _, err := s.createCreativeJob(p.SpaceID, p.ID, "input-text-only", promptOnly)
			if err != nil || textJob.ReferenceAssetID != "" || len(textJob.LibraryMediaPins) != 0 || len(textJob.LibraryInputs) != 1 {
				t.Fatal(textJob, err)
			}
			if err = s.executeCreativeJob(context.Background(), &textJob); err != nil || calls.Load() != 2 {
				t.Fatal(err, calls.Load())
			}
			listed, err := s.listCreativeJobs(p.SpaceID, p.ID)
			if err != nil || len(listed) != 2 || listed[0].LibraryInputContext != "" || len(listed[0].LibraryInputs) == 0 {
				t.Fatal("polling lost metadata or expanded full context", listed, err)
			}
			listed[0].LibraryInputs[0].Uses[0].Revision = 900
			fixed, _, err := s.createCreativeJob(p.SpaceID, p.ID, "input-text-only", promptOnly)
			if err != nil || fixed.LibraryInputContext != textJob.LibraryInputContext || fixed.LibraryInputs[0].Uses[0].Revision != 1 {
				t.Fatal("summary changed the accepted payload or authority", err)
			}
			textResult, err := s.media.AssetForReceipt(p.ID, p.SpaceID, "creative-"+textJob.ID)
			if err != nil || len(textResult.LibrarySources) != 1 {
				t.Fatal("text direction laundered source", err)
			}
			// A newer head never changes the accepted version/context or original receipt.
			next := a.Version.Card
			next.Character = &library.Character{Appearance: "NEW_HEAD_NOT_USED"}
			intentLibraryCall(t, s, "POST", "/api/v1/library/assets/"+a.Asset.ID+"/versions?space_id="+p.SpaceID, "new-input-head", libraryWrite{Kind: "character", ExpectedVersion: a.Version.ID, Card: next, References: []libraryReferenceInput{}}, 201)
			replay, created, err := s.createCreativeJob(p.SpaceID, p.ID, "input-reference", in)
			if err != nil || created || replay.ID != job.ID || strings.Contains(replay.LibraryInputContext, "NEW_HEAD") {
				t.Fatal("head update rewrote accepted input", err)
			}
			replay.LibraryInputs[0].Uses[0].Revision = 999
			again, _, _ := s.createCreativeJob(p.SpaceID, p.ID, "input-reference", in)
			if again.LibraryInputs[0].Uses[0].Revision != 1 {
				t.Fatal("response mutated stored authority")
			}
			queued, _, err := s.createCreativeJob(p.SpaceID, p.ID, "input-revoked-queue", in)
			if err != nil {
				t.Fatal(err)
			}
			setLibraryDispatchAuthorization(t, s, a, 1, "input-revoke", "revoked")
			if err = s.executeCreativeJob(context.Background(), &queued); err == nil || calls.Load() != 2 {
				t.Fatal("revoked input dispatched", err)
			}
			if replay, created, err = s.createCreativeJob(p.SpaceID, p.ID, "input-revoked-queue", in); err != nil || created || replay.ID != queued.ID {
				t.Fatal("revoked receipt lost", err)
			}
			derived := CreativeJobInput{Kind: "image", Prompt: "新的明确创作", ExpectedVersion: p.Version, ExpectedProvider: "openai-images", ExpectedModel: provider.model, ReferenceAssetID: textResult.ID}
			if _, _, err = s.createCreativeJob(p.SpaceID, p.ID, "input-derived-revoked", derived); err == nil {
				t.Fatal("text-only descendant bypassed withdrawal")
			}
			inputLibraryGrant(t, s, a, 2, "input-regrant", "prompt_context", "image_reference")
			if err = s.executeCreativeJob(context.Background(), &queued); err == nil || calls.Load() != 2 {
				t.Fatal("regrant revived old input", err)
			}
		})
	}
}

func TestLibraryInputsRejectUnsupportedAndForeignCombinations(t *testing.T) {
	s := testStore(t)
	p, a, binding, ids := inputLibraryFixture(t, s, "style", library.Card{Name: "雨夜风格", Style: &library.Style{Palette: "青灰"}, ProhibitedChanges: []string{}})
	inputLibraryGrant(t, s, a, 0, "style-input-grant", "prompt_context", "image_reference")
	provider := newOpenAIProvider("synthetic", "http://127.0.0.1:1/images/generations", &http.Client{})
	SetCreativeImage(s, provider, Capabilities{Ready: true, Mode: "real", Provider: "openai-images", Model: provider.model})
	base := CreativeJobInput{Kind: "image", Prompt: "保留雨夜气氛", ExpectedVersion: p.Version, ExpectedProvider: "openai-images", ExpectedModel: provider.model}
	for _, choice := range []libraryInputChoice{{BindingID: binding, Mode: "native_preset"}, {BindingID: binding, Mode: "unsupported"}, {BindingID: binding, Mode: "reference"}, {BindingID: binding, Mode: "reference", MediaAssetIDs: []string{"foreign-media"}}, {BindingID: binding, Mode: "reference", MediaAssetIDs: []string{ids[0], ids[0]}}, {BindingID: binding, Mode: "prompt_only", MediaAssetIDs: ids}, {BindingID: "foreign-binding", Mode: "prompt_only"}} {
		in := base
		in.LibraryInputs = []libraryInputChoice{choice}
		if _, _, err := s.createCreativeJob(p.SpaceID, p.ID, "reject-"+mediaID(), in); err == nil {
			t.Fatal("unsupported/foreign choice accepted", choice)
		}
	}
	duplicate := base
	duplicate.LibraryInputs = []libraryInputChoice{{BindingID: binding, Mode: "prompt_only"}, {BindingID: binding, Mode: "prompt_only"}}
	if _, _, err := s.createCreativeJob(p.SpaceID, p.ID, "reject-duplicate", duplicate); err == nil {
		t.Fatal("duplicate card accepted")
	}
	if jobs, _ := s.listCreativeJobs(p.SpaceID, p.ID); len(jobs) != 0 {
		t.Fatal("rejection left queued work")
	}
	if _, _, err := s.createCreativeJob("team-demo", p.ID, "reject-foreign-project", base); err == nil {
		t.Fatal("foreign project accepted")
	}
}

func TestLibraryNativeVoiceGatewayToASRAndRevocation(t *testing.T) {
	for _, storage := range []string{"file", "postgres"} {
		t.Run(storage, func(t *testing.T) {
			s := testStore(t)
			if storage == "postgres" {
				s = postgresTestStore(t, postgresTestDB(t))
			}
			var speechCalls, asrCalls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v1/audio/speech":
					speechCalls.Add(1)
					var b map[string]string
					_ = json.NewDecoder(r.Body).Decode(&b)
					if b["voice"] != "fixed-native-voice" || b["input"] != "这是原始对白。" || b["model"] != "synthetic-speech" {
						t.Error("preset or exact dialogue not consumed", b)
					}
					_, _ = w.Write(syntheticWAV(2))
				case "/v1/audio/transcriptions":
					asrCalls.Add(1)
					if err := r.ParseMultipartForm(25 << 20); err != nil {
						t.Error(err)
						return
					}
					defer r.MultipartForm.RemoveAll()
					f, _, err := r.FormFile("file")
					if err != nil {
						t.Error(err)
						return
					}
					b, _ := io.ReadAll(f)
					f.Close()
					if !bytes.Equal(b, syntheticWAV(2)) || r.FormValue("model") != "synthetic-speech" {
						t.Error("ASR did not consume fixed generated dialogue")
					}
					_, _ = w.Write([]byte(`{"segments":[{"start":0,"end":1,"text":"这是原始对白。"}]}`))
				default:
					t.Error("unexpected route", r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer upstream.Close()
			u, _ := url.Parse(upstream.URL)
			t.Setenv("MODEL_GATEWAY_ALLOWED_HOSTS", u.Host)
			models, err := OpenModelConfigStore(t.TempDir(), bytes.Repeat([]byte{17}, 32))
			if err != nil {
				t.Fatal(err)
			}
			report, err := models.saveGateway("personal-demo", 0, AIGateway{Name: "合成网关", BaseURL: upstream.URL + "/v1", Enabled: true, TimeoutSeconds: 30}, ModelCredentialInput{Action: "set", Secret: "synthetic-speech-key"})
			if err != nil {
				t.Fatal(err)
			}
			report, err = models.saveGatewayModel("personal-demo", 0, GatewayModel{GatewayID: report.Gateways[0].ID, Name: "合成配音与识别", Model: "synthetic-speech", Capabilities: []string{"speech", "asr_timestamps"}, Enabled: true, MaxOutputTokens: 2000})
			if err != nil {
				t.Fatal(err)
			}
			model := report.Models[0]
			for _, feature := range []string{"speech_synthesis", "subtitle_alignment"} {
				if _, err = models.saveFeatureBinding("personal-demo", "creator", 0, FeatureBinding{FeatureKey: feature, ModelConfigID: model.ID, Enabled: true}); err != nil {
					t.Fatal(err)
				}
			}
			auth := &AuthStore{models: models, data: authSnapshot{Organizations: map[string]AuthOrganization{"personal-demo": {Members: map[string]string{"creator": "editor"}}}}}
			ConfigureExecutionGovernance(s, &ExecutionGovernor{Ledger: s.budget, Moderator: testExecutionModerator{true}})
			ConfigureManagedRuntime(s, auth)
			card := library.Card{Name: "小满的原生音色", Description: "NOT_DIALOGUE_DESCRIPTION", Voice: &library.Voice{Timbre: "NOT_DIALOGUE_TIMBRE", Performance: "NOT_DIALOGUE_PERFORMANCE", NativePreset: &library.NativeVoicePreset{ModelConfigID: model.ID, Provider: "gateway-speech", Model: model.Model, Voice: "fixed-native-voice"}}, ProhibitedChanges: []string{"NOT_DIALOGUE_CONSTRAINT"}}
			p, a, binding, _ := inputLibraryFixture(t, s, "voice", card)
			inputLibraryGrant(t, s, a, 0, "voice-input-grant", "speech_preset", "audio_analysis")
			in := CreativeJobInput{Kind: "speech", Prompt: "这是原始对白。", ExpectedVersion: p.Version, ModelRequest: ModelRequest{ModelConfigID: model.ID, ModelConfigVersion: model.Version}, LibraryInputs: []libraryInputChoice{{BindingID: binding, Mode: "native_preset"}}}
			path := "/api/v1/projects/" + p.ID + "/creative-jobs?space_id=" + p.SpaceID
			call := func(key string, status int) CreativeJob {
				raw, _ := json.Marshal(in)
				r := httptest.NewRequest("POST", path, bytes.NewReader(raw))
				r.Header.Set("Content-Type", "application/json")
				r.Header.Set("Idempotency-Key", key)
				r = r.WithContext(context.WithValue(r.Context(), authScopeKey{}, authScope{SpaceID: p.SpaceID, UserID: "creator", Role: "editor"}))
				w := httptest.NewRecorder()
				Handler(s, "").ServeHTTP(w, r)
				if w.Code != status {
					t.Fatalf("native HTTP %d: %s", w.Code, w.Body.String())
				}
				var out CreativeJob
				_ = json.Unmarshal(w.Body.Bytes(), &out)
				var wire map[string]any
				_ = json.Unmarshal(w.Body.Bytes(), &wire)
				if _, exposed := wire["runtime_selection"]; exposed {
					t.Fatal("internal runtime authority exposed")
				}
				s.creative.mu.Lock()
				stored := cloneCreativeJob(s.creative.data.Jobs[out.ID])
				s.creative.mu.Unlock()
				return stored
			}
			job := call("native-speech", 202)
			if job.RuntimeSelection == nil || job.RuntimeSelection.Voice != "fixed-native-voice" || len(job.LibraryInputs) != 1 || job.LibraryInputContext != "" || len(job.LibraryInputs[0].OmittedFields) != 4 {
				t.Fatal("native intent lost or read as dialogue", job)
			}
			snapshot := cloneCreativeJob(job)
			job.RuntimeSelection.Voice = "forged-response-voice"
			replay := call("native-speech", 200)
			if replay.RuntimeSelection.Voice != "fixed-native-voice" {
				t.Fatal("response changed persisted native selection")
			}
			job = snapshot
			if err = s.executeCreativeJob(context.Background(), &job); err != nil {
				t.Fatal(err)
			}
			audio, err := s.media.AssetForReceipt(p.ID, p.SpaceID, "creative-"+job.ID)
			if err != nil || audio.Kind != "audio" || len(audio.LibrarySources) != 1 || speechCalls.Load() != 1 {
				t.Fatal("native audio ancestry missing", audio, err)
			}
			state := s.media.Get(p.ID, p.SpaceID)
			state, err = s.media.Demo(p.ID, p.SpaceID, "voice-to-asr-demo", state.Version, nil)
			if err != nil {
				t.Fatal(err)
			}
			state.Timeline.Clips[0].AudioAssetID = audio.ID
			state, err = s.media.SaveTimeline(p.ID, p.SpaceID, "voice-to-asr-timeline", state.Version, state.Timeline)
			if err != nil {
				t.Fatal(err)
			}
			selection, entry, err := s.runtime.Resolve(context.Background(), p.SpaceID, "creator", model.ID, "asr")
			if err != nil {
				t.Fatal(err)
			}
			selection.FeatureKey = "subtitle_alignment"
			align := AlignmentInput{Mode: "asr", ClipID: state.Timeline.Clips[0].ID, AudioAssetID: audio.ID, ExpectedVersion: state.Version, ModelRequest: ModelRequest{selection: &selection, selectedCaps: Capabilities{Ready: true, Mode: "real", Provider: entry.Provider, Model: entry.Model}}}
			run := pauseAlignment(t, s)
			task, _, err := s.media.CreateAlignment(p.ID, p.SpaceID, "native-to-asr-queue", align)
			if err != nil {
				t.Fatal(err)
			}
			setLibraryDispatchAuthorization(t, s, a, 1, "native-voice-revoke", "revoked")
			if got := run(task.ID); got.ErrorCode != "library_use_denied" || asrCalls.Load() != 0 {
				t.Fatal("ASR bypassed voice withdrawal", got)
			}
			_ = call("native-speech", 200)
			inputLibraryGrant(t, s, a, 2, "native-voice-regrant", "speech_preset", "audio_analysis")
			if err = s.executeCreativeJob(context.Background(), &snapshot); err == nil || speechCalls.Load() != 1 {
				t.Fatal("voice regrant revived old intent", err)
			}
			run = pauseAlignment(t, s)
			task, _, err = s.media.CreateAlignment(p.ID, p.SpaceID, "native-to-asr-new-intent", align)
			if err != nil {
				t.Fatal(err)
			}
			if got := run(task.ID); got.Status != "succeeded" || asrCalls.Load() != 1 {
				t.Fatal("fresh ASR failed", got)
			}
		})
	}
}

func TestLibraryVoiceVideoDescendantsNeedExplicitReusePermission(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("FFmpeg required for synthetic video fixture")
	}
	for _, storage := range []string{"file", "postgres"} {
		t.Run(storage, func(t *testing.T) {
			s := testStore(t)
			if storage == "postgres" {
				s = postgresTestStore(t, postgresTestDB(t))
			}
			p, a, _, _ := inputLibraryFixture(t, s, "voice", library.Card{Name: "固定声音", Voice: &library.Voice{NativePreset: &library.NativeVoicePreset{ModelConfigID: "model", Provider: "gateway-speech", Model: "synthetic", Voice: "voice"}}, ProhibitedChanges: []string{}})
			inputLibraryGrant(t, s, a, 0, "voice-derivative-original", "speech_preset", "audio_analysis")
			path := filepath.Join(t.TempDir(), "derived.mp4")
			if err := ffmpeg(context.Background(), "-f", "lavfi", "-i", "color=c=blue:s=128x72:r=24:d=1", "-f", "lavfi", "-i", "sine=frequency=440:duration=1", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", "-t", "1", "-f", "mp4", path); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.WithValue(context.Background(), libraryOutputSourcesKey{}, []media.LibrarySource{{AssetID: a.Asset.ID, VersionID: a.Version.ID}})
			state, err := s.media.addAssetContext(ctx, p.ID, p.SpaceID, "voice-derived-video", "本地合成衍生测试", "shot", "", "", "local-lip-sync", data, "video/mp4")
			if err != nil {
				t.Fatal(err)
			}
			id := state.Assets[len(state.Assets)-1].ID
			input := CreativeJobInput{Kind: "video", ReferenceAssetID: id}
			if _, err := s.pinCreativeLibraryMedia(context.Background(), p.ID, p.SpaceID, input); err == nil {
				t.Fatal("native preset permission silently granted derivative reuse")
			}
			inputLibraryGrant(t, s, a, 1, "voice-derivative-explicit", "voice_reference")
			pins, err := s.pinCreativeLibraryMedia(context.Background(), p.ID, p.SpaceID, input)
			if err != nil || len(pins) != 1 || pins[0].Uses[0].Purpose != "voice_reference" {
				t.Fatal("voice in video demanded impossible video grant", pins, err)
			}
			job := CreativeJob{ProjectID: p.ID, SpaceID: p.SpaceID, Kind: "video", ReferenceAssetID: id, LibraryMediaPins: pins}
			ctx = s.withLibraryDispatchGuard(context.Background(), &job)
			if err = checkLibraryDispatch(ctx); err != nil {
				t.Fatal(err)
			}
			setLibraryDispatchAuthorization(t, s, a, 2, "voice-derivative-revoke", "revoked")
			if err = checkLibraryDispatch(ctx); err == nil {
				t.Fatal("voice reuse ignored withdrawal")
			}
		})
	}
}

func TestLibraryInputsPreserveSupportedFrameCombinationsAndPositions(t *testing.T) {
	s := testStore(t)
	p, a, binding, ids := inputLibraryFixture(t, s, "style", library.Card{Name: "固定风格", Style: &library.Style{Palette: "青灰"}, ProhibitedChanges: []string{}})
	inputLibraryGrant(t, s, a, 0, "frame-style-grant", "prompt_context", "video_reference")
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		input, _ := b["input"].([]any)
		found := false
		for _, item := range input {
			row, _ := item.(map[string]any)
			content, _ := row["content"].([]any)
			for _, part := range content {
				v, _ := part.(map[string]any)
				text, _ := v["text"].(string)
				found = found || strings.Contains(text, `"ReferencePositions":[2]`)
			}
		}
		if !found {
			t.Error("reference position did not account for first frame")
		}
		w.WriteHeader(429)
	}))
	defer upstream.Close()
	provider := newGoogleMediaProvider("gemini-video", "synthetic", "gemini-omni-1.1-flash", "", upstream.URL, upstream.Client(), nil)
	SetCreativeVideo(s, provider, Capabilities{Ready: true, Mode: "real", Provider: "gemini-video", Model: "gemini-omni-1.1-flash"})
	frame := s.media.Get(p.ID, p.SpaceID).Assets[0].ID
	in := CreativeJobInput{Kind: "video", Prompt: "合成风格与首帧", ExpectedVersion: p.Version, ExpectedProvider: "gemini-video", ExpectedModel: "gemini-omni-1.1-flash", FirstFrameAssetID: frame, LibraryInputs: []libraryInputChoice{{BindingID: binding, Mode: "reference", MediaAssetIDs: ids}}}
	job, _, err := s.createCreativeJob(p.SpaceID, p.ID, "supported-frame-style", in)
	if err != nil || job.FirstFrameAssetID != frame || job.ReferenceAssetID != "" || !reflect.DeepEqual(job.ReferenceAssetIDs, ids) {
		t.Fatal("supported native combination discarded or rejected", job, err)
	}
	if err = s.executeCreativeJob(context.Background(), &job); err == nil || calls.Load() != 1 {
		t.Fatal("combined reference never dispatched or retried", err, calls.Load())
	}
}

func TestLibraryInputsFailClosedOnUnknownMediaAndExpiredDirection(t *testing.T) {
	s := testStore(t)
	p, a, binding, ids := inputLibraryFixture(t, s, "style", library.Card{Name: "待核对风格", Style: &library.Style{Palette: "青灰"}, ProhibitedChanges: []string{}})
	expiry := time.Now().UTC().Add(5 * time.Second)
	intentLibraryCall(t, s, "POST", "/api/v1/library/assets/"+a.Asset.ID+"/use-authorization?space_id="+p.SpaceID, "expiring-style", library.AuthorizationInput{Status: "granted", VersionIDs: []string{a.Version.ID}, Purposes: []string{"prompt_context", "image_reference"}, Holder: "Creator", Basis: "own_work", Evidence: "Synthetic", Attested: true, ExpiresAt: &expiry}, 200)
	provider := newOpenAIProvider("synthetic", "http://127.0.0.1:1/images/generations", &http.Client{})
	SetCreativeImage(s, provider, Capabilities{Ready: true, Mode: "real", Provider: "openai-images", Model: provider.model})
	in := CreativeJobInput{Kind: "image", Prompt: "合成风格意图", ExpectedVersion: p.Version, ExpectedProvider: "openai-images", ExpectedModel: provider.model, LibraryInputs: []libraryInputChoice{{BindingID: binding, Mode: "prompt_only"}}}
	job, _, err := s.createCreativeJob(p.SpaceID, p.ID, "fixed-expiring-direction", in)
	if err != nil {
		t.Fatal(err)
	}
	if err = checkLibraryDispatch(s.withLibraryDispatchGuard(context.Background(), &job)); err != nil {
		t.Fatal(err)
	}
	bad := cloneCreativeJob(job)
	bad.LibraryInputs[0].ContentSHA256 = strings.Repeat("0", 64)
	if err = checkLibraryDispatch(s.withLibraryDispatchGuard(context.Background(), &bad)); err == nil {
		t.Fatal("changed version hash accepted")
	}
	s.media.mu.Lock()
	d := s.media.clone()
	state := d.Projects[mediaKey(p.SpaceID, p.ID)]
	for i := range state.Assets {
		if state.Assets[i].ID == ids[0] {
			state.Assets[i].ProvenanceUnknown = true
		}
	}
	d.Projects[mediaKey(p.SpaceID, p.ID)] = state
	err = s.media.commit(d)
	s.media.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	reference := in
	reference.LibraryInputs = []libraryInputChoice{{BindingID: binding, Mode: "reference", MediaAssetIDs: ids}}
	if _, _, err = s.createCreativeJob(p.SpaceID, p.ID, "unknown-library-reference", reference); !errors.Is(err, errMediaLineageUnknown) {
		t.Fatal("unknown media silently became prompt-only", err)
	}
	<-time.After(time.Until(expiry))
	if err = checkLibraryDispatch(s.withLibraryDispatchGuard(context.Background(), &job)); err == nil {
		t.Fatal("expired text direction dispatched")
	}
	if replay, created, err := s.createCreativeJob(p.SpaceID, p.ID, "fixed-expiring-direction", in); err != nil || created || replay.ID != job.ID {
		t.Fatal("expiry destroyed original receipt", err)
	}
}
