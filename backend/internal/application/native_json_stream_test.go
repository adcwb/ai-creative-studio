package application

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func nativeReferenceFile(t *testing.T, w, h int, random bool) string {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	var seed uint32 = 37
	for i := 0; i < len(img.Pix); i += 4 {
		for j := 0; j < 3; j++ {
			seed = 1664525*seed + 1013904223
			img.Pix[i+j] = byte(seed >> 24)
			if !random {
				img.Pix[i+j] = byte(i % 251)
			}
		}
		img.Pix[i+3] = 255
	}
	path := filepath.Join(t.TempDir(), "reference.png")
	f, e := os.Create(path)
	if e != nil {
		t.Fatal(e)
	}
	encoder := png.Encoder{CompressionLevel: png.NoCompression}
	if e = encoder.Encode(f, img); e != nil {
		t.Fatal(e)
	}
	if e = f.Close(); e != nil {
		t.Fatal(e)
	}
	return path
}
func nativeFileHash(t *testing.T, path string) (string, int64) {
	t.Helper()
	f, e := os.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	h := sha256.New()
	n, e := io.Copy(h, f)
	if e != nil {
		t.Fatal(e)
	}
	return hex.EncodeToString(h.Sum(nil)), n
}
func nativeWriteBase64(t *testing.T, w io.Writer, path string) {
	t.Helper()
	f, e := os.Open(path)
	if e != nil {
		t.Error(e)
		return
	}
	defer f.Close()
	enc := base64.NewEncoder(base64.StdEncoding, w)
	_, e = io.Copy(enc, f)
	if e != nil {
		t.Error(e)
	}
	if e = enc.Close(); e != nil {
		t.Error(e)
	}
}
func TestNativeMultiReferenceTransportPastAggregateLimit(t *testing.T) {
	a := nativeReferenceFile(t, 2048, 1024, false)
	b := nativeReferenceFile(t, 2048, 1024, true)
	ha, na := nativeFileHash(t, a)
	hb, nb := nativeFileHash(t, b)
	if na+nb <= 10<<20 {
		t.Fatal("invalid fixture")
	}
	refs := []referenceMedia{{Path: a, MIME: "image/png", Role: "reference"}, {Path: b, MIME: "image/png", Role: "reference"}}
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path == "/images/edits" {
			reader, e := r.MultipartReader()
			if e != nil {
				t.Error(e)
				return
			}
			idx := 0
			for {
				part, e := reader.NextPart()
				if e == io.EOF {
					break
				}
				if e != nil {
					t.Error(e)
					return
				}
				if part.FormName() == "image[]" {
					h := sha256.New()
					n, e := io.Copy(h, part)
					want, size := ha, na
					if idx == 1 {
						want, size = hb, nb
					}
					if idx > 1 || e != nil || n != size || hex.EncodeToString(h.Sum(nil)) != want {
						t.Error("multipart changed")
					}
					idx++
				} else {
					io.Copy(io.Discard, part)
				}
			}
			if idx != 2 {
				t.Error("missing refs")
			}
			json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]string{"b64_json": base64.StdEncoding.EncodeToString(syntheticPNG(0))}}})
			return
		}
		var input struct{ Input []struct{ Type, Data string } }
		if e := json.NewDecoder(r.Body).Decode(&input); e != nil {
			t.Error(e)
			return
		}
		if len(input.Input) < 3 {
			t.Error("missing inputs")
			return
		}
		for i, want := range []string{ha, hb} {
			raw, e := base64.StdEncoding.Strict().DecodeString(input.Input[i+1].Data)
			if e != nil {
				t.Error(e)
			}
			sum := sha256.Sum256(raw)
			if hex.EncodeToString(sum[:]) != want {
				t.Error("JSON reference changed")
			}
		}
		json.NewEncoder(w).Encode(googleTestOutput("image", "image/png", base64.StdEncoding.EncodeToString(syntheticPNG(0)), ""))
	}))
	defer srv.Close()
	in := Input{Prompt: "use both synthetic references", Kind: "general"}
	gateway := gatewayImageProvider{api: newCreativeProvider("synthetic", srv.URL, srv.Client()), model: GatewayModel{Model: "synthetic", Capabilities: []string{"image", "image_edit"}, MaxReferenceImages: 2}}
	if _, e := gateway.GenerateWithReferences(context.Background(), in, refs); e != nil {
		t.Fatal(e)
	}
	google := GoogleInteractionsImageProvider{api: newGoogleMediaProvider("gemini-interactions-images", "synthetic", "gemini-3.1-flash-image", "", srv.URL, srv.Client(), nil)}
	if _, e := google.GenerateWithReferences(context.Background(), in, refs); e != nil {
		t.Fatal(e)
	}
	// Do not turn older 20MB documentation into a new application quota.
	// The newest official generic guide says100MB and model limits vary.
	if _, e := google.GenerateWithReferences(context.Background(), in, append(refs, refs[0])); e != nil || calls != 3 {
		t.Fatal("larger inline request blocked locally", e, calls)
	}

}
func TestNativeJSONParserRejectsMalformedEnvelopeAndCleansFiles(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	cases := []string{`{"data":{"audio":"YWJj","audio":"ZA=="}}`, `{"data":{"audio":"YWJj"},"DATA":{}}`, `{"data":{"audio":"YWJj"}} {}`, `{"data":{"audio":"YWJj"}`, `{"data":{"audio":"a"}}`, `{"data":{"audio":"YQ=Q"}}`, `{"data":{"audio":"YQ==","x":true,}}`, `{"data":{"audio":"YQ=="},"x":[1,]}`, `{"data":{"audio":"YQ=="},"x":"` + strings.Repeat("a", 65537) + `"}`, `{"data":{"audio":"YQ=="},"x":` + strings.Repeat("[", 34) + strings.Repeat("]", 34) + `}`}
	for i, body := range cases {
		m, e := newNativeJSONParser(context.Background(), strings.NewReader(body), "base64", func(path []string) bool { return mediaPath(path, "data", "audio") }).next(true)
		if e == nil {
			m.close()
			t.Fatalf("invalid response %d accepted", i)
		}
		files, _ := filepath.Glob(filepath.Join(os.TempDir(), "studio-native-*"))
		if len(files) > 0 {
			t.Fatal("spool leak", files)
		}
	}
	p := newNativeJSONParser(context.Background(), strings.NewReader(`{"data":"YQ==","code":0}`+"\n"+`{"data":`), "base64", func(path []string) bool { return mediaPath(path, "data") })
	m, e := p.next(false)
	if e != nil {
		t.Fatal(e)
	}
	m.close()
	if _, e = p.next(false); e == nil || e == io.EOF {
		t.Fatal("truncated NDJSON became EOF", e)
	}
	m, e = newNativeJSONParser(context.Background(), strings.NewReader(`{"data":{"audio":"YQ\u003d\u003d"}}`), "base64", func(path []string) bool { return mediaPath(path, "data", "audio") }).next(true)
	if e != nil {
		t.Fatal(e)
	}
	defer m.close()
	var data struct{ Data struct{ Audio string } }
	json.Unmarshal(m.metadata, &data)
	var out bytes.Buffer
	if e = m.copy(context.Background(), data.Data.Audio, &out); e != nil || out.String() != "a" {
		t.Fatal("escaped media", e)
	}
}
func TestNativeImagesStreamPastResponseAndPNGByteLimits(t *testing.T) {
	path := nativeReferenceFile(t, 4096, 3072, true)
	_, size := nativeFileHash(t, path)
	if base64.StdEncoding.EncodedLen(int(size)) <= maxProviderResponseBytes {
		t.Fatal("small fixture")
	}
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		io.Copy(io.Discard, r.Body)
		fmt.Fprint(w, `{"data":[{"b64_json":"`)
		nativeWriteBase64(t, w, path)
		fmt.Fprint(w, `"}]}`)
	}))
	defer srv.Close()
	p := newNativeImageProvider("ark-images", "synthetic", "synthetic-model", srv.URL, srv.Client())
	out, e := p.Generate(context.Background(), Input{Prompt: "synthetic native image", Kind: "general"})
	if e != nil || out.Width != 4096 || out.Height != 3072 || len(out.Data) <= 32<<20 || calls != 1 {
		t.Fatal("large native image", e, len(out.Data), calls)
	}
	if e = validateLargePNG(out.Data, out.Width, out.Height); e != nil {
		t.Fatal(e)
	}
	// The image must remain readable after storage; removing only the HTTP gate
	// would otherwise leave the old 16MiB result-download gate hidden here.
	store, e := OpenStore(t.TempDir(), Capabilities{Provider: "ark-images", Mode: "real", Ready: true, Model: "synthetic-model"})
	if e != nil {
		t.Fatal(e)
	}
	defer store.Close()
	blobs, e := NewFileBlobStore(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer blobs.Close()
	if e = configureBlobStorage(store, blobs); e != nil {
		t.Fatal(e)
	}

	task, _, e := store.Create("personal-demo", "large-native-result", Input{Prompt: "synthetic stored image", Kind: "general"})
	if e != nil {
		t.Fatal(e)
	}
	if e = store.Finish(task, out, nil); e != nil {
		t.Fatal(e)
	}
	if e = os.Remove(filepath.Join(filepath.Dir(store.path), "results", task.ID+".png")); e != nil {
		t.Fatal(e)
	}

	restored, e := store.ReadResult("personal-demo", task.ID)
	if e != nil || !bytes.Equal(restored.Data, out.Data) {
		t.Fatal("stored native result", e)
	}
	if _, e = store.ReadResult("team-demo", task.ID); !errors.Is(e, ErrNotFound) {
		t.Fatal("foreign result access", e)
	}

	mini := newMiniMaxVideoProvider("synthetic", "MiniMax-H3", srv.URL, srv.Client(), nil)
	_, capacityErr := mini.SubmitVideoWithFile(context.Background(), "synthetic first frame", path, "image/png")
	var failure *ProviderError
	if !errors.As(capacityErr, &failure) || failure.Code != "provider_inline_capacity" || calls != 1 {
		t.Fatal("MiniMax native 30MB capacity/no-send", capacityErr, calls)
	}
	code, _ := safeFailure(capacityErr, "minimax-video")
	if code != "provider_inline_capacity" {
		t.Fatal("capacity classification lost")
	}

	project := createProject(t, store)
	if _, e = store.media.AddAsset(project.ID, project.SpaceID, "large-native-shot", "large native shot", "shot", "", "", "ark-images", out.Data, "image/png"); e != nil {
		t.Fatal("native shot ingest", e)
	}

}
func TestNativeHexSpeechStreamingAndLateFailure(t *testing.T) {
	path := largeWaveFile(t)
	hash, size := nativeFileHash(t, path)
	fail := false
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		io.Copy(io.Discard, r.Body)
		fmt.Fprint(w, `{"data":{"audio":"`)
		f, e := os.Open(path)
		if e != nil {
			t.Error(e)
			return
		}
		_, e = io.Copy(hex.NewEncoder(w), f)
		f.Close()
		if e != nil {
			t.Error(e)
			return
		}
		code := 0
		if fail {
			code = 1000
		}
		fmt.Fprintf(w, `","status":2},"base_resp":{"status_code":%d}}`, code)
	}))
	defer srv.Close()
	p := MiniMaxSpeechProvider{transport: newCreativeProvider("synthetic", srv.URL, srv.Client()), model: "speech-2.8-hd", voice: "synthetic"}
	out := sha256.New()
	bounded := &boundedMediaWriter{}
	if e := p.SpeechTo(context.Background(), "synthetic native voice", io.MultiWriter(out, bounded)); e != nil || bounded.n != size || hex.EncodeToString(out.Sum(nil)) != hash {
		t.Fatal("hex stream", e, bounded.n)
	}
	fail = true
	bounded = &boundedMediaWriter{}
	if e := p.SpeechTo(context.Background(), "synthetic failure", bounded); e == nil || bounded.n != 0 || calls != 2 {
		t.Fatal("late error leaked/retried", e, bounded.n, calls)
	}
}
func TestNativeNDJSONHasNoCumulativeChunkQuota(t *testing.T) {
	path := largeWaveFile(t)
	want, size := nativeFileHash(t, path)
	calls := 0
	bad := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		io.Copy(io.Discard, r.Body)
		fmt.Fprint(w, `{"code":0,"data":"`)
		nativeWriteBase64(t, w, path)
		fmt.Fprintln(w, `"}`)
		for i := 0; i < 4200; i++ {
			fmt.Fprintln(w, `{"code":0}`)
		}
		if bad {
			fmt.Fprint(w, `{"code":1000,"data":""}`)
		}
	}))
	defer srv.Close()
	p := ByteDanceSpeechProvider{key: "synthetic", resource: "synthetic", voice: "synthetic", endpoint: srv.URL, client: srv.Client()}
	hash := sha256.New()
	out := &boundedMediaWriter{}
	if e := p.SpeechTo(context.Background(), "synthetic", io.MultiWriter(hash, out)); e != nil || out.n != size || hex.EncodeToString(hash.Sum(nil)) != want {
		t.Fatal("large NDJSON audio", e, out.n)
	}
	bad = true
	out = &boundedMediaWriter{}
	if e := p.SpeechTo(context.Background(), "synthetic", out); e == nil || out.n != 0 || calls != 2 {
		t.Fatal("late NDJSON", e, out.n, calls)
	}
}

