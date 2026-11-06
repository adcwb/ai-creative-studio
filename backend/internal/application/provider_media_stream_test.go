package application

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func largeWaveFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "synthetic-large.wav")
	f, e := os.Create(path)
	if e != nil {
		t.Fatal(e)
	}
	const size = 60 << 20 // 24-bit stereo PCM at 96kHz: 109.23 seconds, not padding.
	header := make([]byte, 44)
	copy(header, "RIFF")
	binary.LittleEndian.PutUint32(header[4:], size+36)
	copy(header[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(header[16:], 16)
	binary.LittleEndian.PutUint16(header[20:], 1)
	binary.LittleEndian.PutUint16(header[22:], 2)
	binary.LittleEndian.PutUint32(header[24:], 96000)
	binary.LittleEndian.PutUint32(header[28:], 576000)
	binary.LittleEndian.PutUint16(header[32:], 6)
	binary.LittleEndian.PutUint16(header[34:], 24)
	copy(header[36:], "data")
	binary.LittleEndian.PutUint32(header[40:], size)
	if _, e = f.Write(header); e != nil {
		t.Fatal(e)
	}
	chunk := make([]byte, 64<<10)
	for i := range chunk {
		chunk[i] = byte(i % 251)
	}
	for remaining := size; remaining > 0; {
		n := min(remaining, len(chunk))
		if _, e = f.Write(chunk[:n]); e != nil {
			t.Fatal(e)
		}
		remaining -= n
	}
	if e = f.Close(); e != nil {
		t.Fatal(e)
	}
	return path
}

type boundedMediaWriter struct {
	n   int64
	max int
}
type failedMediaWriter struct{}

func (failedMediaWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func (w *boundedMediaWriter) Write(b []byte) (int, error) {
	if len(b) > 64<<10 {
		panic("unbounded upstream write")
	}
	w.max = max(w.max, len(b))
	w.n += int64(len(b))
	return len(b), nil
}
func streamReviewPolicy(t *testing.T, sha string, allow bool) *ExecutionPolicy {
	t.Helper()
	path := filepath.Join(t.TempDir(), "review")
	status := "allow"
	if !allow {
		status = "review"
	}
	program := `#!/usr/bin/python3
import base64,datetime,hashlib,json,sys
marker=b'"Media":[{"Data":"'
head=b''
while marker not in head:
    part=sys.stdin.buffer.read(65536)
    assert part
    head+=part
    assert len(head)<131072
prefix,data=head.split(marker,1)
h=hashlib.sha256()
pending=b''
while True:
    end=data.find(b'"')
    if end>=0:
        pending+=data[:end]
        h.update(base64.b64decode(pending,validate=True))
        tail=data[end+1:]+sys.stdin.buffer.read(65537)
        assert len(tail)<65536
        break
    pending+=data
    size=len(pending)//4*4
    if size:
        h.update(base64.b64decode(pending[:size],validate=True))
        pending=pending[size:]
    data=sys.stdin.buffer.read(65536)
    assert data
r=json.loads(prefix+b'"Media":[{"Data":""'+tail)
assert r['Stage']=='output' and len(r['Media'])==1
assert 'Stream' not in r['Media'][0]
assert h.hexdigest()==EXPECTED_SHA
print(json.dumps(dict(provider='local-fixture',model='synthetic',status=EXPECTED_STATUS,categories=[],checked_at=datetime.datetime.now(datetime.timezone.utc).isoformat())))
`
	program = strings.ReplaceAll(strings.ReplaceAll(program, "EXPECTED_SHA", fmtQuote(sha)), "EXPECTED_STATUS", fmtQuote(status))
	if e := os.WriteFile(path, []byte(program), 0700); e != nil {
		t.Fatal(e)
	}
	return &ExecutionPolicy{ModerationCommand: path, ModerationContract: "local-or-free-v1", ModerationSource: "synthetic local test", ModerationExpiresAt: time.Now().Add(time.Hour)}
}
func fmtQuote(s string) string { b, _ := json.Marshal(s); return string(b) }

type streamTestModerator func(context.Context, ExecutionRequest) (ModerationDecision, error)

func (f streamTestModerator) ReviewExecution(ctx context.Context, r ExecutionRequest) (ModerationDecision, error) {
	return f(ctx, r)
}

func TestGatewayLargeSpeechStreamsThroughReviewAndAssetReceipt(t *testing.T) {
	path := largeWaveFile(t)
	info, e := os.Stat(path)
	if e != nil {
		t.Fatal(e)
	}
	f, _ := os.Open(path)
	h := sha256.New()
	_, e = io.Copy(h, f)
	f.Close()
	if e != nil {
		t.Fatal(e)
	}
	sha := hex.EncodeToString(h.Sum(nil))
	var posts atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/audio/speech" {
			t.Error("unexpected request", r.Method, r.URL.Path)
			w.WriteHeader(400)
			return
		}
		posts.Add(1)
		w.Header().Set("Content-Type", "audio/wav")
		http.ServeFile(w, r, path)
	}))
	defer upstream.Close()
	provider := &gatewayCreativeProvider{OpenAICreativeProvider: newCreativeProvider("synthetic-secret", upstream.URL, upstream.Client()), model: GatewayModel{Model: "synthetic-speech"}, voice: "alloy"}
	writer := &boundedMediaWriter{}
	if e = provider.SpeechTo(context.Background(), "合成流式配音", writer); e != nil || writer.n != info.Size() {
		t.Fatal("stream transport", e, writer.n, info.Size())
	}
	for _, allow := range []bool{true, false} {
		t.Run(fmtQuote(allowString(allow)), func(t *testing.T) {
			s, e := OpenStore(t.TempDir())
			if e != nil {
				t.Fatal(e)
			}
			defer s.Close()
			p, _, e := s.CreateProject("personal-demo", "project", ProjectInput{Title: "Large audio", Brief: "Synthetic local protocol"})
			if e != nil {
				t.Fatal(e)
			}
			s.execution = &ExecutionGovernor{Moderator: streamReviewPolicy(t, sha, allow)}
			job := CreativeJob{ID: "streamed-speech", ProjectID: p.ID, SpaceID: p.SpaceID, Kind: "speech", Provider: "gateway-speech", Model: "synthetic-speech", Prompt: "合成流式配音", RuntimeSelection: &RuntimeSelection{TenantID: p.SpaceID, Capability: "speech"}}
			e = s.executeCreativeJobWith(context.Background(), &job, &RuntimeProviders{Creative: runtimeCreative{p: provider, capability: "speech"}})
			assets := s.media.Get(p.ID, p.SpaceID).Assets
			if allow {
				if e != nil || len(assets) != 1 || assets[0].ContentSHA256 != sha || job.AssetID != assets[0].ID {
					t.Fatal("large reviewed asset", e, assets)
				}
			} else if e == nil || len(assets) != 0 {
				t.Fatal("denied output published", e, assets)
			}
			leftovers, _ := filepath.Glob(filepath.Join(s.media.dir, ".provider-media-*"))
			if len(leftovers) != 0 {
				t.Fatal("private response leaked", leftovers)
			}
		})
	}
	if posts.Load() != 3 {
		t.Fatal("automatic retry", posts.Load())
	}
}
func allowString(v bool) string {
	if v {
		return "allow"
	}
	return "deny"
}

