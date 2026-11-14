package application

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/adcwb/ai-creative-studio/backend/internal/platform/jobqueue"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func cloudAvatarFixture(t *testing.T, s *FileStore, space string) (Project, CreativeJobInput, []byte) {
	t.Helper()
	p, _, in, video := avatarFixture(t, s, space)
	media, err := s.media.AddAsset(p.ID, space, "cloud-avatar-photo", "Cloud test photo", "reference", "", "", "synthetic", pngFixture(t, 400, 400), "image/png")
	if err != nil {
		t.Fatal(err)
	}
	a, _, err := s.createDigitalHuman(space, p.ID, "cloud-avatar-profile", "test-actor", DigitalHumanInput{Name: "Cloud test person", ReferenceAssetID: media.Assets[len(media.Assets)-1].ID, ConsentReference: "synthetic-cloud-consent", LikenessConfirmed: true, SinglePersonConfirmed: true})
	if err != nil {
		t.Fatal(err)
	}
	grant, e := s.saveVoiceAuthorization(space, p.ID, "cloud-voice", "test-actor", in.AudioAssetID, in.VoiceConsentReference, true)
	if e != nil {
		t.Fatal(e)
	}
	in.Kind = "digital_human_authorized"
	in.VoiceAuthorizationID = grant.ID
	in.consentActor = "test-actor"
	in.AvatarID = a.ID
	in.ExpectedProvider = wanAvatarProvider
	in.ExpectedModel = "wan2.2-s2v"
	in.CloudTransferConfirmed = true
	return p, in, video
}
func TestDigitalHumanCloudAsyncRecoveryNeverResubmits(t *testing.T) {
	for _, mode := range []string{"file", "postgres"} {
		t.Run(mode, func(t *testing.T) {
			s := testStore(t)
			root := t.TempDir()
			if mode == "postgres" {
				s = postgresTestStore(t, postgresTestDB(t))
				blob, err := NewFileBlobStore(root)
				if err != nil {
					t.Fatal(err)
				}
				if err = configureBlobStorage(s, blob); err != nil {
					t.Fatal(err)
				}
			}
			p, in, video := cloudAvatarFixture(t, s, "cloud-avatar-space")
			var mu sync.Mutex
			posts, gets, uploads, deletes := 0, 0, 0, 0
			interrupt := true
			lost := false
			stored := map[string][]byte{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				switch {
				case r.Method == "PUT":
					if r.Header.Get("Authorization") != "" || r.URL.Query().Get("X-Amz-Expires") != "120" {
						t.Error("object credentials not presigned-only")
					}
					data, _ := io.ReadAll(r.Body)
					stored[r.URL.Path] = data
					uploads++
					w.WriteHeader(200)
				case r.Method == "DELETE":
					delete(stored, r.URL.Path)
					deletes++
					w.WriteHeader(204)
				case r.URL.Path == "/api/v1/services/aigc/image2video/video-synthesis":
					posts++
					if r.Header.Get("Authorization") != "Bearer synthetic-cloud-secret" || r.Header.Get("X-DashScope-Async") != "enable" {
						t.Error("missing cloud protocol headers")
					}
					var body struct {
						Model      string            `json:"model"`
						Input      map[string]string `json:"input"`
						Parameters map[string]string `json:"parameters"`
					}
					if json.NewDecoder(r.Body).Decode(&body) != nil || body.Model != "wan2.2-s2v" || body.Parameters["resolution"] != "720P" {
						t.Error("invalid actual submission")
					}
					for _, name := range []string{"image_url", "audio_url"} {
						u, e := url.Parse(body.Input[name])
						if e != nil || u.Query().Get("X-Amz-Expires") != "86400" || len(stored[u.Path]) == 0 {
							t.Error("missing private transferred input", name)
						}
					}
					if lost {
						w.WriteHeader(502)
						fmt.Fprint(w, `<html>synthetic ambiguous error</html>`)
						return
					}
					fmt.Fprint(w, `{"output":{"task_id":"task-fixture-1","task_status":"PENDING"}}`)
				case r.URL.Path == "/api/v1/tasks/task-fixture-1":
					gets++
					if interrupt {
						w.WriteHeader(503)
						return
					}
					fmt.Fprint(w, `{"output":{"task_id":"task-fixture-1","task_status":"SUCCEEDED","results":{"video_url":"https://result.example.invalid/signed-secret?token=do-not-expose"}}}`)
				default:
					w.WriteHeader(501)
				}
			}))
			defer server.Close()
			endpoint, _ := url.Parse(server.URL)
			bridge := &S3ReferenceBridge{endpoint: endpoint, config: S3ReferenceConfig{Bucket: "test-private", Region: "test-region", AccessKey: "synthetic-access", SecretKey: "synthetic-secret"}, now: time.Now, client: server.Client()}
			cloud := &avatarCloud{api: newCreativeProvider("synthetic-cloud-secret", server.URL+"/api/v1", server.Client()), inputs: bridge, tenants: map[string]bool{p.SpaceID: true}, resolution: "720P", fingerprint: strings.Repeat("a", 64), pollInterval: time.Millisecond, download: func(_ context.Context, raw string, out io.Writer) error {
				if !strings.HasPrefix(raw, "https://result.example.invalid/") {
					t.Fatal(raw)
				}
				_, e := out.Write(video)
				return e
			}}
			s.creative.avatarPhoto = cloud
			s.creative.avatarPhotoCaps = Capabilities{Provider: wanAvatarProvider, Model: "wan2.2-s2v", Mode: "real", Ready: true}
			denied := in
			denied.CloudTransferConfirmed = false
			if _, _, e := s.createCreativeJob(p.SpaceID, p.ID, "no-transfer", denied); e == nil {
				t.Fatal("unconfirmed external transfer accepted")
			}
			job, _, err := s.createCreativeJob(p.SpaceID, p.ID, "cloud-job", in)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "postgres" {
				q := jobqueue.Queue{DB: s.pg.db, Lease: time.Minute}
				lease, ok, e := q.Claim(context.Background(), "creative")
				if e != nil || !ok {
					t.Fatal(e)
				}
				if e = q.Dispatch(context.Background(), lease); e != nil {
					t.Fatal(e)
				}
				job.lease = &lease
			}
			setAvatarTestRunning(t, s, &job)
			if err = s.executeCreativeJob(context.Background(), &job); err == nil || job.AvatarCheckpoint == nil || job.AvatarCheckpoint.Phase != "remote_pending" {
				t.Fatal("acknowledged task not recoverable", err, job.AvatarCheckpoint)
			}
			job.Status = "failed"
			if err = s.creative.updateJob(job); err != nil {
				t.Fatal(err)
			}
			var restored *FileStore
			if mode == "file" {
				dir := filepath.Dir(s.creative.path)
				if err = s.Close(); err != nil {
					t.Fatal(err)
				}
				restored, err = OpenStore(dir)
			}
			if mode == "postgres" {
				restored = postgresTestStore(t, s.pg.db)
				blob, _ := NewFileBlobStore(root)
				err = configureBlobStorage(restored, blob)
			}
			if err != nil {
				t.Fatal(err)
			}
			restored.creative.avatarLicenseConfirmed = true
			restored.creative.avatarPhoto = cloud
			restored.creative.avatarPhotoCaps = s.creative.avatarPhotoCaps
			job, err = restored.resumeCreativeJob(p.SpaceID, p.ID, job.ID, "cloud-resume")
			if err != nil {
				t.Fatal(err)
			}
			if mode == "postgres" {
				q := jobqueue.Queue{DB: restored.pg.db, Lease: time.Minute}
				lease, ok, e := q.Claim(context.Background(), "creative_resume")
				if e != nil || !ok {
					t.Fatal(e)
				}
				if e = q.Dispatch(context.Background(), lease); e != nil {
					t.Fatal(e)
				}
				job.lease = &lease
			}
			setAvatarTestRunning(t, restored, &job)
			mu.Lock()
			interrupt = false
			mu.Unlock()
			if err = restored.executeCreativeJob(context.Background(), &job); err != nil {
				t.Fatal(err)
			}
			job.Status = "succeeded"
			if err = restored.creative.updateJob(job); err != nil {
				t.Fatal(err)
			}
			public, _ := json.Marshal(publicCreativeJob(job))
			if strings.Contains(string(public), "signed-secret") || strings.Contains(string(public), "cloud_destination") || strings.Contains(string(public), "avatar-inputs/") {
				t.Fatal("private checkpoint leaked", string(public))
			}
			mu.Lock()
			if posts != 1 || uploads != 2 || deletes != 2 || gets != 2 {
				t.Fatal(posts, uploads, deletes, gets)
			}
			lost = true
			mu.Unlock()
			s = restored
			second, _, err := s.createCreativeJob(p.SpaceID, p.ID, "cloud-lost-job", in)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "postgres" {
				q := jobqueue.Queue{DB: s.pg.db, Lease: time.Minute}
				lease, ok, e := q.Claim(context.Background(), "creative")
				if e != nil || !ok {
					t.Fatal(e)
				}
				if e = q.Dispatch(context.Background(), lease); e != nil {
					t.Fatal(e)
				}
				second.lease = &lease
			}
			setAvatarTestRunning(t, s, &second)
			if err = s.executeCreativeJob(context.Background(), &second); err == nil || second.AvatarCheckpoint.Phase != "dispatching" {
				t.Fatal("ambiguous submission not retained", err)
			}
			if err = s.executeCreativeJob(context.Background(), &second); err == nil {
				t.Fatal("unacknowledged submission repeated")
			}
			mu.Lock()
			defer mu.Unlock()
			if posts != 2 {
				t.Fatal("unknown remote POST repeated", posts)
			}
		})
	}
}
func TestDigitalHumanCloudConfigurationAndInputWindow(t *testing.T) {
	t.Setenv("DIGITAL_HUMAN_CLOUD_WORKSPACE", "invalid/host")
	t.Setenv("DIGITAL_HUMAN_CLOUD_API_KEY", "synthetic-key")
	if _, e := configureAvatarCloud(); e == nil {
		t.Fatal("invalid endpoint accepted")
	}
	s := testStore(t)
	p, _, in, _ := avatarFixture(t, s, "cloud-boundary")
	cloud := &avatarCloud{tenants: map[string]bool{p.SpaceID: true}, inputs: &S3ReferenceBridge{}}
	in.CloudTransferConfirmed = true
	job := CreativeJob{Kind: "digital_human_authorized", VoiceAuthorizationID: "synthetic-grant", SpaceID: p.SpaceID, Provider: wanAvatarProvider, Model: "wan2.2-s2v", CloudTransferConfirmed: true}
	if e := cloud.allowed(job); e != nil {
		t.Fatal(e)
	}
	delete(cloud.tenants, p.SpaceID)
	if cloud.allowed(job) == nil {
		t.Fatal("space transfer authorization ignored")
	}
	ref, rm, _ := s.media.file(p.ID, p.SpaceID, s.creative.data.DigitalHumans[in.AvatarID].ReferenceAssetID, false, false)
	audio, am, _ := s.media.file(p.ID, p.SpaceID, in.AudioAssetID, false, false)
	if cloud.validate(&avatarWork{route: "photo", referencePath: ref, audioPath: audio, referenceMIME: rm, audioMIME: am, duration: 1}) == nil {
		t.Fatal("wan native 400px minimum ignored")
	}
	if _, err := os.Stat(audio); err != nil {
		t.Fatal(err)
	}
}

