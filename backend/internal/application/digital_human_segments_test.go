package application

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/adcwb/ai-creative-studio/backend/internal/platform/jobqueue"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func leaseAvatarSegmentTest(t *testing.T, s *FileStore, job *CreativeJob, kind string) {
	t.Helper()
	if s.pg != nil {
		q := jobqueue.Queue{DB: s.pg.db, Lease: time.Minute}
		j, ok, e := q.Claim(context.Background(), kind)
		if e != nil || !ok {
			t.Fatal(j, ok, e)
		}
		if e = q.Dispatch(context.Background(), j); e != nil {
			t.Fatal(e)
		}
		job.lease = &j
	}
	setAvatarTestRunning(t, s, job)
}
func TestDigitalHumanAutomaticSegmentsRecoverExactAudioWithoutDuplicateGeneration(t *testing.T) {
	for _, mode := range []string{"file", "postgres"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			s := testStore(t)
			blobRoot := t.TempDir()
			if mode == "postgres" {
				s = postgresTestStore(t, postgresTestDB(t))
				b, _ := NewFileBlobStore(blobRoot)
				if e := configureBlobStorage(s, b); e != nil {
					t.Fatal(e)
				}
			}
			p, in, _ := cloudAvatarFixture(t, s, "segment-space")
			temp := t.TempDir()
			audioPath := filepath.Join(temp, "long.wav")
			if e := ffmpeg(ctx, "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000:duration=23.125", "-c:a", "pcm_s16le", audioPath); e != nil {
				t.Fatal(e)
			}
			wav, _ := os.ReadFile(audioPath)
			media, e := s.media.AddAsset(p.ID, p.SpaceID, "long-audio", "Long authorized speech", "voice", "", "", "synthetic", wav, "audio/wav")
			if e != nil {
				t.Fatal(e)
			}
			in.AudioAssetID = media.Assets[len(media.Assets)-1].ID
			grant, e := s.saveVoiceAuthorization(p.SpaceID, p.ID, "long-voice", "test-actor", in.AudioAssetID, in.VoiceConsentReference, true)
			if e != nil {
				t.Fatal(e)
			}
			in.VoiceAuthorizationID = grant.ID
			var mu sync.Mutex
			posts := 0
			blocked := true
			failPost := 0
			revokeOnGet := false
			durations := map[string]float64{}
			uploads := map[string][]byte{}
			queries := map[string]int{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				switch {
				case r.Method == "PUT":
					b, _ := io.ReadAll(r.Body)
					uploads[r.URL.Path] = b
					w.WriteHeader(200)
				case r.Method == "DELETE":
					delete(uploads, r.URL.Path)
					w.WriteHeader(204)
				case r.Method == "POST":
					posts++
					var body struct {
						Input map[string]string `json:"input"`
					}
					json.NewDecoder(r.Body).Decode(&body)
					u, _ := url.Parse(body.Input["audio_url"])
					b := uploads[u.Path]
					if len(b) < 44 {
						t.Error("missing audio transfer")
					}
					duration := float64(len(b)-44) / 96000
					if duration >= 20 || duration <= 0 {
						t.Error("native audio window", duration)
					}
					id := fmt.Sprintf("part-%d", posts)
					durations[id] = duration
					if posts == failPost {
						w.WriteHeader(502)
						return
					}
					fmt.Fprintf(w, `{"output":{"task_id":%q,"task_status":"PENDING"}}`, id)
				case strings.HasPrefix(r.URL.Path, "/api/v1/tasks/"):
					id := strings.TrimPrefix(r.URL.Path, "/api/v1/tasks/")
					queries[id]++
					if revokeOnGet && id == "part-5" {
						if _, err := s.revokeDigitalHuman(p.SpaceID, p.ID, in.AvatarID); err != nil {
							t.Error(err)
						}
					}
					if blocked && id == "part-2" {
						w.WriteHeader(503)
						return
					}
					fmt.Fprintf(w, `{"output":{"task_id":%q,"task_status":"SUCCEEDED","results":{"video_url":%q}}}`, id, "https://synthetic.invalid/"+id)
				default:
					w.WriteHeader(501)
				}
			}))
			defer server.Close()
			endpoint, _ := url.Parse(server.URL)
			bridge := &S3ReferenceBridge{endpoint: endpoint, config: S3ReferenceConfig{Bucket: "private", Region: "test", AccessKey: "synthetic", SecretKey: "synthetic"}, now: time.Now, client: server.Client()}
			cloud := &avatarCloud{api: newCreativeProvider("synthetic", server.URL+"/api/v1", server.Client()), inputs: bridge, tenants: map[string]bool{p.SpaceID: true}, resolution: "480P", fingerprint: strings.Repeat("b", 64), pollInterval: time.Millisecond, download: func(ctx context.Context, raw string, out io.Writer) error {
				id := strings.TrimPrefix(raw, "https://synthetic.invalid/")
				mu.Lock()
				duration := durations[id]
				mu.Unlock()
				if duration <= 0 {
					return fmt.Errorf("unexpected download")
				}
				file := filepath.Join(temp, id+".mp4")
				if e := ffmpeg(ctx, "-f", "lavfi", "-i", "color=c=teal:s=64x64:r=24:d="+strconv.FormatFloat(duration, 'f', 6, 64), "-an", "-c:v", "libx264", "-pix_fmt", "yuv420p", file); e != nil {
					return e
				}
				f, e := os.Open(file)
				if e != nil {
					return e
				}
				defer f.Close()
				_, e = io.Copy(out, f)
				return e
			}}
			s.creative.avatarPhoto = cloud
			s.creative.avatarPhotoCaps = Capabilities{Provider: wanAvatarProvider, Model: "wan2.2-s2v", Mode: "real", Ready: true}
			job, _, e := s.createCreativeJob(p.SpaceID, p.ID, "segmented-job", in)
			if e != nil {
				t.Fatal(e)
			}
			leaseAvatarSegmentTest(t, s, &job, "creative")
			if s.creativeJobTimeout(job) < 20*time.Minute {
				t.Fatal("long native plan inherited a single-call deadline")
			}
			if e = s.executeCreativeJob(ctx, &job); e == nil {
				t.Fatal("poll interruption absent")
			}
			if !avatarRecoveryAvailable(job) || job.AvatarCheckpoint.Format != 2 || len(job.AvatarCheckpoint.Segments) != 2 || job.AvatarCheckpoint.Segments[0].Phase != "done" || job.AvatarCheckpoint.Segments[1].Phase != "remote_pending" {
				t.Fatal("segment checkpoint lost", e, job.AvatarCheckpoint)
			}
			if mode == "postgres" {
				if _, e = s.pg.db.Exec(`UPDATE execution_jobs SET lease_until=clock_timestamp()-interval '1 second' WHERE tenant_id=$1 AND id=$2`, p.SpaceID, job.ID); e != nil {
					t.Fatal(e)
				}
				q := jobqueue.Queue{DB: s.pg.db, Lease: time.Minute}
				if _, _, e = q.Claim(ctx, "creative"); e != nil {
					t.Fatal(e)
				}
				if e = ReconcilePostgresJobs(ctx, s.pg.db); e != nil {
					t.Fatal(e)
				}
			}
			var restored *FileStore
			if mode == "file" {
				dir := filepath.Dir(s.creative.path)
				if e = s.Close(); e != nil {
					t.Fatal(e)
				}
				restored, e = OpenStore(dir)
			} else {
				restored = postgresTestStore(t, s.pg.db)
				b, _ := NewFileBlobStore(blobRoot)
				e = configureBlobStorage(restored, b)
			}
			if e != nil {
				t.Fatal(e)
			}
			restored.creative.avatarLicenseConfirmed = true
			restored.creative.avatarPhoto = cloud
			restored.creative.avatarPhotoCaps = s.creative.avatarPhotoCaps
			if mode == "postgres" {
				job, e = restored.resumeCreativeJob(p.SpaceID, p.ID, job.ID, "resume-segments")
				if e != nil {
					t.Fatal(e)
				}
				leaseAvatarSegmentTest(t, restored, &job, "creative_resume")
			} else {
				job = restored.creative.data.Jobs[job.ID]
				if job.Status != "queued" {
					t.Fatal("file restart did not schedule proven recovery", job.Status)
				}
				setAvatarTestRunning(t, restored, &job)
			}
			mu.Lock()
			blocked = false
			mu.Unlock()
			if e = restored.executeCreativeJob(ctx, &job); e != nil {
				t.Fatal(e)
			}
			job.Status = "succeeded"
			if e = restored.creative.updateJob(job); e != nil {
				t.Fatal(e)
			}
			path, _, e := restored.media.file(p.ID, p.SpaceID, job.AssetID, false, false)
			if e != nil {
				t.Fatal(e)
			}
			probe, e := probeMedia(path)
			if e != nil {
				t.Fatal(e)
			}
			duration, _ := strconv.ParseFloat(probe.Format.Duration, 64)
			if math.Abs(duration-23.125) > .1 {
				t.Fatal("time drift", duration)
			}
			audioFound := false
			for _, v := range probe.Streams {
				if v.CodecType == "audio" {
					audioFound = true
				}
			}
			if !audioFound {
				t.Fatal("original speech missing")
			}
			refs, e := avatarCheckpointReferences(restored.creative.data)
			if e != nil || len(refs) != 4 {
				t.Fatal("segment proofs missing from blob migration", refs, e)
			}
			mu.Lock()
			if posts != 2 || queries["part-1"] != 1 || queries["part-2"] != 2 || durations["part-1"] != 18 || durations["part-2"] != 5.125 {
				t.Fatal(posts, queries, durations)
			}
			failPost = 4
			mu.Unlock()
			s = restored
			second, _, e := s.createCreativeJob(p.SpaceID, p.ID, "unknown-segment", in)
			if e != nil {
				t.Fatal(e)
			}
			leaseAvatarSegmentTest(t, s, &second, "creative")
			if e = s.executeCreativeJob(ctx, &second); e == nil || avatarRecoveryAvailable(second) {
				t.Fatal("ambiguous segment treated as safe", e)
			}
			if e = s.executeCreativeJob(ctx, &second); e == nil {
				t.Fatal("unknown segment resubmitted")
			}
			mu.Lock()
			if posts != 4 {
				t.Fatal("duplicate generation", posts)
			}
			revokeOnGet = true
			mu.Unlock()
			second.Status = "failed"
			if e = s.creative.updateJob(second); e != nil {
				t.Fatal(e)
			}
			third, _, e := s.createCreativeJob(p.SpaceID, p.ID, "revoke-segment", in)
			if e != nil {
				t.Fatal(e)
			}
			leaseAvatarSegmentTest(t, s, &third, "creative")
			if e = s.executeCreativeJob(ctx, &third); e == nil {
				t.Fatal("revoked segment published")
			}
			mu.Lock()
			defer mu.Unlock()
			if posts != 5 {
				t.Fatal("later segment dispatched after revocation", posts)
			}
			if _, e = s.media.AssetForReceipt(p.ID, p.SpaceID, "creative-"+third.ID); e == nil {
				t.Fatal("revoked output became observable")
			}

		})
	}
}

