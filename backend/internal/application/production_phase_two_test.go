package application

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestProductionEpisodeIsolationAndExportSnapshot(t *testing.T) {
	dir := t.TempDir()
	m, err := openMediaStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer m.close()
	p, err := m.AddAsset("p", "s", "image-001", "image", "shot", "", "", "uploaded", syntheticPNG(0), "image/png")
	if err != nil {
		t.Fatal(err)
	}
	one := MediaTimeline{MusicVolume: .2, Clips: []MediaClip{{ID: "clip-1", AssetID: p.Assets[0].ID, Duration: 3, Subtitle: "first", AudioVolume: 1}}}
	p, err = m.SaveTimeline("p", "s", "episode-one", p.Version, one)
	if err != nil {
		t.Fatal(err)
	}
	two := MediaTimeline{EpisodeNumber: 2, MusicVolume: .2, Clips: []MediaClip{{ID: "clip-1", AssetID: p.Assets[0].ID, Duration: 5, Subtitle: "second", AudioVolume: 1}}}
	version := p.Version
	p, err = m.SaveTimeline("p", "s", "episode-two", version, two)
	if err != nil || !reflect.DeepEqual(p.Timeline, one) || p.EpisodeTimelines[2].Clips[0].Subtitle != "second" {
		t.Fatal(p, err)
	}
	if _, err = m.SaveTimeline("p", "s", "episode-two", version, two); err != nil {
		t.Fatal("replay", err)
	}
	if _, err = m.SaveTimeline("p", "s", "stale-episode", version, two); !errors.Is(err, errMediaVersion) {
		t.Fatal("stale", err)
	}
	// Both episodes deliberately reuse a clip ID: alignment provenance must still differ.
	task, _, err := m.CreateAlignment("p", "s", "episode-align", AlignmentInput{EpisodeNumber: 2, ClipID: "clip-1", Mode: "estimate", Text: "second", ExpectedVersion: p.Version})
	if err != nil {
		t.Fatal(err)
	}
	task = awaitAlignment(t, m.alignment, task.ID)
	if task.Status != "succeeded" || task.EpisodeNumber != 2 || task.Duration != 5 {
		t.Fatal(task)
	}
	one.Clips[0].SubtitleCues = task.Cues
	one.Clips[0].SubtitleSource = "estimated"
	one.Clips[0].SubtitleAlignmentID = task.ID
	if _, err = m.SaveTimeline("p", "s", "wrong-episode", p.Version, one); err == nil {
		t.Fatal("cross-episode alignment accepted")
	}
	two.Clips[0].SubtitleCues = task.Cues
	two.Clips[0].SubtitleSource = "estimated"
	two.Clips[0].SubtitleAlignmentID = task.ID
	p, err = m.SaveTimeline("p", "s", "correct-episode", p.Version, two)
	if err != nil {
		t.Fatal(err)
	}
	exported, err := m.StartEpisodeExport("p", "s", "export-episode-two", p.Version, 2)
	if err != nil || exported.EpisodeNumber != 2 || exported.Duration != 5 || exported.Timeline.Clips[0].Subtitle != "second" {
		t.Fatal(exported, err)
	}
	if _, err = m.StartEpisodeExport("p", "s", "export-episode-two", p.Version, 1); !errors.Is(err, errMediaKey) {
		t.Fatal("export key reused for another episode", err)
	}
	m.close() // This test asserts persisted snapshots, not encoder availability.
	restored, err := openMediaStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.close()
	got := restored.Get("p", "s")
	if got.Timeline.Clips[0].Subtitle != "first" || got.EpisodeTimelines[2].Clips[0].Subtitle != "second" || got.Exports[0].EpisodeNumber != 2 {
		t.Fatal("episode persistence", got)
	}
}

