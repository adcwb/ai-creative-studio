package application

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

func directorFixture(t *testing.T, s *FileStore) Project {
	t.Helper()
	p, _, e := s.CreateProject("personal-demo", "director-project", ProjectInput{Title: "导演镜头", Brief: "本地合成上游，不调用收费服务"})
	if e != nil {
		t.Fatal(e)
	}
	shots := []StoryboardShot{}
	for _, id := range []string{"previous", "current", "next"} {
		shots = append(shots, StoryboardShot{ID: id, Description: id + " visual", ShotSize: "medium", CameraMovement: "static", DurationSeconds: 3, Dialogue: "PRIVATE_DIALOGUE_" + id, Sound: "PRIVATE_SOUND_" + id, Composition: strings.Repeat("frame direction ", 50), Axis: "keep screen left", Eyeline: "look right", Angle: "low angle", Blocking: "pause before entry", Lighting: "warm edge light", Purpose: "withhold the reveal", Performance: "hesitation", Continuity: "coat stays blue"})
	}
	p, e = s.SaveProjectStoryboard(p.SpaceID, p.ID, "director-board", StoryboardSaveInput{ExpectedVersion: p.Version, Shots: shots})
	if e != nil {
		t.Fatal(e)
	}
	return p
}
func TestDirectorSnapshotActualVisualRequestAndImmutableSource(t *testing.T) {
	for _, storage := range []string{"file", "postgres"} {
		t.Run(storage, func(t *testing.T) {
			s := testStore(t)
			if storage == "postgres" {
				s = postgresTestStore(t, postgresTestDB(t))
			}
			p := directorFixture(t, s)
			var calls atomic.Int32
			var captured string
			output := pngFixture(t, 1536, 864)
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.URL.Path != "/images/generations" {
					t.Error("unexpected path", r.URL.Path)
				}
				var body map[string]any
				_ = json.NewDecoder(r.Body).Decode(&body)
				captured, _ = body["prompt"].(string)
				_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]string{"b64_json": base64.StdEncoding.EncodeToString(output)}}})
			}))
			defer up.Close()
			provider := newOpenAIProvider("synthetic", up.URL+"/images/generations", up.Client())
			SetCreativeImage(s, provider, Capabilities{Ready: true, Mode: "real", Provider: "openai-images", Model: provider.model})
			in := CreativeJobInput{Kind: "image", Prompt: "create the middle shot", ExpectedVersion: p.Version, ExpectedProvider: "openai-images", ExpectedModel: provider.model, ShotID: "current", UseDirectorContext: true}
			job, created, e := s.createCreativeJob(p.SpaceID, p.ID, "director-job", in)
			if e != nil || !created || job.DirectorContext == nil {
				t.Fatal(e, job)
			}

			moderationJob := cloneCreativeJob(job)
			moderationJob.RuntimeSelection = &RuntimeSelection{Capability: "image"}
			moderation, e := s.creativeExecutionRequest(moderationJob)
			if e != nil || !strings.Contains(moderation.Text, job.DirectorContext.Current.Composition) || strings.Contains(moderation.Text, "PRIVATE_") {
				t.Fatal("full visual direction missing from input review", e)
			}
			original := cloneCreativeJob(job)
			job.DirectorContext.Previous.Description = "response mutation"
			job.DirectorContext.OmittedFields[0] = "response mutation"
			replay, created, e := s.createCreativeJob(p.SpaceID, p.ID, "director-job", in)
			if e != nil || created || !reflect.DeepEqual(replay.DirectorContext, original.DirectorContext) {
				t.Fatal("stored source shared", e)
			}
			changed := append([]StoryboardShot(nil), p.Storyboard.Shots...)
			changed[1].Composition = "LATER_FRAME_DO_NOT_SEND"
			changed[2].Description = "LATER_NEIGHBOUR_DO_NOT_SEND"
			newer, e := s.SaveProjectStoryboard(p.SpaceID, p.ID, "director-change", StoryboardSaveInput{ExpectedVersion: p.Version, Shots: changed})
			if e != nil {
				t.Fatal(e)
			}
			// Accepted work uses its immutable historical source. Changed current plans
			// never silently replace that source or cause a second upstream POST.
			if e = s.executeCreativeJob(context.Background(), &replay); e != nil {
				t.Fatal(e)
			}
			if calls.Load() != 1 || !strings.Contains(captured, original.DirectorContext.Current.Composition) || !strings.Contains(captured, "previous visual") || !strings.Contains(captured, "next visual") || strings.Contains(captured, "PRIVATE_") || strings.Contains(captured, "LATER_") || strings.Contains(captured, "duration_intent_seconds") {
				t.Fatal("visual payload lost, replaced, truncated or included sound", calls.Load(), captured)
			}
			state := s.media.Get(p.ID, p.SpaceID)
			if len(state.Assets) != 1 || len(state.Selections) != 0 || len(state.Timeline.Clips) != 0 {
				t.Fatal("generation changed artistic decisions", state)
			}
			jobs, e := s.listCreativeJobs(p.SpaceID, p.ID)
			if e != nil || len(jobs) != 1 || jobs[0].DirectorContext != nil || jobs[0].DirectorSourceSHA256 != original.DirectorSourceSHA256 {
				t.Fatal("polling includes full source", e)
			}
			var detail CreativeJob
			_ = json.Unmarshal(intentLibraryCall(t, s, "GET", "/api/v1/projects/"+p.ID+"/creative-jobs/"+job.ID+"?space_id="+p.SpaceID, "", nil, 200), &detail)
			if !reflect.DeepEqual(detail.DirectorContext, original.DirectorContext) || detail.RuntimeSelection != nil {
				t.Fatal("detail lost source or exposed runtime")
			}
			intentLibraryCall(t, s, "GET", "/api/v1/projects/"+p.ID+"/creative-jobs/"+job.ID+"?space_id=team-demo", "", nil, 404)
			bad := cloneCreativeJob(replay)
			bad.DirectorContext.Current.Composition = "tampered"
			if e = s.executeCreativeJob(context.Background(), &bad); !errors.Is(e, ErrExecutionGovernance) || calls.Load() != 1 {
				t.Fatal("corrupt source dispatched", e)
			}
			modified := in
			modified.UseDirectorContext = false
			if _, _, e = s.createCreativeJob(p.SpaceID, p.ID, "director-job", modified); !errors.Is(e, ErrIdempotencyConflict) {
				t.Fatal("meaning change replayed", e)
			}
			if _, _, e = s.createCreativeJob(p.SpaceID, p.ID, "director-cas", in); !errors.Is(e, ErrVersionConflict) {
				t.Fatal("stale CAS accepted", e)
			}
			current, _ := s.GetProject(p.SpaceID, p.ID)
			if current.Version != newer.Version || current.Storyboard.Shots[1].Composition != changed[1].Composition {
				t.Fatal("historical work overwrote current plan")
			}
		})
	}
}
func TestDirectorControlsNeverPromoteTextToNative(t *testing.T) {
	for _, kind := range []string{"image", "video", "speech"} {
		unknown := directorSupport(kind, "", true, CreativeControls{4, true, true})
		for _, mode := range unknown {
			if mode != "unsupported" {
				t.Fatal("unresolved route claimed support", kind, unknown)
			}
		}
	}
	image := directorSupport("image", "gateway-images", true, CreativeControls{MaxReferences: 2})
	if image["angle"] != "prompt_only" || image["sound"] != "unsupported" || image["duration_seconds"] != "unsupported" || image["image_reference"] != "reference" || image["camera_movement"] == "native" {
		t.Fatal(image)
	}
	video := directorSupport("video", "gemini-video", true, CreativeControls{4, true, true})
	if video["first_frame"] != "native" || video["motion_reference"] != "reference" || video["duration_seconds"] != "prompt_only" || video["sound"] != "unsupported" {
		t.Fatal(video)
	}
	if directorSupport("speech", "gateway-speech", false, CreativeControls{})["native_voice"] != "native" {
		t.Fatal("known voice missing")
	}
}
func TestDirectorContextScopeAndKind(t *testing.T) {
	s := testStore(t)
	p := directorFixture(t, s)
	for _, in := range []CreativeJobInput{{UseDirectorContext: true, Kind: "speech", ShotID: "current"}, {UseDirectorContext: true, Kind: "image"}, {UseDirectorContext: true, Kind: "image", ShotID: "missing"}} {
		if _, e := prepareDirectorSnapshot(p, in); e == nil {
			t.Fatal("invalid scope accepted", in)
		}
	}
	p.ScriptVersion++
	if _, e := prepareDirectorSnapshot(p, CreativeJobInput{UseDirectorContext: true, Kind: "image", ShotID: "current"}); e == nil {
		t.Fatal("stale script source accepted")
	}
	p.ScriptVersion--
	p.Script = &Script{Scenes: []ScriptScene{{EpisodeNumber: 1}, {EpisodeNumber: 2}}}
	for i := range p.Storyboard.Shots {
		p.Storyboard.Shots[i].SceneIndex = 1
	}
	p.Storyboard.Shots[2].SceneIndex = 2
	v, e := prepareDirectorSnapshot(p, CreativeJobInput{UseDirectorContext: true, Kind: "video", ShotID: "current"})
	if e != nil || v.Previous == nil || v.Next != nil || !reflect.DeepEqual(v.OmittedNeighbours, []string{"next"}) {
		t.Fatal("other episode plan sent", v, e)
	}
}

