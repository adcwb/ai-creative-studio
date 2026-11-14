package application

import (
	"context"
	"image"
	"math"
	"os"
	"path/filepath"

	"github.com/cloudwego/eino/compose"

	"github.com/adcwb/ai-creative-studio/backend/internal/platform/nativefs"
)

// PhotoAvatarProvider drives a still image using supplied speech. It must return
// actual model frames, not a slideshow or a loop masquerading as animation.
type PhotoAvatarProvider interface {
	Animate(context.Context, []byte, string, []byte, string) ([]byte, error)
}

func ConfigureDigitalHumanFromEnv(s *FileStore) {
	p, caps := configureLocalTransform("DIGITAL_HUMAN_PHOTO", "local-photo-avatar")
	if selected := os.Getenv("DIGITAL_HUMAN_PHOTO_PROVIDER"); selected != "" && selected != "local" {
		s.creative.avatarPhoto = nil
		caps = Capabilities{Provider: "unavailable", Mode: "unavailable", ReasonCode: "provider_unavailable", Message: "专用云数字人配置无效，请核对工作空间、私有素材桥接和允许空间"}
		if selected == wanAvatarProvider {
			if cloud, err := configureAvatarCloud(); err == nil {
				s.creative.avatarPhoto = cloud
				caps = Capabilities{Provider: wanAvatarProvider, Model: "wan2.2-s2v", Mode: "real", Ready: true, Message: "百炼照片口播专用协议；原生480P/720P，单段音频小于20秒；需明确授权外传素材，效果与商业许可未验收"}
			}
		}
		s.creative.avatarPhotoCaps = caps
		s.creative.avatarLicenseConfirmed = os.Getenv("DIGITAL_HUMAN_LICENSE_ACK") == "true"
		return
	}
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
	if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
		return ErrInvalidProjectInput
	}
	if audio || asset.Kind == "video" {
		if asset.Duration <= 0 || math.IsNaN(asset.Duration) || math.IsInf(asset.Duration, 0) {
			return projectInputError("数字人音视频必须有可解析的有效时长")
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
	file, err := openLocalBlob(path)
	if err != nil {
		return err
	}
	defer file.Close()
	cfg, _, err := image.DecodeConfig(file)
	if err != nil || cfg.Width < 256 || cfg.Height < 256 {
		return projectInputError("形象照片宽高至少256像素")
	}
	return nil
}

type avatarWork struct {
	// Byte fields exist only for the legacy compatibility test helper.
	reference, audio, video                         []byte
	referenceMIME, audioMIME, route                 string
	referencePath, audioPath, videoPath, outputPath string
	duration                                        float64
}

// executeDigitalHuman keeps inference and encoding on private files. Speech is
// a separately requested job; this pipeline never silently calls TTS or a
// generic video model in place of a dedicated avatar engine.
func (s *FileStore) executeDigitalHuman(ctx context.Context, job *CreativeJob) error {
	c := s.creative
	dir, err := nativefs.MkdirTemp("", "studio-avatar-job-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
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
			if (!avatarCheckpointReady(*job) && (!caps.Ready || caps.Provider != job.Provider || caps.Model != job.Model)) || !c.avatarLicenseConfirmed {
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
			return &avatarWork{referencePath: refPath, audioPath: audioPath, referenceMIME: refMIME, audioMIME: audioMIME, route: a.Route, duration: audio.Duration, videoPath: filepath.Join(dir, "model.mp4"), outputPath: filepath.Join(dir, "composed.mp4")}, nil
		})).
		AppendLambda(compose.InvokableLambda(func(ctx context.Context, work *avatarWork) (*avatarWork, error) {
			if err := s.checkAvatarJob(ctx, job); err != nil {
				return nil, err
			}
			var provider any = c.avatarPhoto
			operation := "photo_avatar"
			if work.route == "video" {
				provider = c.lipSync
				operation = "lip_sync"
			}
			if cloud, ok := provider.(*avatarCloud); ok && !avatarCheckpointReady(*job) {
				if e := s.executeCloudAvatar(s.avatarDispatchContext(ctx, job), job, work, cloud); e != nil {
					return nil, e
				}
				return work, nil
			}
			if restored, e := s.restoreAvatarCheckpoint(ctx, job, work); e != nil {
				return nil, e
			} else if restored {
				return work, nil
			}
			if provider == nil {
				return nil, providerFailure("provider_unavailable")
			}
			dispatchCtx := s.avatarDispatchContext(ctx, job)
			local, fileMode := provider.(*LocalTransform)
			if fileMode && local.protocol == "files-v1" {
				{
					sha, e := avatarSourceSHA(ctx, *job, work)
					if e != nil {
						return nil, e
					}
					if e = s.creative.recordAvatarCheckpoint(ctx, job, avatarCheckpoint{Format: 1, Phase: "dispatching", SourceSHA256: sha}); e != nil {
						return nil, e
					}
				}
				if err := local.AvatarFiles(dispatchCtx, operation, work.referencePath, work.referenceMIME, work.audioPath, work.audioMIME, work.videoPath); err != nil {
					return nil, err
				}
				if job.AvatarCheckpoint != nil {
					if e := s.saveAvatarMediaCheckpoint(ctx, job, "engine_done", work.videoPath); e != nil {
						return nil, e
					}
				}
				return work, nil
			}
			// Compatibility with existing JSON executors is explicit and bounded. These
			// are adapter transport limits, not global asset/project upload quotas.
			for _, path := range []string{work.referencePath, work.audioPath} {
				info, err := os.Stat(path)
				if err != nil {
					return nil, err
				}
				if info.Size() > 10<<20 {
					return nil, providerFailure("avatar_legacy_protocol_limit")
				}
			}
			ref, err := os.ReadFile(work.referencePath)
			if err != nil {
				return nil, err
			}
			audio, err := os.ReadFile(work.audioPath)
			if err != nil {
				return nil, err
			}
			var video []byte
			if operation == "photo_avatar" {
				video, err = c.avatarPhoto.Animate(dispatchCtx, ref, work.referenceMIME, audio, work.audioMIME)
			} else {
				video, err = c.lipSync.Sync(dispatchCtx, ref, audio, work.audioMIME)
			}
			if err != nil {
				return nil, err
			}
			if len(video) < 12 || len(video) > 32<<20 {
				return nil, providerFailure("invalid_provider_response")
			}
			if err = os.WriteFile(work.videoPath, video, 0600); err != nil {
				return nil, err
			}
			return work, nil
		})).
		AppendLambda(compose.InvokableLambda(func(ctx context.Context, work *avatarWork) (*avatarWork, error) {
			if err := s.checkAvatarJob(ctx, job); err != nil {
				return nil, err
			}
			if restored, e := s.restoreAvatarComposed(ctx, job, work); e != nil {
				return nil, e
			} else if restored {
				return work, nil
			}
			if e := composeAvatarFiles(ctx, work); e != nil {
				return nil, e
			}
			if job.AvatarCheckpoint != nil {
				if e := s.saveAvatarMediaCheckpoint(ctx, job, "composed", work.outputPath); e != nil {
					return nil, e
				}
			}
			return work, nil
		}))
	run, err := chain.Compile(ctx)
	if err != nil {
		return err
	}
	work, err := run.Invoke(ctx, job)
	if err != nil {
		return err
	}
	if err = checkLibraryDispatch(ctx); err != nil {
		return err
	}
	if err = s.checkVoiceAuthorization(ctx, job); err != nil {
		return err
	}
	// Serialize publication against fresh revocation, including the PostgreSQL
	// creative row lock. Never hold it while waiting for inference/transcoding.
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, err = c.avatarLocked(job.SpaceID, job.ProjectID, job.AvatarID); err != nil {
		return providerFailure("avatar_authorization_revoked")
	}
	if job.Kind == "digital_human_authorized" {
		if _, e := s.voiceAuthorizationLocked(job.SpaceID, job.ProjectID, job.VoiceAuthorizationID, job.AudioAssetID); e != nil {
			return providerFailure("avatar_authorization_revoked")
		}
	}
	if saved, ok := c.data.Jobs[job.ID]; ok && saved.Status == "cancelled" {
		return context.Canceled
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	_, err = s.media.addAssetFile(ctx, job.ProjectID, job.SpaceID, "creative-"+job.ID, "AI生成 · 数字人口播", "shot", job.Character, job.ShotID, job.Provider, work.outputPath, "video/mp4", func() bool {
		return s.runtime == nil || s.runtime.auth == nil || (job.VoiceConsentBy != "" && s.runtime.auth.can(job.VoiceConsentBy, job.SpaceID, "workspace.write"))
	}, job.lease)
	if err != nil {
		return err
	}
	asset, err := s.media.AssetForReceipt(job.ProjectID, job.SpaceID, "creative-"+job.ID)
	if err == nil {
		job.AssetID = asset.ID
	}
	return err
}

// composeAvatarFiles replaces model sound with the authorized audio and burns
// AI disclosure at native dimensions/frame cadence. No 720p/24fps/32MiB product
// cap is applied. This neither upscales to 4K nor claims improved source detail.
func composeAvatarFiles(ctx context.Context, work *avatarWork) error {
	if err := validateProviderMediaFile(ctx, work.videoPath, "video/mp4"); err != nil {
		return err
	}
	if _, duration, e := validateMedia(work.videoPath, "video/mp4"); e != nil || duration+0.1 < work.duration {
		return providerFailure("invalid_provider_response")
	}
	err := ffmpeg(ctx, "-protocol_whitelist", "file,pipe", "-i", work.videoPath, "-protocol_whitelist", "file,pipe", "-i", work.audioPath, "-map", "0:v:0", "-map", "1:a:0", "-vf", "scale=w=trunc(iw/2)*2:h=trunc(ih/2)*2,drawtext=font='sans':text='AI Generated':fontsize=20:fontcolor=white:box=1:boxcolor=black@0.6:x=12:y=12", "-t", seconds(work.duration), "-c:v", "libx264", "-preset", "veryfast", "-crf", "23", "-pix_fmt", "yuv420p", "-c:a", "aac", "-movflags", "+faststart", work.outputPath)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return providerFailure("avatar_composition_failed")
	}
	if err = validateProviderMediaFile(ctx, work.outputPath, "video/mp4"); err != nil {
		return err
	}
	if _, duration, e := validateMedia(work.outputPath, "video/mp4"); e != nil || math.Abs(duration-work.duration) > 0.25 {
		return providerFailure("invalid_provider_response")
	}
	probe, err := probeMedia(work.outputPath)
	if err != nil {
		return err
	}
	audioFound := false
	for _, stream := range probe.Streams {
		audioFound = audioFound || stream.CodecType == "audio"
	}
	if !audioFound {
		return providerFailure("invalid_provider_response")
	}
	return nil
}

// Legacy test/helper bridge; production uses composeAvatarFiles throughout.
func composeAvatarVideo(ctx context.Context, work *avatarWork) ([]byte, error) {
	if len(work.video) < 12 || string(work.video[4:8]) != "ftyp" {
		return nil, providerFailure("invalid_provider_response")
	}
	dir, err := nativefs.MkdirTemp("", "studio-avatar-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	work.videoPath = filepath.Join(dir, "model.mp4")
	work.audioPath = filepath.Join(dir, "audio")
	work.outputPath = filepath.Join(dir, "output.mp4")
	if err = os.WriteFile(work.videoPath, work.video, 0600); err != nil {
		return nil, err
	}
	if err = os.WriteFile(work.audioPath, work.audio, 0600); err != nil {
		return nil, err
	}
	if err = composeAvatarFiles(ctx, work); err != nil {
		return nil, err
	}
	return os.ReadFile(work.outputPath)
}