func TestStreamedReviewDetectsChangedBytesAndRequiresCapableModerator(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private")
	original := bytes.Repeat([]byte("real-media"), 10000)
	if e := os.WriteFile(path, original, 0600); e != nil {
		t.Fatal(e)
	}
	media, e := executionFileMedia(context.Background(), path, "audio/wav")
	if e != nil || media.Stream == nil {
		t.Fatal(e)
	}
	request := ExecutionRequest{Stage: "output", HasMedia: true, Media: []ExecutionMedia{media}}
	policy := streamReviewPolicy(t, media.Stream.SHA256, true)
	g := ExecutionGovernor{Moderator: policy}
	if e = g.ReviewResult(context.Background(), request); e != nil {
		t.Fatal("streamed review", e)
	}
	if e = os.WriteFile(path, bytes.Repeat([]byte("changed!!!"), 10000), 0600); e != nil {
		t.Fatal(e)
	}
	if e = g.ReviewResult(context.Background(), request); e == nil {
		t.Fatal("changed stream accepted")
	}
	g.Moderator = streamTestModerator(func(context.Context, ExecutionRequest) (ModerationDecision, error) {
		t.Fatal("metadata-only review called")
		return ModerationDecision{}, nil
	})
	if e = g.ReviewResult(context.Background(), request); e == nil {
		t.Fatal("nonstreaming adapter allowed")
	}
}

