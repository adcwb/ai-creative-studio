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
			blockedTask := "part-2"
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
					if revokeOnGet && id == "part-7" {
						if _, err := s.revokeDigitalHuman(p.SpaceID, p.ID, in.AvatarID); err != nil {
							t.Error(err)
						}
					}
					if blocked && id == blockedTask {
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
			blocked = true
			blockedTask = "part-3"
			mu.Unlock()
			s = restored
			originalOutputPath := path
			// Missing/corrupt reused evidence must fail before any paid replacement
			// request, even if its JSON metadata still names the correct blob.
			corrupt, err := s.replaceAvatarSegments(ctx, p.SpaceID, p.ID, job.ID, "corrupt-reuse", "test-actor", avatarReplacementInput{SourceAsset: job.AssetID, ExpectedVersion: p.Version, Segments: []int{1}, Confirmed: true})
			if err != nil {
				t.Fatal(err)
			}
			proof := job.AvatarCheckpoint.Segments[1].Video
			proofPath := filepath.Join(s.media.dir, filepath.Base(proof.Key))
			if mode == "postgres" {
				proofPath = filepath.Join(blobRoot, proof.Key)
			}
			originalBytes, err := os.ReadFile(proofPath)
			if err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(proofPath, []byte("corrupt-proof"), 0600); err != nil {
				t.Fatal(err)
			}
			leaseAvatarSegmentTest(t, s, &corrupt, "creative")
			corruptErr := s.executeCreativeJob(ctx, &corrupt)
			if err = os.WriteFile(proofPath, originalBytes, 0600); err != nil {
				t.Fatal(err)
			}
			if corruptErr == nil {
				t.Fatal("corrupt reused proof accepted")
			}
			mu.Lock()
			noPaidDispatch := posts == 2
			mu.Unlock()
			if !noPaidDispatch {
				t.Fatal("paid dispatch before reuse integrity check")
			}
			corrupt.Status = "failed"
			if err = s.creative.updateJob(corrupt); err != nil {
				t.Fatal(err)
			}
			// An immutable new take replaces only one selected window. Existing
			// confirmed clips are referenced, never copied or submitted again.
			replaceIn := avatarReplacementInput{SourceAsset: job.AssetID, ExpectedVersion: p.Version, Segments: []int{2}, Confirmed: true}
			replacement, err := s.replaceAvatarSegments(ctx, p.SpaceID, p.ID, job.ID, "replace-part", "replacement-actor", replaceIn)
			if err != nil {
				t.Fatal("replacement admission", err)
			}
			replayed, err := s.replaceAvatarSegments(ctx, p.SpaceID, p.ID, job.ID, "replace-part", "replacement-actor", replaceIn)
			if err != nil || replayed.ID != replacement.ID {
				t.Fatal("replacement replay", err)
			}
			bad := replaceIn
			bad.SourceAsset = "stale-output"
			if _, err = s.replaceAvatarSegments(ctx, p.SpaceID, p.ID, job.ID, "stale-replace", "test-actor", bad); err == nil {
				t.Fatal("stale output accepted")
			}
			if _, err = s.replaceAvatarSegments(ctx, "other-space", p.ID, job.ID, "foreign-replace", "test-actor", replaceIn); err == nil {
				t.Fatal("foreign replacement accepted")
			}
			if replacement.ReplacementOf != job.ID || replacement.ID == job.ID || replacement.AvatarCheckpoint.Segments[0].ReusedFrom != job.ID || !sameAvatarBlob(replacement.AvatarCheckpoint.Segments[0].Video, job.AvatarCheckpoint.Segments[0].Video) {
				t.Fatal("replacement provenance", replacement)
			}
			leaseAvatarSegmentTest(t, s, &replacement, "creative")
			if err = s.executeCreativeJob(ctx, &replacement); err == nil || !avatarRecoveryAvailable(replacement) {
				t.Fatal("replacement interruption", err)
			}
			if replacement.AvatarCheckpoint.Segments[1].Phase != "remote_pending" {
				t.Fatal("replacement lost original remote task")
			}
			mu.Lock()
			blocked = false
			mu.Unlock()
			// The same checkpoint continues by GET; this cannot submit part 3 twice.
			if err = s.executeCreativeJob(ctx, &replacement); err != nil {
				t.Fatal("replacement recovery", err)
			}
			replacement.Status = "succeeded"
			if err = s.creative.updateJob(replacement); err != nil {
				t.Fatal(err)
			}
			if err = validateVoiceAuthorizations(s.creative.data); err != nil {
				t.Fatal("lineage validation", err)
			}
			path, _, err = s.media.file(p.ID, p.SpaceID, replacement.AssetID, false, false)
			if err != nil {
				t.Fatal(err)
			}
			replacedProbe, err := probeMedia(path)
			if err != nil {
				t.Fatal(err)
			}
			replacedDuration, _ := strconv.ParseFloat(replacedProbe.Format.Duration, 64)
			if math.Abs(replacedDuration-23.125) > .1 || replacement.AssetID == job.AssetID {
				t.Fatal("replacement changed duration or overwrote output", replacedDuration)
			}
			// Replacing the opposite window of a replacement keeps the exact
			// original ownership of reused proofs and a separate parent version.
			nested, err := s.replaceAvatarSegments(ctx, p.SpaceID, p.ID, replacement.ID, "replace-earlier-window", "test-actor", avatarReplacementInput{SourceAsset: replacement.AssetID, ExpectedVersion: p.Version, Segments: []int{1}, Confirmed: true})
			if err != nil {
				t.Fatal("nested replacement", err)
			}
			if nested.ReplacementOf != replacement.ID || nested.AvatarCheckpoint.Segments[1].ReusedFrom != replacement.ID {
				t.Fatal("nested proof owner not preserved")
			}
			leaseAvatarSegmentTest(t, s, &nested, "creative")
			if err = s.executeCreativeJob(ctx, &nested); err != nil {
				t.Fatal("earlier-window replacement", err)
			}
			nested.Status = "succeeded"
			if err = s.creative.updateJob(nested); err != nil {
				t.Fatal(err)
			}
			// Whole-track remuxing must preserve exactly the same encoded speech,
			// instead of splicing independently encoded segment audio at joins.
			hashes := []string{}
			for n, inputPath := range []string{originalOutputPath, path} {
				hashPath := filepath.Join(temp, fmt.Sprintf("audio-hash-%d.txt", n))
				if err = ffmpeg(ctx, "-protocol_whitelist", "file,pipe", "-i", inputPath, "-map", "0:a:0", "-c:a", "copy", "-f", "hash", "-hash", "sha256", hashPath); err != nil {
					t.Fatal(err)
				}
				raw, readErr := os.ReadFile(hashPath)
				if readErr != nil {
					t.Fatal(readErr)
				}
				hashes = append(hashes, string(raw))
			}
			if hashes[0] != hashes[1] {
				t.Fatal("replacement changed authorized speech track")
			}
			// Reopening storage validates persisted lineage, not just in-memory state.
			if mode == "file" {
				dir := filepath.Dir(s.creative.path)
				if err = s.Close(); err != nil {
					t.Fatal(err)
				}
				s, err = OpenStore(dir)
				if err != nil {
					t.Fatal("replacement reopen", err)
				}
			} else {
				s = postgresTestStore(t, s.pg.db)
				b, _ := NewFileBlobStore(blobRoot)
				if err = configureBlobStorage(s, b); err != nil {
					t.Fatal(err)
				}
			}
			s.creative.avatarLicenseConfirmed = true
			s.creative.avatarPhoto = cloud
			s.creative.avatarPhotoCaps = Capabilities{Provider: wanAvatarProvider, Model: "wan2.2-s2v", Mode: "real", Ready: true}
			savedReplacement := s.creative.data.Jobs[replacement.ID]
			if savedReplacement.ReplacementOf != job.ID || savedReplacement.AvatarCheckpoint.Segments[0].ReusedFrom != job.ID {
				t.Fatal("replacement lineage lost on reopen")
			}
			tampered := cloneCreativeJob(savedReplacement)
			tampered.AvatarCheckpoint = cloneAvatarCheckpoint(savedReplacement.AvatarCheckpoint)
			tampered.AvatarCheckpoint.Segments[0].AudioSHA = strings.Repeat("d", 64)
			badState := creativeDisk{Jobs: map[string]CreativeJob{job.ID: job, tampered.ID: tampered}}
			if validateAvatarDerivatives(badState) == nil {
				t.Fatal("retimed/mismatched proof accepted")
			}
			original := s.creative.data.Jobs[job.ID]
			if original.AssetID != job.AssetID || original.AvatarCheckpoint.Segments[1].RemoteID != "part-2" {
				t.Fatal("original take mutated")
			}
			mu.Lock()
			if posts != 4 || queries["part-1"] != 1 || queries["part-3"] != 2 || queries["part-4"] != 1 {
				t.Fatal("replacement resubmitted unselected/unknown task", posts, queries)
			}
			failPost = 6
			mu.Unlock()
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
			if posts != 6 {
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
			if posts != 7 {
				t.Fatal("later segment dispatched after revocation", posts)
			}
			if _, err := s.replaceAvatarSegments(ctx, p.SpaceID, p.ID, job.ID, "revoked-replace", "test-actor", replaceIn); err == nil {
				t.Fatal("revoked replacement admitted")
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