func TestProductionReferenceProtocols(t *testing.T) {
	a, b := nativeTestImage(t, "png"), nativeTestImage(t, "jpeg")
	video := []byte{0, 0, 0, 20, 'f', 't', 'y', 'p', 'i', 's', 'o', 'm'}
	var requests []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		requests = append(requests, body)
		if body["model"] == "gemini-omni-1.1-flash" {
			_ = json.NewEncoder(w).Encode(googleTestOutput("video", "video/mp4", "", "https://generativelanguage.googleapis.com/v1beta/files/ref-123:download?alt=media"))
		} else {
			_ = json.NewEncoder(w).Encode(googleTestOutput("image", "image/png", base64.StdEncoding.EncodeToString(a), ""))
		}
	}))
	defer server.Close()
	image := &GoogleInteractionsImageProvider{api: newGoogleMediaProvider("gemini-interactions-images", "synthetic", "gemini-3.1-flash-image", "", server.URL, server.Client(), nil)}
	_, err := image.GenerateWithReferences(context.Background(), Input{Prompt: "keep both references", Kind: "portrait"}, []referenceMedia{{a, "image/png", "reference"}, {b, "image/jpeg", "reference"}})
	if err != nil {
		t.Fatal(err)
	}
	input := requests[0]["input"].([]any)
	if len(input) != 3 || input[1].(map[string]any)["data"] != base64.StdEncoding.EncodeToString(a) || input[2].(map[string]any)["data"] != base64.StdEncoding.EncodeToString(b) {
		t.Fatal("ordered image references lost", input)
	}
	p := newGoogleMediaProvider("gemini-video", "synthetic", "gemini-omni-1.1-flash", "", server.URL, server.Client(), nil)
	refs := []referenceMedia{{a, "image/png", "first"}, {b, "image/jpeg", "last"}, {a, "image/png", "reference"}, {video, "video/mp4", "motion"}}
	job, err := p.SubmitControlledVideo(context.Background(), "walk into the scene", refs)
	if err != nil || job.ID != "ref-123" {
		t.Fatal(job, err)
	}
	content := requests[1]["input"].([]any)[0].(map[string]any)["content"].([]any)
	if len(content) != 5 || content[3].(map[string]any)["data"] != base64.StdEncoding.EncodeToString(video) {
		t.Fatal("video reference lost", content)
	}
	text := content[4].(map[string]any)["text"].(string)
	for _, tag := range []string{"<FIRST_FRAME>@Image1", "<LAST_FRAME>@Image2", "<IMAGE_REF_0>@Image3", "<VIDEO_REF_0>@Video1"} {
		if !strings.Contains(text, tag) {
			t.Fatal("missing role", tag, text)
		}
	}
	if _, err = p.SubmitControlledVideo(context.Background(), "invalid last frame", []referenceMedia{{a, "image/png", "last"}}); err == nil {
		t.Fatal("last without first accepted")
	}
	p.model = "unknown-model"
	if _, err = p.SubmitControlledVideo(context.Background(), "unsupported controls", refs); err == nil {
		t.Fatal("unknown model accepted")
	}
	if len(requests) != 2 {
		t.Fatal("invalid input reached provider")
	}
}

func TestProductionReferenceValidationAndImmutableJobs(t *testing.T) {
	s := testStore(t)
	p := createProject(t, s)
	state, err := s.media.AddAsset(p.ID, p.SpaceID, "ref-upload", "reference", "shot", "", "", "uploaded", syntheticPNG(0), "image/png")
	if err != nil {
		t.Fatal(err)
	}
	id := state.Assets[0].ID
	caps := Capabilities{Provider: "gemini-interactions-images", Model: "gemini-3.1-flash-image", Ready: true, Mode: "real"}
	in := CreativeJobInput{Kind: "image", Prompt: "a scene with references", ExpectedVersion: p.Version, ExpectedProvider: caps.Provider, ExpectedModel: caps.Model, ReferenceAssetIDs: []string{id}}
	if err = s.validateCreativeControls(p.ID, p.SpaceID, in, caps); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*CreativeJobInput){func(v *CreativeJobInput) { v.ReferenceAssetIDs = []string{id, id} }, func(v *CreativeJobInput) { v.ReferenceAssetID = id }, func(v *CreativeJobInput) { v.ReferenceAssetIDs = []string{"foreign"} }, func(v *CreativeJobInput) { v.FirstFrameAssetID = id }} {
		bad := in
		change(&bad)
		if s.validateCreativeControls(p.ID, p.SpaceID, bad, caps) == nil {
			t.Fatal("invalid references accepted", bad)
		}
	}
	if s.validateCreativeControls("another-project", p.SpaceID, in, caps) == nil {
		t.Fatal("foreign project accepted")
	}
	s.creative.imageCaps = caps
	job, created, err := s.createCreativeJob(p.SpaceID, p.ID, "multi-reference-job", in)
	if err != nil || !created {
		t.Fatal(job, created, err)
	}
	job.ReferenceAssetIDs[0] = "tampered"
	replay, created, err := s.createCreativeJob(p.SpaceID, p.ID, "multi-reference-job", in)
	if err != nil || created || replay.ReferenceAssetIDs[0] != id {
		t.Fatal("reference snapshot mutated", replay, err)
	}
}

