package application

// Dedicated wan2.2-s2v contract, checked 2026-11-14:
// https://www.alibabacloud.com/help/en/model-studio/wan-s2v-api
// This is not the generic video capability and never supplies generated audio.
import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
)

const wanAvatarProvider = "dashscope-s2v"

var wanTaskID = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,127}$`)
var wanWorkspaceID = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9-]{0,62}$`)
var avatarObjectKey = regexp.MustCompile(`^avatar-inputs/[a-f0-9]{64}/(image|audio)$`)

type avatarCloud struct {
	api          *OpenAICreativeProvider
	inputs       *S3ReferenceBridge
	tenants      map[string]bool
	resolution   string
	fingerprint  string
	pollInterval time.Duration
	download     func(context.Context, string, io.Writer) error
}

func (*avatarCloud) Animate(context.Context, []byte, string, []byte, string) ([]byte, error) {
	return nil, providerFailure("unsupported_capability")
}
func configureAvatarCloud() (*avatarCloud, error) {
	workspace := os.Getenv("DIGITAL_HUMAN_CLOUD_WORKSPACE")
	resolution := os.Getenv("DIGITAL_HUMAN_CLOUD_RESOLUTION")
	if resolution == "" {
		resolution = "480P"
	}
	if !wanWorkspaceID.MatchString(workspace) || !validCredential(os.Getenv("DIGITAL_HUMAN_CLOUD_API_KEY")) || (resolution != "480P" && resolution != "720P") {
		return nil, errors.New("invalid cloud avatar configuration")
	}
	bridge, err := ConfigureReferenceBridgeFromEnv()
	if err != nil {
		return nil, err
	}
	s3, ok := bridge.(*S3ReferenceBridge)
	if !ok {
		return nil, errors.New("cloud avatar requires explicitly enabled private reference bridge")
	}
	tenants := map[string]bool{}
	for _, id := range strings.Split(os.Getenv("DIGITAL_HUMAN_CLOUD_TENANTS"), ",") {
		id = strings.TrimSpace(id)
		if id != "" && len(id) <= 128 && !strings.ContainsAny(id, "\x00/\\") && id != "*" {
			tenants[id] = true
		} else {
			return nil, errors.New("cloud avatar requires exact authorized space IDs")
		}
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	p := newCreativeProvider(os.Getenv("DIGITAL_HUMAN_CLOUD_API_KEY"), "https://"+workspace+".cn-beijing.maas.aliyuncs.com/api/v1", &http.Client{Transport: transport, Timeout: 120 * time.Second})
	sha, _ := projectFingerprint("wan-s2v-destination", "v1", []string{p.endpoint, s3.config.Endpoint, s3.config.Bucket, s3.config.Region, resolution})
	return &avatarCloud{api: p, inputs: s3, tenants: tenants, resolution: resolution, fingerprint: sha, pollInterval: 15 * time.Second, download: safeVideoDownloadTo}, nil
}
func (p *avatarCloud) allowed(job CreativeJob) error {
	if job.Kind != "digital_human_authorized" || job.VoiceAuthorizationID == "" || !job.CloudTransferConfirmed || !p.tenants[job.SpaceID] || p.inputs == nil || job.Provider != wanAvatarProvider || job.Model != "wan2.2-s2v" {
		return providerFailure("avatar_cloud_transfer_denied")
	}
	if cp := job.AvatarCheckpoint; cp != nil && (cp.CloudDestination != p.fingerprint) {
		return providerFailure("avatar_cloud_configuration_changed")
	}
	return nil
}
func (p *avatarCloud) validate(work *avatarWork) error {
	if work.route != "photo" {
		return providerFailure("unsupported_capability")
	}
	f, err := openLocalBlob(work.referencePath)
	if err != nil {
		return err
	}
	cfg, _, err := image.DecodeConfig(f)
	f.Close()
	if err != nil || cfg.Width < 400 || cfg.Height < 400 || cfg.Width > 7000 || cfg.Height > 7000 {
		return providerFailure("avatar_cloud_input_window")
	}
	f, err = openLocalBlob(work.audioPath)
	if err != nil {
		return err
	}
	info, err := f.Stat()
	f.Close()
	if err != nil || info.Size() >= 15*1000*1000 || work.duration <= 0 || work.duration >= 20 {
		return providerFailure("avatar_cloud_input_window")
	}
	return nil
}

// No retry/redirect, no raw vendor prose in errors. Async header is required only
// on submission; neither credential nor URLs enter logs/public job responses.
func (p *avatarCloud) metadata(ctx context.Context, method, path string, body any) (wanAvatarResponse, error) {
	var out wanAvatarResponse
	var raw []byte
	var err error
	if body != nil {
		raw, err = json.Marshal(body)
		if err != nil {
			return out, err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, p.api.endpoint+path, bytes.NewReader(raw))
	if err != nil {
		return out, providerFailure("provider_error")
	}
	req.GetBody = nil
	req.Header.Set("Authorization", "Bearer "+p.api.key)
	req.Header.Set("Content-Type", "application/json")
	if method == "POST" {
		req.Header.Set("X-DashScope-Async", "enable")
	}
	resp, err := libraryGuardedRequest(p.api.client, req)
	if err != nil {
		if method == "POST" {
			return out, providerFailure("execution_outcome_unknown")
		}
		return out, providerFailure("provider_error")
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, (64<<10)+1))
	if err != nil || len(data) > 64<<10 {
		if method == "POST" {
			return out, providerFailure("execution_outcome_unknown")
		}
		return out, providerFailure("invalid_provider_response")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		code := "provider_error"
		switch resp.StatusCode {
		case 401, 403:
			code = "provider_auth_failed"
		case 429:
			code = "provider_rate_limited"
		case 400, 404, 413, 422:
			code = "provider_rejected"
		}
		return out, providerFailure(code)
	}
	if json.Unmarshal(data, &out) != nil || !wanTaskID.MatchString(out.Output.ID) {
		if method == "POST" {
			return out, providerFailure("execution_outcome_unknown")
		}
		return out, providerFailure("invalid_provider_response")
	}
	return out, nil
}

type wanAvatarResponse struct {
	Output struct {
		ID      string `json:"task_id"`
		Status  string `json:"task_status"`
		Results struct {
			URL string `json:"video_url"`
		} `json:"results"`
	} `json:"output"`
}

func (p *avatarCloud) poll(ctx context.Context, id string) (wanAvatarResponse, error) {
	if !wanTaskID.MatchString(id) {
		return wanAvatarResponse{}, providerFailure("invalid_provider_response")
	}
	r, err := p.metadata(ctx, "GET", "/tasks/"+id, nil)
	if err == nil && r.Output.ID != id {
		err = providerFailure("invalid_provider_response")
	}
	return r, err
}

func (s *FileStore) executeCloudAvatar(ctx context.Context, job *CreativeJob, work *avatarWork, p *avatarCloud) error {
	if err := p.allowed(*job); err != nil {
		return err
	}
	if err := p.validate(work); err != nil {
		return err
	}
	if job.AvatarCheckpoint == nil {
		sha, err := avatarSourceSHA(ctx, *job, work)
		if err != nil {
			return err
		}
		opaque, _ := projectFingerprint("cloud-avatar-inputs", job.ID, []string{job.SpaceID, job.ProjectID})
		cp := avatarCheckpoint{Format: 1, Phase: "dispatching", SourceSHA256: sha, CloudDestination: p.fingerprint, CloudImageKey: "avatar-inputs/" + opaque + "/image", CloudAudioKey: "avatar-inputs/" + opaque + "/audio"}
		if err = s.creative.recordAvatarCheckpoint(ctx, job, cp); err != nil {
			return err
		}
		imageURL, err := p.inputs.avatarFileURL(ctx, cp.CloudImageKey, work.referencePath, work.referenceMIME)
		if err != nil {
			return err
		}
		audioURL, err := p.inputs.avatarFileURL(ctx, cp.CloudAudioKey, work.audioPath, work.audioMIME)
		if err != nil {
			return err
		}
		if err = s.checkAvatarJob(ctx, job); err != nil {
			return err
		}
		response, err := p.metadata(ctx, "POST", "/services/aigc/image2video/video-synthesis", map[string]any{"model": "wan2.2-s2v", "input": map[string]string{"image_url": imageURL, "audio_url": audioURL}, "parameters": map[string]string{"resolution": p.resolution}})
		if err != nil {
			return err
		}
		if response.Output.Status != "PENDING" && response.Output.Status != "RUNNING" && response.Output.Status != "SUCCEEDED" {
			return providerFailure("execution_outcome_unknown")
		}
		cp.Phase = "remote_pending"
		cp.RemoteID = response.Output.ID
		if err = s.creative.recordAvatarCheckpoint(ctx, job, cp); err != nil {
			return err
		}
	}
	cp := job.AvatarCheckpoint
	if cp.Phase != "remote_pending" {
		return providerFailure("execution_outcome_unknown")
	}
	sha, err := avatarSourceSHA(ctx, *job, work)
	if err != nil {
		return err
	}
	if sha != cp.SourceSHA256 {
		return providerFailure("avatar_source_changed")
	}
	for {
		if err = s.checkAvatarJob(ctx, job); err != nil {
			return err
		}
		if err = p.allowed(*job); err != nil {
			return err
		}
		result, err := p.poll(ctx, cp.RemoteID)
		if err != nil {
			return err
		}
		switch result.Output.Status {
		case "SUCCEEDED":
			if err = s.checkAvatarJob(ctx, job); err != nil {
				return err
			}
			f, err := os.OpenFile(work.videoPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			if err != nil {
				return err
			}
			err = p.download(ctx, result.Output.Results.URL, f)
			closeErr := f.Close()
			if err != nil {
				return err
			}
			if closeErr != nil {
				return closeErr
			}
			if err = validateProviderMediaFile(ctx, work.videoPath, "video/mp4"); err != nil {
				return err
			}
			if err = s.saveAvatarMediaCheckpoint(ctx, job, "engine_done", work.videoPath); err != nil {
				return err
			}
			// Best effort: signed access expires independently. Bucket lifecycle must
			// remove copies when a worker crashes/cancels or deletion is unavailable.
			p.inputs.deleteAvatarInput(ctx, cp.CloudImageKey)
			p.inputs.deleteAvatarInput(ctx, cp.CloudAudioKey)
			return nil
		case "FAILED", "CANCELED":
			p.inputs.deleteAvatarInput(ctx, cp.CloudImageKey)
			p.inputs.deleteAvatarInput(ctx, cp.CloudAudioKey)
			return providerFailure("avatar_cloud_task_failed")
		case "UNKNOWN":
			return providerFailure("avatar_cloud_task_unknown")
		case "PENDING", "RUNNING":
		default:
			return providerFailure("invalid_provider_response")
		}
		timer := time.NewTimer(p.pollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

// The storage bridge does not expose these signed URLs through the application's
// media API. Explicit operator destination + exact space allowlist + creator
// confirmation are all required before any transfer.
func (b *S3ReferenceBridge) avatarTarget(key string) (*url.URL, error) {
	if !avatarObjectKey.MatchString(key) {
		return nil, providerFailure("reference_bridge_failed")
	}
	target := *b.endpoint
	target.Path = "/" + b.config.Bucket + "/" + key
	return &target, nil
}
func (b *S3ReferenceBridge) avatarClient(ctx context.Context, target *url.URL) (*http.Client, func(), error) {
	if b.client != nil {
		return b.client, func() {}, nil
	}
	_, ip, err := downloadDestination(ctx, target.String(), net.DefaultResolver.LookupNetIP)
	if err != nil {
		return nil, nil, providerFailure("unsafe_reference_bridge")
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	transport := pinnedDownloadTransport(ip, dialer.DialContext)
	return &http.Client{Transport: transport, Timeout: 120 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, transport.CloseIdleConnections, nil
}
func (b *S3ReferenceBridge) avatarFileURL(ctx context.Context, key, path, mime string) (string, error) {
	target, err := b.avatarTarget(key)
	if err != nil {
		return "", err
	}
	client, done, err := b.avatarClient(ctx, target)
	if err != nil {
		return "", err
	}
	defer done()
	f, err := openLocalBlob(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, "PUT", s3Presign(b.config, target, "PUT", b.now(), 120), contextBlobReader{ctx, f})
	if err != nil {
		return "", err
	}
	req.GetBody = nil
	req.ContentLength = stat.Size()
	req.Header.Set("Content-Type", mime)
	resp, err := libraryGuardedRequest(client, req)
	if err != nil {
		return "", providerFailure("reference_bridge_failed")
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != 200 && resp.StatusCode != 204 {
		return "", providerFailure("reference_bridge_failed")
	}
	return s3Presign(b.config, target, "GET", b.now(), 86400), nil
}
func (b *S3ReferenceBridge) deleteAvatarInput(ctx context.Context, key string) {
	target, err := b.avatarTarget(key)
	if err != nil {
		return
	}
	client, done, err := b.avatarClient(ctx, target)
	if err != nil {
		return
	}
	defer done()
	req, err := http.NewRequestWithContext(ctx, "DELETE", s3Presign(b.config, target, "DELETE", b.now(), 120), nil)
	if err != nil {
		return
	}
	resp, err := client.Do(req)
	if err == nil {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
	}
}