func TestPostgresDirectorSourcesOfflineRoundTrip(t *testing.T) {
	dir := t.TempDir()
	file, e := OpenStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	p := directorFixture(t, file)
	provider := newOpenAIProvider("synthetic", "http://127.0.0.1:1/images/generations", http.DefaultClient)
	SetCreativeImage(file, provider, Capabilities{Ready: true, Mode: "real", Provider: "openai-images", Model: provider.model})
	in := CreativeJobInput{Kind: "image", Prompt: "pin without dispatch", ExpectedVersion: p.Version, ExpectedProvider: "openai-images", ExpectedModel: provider.model, ShotID: "current", UseDirectorContext: true}
	original, _, e := file.createCreativeJob(p.SpaceID, p.ID, "offline-director", in)
	if e != nil {
		t.Fatal(e)
	}
	if e = file.Close(); e != nil {
		t.Fatal(e)
	}
	db := postgresTestDB(t)
	ctx := context.Background()
	if _, e = ImportPostgresData(ctx, dir, db); e != nil {
		t.Fatal(e)
	}
	pg := postgresTestStore(t, db)
	pg.creative.mu.Lock()
	v := cloneCreativeJob(pg.creative.data.Jobs[original.ID])
	pg.creative.mu.Unlock()
	if !reflect.DeepEqual(v.DirectorContext, original.DirectorContext) || validateDirectorSnapshot(&v) != nil {
		t.Fatal("director source changed on import")
	}
	target := t.TempDir() + "/restored"
	if _, e = ExportPostgresData(ctx, target, db); e != nil {
		t.Fatal(e)
	}
	restored, e := OpenStore(target)
	if e != nil {
		t.Fatal(e)
	}
	defer restored.Close()
	replay, created, e := restored.createCreativeJob(p.SpaceID, p.ID, "offline-director", in)
	if e != nil || created || replay.ID != original.ID || !reflect.DeepEqual(replay.DirectorContext, original.DirectorContext) {
		t.Fatal("offline receipt/source replaced", e)
	}
	got, e := restored.GetProject(p.SpaceID, p.ID)
	if e != nil || !reflect.DeepEqual(got.Storyboard, p.Storyboard) {
		t.Fatal("authored shot fields lost", e)
	}
}