func TestProductionReviewAssignmentHTTP(t *testing.T) {
	s := testStore(t)
	p := createProject(t, s)
	state, err := s.media.AddAsset(p.ID, p.SpaceID, "review-image", "image", "shot", "", "", "uploaded", syntheticPNG(0), "image/png")
	if err != nil {
		t.Fatal(err)
	}
	path := "/projects/" + p.ID + "/media/decisions?space_id=" + p.SpaceID
	in := ProductionDecision{ExpectedVersion: state.Version, Action: "review", AssetID: state.Assets[0].ID, Text: "match the costume", AssigneeID: "foreign-user", Priority: "high"}
	projectHTTP(t, s, "POST", path, "foreign-assignee", projectJSON(t, in), 400)
	in.AssigneeID = "demo-user"
	projectHTTP(t, s, "POST", path, "assign-review", projectJSON(t, in), 200)
	state = s.media.Get(p.ID, p.SpaceID)
	r := state.Reviews[0]
	if r.CreatedBy != "demo-user" || r.UpdatedBy != "demo-user" || r.AssigneeID != "demo-user" || r.Priority != "high" {
		t.Fatal(r)
	}
	in.Action = "assign"
	in.ReviewID = r.ID
	in.ExpectedVersion = state.Version
	in.AssigneeID = ""
	in.Priority = "low"
	body := projectJSON(t, in)
	projectHTTP(t, s, "POST", path, "unassign-review", body, 200)
	projectHTTP(t, s, "POST", path, "unassign-review", body, 200)
	projectHTTP(t, s, "POST", path, "stale-review", body, 409)
	state = s.media.Get(p.ID, p.SpaceID)
	if state.Reviews[0].AssigneeID != "" || state.Reviews[0].Priority != "low" || state.Reviews[0].CreatedBy != r.CreatedBy || state.Version != in.ExpectedVersion+1 {
		t.Fatal(state)
	}
}

func TestProductionQuoteBindsReferenceBytesOrderAndRoles(t *testing.T) {
	s := testStore(t)
	p := createProject(t, s)
	var ids []string
	for i, data := range [][]byte{nativeTestImage(t, "png"), nativeTestImage(t, "jpeg")} {
		mime := "image/png"
		if i == 1 {
			mime = "image/jpeg"
		}
		state, err := s.media.AddAsset(p.ID, p.SpaceID, "quote-ref-"+mime, "ref", "shot", "", "", "uploaded", data, mime)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, state.Assets[len(state.Assets)-1].ID)
	}
	selection := RuntimeSelection{TenantID: p.SpaceID, Capability: "video"}
	entry := ModelConfigEntry{Provider: "gemini-video", Model: "gemini-omni-1.1-flash"}
	in := CreativeJobInput{Kind: "video", ExpectedVersion: p.Version, Prompt: "follow the references", ReferenceAssetIDs: ids}
	preview := func(input CreativeJobInput) ExecutionRequest {
		t.Helper()
		raw, _ := json.Marshal(input)
		r, err := s.buildQuoteExecutionRequest(p.SpaceID, RuntimeQuoteInput{Operation: "creative", ProjectID: p.ID, Input: raw}, selection, entry)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	before := preview(in)
	worker, err := s.creativeExecutionRequest(CreativeJob{Kind: in.Kind, Prompt: in.Prompt, SpaceID: p.SpaceID, ProjectID: p.ID, Provider: entry.Provider, Model: entry.Model, ReferenceAssetIDs: ids, RuntimeSelection: &selection, Source: ProjectInput{Title: p.Title, Brief: p.Brief}})
	if err != nil || len(before.Media) != 2 || ExecutionRequestDigest(before) != ExecutionRequestDigest(worker) {
		t.Fatal("preview and worker differ", err)
	}
	in.ReferenceAssetIDs = []string{ids[1], ids[0]}
	if ExecutionRequestDigest(before) == ExecutionRequestDigest(preview(in)) {
		t.Fatal("order not bound")
	}
	in.ReferenceAssetIDs = nil
	in.FirstFrameAssetID = ids[0]
	in.LastFrameAssetID = ids[1]
	if ExecutionRequestDigest(before) == ExecutionRequestDigest(preview(in)) {
		t.Fatal("roles not bound")
	}
}