func TestGatewayStreamingRejectsTruncationAndWriterFailureWithoutRetry(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Length", "100000")
		_, _ = w.Write(append([]byte("RIFFxxxxWAVE"), make([]byte, 64)...))
	}))
	defer upstream.Close()
	p := &gatewayCreativeProvider{OpenAICreativeProvider: newCreativeProvider("synthetic", upstream.URL, upstream.Client()), voice: "alloy"}
	if e := p.SpeechTo(context.Background(), "synthetic", io.Discard); e == nil {
		t.Fatal("truncated response accepted")
	}
	if calls.Load() != 1 {
		t.Fatal("retried")
	}
	if e := copyProviderMedia(context.Background(), strings.NewReader("not a wave"), io.Discard, "audio/wav"); e == nil {
		t.Fatal("invalid signature accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if e := p.SpeechTo(ctx, "synthetic", io.Discard); e == nil {
		t.Fatal("cancelled call accepted")
	}
	if calls.Load() != 1 {
		t.Fatal("cancelled call dispatched")
	}
	validPrefix := make([]byte, 64)
	copy(validPrefix, "RIFFxxxxWAVE")
	if e := copyProviderMedia(context.Background(), bytes.NewReader(validPrefix), failedMediaWriter{}, "audio/wav"); e == nil {
		t.Fatal("destination error ignored")
	}
}

func TestStreamedTruncatedContainerIsNotReviewedOrPublished(t *testing.T) {
	source := largeWaveFile(t)
	f, e := os.Open(source)
	if e != nil {
		t.Fatal(e)
	}
	truncated := make([]byte, 64<<10)
	_, e = io.ReadFull(f, truncated)
	f.Close()
	if e != nil {
		t.Fatal(e)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(truncated) }))
	defer server.Close()
	s, e := OpenStore(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	project, _, e := s.CreateProject("personal-demo", "truncated", ProjectInput{Title: "Truncated", Brief: "Synthetic invalid provider response"})
	if e != nil {
		t.Fatal(e)
	}
	s.execution = &ExecutionGovernor{Moderator: streamTestModerator(func(context.Context, ExecutionRequest) (ModerationDecision, error) {
		t.Fatal("truncated output reached review")
		return ModerationDecision{}, nil
	})}
	provider := &gatewayCreativeProvider{OpenAICreativeProvider: newCreativeProvider("synthetic", server.URL, server.Client()), voice: "alloy"}
	job := CreativeJob{ID: "truncated", ProjectID: project.ID, SpaceID: project.SpaceID, Kind: "speech", Provider: "gateway-speech", Model: "synthetic", Prompt: "Synthetic truncated speech"}
	if e = s.executeCreativeJobWith(context.Background(), &job, &RuntimeProviders{Creative: runtimeCreative{p: provider, capability: "speech"}}); e == nil {
		t.Fatal("truncated WAV published")
	}
	if len(s.media.Get(project.ID, project.SpaceID).Assets) != 0 {
		t.Fatal("invalid output registered")
	}
	leftovers, _ := filepath.Glob(filepath.Join(s.media.dir, ".provider-media-*"))
	if len(leftovers) != 0 {
		t.Fatal("private invalid output leaked")
	}
}

