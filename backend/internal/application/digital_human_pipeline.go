package application

import (
	"bytes"
	"context"
	"image"
	"math"
	"os"
	"path/filepath"

	"github.com/cloudwego/eino/compose"
)

// PhotoAvatarProvider drives a still image using supplied speech. It must return
// actual model frames, not a slideshow or a loop masquerading as animation.
type PhotoAvatarProvider interface {
	Animate(context.Context, []byte, string, []byte, string) ([]byte, error)
}

func ConfigureDigitalHumanFromEnv(s *FileStore) {
	p, caps := configureLocalTransform("DIGITAL_HUMAN_PHOTO", "local-photo-avatar")
	if p != nil {
		s.creative.avatarPhoto = p
	}
	s.creative.avatarPhotoCaps = caps
	s.creative.avatarLicenseConfirmed = os.Getenv("DIGITAL_HUMAN_LICENSE_ACK") == "true"
}

func (c *creativeStore) avatarCaps(route string) Capabilities {
	caps := c.avatarPhotoCaps
	if route == "video" {
		caps = c.lipSyncCaps
	}
	if caps.Provider == "" {
		caps = Capabilities{Provider: "unavailable", Mode: "unavailable", Message: "未配置照片数字人模型"}
	}
	if !c.avatarLicenseConfirmed {
		caps.Ready = false
		caps.ReasonCode = "avatar_license_unconfirmed"
		caps.Message = "部署方尚未确认数字人引擎使用许可"
	}
	return caps
}

func (t *LocalTransform) Animate(ctx context.Context, reference []byte, mime string, audio []byte, audioMIME string) ([]byte, error) {
	if (mime != "image/png" && mime != "image/jpeg") || len(reference) == 0 || len(reference) > 10<<20 || len(audio) == 0 || len(audio) > 10<<20 || (audioMIME != "audio/wav" && audioMIME != "audio/mpeg") {
		return nil, providerFailure("provider_rejected")
	}
	b, err := t.run(ctx, map[string]any{"operation": "photo_avatar", "model": t.model, "reference": reference, "reference_mime_type": mime, "audio": audio, "audio_mime_type": audioMIME}, 45<<20)
	if err != nil {
		return nil, err
	}
	var out struct {
		Video []byte `json:"video"`
	}
	if strictCreativeJSON(b, &out) != nil || len(out.Video) < 12 || len(out.Video) > 32<<20 || string(out.Video[4:8]) != "ftyp" {
		return nil, providerFailure("invalid_provider_response")
	}
	return out.Video, nil
}

