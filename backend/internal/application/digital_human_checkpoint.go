package application

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/adcwb/ai-creative-studio/backend/internal/platform/jobqueue"
	"github.com/adcwb/ai-creative-studio/backend/internal/platform/nativefs"
	"io"
	"os"
	"path/filepath"
	"time"
)

type avatarCheckpoint struct {
	RemoteID         string    `json:"remote_id,omitempty"`
	CloudDestination string    `json:"cloud_destination,omitempty"`
	CloudImageKey    string    `json:"cloud_image_key,omitempty"`
	CloudAudioKey    string    `json:"cloud_audio_key,omitempty"`
	Format           int       `json:"format"`
	Phase            string    `json:"phase"`
	SourceSHA256     string    `json:"source_sha256"`
	Engine           *BlobInfo `json:"engine,omitempty"`
	Composed         *BlobInfo `json:"composed,omitempty"`
}

func avatarCheckpointKey(job CreativeJob, stage string) string {
	key, _ := projectFingerprint("avatar-checkpoint-"+stage, job.ID, []string{job.SpaceID, job.ProjectID})
	return blobKey(job.SpaceID, job.ProjectID, key[:32]+".mp4")
}
func validAvatarCheckpoint(job CreativeJob) bool {
	cp := job.AvatarCheckpoint
	if cp == nil {
		return true
	}
	if !isAvatarKind(job.Kind) || cp.Format != 1 || len(cp.SourceSHA256) != 64 {
		return false
	}
	if cp.CloudDestination != "" || cp.RemoteID != "" || cp.CloudImageKey != "" || cp.CloudAudioKey != "" {
		opaque, _ := projectFingerprint("cloud-avatar-inputs", job.ID, []string{job.SpaceID, job.ProjectID})
		if job.Provider != wanAvatarProvider || !job.CloudTransferConfirmed || len(cp.CloudDestination) != 64 || cp.CloudImageKey != "avatar-inputs/"+opaque+"/image" || cp.CloudAudioKey != "avatar-inputs/"+opaque+"/audio" {
			return false
		}
		if cp.Phase != "dispatching" && !wanTaskID.MatchString(cp.RemoteID) {
			return false
		}
	} else if cp.Phase == "remote_pending" {
		return false
	}
	if cp.Phase == "remote_pending" {
		return cp.Engine == nil && cp.Composed == nil
	}
	if cp.Phase == "dispatching" {
		return cp.Engine == nil && cp.Composed == nil && cp.RemoteID == ""
	}
	if cp.Phase != "engine_done" && cp.Phase != "composed" || cp.Engine == nil || !validBlobInfo(*cp.Engine) || cp.Engine.Key != avatarCheckpointKey(job, "engine") {
		return false
	}
	if cp.Phase == "engine_done" {
		return cp.Composed == nil
	}
	return cp.Composed != nil && validBlobInfo(*cp.Composed) && cp.Composed.Key == avatarCheckpointKey(job, "composed")
}
func avatarRecoveryAvailable(job CreativeJob) bool {
	return avatarCheckpointReady(job) || (job.AvatarCheckpoint != nil && validAvatarCheckpoint(job) && job.AvatarCheckpoint.Phase == "remote_pending")
}
func avatarCheckpointReady(job CreativeJob) bool {
	return job.AvatarCheckpoint != nil && validAvatarCheckpoint(job) && (job.AvatarCheckpoint.Phase == "engine_done" || job.AvatarCheckpoint.Phase == "composed")
}
func avatarSourceSHA(ctx context.Context, job CreativeJob, work *avatarWork) (string, error) {
	ref, err := localBlobInfo(ctx, avatarCheckpointKey(job, "reference"), work.referencePath)
	if err != nil {
		return "", err
	}
	audio, err := localBlobInfo(ctx, avatarCheckpointKey(job, "audio"), work.audioPath)
	if err != nil {
		return "", err
	}
	return projectFingerprint("avatar-fixed-inputs", job.ID, []any{job.SpaceID, job.ProjectID, job.Provider, job.Model, job.AvatarID, job.ReferenceAssetID, job.AudioAssetID, job.VoiceAuthorizationID, job.VoiceConsentBy, job.VoiceConsentReference, ref.SHA256, audio.SHA256, work.route})
}
func (c *creativeStore) recordAvatarCheckpoint(ctx context.Context, job *CreativeJob, checkpoint avatarCheckpoint) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	old, ok := c.data.Jobs[job.ID]
	if !ok || old.SpaceID != job.SpaceID || old.ProjectID != job.ProjectID {
		return ErrNotFound
	}
	if old.Status == "cancelled" {
		return context.Canceled
	}
	candidate := cloneCreativeJob(old)
	candidate.AvatarCheckpoint = &checkpoint
	if !validAvatarCheckpoint(candidate) {
		return ErrInvalidProjectInput
	}
	prior := old.AvatarCheckpoint
	if prior == nil {
		if checkpoint.Phase != "dispatching" {
			return ErrConflict
		}
	} else {
		if prior.SourceSHA256 != checkpoint.SourceSHA256 || prior.CloudDestination != checkpoint.CloudDestination || prior.CloudImageKey != checkpoint.CloudImageKey || prior.CloudAudioKey != checkpoint.CloudAudioKey || prior.RemoteID != "" && prior.RemoteID != checkpoint.RemoteID {
			return ErrConflict
		}
		if prior.Phase == checkpoint.Phase {
			a, _ := projectFingerprint("cp", job.ID, prior)
			b, _ := projectFingerprint("cp", job.ID, checkpoint)
			if a != b {
				return ErrConflict
			}
			job.AvatarCheckpoint = prior
			return nil
		}
		if !((prior.Phase == "dispatching" && (checkpoint.Phase == "engine_done" || checkpoint.Phase == "remote_pending") || prior.Phase == "remote_pending" && checkpoint.Phase == "engine_done") || prior.Phase == "engine_done" && checkpoint.Phase == "composed") {
			return ErrConflict
		}
	}
	candidate.UpdatedAt = time.Now().UTC()
	c.data.Jobs[job.ID] = candidate
	if c.pg != nil && job.lease != nil {
		before := c.pg.beforeCommit
		c.pg.beforeCommit = func(ctx context.Context, tx *sql.Tx) error { return jobqueue.CheckLeaseTx(ctx, tx, *job.lease) }
		defer func() { c.pg.beforeCommit = before }()
	}
	if err := c.saveLocked(); err != nil {
		c.data.Jobs[job.ID] = old
		return err
	}
	job.AvatarCheckpoint = &checkpoint
	return nil
}
func (s *FileStore) restoreAvatarCheckpoint(ctx context.Context, job *CreativeJob, work *avatarWork) (bool, error) {
	if job.AvatarCheckpoint == nil {
		return false, nil
	}
	if !validAvatarCheckpoint(*job) {
		return false, errors.Join(providerFailure("avatar_checkpoint_invalid"), ErrBlobIntegrity)
	}
	sha, err := avatarSourceSHA(ctx, *job, work)
	if err != nil {
		return false, err
	}
	if sha != job.AvatarCheckpoint.SourceSHA256 {
		return false, providerFailure("avatar_source_changed")
	}
	if !avatarCheckpointReady(*job) {
		return false, providerFailure("execution_outcome_unknown")
	}
	err = s.materializeAvatarMedia(ctx, *job.AvatarCheckpoint.Engine, work.videoPath)
	return err == nil, err
}
func (s *FileStore) saveAvatarMediaCheckpoint(ctx context.Context, job *CreativeJob, phase, path string) error {
	if job.AvatarCheckpoint == nil {
		return ErrBlobIntegrity
	}
	stage := "engine"
	if phase == "composed" {
		stage = "composed"
	}
	key := avatarCheckpointKey(*job, stage)
	info, err := localBlobInfo(ctx, key, path)
	if err != nil {
		return err
	}
	if s.blobs != nil {
		if err = s.blobs.putFile(ctx, key, path); err != nil {
			return err
		}
	} else {
		if s.pg != nil {
			return errors.Join(providerFailure("avatar_checkpoint_invalid"), ErrBlobIntegrity)
		}
		if err = publishAvatarCheckpointFile(ctx, path, filepath.Join(s.media.dir, filepath.Base(key)), info); err != nil {
			return err
		}
	}
	next := *job.AvatarCheckpoint
	next.Phase = phase
	if phase == "engine_done" {
		next.Engine = &info
	} else {
		next.Composed = &info
	}
	return s.creative.recordAvatarCheckpoint(ctx, job, next)
}