func TestGatewayLargeVideoDownloadsIntoReviewedAssetWithoutGenerationRetry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "synthetic-noise.mp4")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "ffmpeg", "-v", "error", "-threads", "1", "-filter_threads", "1", "-f", "lavfi", "-i", "color=c=gray:s=640x360:r=24,noise=alls=100:allf=t:all_seed=17", "-t", "6", "-c:v", "libx264", "-preset", "ultrafast", "-crf", "0", "-pix_fmt", "yuv420p", "-an", "-movflags", "+faststart", path)
	if b, e := command.CombinedOutput(); e != nil {
		t.Fatal(e, string(b))
	}
	f, e := os.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	info, _ := f.Stat()
	if info.Size() <= 32<<20 {
		f.Close()
		t.Fatal("fixture must be an actual encoded video above 32MiB", info.Size())
	}
	h := sha256.New()
	_, _ = io.Copy(h, f)
	f.Close()
	sha := hex.EncodeToString(h.Sum(nil))
	var gets, posts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			posts.Add(1)
			w.WriteHeader(400)
			return
		}
		gets.Add(1)
		if r.URL.Path == "/videos/known-id" {
			_ = json.NewEncoder(w).Encode(VideoJob{ID: "known-id", Status: "completed"})
			return
		}
		if r.URL.Path != "/videos/known-id/content" {
			w.WriteHeader(404)
			return
		}
		http.ServeFile(w, r, path)
	}))
	defer server.Close()
	p := &gatewayCreativeProvider{OpenAICreativeProvider: newCreativeProvider("synthetic", server.URL, server.Client())}
	s, e := OpenStore(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	project, _, e := s.CreateProject("personal-demo", "video-project", ProjectInput{Title: "Large video", Brief: "Encoded synthetic local fixture"})
	if e != nil {
		t.Fatal(e)
	}
	s.execution = &ExecutionGovernor{Moderator: streamReviewPolicy(t, sha, true)}
	job := CreativeJob{ID: "stream-video", ProjectID: project.ID, SpaceID: project.SpaceID, Kind: "video", Provider: "gateway-video", Model: "synthetic-video", UpstreamID: "known-id", Prompt: "Synthetic video", RuntimeSelection: &RuntimeSelection{TenantID: project.SpaceID, Capability: "video"}}
	if e = s.executeCreativeJobWith(context.Background(), &job, &RuntimeProviders{Creative: runtimeCreative{p: p, capability: "video"}}); e != nil {
		t.Fatal("reviewed video", e, "encoded bytes", info.Size())
	}
	state := s.media.Get(project.ID, project.SpaceID)
	if len(state.Assets) != 1 || state.Assets[0].ContentSHA256 != sha || state.Assets[0].Kind != "video" {
		t.Fatal("video bytes not accepted exactly", state)
	}
	if posts.Load() != 0 || gets.Load() != 2 {
		t.Fatal("known upstream was resubmitted", gets.Load(), posts.Load())
	}
	readOnly := context.WithValue(context.Background(), executionReadOnlyKey{}, true)
	if e = (runtimeCreative{p: p, capability: "video"}).DownloadVideoTo(readOnly, "different-id", io.Discard); e == nil {
		t.Fatal("stream bypassed recovery permit")
	}
	if gets.Load() != 2 {
		t.Fatal("unauthorized stream dispatched")
	}
}

func TestProviderAcceptsValidUnknownLengthWAVStream(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	b, e := exec.CommandContext(ctx, "ffmpeg", "-v", "error", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=24000", "-t", "1", "-c:a", "pcm_s16le", "-f", "wav", "pipe:1").Output()
	if e != nil {
		t.Fatal(e)
	}
	if binary.LittleEndian.Uint32(b[4:8]) != ^uint32(0) {
		t.Fatal("fixture is not streamed WAV")
	}
	path := filepath.Join(t.TempDir(), "stream.wav")
	if e = os.WriteFile(path, b, 0600); e != nil {
		t.Fatal(e)
	}
	if e = validateProviderMediaFile(ctx, path, "audio/wav"); e != nil {
		t.Fatal("valid unknown-length WAV rejected", e)
	}
	actual, e := os.ReadFile(path)
	if e != nil || !bytes.Equal(actual, b) {
		t.Fatal("validation rewrote original stream", e)
	}
	leftovers, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".decode-wav-*"))
	if len(leftovers) != 0 {
		t.Fatal("validation copy leaked", leftovers)
	}
}