func TestDirectorVideoGuidanceDoesNotChangeNativeDuration(t *testing.T) {
	s := testStore(t)
	project := directorFixture(t, s)
	project.Storyboard.Shots[1].DurationSeconds = 121
	fixed, e := prepareDirectorSnapshot(project, CreativeJobInput{Kind: "video", ShotID: "current", UseDirectorContext: true})
	if e != nil {
		t.Fatal(e)
	}
	job := CreativeJob{Kind: "video", ShotID: "current", SourceVersion: project.Version, DirectorContext: fixed, DirectorSourceVersion: fixed.ProjectVersion, DirectorSourceSHA256: fixed.SHA256}
	if e = validateDirectorSnapshot(&job); e != nil {
		t.Fatal(e)
	}
	var calls atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/videos" {
			t.Error("unexpected path", r.URL.Path)
		}
		if e := r.ParseMultipartForm(1 << 20); e != nil {
			t.Error(e)
			return
		}
		defer r.MultipartForm.RemoveAll()
		text := r.FormValue("prompt")
		if r.FormValue("seconds") != "8" || r.FormValue("size") != "1280x720" || !strings.Contains(text, `"duration_intent_seconds":121`) || !strings.Contains(text, fixed.Current.Composition) || strings.Contains(text, "PRIVATE_") {
			t.Error("native duration was confused with intent or source omitted", r.Form)
		}
		_ = json.NewEncoder(w).Encode(VideoJob{ID: "synthetic-video-1", Status: "queued"})
	}))
	defer up.Close()
	provider := &gatewayCreativeProvider{OpenAICreativeProvider: newCreativeProvider("synthetic", up.URL, up.Client()), model: GatewayModel{Model: "synthetic-video", VideoSeconds: 8, VideoSize: "1280x720"}}
	if _, e = provider.SubmitVideo(withDirectorContext(context.Background(), &job), "Create the current visual shot"); e != nil || calls.Load() != 1 {
		t.Fatal("actual video request failed", e, calls.Load())
	}
}