func TestProductionAlignmentQuoteSelectsEpisode(t *testing.T) {
	s := testStore(t)
	p := createProject(t, s)
	state, err := s.media.AddAsset(p.ID, p.SpaceID, "audio-quote-fixture", "audio", "shot", "", "", "uploaded", syntheticPNG(0), "image/png")
	if err != nil {
		t.Fatal(err)
	}
	id := state.Assets[0].ID
	path, _, err := s.media.file(p.ID, p.SpaceID, id, false, false)
	if err != nil {
		t.Fatal(err)
	}
	// Install a local WAV fixture without depending on ffprobe in this quote test.
	if err = os.WriteFile(path, syntheticWAV(6), 0600); err != nil {
		t.Fatal(err)
	}
	state.Assets[0].Kind = "audio"
	state.Assets[0].MIMEType = "audio/wav"
	state.Assets[0].Duration = 6
	state.Timeline.Clips = []MediaClip{{ID: "shared-clip", AudioAssetID: id, Duration: 2}}
	state.EpisodeTimelines = map[int]MediaTimeline{2: {EpisodeNumber: 2, Clips: []MediaClip{{ID: "shared-clip", AudioAssetID: id, Duration: 5, TrimStart: 1}}}}
	s.media.mu.Lock()
	d := s.media.clone()
	d.Projects[mediaKey(p.SpaceID, p.ID)] = state
	err = s.media.commit(d)
	s.media.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(AlignmentInput{EpisodeNumber: 2, ExpectedVersion: state.Version, ClipID: "shared-clip", AudioAssetID: id, Mode: "asr"})
	selection := RuntimeSelection{TenantID: p.SpaceID, Capability: "asr"}
	entry := ModelConfigEntry{Provider: "openai-asr", Model: "whisper-1"}
	preview, err := s.buildQuoteExecutionRequest(p.SpaceID, RuntimeQuoteInput{Operation: "alignment", ProjectID: p.ID, Input: raw}, selection, entry)
	if err != nil {
		t.Fatal(err)
	}
	worker := alignmentExecutionRequest(AlignmentTask{EpisodeNumber: 2, SpaceID: p.SpaceID, ProjectID: p.ID, Provider: entry.Provider, Model: entry.Model, Mode: "asr", Duration: 5, TrimStart: 1, AudioDuration: 6, RuntimeSelection: &selection}, syntheticWAV(6), "audio/wav")
	if ExecutionRequestDigest(preview) != ExecutionRequestDigest(worker) {
		t.Fatal("quote used another episode", string(preview.Parameters))
	}
}

func TestProductionReviewMembersRespectOrganizationRoles(t *testing.T) {
	s := testStore(t)
	auth, err := OpenAuthStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	owner, token, session, err := auth.login("review-owner@example.test", "synthetic-password", true)
	if err != nil {
		t.Fatal(err)
	}
	viewer, viewerToken, viewerSession, err := auth.login("review-viewer@example.test", "synthetic-password", true)
	if err != nil {
		t.Fatal(err)
	}
	space := auth.spaces(owner.ID)[0]["id"]
	p, _, err := s.CreateProject(space, "review-auth-project", ProjectInput{Title: "Review", Brief: "A review assignment"})
	if err != nil {
		t.Fatal(err)
	}
	state, err := s.media.AddAsset(p.ID, space, "review-auth-asset", "image", "shot", "", "", "uploaded", syntheticPNG(0), "image/png")
	if err != nil {
		t.Fatal(err)
	}
	h := SecureHandler(s, "http://localhost:5173", auth, AuthOptions{})
	path := "/api/v1/projects/" + p.ID + "/media"
	authRequest(t, h, "GET", path+"/review-members?space_id="+space, "", viewerToken, "", 403)
	memberPath := "/api/v1/organizations/" + space + "/members"
	authRequest(t, h, "PUT", memberPath, `{"email":"review-viewer@example.test","role":"viewer"}`, token, session.CSRF, 200)
	response := authRequest(t, h, "GET", path+"/review-members?space_id="+space, "", token, "", 200)
	var members []productionMember
	if json.Unmarshal(response.Body.Bytes(), &members) != nil || len(members) != 1 || members[0].ID != owner.ID {
		t.Fatal("viewer can be assigned", response.Body.String())
	}
	in := ProductionDecision{ExpectedVersion: state.Version, Action: "review", AssetID: state.Assets[0].ID, Text: "fix continuity", AssigneeID: viewer.ID, Priority: "high"}
	raw, _ := json.Marshal(in)
	authRequest(t, h, "POST", path+"/decisions?space_id="+space, string(raw), token, session.CSRF, 400)
	authRequest(t, h, "POST", path+"/decisions?space_id="+space, string(raw), viewerToken, viewerSession.CSRF, 403)
	authRequest(t, h, "PUT", memberPath, `{"email":"review-viewer@example.test","role":"editor"}`, token, session.CSRF, 200)
	authRequest(t, h, "POST", path+"/decisions?space_id="+space, string(raw), token, session.CSRF, 200)
	note := s.media.Get(p.ID, space).Reviews[0]
	if note.CreatedBy != owner.ID || note.AssigneeID != viewer.ID {
		t.Fatal("server actor not preserved", note)
	}
}