// validateAvatarMedia performs objective format/size checks. Face count,
// occlusion and identity quality remain an explicit human review, not a claim
// inferred from a file header or a general-purpose text model.
func (s *FileStore) validateAvatarMedia(project, space string, asset MediaAsset, audio bool) error {
	path, mime, err := s.media.file(project, space, asset.ID, false, false)
	if err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil || info.Size() > 10<<20 {
		return projectInputError("数字人输入素材最大10MiB")
	}
	if audio || asset.Kind == "video" {
		if asset.Duration <= 0 || asset.Duration > 30 || math.IsNaN(asset.Duration) || math.IsInf(asset.Duration, 0) {
			return projectInputError("数字人首版仅支持30秒以内的音视频")
		}
		if audio && mime != "audio/wav" && mime != "audio/mpeg" {
			return ErrInvalidProjectInput
		}
		if !audio && mime != "video/mp4" {
			return ErrInvalidProjectInput
		}
		return nil
	}
	if mime != "image/png" && mime != "image/jpeg" {
		return ErrInvalidProjectInput
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width < 256 || cfg.Height < 256 {
		return projectInputError("形象照片宽高至少256像素")
	}
	return nil
}

type avatarWork struct {
	reference, audio, video         []byte
	referenceMIME, audioMIME, route string
	duration                        float64
}

// executeDigitalHuman uses Eino for validation -> model driving -> encoding and
// technical QC. Speech is a separate, explicitly quoted creative job, so this
// pipeline never makes an unconfirmed second billable request.
func (s *FileStore) executeDigitalHuman(ctx context.Context, job *CreativeJob) error {
	c := s.creative
	chain := compose.NewChain[*CreativeJob, *avatarWork]().
		AppendLambda(compose.InvokableLambda(func(ctx context.Context, job *CreativeJob) (*avatarWork, error) {
			if err := s.checkAvatarJob(ctx, job); err != nil {
				return nil, err
			}
			c.mu.Lock()
			a, err := c.avatarLocked(job.SpaceID, job.ProjectID, job.AvatarID)
			c.mu.Unlock()
			if err != nil {
				return nil, err
			}
			caps := c.avatarCaps(a.Route)
			if !caps.Ready || caps.Provider != job.Provider || caps.Model != job.Model {
				return nil, providerFailure("provider_unavailable")
			}
			if !job.VoiceConfirmed || job.VoiceConsentReference == "" || job.ReferenceAssetID != a.ReferenceAssetID {
				return nil, providerFailure("avatar_authorization_revoked")
			}
			media := s.media.Get(job.ProjectID, job.SpaceID)
			ref, found := mediaAsset(media, a.ReferenceAssetID)
			if !found {
				return nil, ErrNotFound
			}
			audio, found := mediaAsset(media, job.AudioAssetID)
			if !found {
				return nil, ErrNotFound
			}
			if err = s.validateAvatarMedia(job.ProjectID, job.SpaceID, ref, false); err != nil {
				return nil, err
			}
			if err = s.validateAvatarMedia(job.ProjectID, job.SpaceID, audio, true); err != nil {
				return nil, err
			}
			if a.Route == "video" && audio.Duration > ref.Duration+0.1 {
				return nil, providerFailure("provider_rejected")
			}
			refPath, refMIME, err := s.media.file(job.ProjectID, job.SpaceID, ref.ID, false, false)
			if err != nil {
				return nil, err
			}
			audioPath, audioMIME, err := s.media.file(job.ProjectID, job.SpaceID, audio.ID, false, false)
			if err != nil {
				return nil, err
			}
			work := &avatarWork{referenceMIME: refMIME, audioMIME: audioMIME, route: a.Route, duration: audio.Duration}
			work.reference, err = os.ReadFile(refPath)
			if err != nil {
				return nil, err
			}
			work.audio, err = os.ReadFile(audioPath)
			return work, err
		})).
		AppendLambda(compose.InvokableLambda(func(ctx context.Context, work *avatarWork) (*avatarWork, error) {
			if err := s.checkAvatarJob(ctx, job); err != nil {
				return nil, err
			}
			var err error
			if work.route == "photo" {
				if c.avatarPhoto == nil {
					return nil, providerFailure("provider_unavailable")
				}
				work.video, err = c.avatarPhoto.Animate(ctx, work.reference, work.referenceMIME, work.audio, work.audioMIME)
			} else {
				if c.lipSync == nil {
					return nil, providerFailure("provider_unavailable")
				}
				work.video, err = c.lipSync.Sync(ctx, work.reference, work.audio, work.audioMIME)
			}
			return work, err
		})).
		AppendLambda(compose.InvokableLambda(func(ctx context.Context, work *avatarWork) (*avatarWork, error) {
			if err := s.checkAvatarJob(ctx, job); err != nil {
				return nil, err
			}
			video, err := composeAvatarVideo(ctx, work)
			work.video = video
			return work, err
		}))
	run, err := chain.Compile(ctx)
	if err != nil {
		return err
	}
	work, err := run.Invoke(ctx, job)
	if err != nil {
		return err
	}
	// Serialize publication with revocation (also a PostgreSQL row lock). Never
	// keep this lock while waiting for model inference or transcoding.
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, err = c.avatarLocked(job.SpaceID, job.ProjectID, job.AvatarID); err != nil {
		return providerFailure("avatar_authorization_revoked")
	}
	if saved, ok := c.data.Jobs[job.ID]; ok && saved.Status == "cancelled" {
		return context.Canceled
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	_, err = s.media.AddAsset(job.ProjectID, job.SpaceID, "creative-"+job.ID, "AI生成 · 数字人口播", "shot", job.Character, job.ShotID, job.Provider, work.video, "video/mp4", job.lease)
	if err != nil {
		return err
	}
	asset, err := s.media.AssetForReceipt(job.ProjectID, job.SpaceID, "creative-"+job.ID)
	if err == nil {
		job.AssetID = asset.ID
	}
	return err
}

// composeAvatarVideo replaces any model soundtrack with the authorized audio,
// burns in an AI disclosure, and rejects missing/short/invalid model output.
func composeAvatarVideo(ctx context.Context, work *avatarWork) ([]byte, error) {
	if len(work.video) < 12 || len(work.video) > 32<<20 {
		return nil, providerFailure("invalid_provider_response")
	}
	dir, err := os.MkdirTemp("", "studio-avatar-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	video, audio, out := filepath.Join(dir, "video.mp4"), filepath.Join(dir, "audio"), filepath.Join(dir, "output.mp4")
	if err = os.WriteFile(video, work.video, 0600); err != nil {
		return nil, err
	}
	if _, duration, e := validateMedia(video, "video/mp4"); e != nil || duration+0.1 < work.duration {
		return nil, providerFailure("invalid_provider_response")
	}
	if err = os.WriteFile(audio, work.audio, 0600); err != nil {
		return nil, err
	}
	err = ffmpeg(ctx, "-protocol_whitelist", "file,pipe", "-i", video, "-protocol_whitelist", "file,pipe", "-i", audio, "-map", "0:v:0", "-map", "1:a:0", "-vf", "scale=w='min(1280,iw)':h='min(720,ih)':force_original_aspect_ratio=decrease:force_divisible_by=2,drawtext=font='sans':text='AI Generated':fontsize=20:fontcolor=white:box=1:boxcolor=black@0.6:x=12:y=12", "-t", seconds(work.duration), "-r", "24", "-c:v", "libx264", "-preset", "veryfast", "-crf", "23", "-pix_fmt", "yuv420p", "-c:a", "aac", "-movflags", "+faststart", "-fs", "33554432", out)
	if err != nil {
		return nil, providerFailure("avatar_composition_failed")
	}
	if _, duration, e := validateMedia(out, "video/mp4"); e != nil || math.Abs(duration-work.duration) > 0.25 {
		return nil, providerFailure("invalid_provider_response")
	}
	probe, err := probeMedia(out)
	if err != nil {
		return nil, err
	}
	audioFound := false
	for _, stream := range probe.Streams {
		audioFound = audioFound || stream.CodecType == "audio"
	}
	if !audioFound {
		return nil, providerFailure("invalid_provider_response")
	}
	// Read only after checking the filesystem bound, including encoder overhead.
	info, err := os.Stat(out)
	if err != nil || info.Size() > 32<<20 {
		return nil, providerFailure("invalid_provider_response")
	}
	return os.ReadFile(out)
}
