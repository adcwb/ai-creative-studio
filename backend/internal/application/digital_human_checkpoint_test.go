package application

import (
	"context"
	"errors"
	"github.com/adcwb/ai-creative-studio/backend/internal/platform/jobqueue"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type avatarFailBlob struct {
	*FileBlobStore
	fail string
}

func (s *avatarFailBlob) PutStream(ctx context.Context, info BlobInfo, r io.Reader) (BlobInfo, error) {
	if info.Key == s.fail {
		return BlobInfo{}, errors.New("synthetic checkpoint write interruption")
	}
	return s.FileBlobStore.PutStream(ctx, info, r)
}
func setAvatarTestRunning(t *testing.T, s *FileStore, job *CreativeJob) {
	t.Helper()
	job.Status = "running"
	s.creative.mu.Lock()
	s.creative.data.Jobs[job.ID] = *job
	err := s.creative.saveLocked()
	s.creative.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
}
func TestDigitalHumanCheckpointRestartsWithoutEngineAcrossWorkers(t *testing.T) {
	for _, mode := range []string{"file", "postgres"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			s := testStore(t)
			var queue jobqueue.Queue
			root := t.TempDir()
			if mode == "postgres" {
				s = postgresTestStore(t, postgresTestDB(t))
				store, err := NewFileBlobStore(root)
				if err != nil {
					t.Fatal(err)
				}
				wrapped := &avatarFailBlob{FileBlobStore: store}
				if err = configureBlobStorage(s, wrapped); err != nil {
					t.Fatal(err)
				}
				queue = jobqueue.Queue{DB: s.pg.db, Lease: time.Minute}
			}
			p, _, in, _ := avatarFixture(t, s, "avatar-checkpoint-space")
			s.creative.avatarPhoto = &LocalTransform{command: fileAvatarExecutable(t), model: in.ExpectedModel, protocol: "files-v1", timeout: 15 * time.Second}
			job, _, err := s.createCreativeJob(p.SpaceID, p.ID, "checkpoint-job", in)
			if err != nil {
				t.Fatal(err)
			}
			collision := filepath.Join(s.media.dir, filepath.Base(avatarCheckpointKey(job, "composed")))
			if mode == "file" {
				if err = os.WriteFile(collision, []byte("synthetic incomplete cache collision"), 0600); err != nil {
					t.Fatal(err)
				}
			} else {
				s.blobs.store.(*avatarFailBlob).fail = avatarCheckpointKey(job, "composed")
				lease, ok, e := queue.Claim(ctx, "creative")
				if e != nil || !ok {
					t.Fatal(lease, ok, e)
				}
				if e = queue.Dispatch(ctx, lease); e != nil {
					t.Fatal(e)
				}
				job.lease = &lease
			}
			setAvatarTestRunning(t, s, &job)
			if err = s.executeCreativeJob(ctx, &job); err == nil {
				t.Fatal("interruption did not stop publication")
			}
			if job.AvatarCheckpoint == nil || job.AvatarCheckpoint.Phase != "engine_done" || !avatarCheckpointReady(job) {
				t.Fatal("confirmed engine proof not saved", job.AvatarCheckpoint)
			}
			if _, err = s.media.AssetForReceipt(p.ID, p.SpaceID, "creative-"+job.ID); err == nil {
				t.Fatal("published before composed proof")
			}
			var restored *FileStore
			if mode == "file" {
				dir := filepath.Dir(s.path)
				if err = os.Remove(collision); err != nil {
					t.Fatal(err)
				}
				if err = s.Close(); err != nil {
					t.Fatal(err)
				}
				restored, err = OpenStore(dir)
			} else {
				job.Status = "failed"
				if err = s.creative.updateJob(job); err != nil {
					t.Fatal(err)
				}
				restored = postgresTestStore(t, s.pg.db)
				store, e := NewFileBlobStore(root)
				if e != nil {
					t.Fatal(e)
				}
				err = configureBlobStorage(restored, store)
			}
			if err != nil {
				t.Fatal(err)
			}
			defer restored.Close()
			restored.creative.avatarLicenseConfirmed = true
			// No engine is configured in this process: success can only use the verified checkpoint.
			if restored.creative.avatarPhoto != nil {
				t.Fatal("test would hide a repeated model invocation")
			}
			if mode == "postgres" {
				job, err = restored.resumeCreativeJob(p.SpaceID, p.ID, job.ID, "resume-checkpoint")
				if err != nil {
					t.Fatal(err)
				}
				lease, ok, e := queue.Claim(ctx, "creative_resume")
				if e != nil || !ok {
					t.Fatal(lease, ok, e)
				}
				if e = queue.Dispatch(ctx, lease); e != nil {
					t.Fatal(e)
				}
				job.lease = &lease
			} else {
				job = restored.creative.data.Jobs[job.ID]
				if job.Status != "queued" {
					t.Fatal("known proof was not restored", job.Status)
				}
			}
			setAvatarTestRunning(t, restored, &job)
			if err = restored.executeCreativeJob(ctx, &job); err != nil {
				t.Fatal("checkpoint recovery", err)
			}
			job.Status = "succeeded"
			if err = restored.creative.updateJob(job); err != nil {
				t.Fatal(err)
			}
			result, err := restored.media.AssetForReceipt(p.ID, p.SpaceID, "creative-"+job.ID)
			if err != nil || result.Kind != "video" || result.Duration < .9 {
				t.Fatal(result, err)
			}
			if mode == "postgres" {
				again, e := restored.resumeCreativeJob(p.SpaceID, p.ID, job.ID, "resume-checkpoint")
				if e != nil || again.Status != "succeeded" || again.AssetID != result.ID {
					t.Fatal("resume receipt rewound", again, e)
				}
			}
			if publicCreativeJob(job).AvatarCheckpoint != nil || publicCreativeJob(job).AvatarRecoveryPhase != "composed" {
				t.Fatal("private checkpoint exposed")
			}
			if mode == "file" {
				if err = os.WriteFile(filepath.Join(restored.media.dir, filepath.Base(job.AvatarCheckpoint.Engine.Key)), []byte("tamper"), 0600); err != nil {
					t.Fatal(err)
				}
				if err = restored.executeCreativeJob(ctx, &job); err == nil {
					t.Fatal("corrupt proof accepted")
				}
			}
		})
	}
}
func TestDigitalHumanUnconfirmedCheckpointNeverRepeatsEngine(t *testing.T) {
	s := testStore(t)
	p, _, in, video := avatarFixture(t, s, "checkpoint-unknown")
	provider := &avatarFixtureProvider{video: video}
	s.creative.avatarPhoto = provider
	job, _, err := s.createCreativeJob(p.SpaceID, p.ID, "unknown-native", in)
	if err != nil {
		t.Fatal(err)
	}
	ref, refMIME, err := s.media.file(p.ID, p.SpaceID, job.ReferenceAssetID, false, false)
	if err != nil {
		t.Fatal(err)
	}
	audio, audioMIME, err := s.media.file(p.ID, p.SpaceID, job.AudioAssetID, false, false)
	if err != nil {
		t.Fatal(err)
	}
	sha, err := avatarSourceSHA(context.Background(), job, &avatarWork{referencePath: ref, audioPath: audio, referenceMIME: refMIME, audioMIME: audioMIME, route: "photo"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.creative.recordAvatarCheckpoint(context.Background(), &job, avatarCheckpoint{Format: 1, Phase: "dispatching", SourceSHA256: sha}); err != nil {
		t.Fatal(err)
	}
	job.Status = "failed"
	if err = s.creative.updateJob(job); err != nil {
		t.Fatal(err)
	}
	if _, err = s.resumeCreativeJob(p.SpaceID, p.ID, job.ID, "do-not-repeat"); err == nil {
		t.Fatal("unconfirmed result resumed")
	}
	err = s.executeCreativeJob(context.Background(), &job)
	var failure *ProviderError
	if !errors.As(err, &failure) || failure.Code != "execution_outcome_unknown" || provider.calls != 0 {
		t.Fatal("unknown inference repeated", err, provider.calls)
	}
}

func TestDigitalHumanCheckpointBlobMigrationIncludesPrivateIntermediates(t *testing.T) {
	s := testStore(t)
	p, _, in, _ := avatarFixture(t, s, "avatar-migrate")
	s.creative.avatarPhoto = &LocalTransform{command: fileAvatarExecutable(t), model: in.ExpectedModel, protocol: "files-v1", timeout: 15 * time.Second}
	job, _, err := s.createCreativeJob(p.SpaceID, p.ID, "migration-native", in)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.executeCreativeJob(context.Background(), &job); err != nil {
		t.Fatal(err)
	}
	job.Status = "succeeded"
	if err = s.creative.updateJob(job); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Dir(s.path)
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := NewFileBlobStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = MigrateBlobStorage(context.Background(), dir, store, false); err != nil {
		t.Fatal(err)
	}
	restored, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if err = configureBlobStorage(restored, store); err != nil {
		t.Fatal("checkpoint migration incomplete", err)
	}
	for _, expected := range []*BlobInfo{job.AvatarCheckpoint.Engine, job.AvatarCheckpoint.Composed} {
		actual, e := restored.blobs.info(context.Background(), expected.Key)
		if e != nil || actual != *expected {
			t.Fatal(actual, e)
		}
	}
	restored.creative.avatarLicenseConfirmed = true
	if err = restored.executeCreativeJob(context.Background(), &job); err != nil {
		t.Fatal("migrated result needs engine", err)
	}
}
