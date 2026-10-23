package studio

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestAlignmentCueBoundsAndEstimation(t *testing.T) {
	cues, err := estimateSubtitleCues("第一句。Second line. 3.14 is a number!", 6)
	if err != nil || len(cues) != 3 || cues[2].End != 6 {
		t.Fatal(cues, err)
	}
	valid := []SubtitleCue{{Start: 0.25, End: 0.75, Text: "one"}, {Start: 1, End: 1.75, Text: "two"}}
	if err = validateSubtitleCues(valid, 2); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range [][]SubtitleCue{{{Start: 1, End: 0, Text: "x"}}, {{Start: -1, End: 1, Text: "x"}}, {{Start: 0, End: 3, Text: "x"}}, {{Start: 0, End: 1, Text: "x"}, {Start: .5, End: 2, Text: "y"}}, {{Start: math.NaN(), End: 1, Text: "x"}}, {{Start: 0, End: math.Inf(1), Text: "x"}}, {{Start: 0, End: 1, Text: strings.Repeat("字", 301)}}, {{Start: 0, End: .0001, Text: "x"}}} {
		if validateSubtitleCues(invalid, 2) == nil {
			t.Fatal("invalid cues accepted", invalid)
		}
	}
	for _, raw := range []string{`{"end":1,"text":"x"}`, `{"start":null,"end":1,"text":"x"}`, `{"start":0,"end":1,"text":"x","extra":true}`} {
		var cue SubtitleCue
		if json.Unmarshal([]byte(raw), &cue) == nil {
			t.Fatal("invalid cue JSON accepted", raw)
		}
	}
}
func TestAlignmentOpenAIMultipartAndTiming(t *testing.T) {
	for _, kind := range []string{"segments", "words"} {
		t.Run(kind, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/audio/transcriptions" {
					t.Error(r.URL.Path)
				}
				if err := r.ParseMultipartForm(25 << 20); err != nil {
					t.Error(err)
					return
				}
				defer r.MultipartForm.RemoveAll()
				if r.FormValue("model") != "whisper-1" || r.FormValue("response_format") != "verbose_json" || len(r.MultipartForm.Value["timestamp_granularities[]"]) != 2 {
					t.Error("timestamp contract")
				}
				f, _, err := r.FormFile("file")
				if err != nil {
					t.Error(err)
					return
				}
				defer f.Close()
				data, _ := io.ReadAll(f)
				if len(data) < 44 || string(data[8:12]) != "WAVE" {
					t.Error("not inline audio")
				}
				if kind == "segments" {
					_, _ = w.Write([]byte(`{"segments":[{"id":0,"start":0.25,"end":1,"text":"第一句","tokens":[1]}]}`))
				} else {
					_, _ = w.Write([]byte(`{"words":[{"word":"Hello","start":0.25,"end":0.5},{"word":"world.","start":0.6,"end":1}]}`))
				}
			}))
			defer server.Close()
			p := newOpenAIAlignmentProvider("synthetic", "whisper-1", server.URL+"/audio/transcriptions", server.Client())
			result, err := p.Transcribe(context.Background(), syntheticWAV(2), "audio/wav", 2)
			if err != nil || result.TimingBasis != "asr-"+kind || len(result.Cues) != 1 || result.Cues[0].Start != .25 || result.Cues[0].End != 1 {
				t.Fatal(result, err)
			}
		})
	}
}
func TestAlignmentOpenAIRejectsFailureWithoutRetry(t *testing.T) {
	for _, status := range []int{302, 401, 429, 500} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var calls atomic.Int32
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Location", "http://127.0.0.1:9/private")
				w.WriteHeader(status)
				_, _ = w.Write([]byte("upstream-secret"))
			}))
			defer s.Close()
			p := newOpenAIAlignmentProvider("synthetic", "whisper-1", s.URL, s.Client())
			_, err := p.Transcribe(context.Background(), syntheticWAV(1), "audio/wav", 1)
			if err == nil || strings.Contains(err.Error(), "secret") || calls.Load() != 1 {
				t.Fatal(err, calls.Load())
			}
		})
	}
	for _, body := range []string{`{"text":"no timestamps"}`, `{"segments":[{"end":1,"text":"missing start"}]}`, `{"segments":[{"start":0,"end":3,"text":"beyond audio"}]}`, `{"segments":[{"start":0,"end":1.5,"text":"one"},{"start":1,"end":2,"text":"overlap"}]}`} {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) }))
		p := newOpenAIAlignmentProvider("synthetic", "whisper-1", s.URL, s.Client())
		if _, err := p.Transcribe(context.Background(), syntheticWAV(2), "audio/wav", 2); err == nil {
			t.Error("invalid upstream accepted", body)
		}
		s.Close()
	}
}
func alignmentFixture(t *testing.T) (*MediaStore, MediaState) {
	t.Helper()
	m, err := openMediaStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p, err := m.Demo("p", "s", "demo-001", 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	p.Timeline.Clips[0].AudioAssetID = p.Assets[0].ID
	p, err = m.SaveTimeline("p", "s", "timeline-001", p.Version, p.Timeline)
	if err != nil {
		t.Fatal(err)
	}
	return m, p
}
func awaitAlignment(t *testing.T, a *AlignmentStore, id string) AlignmentTask {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for {
		for _, task := range a.list("p", "s") {
			if task.ID == id && (task.Status == "succeeded" || task.Status == "failed" || task.Status == "cancelled") {
				return task
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("alignment timeout")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
func TestAlignmentPersistentEstimateProvenanceAndConflict(t *testing.T) {
	m, p := alignmentFixture(t)
	defer m.close()
	in := AlignmentInput{ClipID: p.Timeline.Clips[0].ID, Mode: "estimate", Text: "第一句。第二句。", ExpectedVersion: p.Version}
	task, created, err := m.CreateAlignment("p", "s", "align-001", in)
	if err != nil || !created {
		t.Fatal(task, created, err)
	}
	task = awaitAlignment(t, m.alignment, task.ID)
	if task.Status != "succeeded" || task.TimingBasis != "text-estimate" || len(task.Warnings) == 0 {
		t.Fatal(task)
	}
	replay, created, err := m.CreateAlignment("p", "s", "align-001", in)
	if err != nil || created || replay.ID != task.ID {
		t.Fatal(replay, created, err)
	}
	different := in
	different.Text = "不同文本"
	if _, _, err = m.CreateAlignment("p", "s", "align-001", different); !errors.Is(err, errMediaKey) {
		t.Fatal(err)
	}
	different.ExpectedVersion--
	if _, _, err = m.CreateAlignment("p", "s", "align-002", different); !errors.Is(err, errMediaVersion) {
		t.Fatal(err)
	}
	current := m.Get("p", "s")
	if current.Version != p.Version || len(current.Timeline.Clips[0].SubtitleCues) != 0 {
		t.Fatal("alignment auto-overwrote timeline")
	}
	clip := &current.Timeline.Clips[0]
	clip.SubtitleCues = task.Cues
	clip.SubtitleSource = "estimated"
	clip.SubtitleAlignmentID = task.ID
	saved, err := m.SaveTimeline("p", "s", "apply-001", current.Version, current.Timeline)
	if err != nil {
		t.Fatal(err)
	}
	saved.Timeline.Clips[0].SubtitleCues[0].Text = "人工修订"
	if _, err = m.SaveTimeline("p", "s", "apply-002", saved.Version, saved.Timeline); !errors.Is(err, errMediaInput) {
		t.Fatal("changed cues retained estimated source", err)
	}
	saved.Timeline.Clips[0].SubtitleSource = "manual"
	if _, err = m.SaveTimeline("p", "s", "apply-002", saved.Version, saved.Timeline); err != nil {
		t.Fatal(err)
	}
	reopened, err := openAlignmentStore(m.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.close()
	if len(reopened.list("p", "s")) != 1 || len(reopened.list("p", "other")) != 0 {
		t.Fatal("persistence/isolation")
	}
}
func TestAlignmentASRClipsAudioTimeAndNeverResends(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte(`{"segments":[{"start":0.25,"end":0.75,"text":"一"},{"start":1,"end":2.5,"text":"二"}]}`))
	}))
	defer server.Close()
	m, p := alignmentFixture(t)
	defer m.close()
	a := m.alignment
	a.provider = newOpenAIAlignmentProvider("synthetic", "whisper-1", server.URL, server.Client())
	a.caps = Capabilities{Provider: "openai-asr", Model: "whisper-1", Mode: "real", Ready: true}
	in := AlignmentInput{ClipID: p.Timeline.Clips[0].ID, AudioAssetID: p.Assets[0].ID, Mode: "asr", ExpectedVersion: p.Version, ExpectedProvider: "openai-asr", ExpectedModel: "whisper-1"}
	task, _, err := m.CreateAlignment("p", "s", "asr-001", in)
	if err != nil {
		t.Fatal(err)
	}
	task = awaitAlignment(t, a, task.ID)
	if task.Status != "succeeded" || len(task.Cues) != 2 || task.Cues[1].End != 2 || len(task.Warnings) < 2 {
		t.Fatal(task)
	}
	if _, created, err := m.CreateAlignment("p", "s", "asr-001", in); err != nil || created || calls.Load() != 1 {
		t.Fatal(created, err, calls.Load())
	}
	a.mu.Lock()
	d := a.cloneLocked()
	stored := d.Tasks[task.ID]
	stored.Status = "running"
	d.Tasks[task.ID] = stored
	if err = a.commitLocked(d); err != nil {
		t.Fatal(err)
	}
	a.mu.Unlock()
	reopened, err := openAlignmentStore(m.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.close()
	if got := reopened.list("p", "s")[0]; got.Status != "failed" || got.ErrorCode != "execution_interrupted" {
		t.Fatal(got)
	}
	if calls.Load() != 1 {
		t.Fatal("recovery repeated ASR")
	}
}
func TestAlignmentCancelRunningAndUnavailableGuard(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		close(started)
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer server.Close()
	defer close(release)
	m, p := alignmentFixture(t)
	defer m.close()
	in := AlignmentInput{ClipID: p.Timeline.Clips[0].ID, AudioAssetID: p.Assets[0].ID, Mode: "asr", ExpectedVersion: p.Version, ExpectedProvider: "openai-asr", ExpectedModel: "whisper-1"}
	if _, _, err := m.CreateAlignment("p", "s", "asr-001", in); err != ErrProviderNotReady {
		t.Fatal(err)
	}
	a := m.alignment
	a.provider = newOpenAIAlignmentProvider("synthetic", "whisper-1", server.URL, server.Client())
	a.caps = Capabilities{Provider: "openai-asr", Model: "whisper-1", Mode: "real", Ready: true}
	wrong := in
	wrong.ExpectedModel = "wrong"
	if _, _, err := m.CreateAlignment("p", "s", "asr-001", wrong); err != ErrProviderChanged {
		t.Fatal(err)
	}
	task, _, err := m.CreateAlignment("p", "s", "asr-001", in)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("request did not start")
	}
	if _, err = a.cancel("p", "s", "cancel-001", task.ID); err != nil {
		t.Fatal(err)
	}
	a.wg.Wait()
	task = awaitAlignment(t, a, task.ID)
	if task.Status != "cancelled" || len(task.Cues) != 0 || calls.Load() != 1 {
		t.Fatal(task, calls.Load())
	}
	if _, created, err := m.CreateAlignment("p", "s", "asr-001", in); err != nil || created {
		t.Fatal(err)
	}
}
func TestAlignmentConfigAndCorruptState(t *testing.T) {
	t.Setenv("ASR_PROVIDER", "openai-asr")
	t.Setenv("ASR_MODEL", "gpt-4o-transcribe")
	t.Setenv("OPENAI_API_KEY", "synthetic")
	if p, caps := ConfigureAlignmentFromEnv(); p != nil || caps.Ready || caps.ReasonCode != "unsupported_timestamp_model" {
		t.Fatal(caps)
	}
	t.Setenv("ASR_MODEL", "whisper-1")
	t.Setenv("OPENAI_API_KEY", "")
	if p, caps := ConfigureAlignmentFromEnv(); p != nil || caps.Ready {
		t.Fatal(caps)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "alignments.json"), []byte(`{"version":1,"tasks":null}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := openAlignmentStore(dir); err == nil {
		t.Fatal("corrupt alignment state accepted")
	}
}