func (s *FileStore) restoreAvatarComposed(ctx context.Context, job *CreativeJob, work *avatarWork) (bool, error) {
	if job.AvatarCheckpoint == nil || job.AvatarCheckpoint.Phase != "composed" {
		return false, nil
	}
	if job.AvatarCheckpoint.Composed == nil {
		return false, errors.Join(providerFailure("avatar_checkpoint_invalid"), ErrBlobIntegrity)
	}
	err := s.materializeAvatarMedia(ctx, *job.AvatarCheckpoint.Composed, work.outputPath)
	if err == nil {
		err = validateProviderMediaFile(ctx, work.outputPath, "video/mp4")
	}
	return err == nil, err
}
func publicCreativeJob(job CreativeJob) CreativeJob {
	job = cloneCreativeJob(job)
	job.RuntimeSelection = nil
	if job.AvatarCheckpoint != nil {
		job.AvatarRecoveryPhase = job.AvatarCheckpoint.Phase
		job.AvatarCheckpoint = nil
	}
	return job
}

func (s *FileStore) materializeAvatarMedia(ctx context.Context, info BlobInfo, target string) error {
	if s.blobs != nil {
		actual, err := s.blobs.info(ctx, info.Key)
		if err != nil || actual != info {
			return errors.Join(providerFailure("avatar_checkpoint_invalid"), ErrBlobIntegrity)
		}
		_, err = s.blobs.materialize(ctx, info.Key, target)
		return err
	}
	if s.pg != nil {
		return ErrBlobIntegrity
	}
	source := filepath.Join(s.media.dir, filepath.Base(info.Key))
	actual, err := localBlobInfo(ctx, info.Key, source)
	if err != nil || actual != info {
		return ErrBlobIntegrity
	}
	return copyAvatarFile(ctx, source, target)
}
func publishAvatarCheckpointFile(ctx context.Context, source, target string, expected BlobInfo) error {
	if actual, err := localBlobInfo(ctx, expected.Key, target); err == nil {
		if actual == expected {
			return nil
		}
		return ErrBlobIntegrity
	} else if !os.IsNotExist(err) {
		return err
	}
	in, err := openLocalBlob(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.CreateTemp(filepath.Dir(target), ".avatar-checkpoint-")
	if err != nil {
		return err
	}
	defer os.Remove(out.Name())
	_, err = io.CopyBuffer(out, contextBlobReader{ctx, in}, make([]byte, 64<<10))
	if err == nil {
		err = nativefs.SyncFile(out)
	}
	err = errors.Join(err, out.Close())
	if err != nil {
		return err
	}
	if err = os.Link(out.Name(), target); err != nil {
		if actual, e := localBlobInfo(ctx, expected.Key, target); e == nil && actual == expected {
			return nil
		}
		return err
	}
	return nativefs.SyncDirectory(filepath.Dir(target))
}
func avatarCheckpointReferences(d creativeDisk) ([]BlobInfo, error) {
	refs := []BlobInfo{}
	for _, job := range d.Jobs {
		if !validAvatarCheckpoint(job) {
			return nil, ErrBlobIntegrity
		}
		if job.AvatarCheckpoint != nil {
			if job.AvatarCheckpoint.Engine != nil {
				refs = append(refs, *job.AvatarCheckpoint.Engine)
			}
			if job.AvatarCheckpoint.Composed != nil {
				refs = append(refs, *job.AvatarCheckpoint.Composed)
			}
		}
	}
	return refs, nil
}
func fileAvatarCheckpointReferences(dir string) ([]BlobInfo, error) {
	raw, err := safeReadLocal(filepath.Join(dir, "creative.json"), 64<<20)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var disk creativeDisk
	if json.Unmarshal(raw, &disk) != nil || disk.Version != 1 || disk.Jobs == nil {
		return nil, ErrBlobIntegrity
	}
	return avatarCheckpointReferences(disk)
}