func TestNativeReferenceFileVideoDispatchPastOldByteLimit(t *testing.T) {
	path := nativeReferenceFile(t, 3072, 1536, true)
	want, size := nativeFileHash(t, path)
	if size <= 10<<20 {
		t.Fatal("small fixture")
	}
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var body struct {
			Input   []struct{ Content []struct{ Data string } }
			Content []struct {
				ImageURL struct {
					URL string `json:"url"`
				} `json:"image_url"`
			}
		}
		if e := json.NewDecoder(r.Body).Decode(&body); e != nil {
			t.Error(e)
			return
		}
		encoded := ""
		for _, v := range body.Content {
			if v.ImageURL.URL != "" {
				encoded = strings.TrimPrefix(v.ImageURL.URL, "data:image/png;base64,")
			}
		}
		for _, v := range body.Input {
			for _, b := range v.Content {
				if b.Data != "" {
					encoded = b.Data
				}
			}
		}
		raw, e := base64.StdEncoding.Strict().DecodeString(encoded)
		sum := sha256.Sum256(raw)
		if e != nil || int64(len(raw)) != size || hex.EncodeToString(sum[:]) != want {
			t.Error("native video reference changed")
		}
		switch r.URL.Path {
		case "/v2/video_generation":
			fmt.Fprint(w, `{"task_id":"12345"}`)
		case "/contents/generations/tasks":
			fmt.Fprint(w, `{"id":"cgt-synthetic"}`)
		case "/v1beta/interactions":
			json.NewEncoder(w).Encode(googleTestOutput("video", "video/mp4", "", "files/synthetic"))
		default:
			t.Error("unexpected path", r.URL.Path)
		}
	}))
	defer server.Close()
	ctx := context.Background()
	ark := newArkVideoProvider("synthetic", "synthetic-model", server.URL, server.Client(), nil)
	mini := newMiniMaxVideoProvider("synthetic", "MiniMax-H3", server.URL, server.Client(), nil)
	google := newGoogleMediaProvider("gemini-video", "synthetic", "gemini-omni-1.1-flash", "", server.URL, server.Client(), nil)
	for _, provider := range []CreativeProvider{ark, mini, google} {
		wrapper := runtimeCreative{p: provider, capability: "video"}
		if !supportsVideoFiles(wrapper) {
			t.Fatal("file method lost through wrapper")
		}
		if _, e := wrapper.SubmitVideoWithFile(ctx, "synthetic first frame", path, "image/png"); e != nil {
			t.Fatal(e)
		}
		denied := runtimeCreative{p: provider, capability: "text"}
		if _, e := denied.SubmitVideoWithFile(ctx, "deny", path, "image/png"); e == nil {
			t.Fatal("wrong capability dispatched")
		}
	}
	if calls != 3 {
		t.Fatal("extra generation attempt", calls)
	}
}
func TestNativeFileReferenceScopeAndReviewBytes(t *testing.T) {
	s := testStore(t)
	project := createProject(t, s)
	var ids []string
	for i := 0; i < 2; i++ {
		path := nativeReferenceFile(t, 2048, 1024, i == 1)
		b, e := os.ReadFile(path)
		if e != nil {
			t.Fatal(e)
		}
		state, e := s.media.AddAsset(project.ID, project.SpaceID, fmt.Sprintf("native-ref-%d", i), "reference", "reference", "", "", "uploaded", b, "image/png")
		if e != nil {
			t.Fatal(e)
		}
		ids = append(ids, state.Assets[len(state.Assets)-1].ID)
	}
	in := CreativeJobInput{ReferenceAssetIDs: ids}
	refs, e := s.media.loadCreativeReferences(project.ID, project.SpaceID, in)
	if e != nil || len(refs) != 2 {
		t.Fatal("file references", e)
	}
	var total int64
	for _, ref := range refs {
		if ref.Path == "" || ref.Data != nil {
			t.Fatal("references loaded whole into memory")
		}
		want, size := nativeFileHash(t, ref.Path)
		m, e := referenceExecutionMedia(context.Background(), ref)
		if e != nil || m.Stream == nil || m.Stream.SHA256 != want || m.Stream.Size != size {
			t.Fatal("actual review source lost", e)
		}
		total += size
	}
	if total <= 10<<20 {
		t.Fatal("fixture too small")
	}
	if _, e = s.media.loadCreativeReferences("foreign-project", project.SpaceID, in); e == nil {
		t.Fatal("foreign project")
	}
	if _, e = s.media.loadCreativeReferences(project.ID, "foreign-tenant", in); e == nil {
		t.Fatal("foreign tenant")
	}
}
func TestNativeJSONRequestExactWireBoundaryAndCancellation(t *testing.T) {
	value := map[string]any{"data": inlineMedia{reference: referenceMedia{Data: []byte{1, 2, 3, 4}}}, "text": "quote\"和换行\n"}
	var expected bytes.Buffer
	if e := writeNativeJSON(context.Background(), &expected, value); e != nil {
		t.Fatal(e)
	}
	body, size, done, e := nativeJSONBody(context.Background(), value, int64(expected.Len()))
	if e != nil {
		t.Fatal(e)
	}
	actual, e := io.ReadAll(body)
	body.Close()
	if e != nil || <-done != nil || !bytes.Equal(actual, expected.Bytes()) || size != int64(len(actual)) {
		t.Fatal("wire count", e)
	}
	if b, _, _, e := nativeJSONBody(context.Background(), value, size-1); e == nil {
		b.Close()
		t.Fatal("boundary ignored")
	}
	body, _, done, e = nativeJSONBody(context.Background(), map[string]any{"data": inlineMedia{reference: referenceMedia{Data: make([]byte, 1<<20)}}}, 0)
	if e != nil {
		t.Fatal(e)
	}
	body.Close()
	if e = <-done; e == nil {
		t.Fatal("abandoned send was successful")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if m, e := newNativeJSONParser(ctx, strings.NewReader(`{"data":"YQ=="}`), "base64", func(p []string) bool { return mediaPath(p, "data") }).next(true); e == nil {
		m.close()
		t.Fatal("cancelled response accepted")
	}
}

func TestNativeImageReferencesStillRejectVideoRolesBeforeDispatch(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(500) }))
	defer server.Close()
	ctx := context.Background()
	in := Input{Kind: "general", Prompt: "synthetic invalid media role"}
	video := []byte{0, 0, 0, 20, 'f', 't', 'y', 'p', 'i', 's', 'o', 'm'}
	gateway := gatewayImageProvider{api: newCreativeProvider("synthetic", server.URL, server.Client()), model: GatewayModel{Model: "synthetic", Capabilities: []string{"image", "image_edit"}, MaxReferenceImages: 2}}
	if _, e := gateway.GenerateWithReferences(ctx, in, []referenceMedia{{Data: video, MIME: "video/mp4", Role: "reference"}}); e == nil {
		t.Fatal("image gateway accepted a video")
	}
	mini := newMiniMaxImageProvider("synthetic", "image-01", server.URL, server.Client())
	if _, e := mini.GenerateWithReference(ctx, in, video, "video/mp4"); e == nil {
		t.Fatal("native image accepted a video")
	}
	google := newGoogleMediaProvider("gemini-video", "synthetic", "gemini-omni-1.1-flash", "", server.URL, server.Client(), nil)
	if _, e := google.SubmitControlledVideo(ctx, "synthetic invalid first frame", []referenceMedia{{Data: video, MIME: "video/mp4", Role: "first"}}); e == nil {
		t.Fatal("first frame accepted a video")
	}
	if calls != 0 {
		t.Fatal("invalid role reached upstream", calls)
	}
}

// Legacy image adapters still return Data in memory. Its actual content must
// reach moderation without rebuilding one >64MiB JSON/base64 allocation.
func TestNativeInMemoryMediaReviewStreamsActualContent(t *testing.T) {
	path := largeWaveFile(t)
	sha, _ := nativeFileHash(t, path)
	data, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	request := ExecutionRequest{Stage: "output", HasMedia: true, Media: []ExecutionMedia{{Data: data, MIME: "audio/wav"}}}
	g := ExecutionGovernor{Moderator: streamReviewPolicy(t, sha, true)}
	if e = g.ReviewResult(context.Background(), request); e != nil {
		t.Fatal("large Data media review", e)
	}
	data[len(data)-1] ^= 1
	if e = g.ReviewResult(context.Background(), request); e == nil {
		t.Fatal("actual changed Data escaped review")
	}
}