func TestDigitalHumanCloudProtocolRejectsForeignIDsAndUnsafeDownloads(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/tasks/original-task" || r.Method != "GET" {
			t.Error("unexpected request", r.Method, r.URL.Path)
		}
		fmt.Fprint(w, `{"output":{"task_id":"foreign-task","task_status":"SUCCEEDED","results":{"video_url":"http://127.0.0.1/private"}}}`)
	}))
	defer server.Close()
	p := &avatarCloud{api: newCreativeProvider("synthetic-only", server.URL, server.Client())}
	if _, err := p.poll(context.Background(), "../other-task"); err == nil || calls != 0 {
		t.Fatal("task path traversal accepted")
	}
	if _, err := p.poll(context.Background(), "original-task"); err == nil || calls != 1 {
		t.Fatal("foreign result accepted")
	}
	if err := safeVideoDownloadTo(context.Background(), "http://127.0.0.1/private", io.Discard); err == nil {
		t.Fatal("private result URL accepted")
	}
	job := CreativeJob{Kind: "digital_human_authorized", VoiceAuthorizationID: "grant", SpaceID: "allowed", Provider: wanAvatarProvider, Model: "wan2.2-s2v", CloudTransferConfirmed: true, AvatarCheckpoint: &avatarCheckpoint{CloudDestination: "old-destination"}}
	p.inputs = &S3ReferenceBridge{}
	p.tenants = map[string]bool{"allowed": true}
	p.fingerprint = "new-destination"
	if p.allowed(job) == nil {
		t.Fatal("changed destination used to query original task")
	}
}