func TestAvatarSegmentPlanCannotChangeAtPublication(t *testing.T) {
	s := testStore(t)
	p, in, video := cloudAvatarFixture(t, s, "fixed-segment-plan")
	s.creative.avatarPhotoCaps = Capabilities{Provider: wanAvatarProvider, Model: "wan2.2-s2v", Mode: "real", Ready: true}
	s.creative.avatarPhoto = &avatarCloud{inputs: &S3ReferenceBridge{}, tenants: map[string]bool{p.SpaceID: true}, fingerprint: strings.Repeat("b", 64)}
	job, _, e := s.createCreativeJob(p.SpaceID, p.ID, "fixed-plan", in)
	if e != nil {
		t.Fatal(e)
	}
	setAvatarTestRunning(t, s, &job)
	opaque, _ := projectFingerprint("cloud-avatar-inputs", job.ID, []string{job.SpaceID, job.ProjectID})
	cp := avatarCheckpoint{Format: 2, Phase: "segments", SourceSHA256: strings.Repeat("a", 64), CloudDestination: strings.Repeat("b", 64), CloudImageKey: "avatar-inputs/" + opaque + "/image", CloudAudioKey: "avatar-inputs/" + opaque + "/audio", TotalSamples: 48000, Segments: []avatarSegment{{Start: 0, Samples: 48000, AudioSHA: strings.Repeat("c", 64), Phase: "queued"}}}
	if e = s.creative.recordAvatarCheckpoint(context.Background(), &job, cp); e != nil {
		t.Fatal(e)
	}
	cp.Segments = append([]avatarSegment(nil), cp.Segments...)
	cp.Segments[0].Phase = "dispatching"
	if e = s.creative.recordAvatarCheckpoint(context.Background(), &job, cp); e != nil {
		t.Fatal(e)
	}
	cp.Segments = append([]avatarSegment(nil), cp.Segments...)
	cp.Segments[0].Phase = "remote_pending"
	cp.Segments[0].RemoteID = "fixed-task"
	if e = s.creative.recordAvatarCheckpoint(context.Background(), &job, cp); e != nil {
		t.Fatal(e)
	}
	file := filepath.Join(t.TempDir(), "verified.mp4")
	if e = os.WriteFile(file, video, 0600); e != nil {
		t.Fatal(e)
	}
	if e = s.saveAvatarSegmentVideo(context.Background(), &job, 0, file); e != nil {
		t.Fatal(e)
	}
	changed := cloneAvatarCheckpoint(job.AvatarCheckpoint)
	changed.Phase = "engine_done"
	changed.Engine = &BlobInfo{Key: avatarCheckpointKey(job, "engine"), Size: int64(len(video)), SHA256: job.AvatarCheckpoint.Segments[0].Video.SHA256}
	changed.TotalSamples++
	changed.Segments[0].Samples++
	if e = s.creative.recordAvatarCheckpoint(context.Background(), &job, *changed); e == nil {
		t.Fatal("fixed input plan changed while publishing engine result")
	}
	if job.AvatarCheckpoint.TotalSamples != 48000 || job.AvatarCheckpoint.Phase != "segments" {
		t.Fatal("rejected update damaged prior checkpoint")
	}
}