func TestDigitalHumanCloudConfiguredWithoutNetworkAndNeverFallsBack(t *testing.T) {
	t.Setenv("DIGITAL_HUMAN_PHOTO_PROVIDER", wanAvatarProvider)
	t.Setenv("DIGITAL_HUMAN_CLOUD_WORKSPACE", "synthetic-workspace")
	t.Setenv("DIGITAL_HUMAN_CLOUD_API_KEY", "synthetic-cloud-key")
	t.Setenv("DIGITAL_HUMAN_CLOUD_TENANTS", "space-a,space-b")
	t.Setenv("DIGITAL_HUMAN_CLOUD_RESOLUTION", "720P")
	t.Setenv("DIGITAL_HUMAN_LICENSE_ACK", "true")
	t.Setenv("REFERENCE_BRIDGE_ENABLED", "true")
	t.Setenv("REFERENCE_S3_ENDPOINT", "https://objects.example.invalid")
	t.Setenv("REFERENCE_S3_BUCKET", "private-fixture")
	t.Setenv("REFERENCE_S3_REGION", "test-region")
	t.Setenv("REFERENCE_S3_ACCESS_KEY", "synthetic-access")
	t.Setenv("REFERENCE_S3_SECRET_KEY", "synthetic-secret")
	s := testStore(t)
	ConfigureDigitalHumanFromEnv(s)
	p, ok := s.creative.avatarPhoto.(*avatarCloud)
	if !ok || !s.creative.avatarCaps("photo").Ready || p.api.endpoint != "https://synthetic-workspace.cn-beijing.maas.aliyuncs.com/api/v1" || !p.tenants["space-a"] || p.resolution != "720P" {
		t.Fatal("dedicated protocol not wired")
	}
	t.Setenv("DIGITAL_HUMAN_CLOUD_TENANTS", "*")
	ConfigureDigitalHumanFromEnv(s)
	if s.creative.avatarPhoto != nil || s.creative.avatarCaps("photo").Ready {
		t.Fatal("invalid cloud config fell back to another executor")
	}
}
