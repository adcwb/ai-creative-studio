package application

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const avatarSegmentSamples int64 = 18 * 48000 // margin below the vendor's strict <20s window

type avatarSegmentProgress struct {
	CutReason string  `json:"cut_reason,omitempty"`
	Reused    bool    `json:"reused,omitempty"`
	Index     int     `json:"index"`
	Start     float64 `json:"start_seconds"`
	Duration  float64 `json:"duration_seconds"`
	Status    string  `json:"status"`
}
type avatarSegment struct {
	CutReason  string    `json:"cut_reason,omitempty"`
	ReusedFrom string    `json:"reused_from_job_id,omitempty"`
	Start      int64     `json:"start_sample"`
	Samples    int64     `json:"samples"`
	AudioSHA   string    `json:"audio_sha256"`
	Phase      string    `json:"phase"`
	RemoteID   string    `json:"remote_id,omitempty"`
	Video      *BlobInfo `json:"video,omitempty"`
}

func avatarSegmentKey(job CreativeJob, index int) string {
	return avatarCheckpointKey(job, fmt.Sprintf("segment-%d", index))
}
func avatarSegmentAudioKey(job CreativeJob, index int) string {
	sha, _ := projectFingerprint("avatar-segment-audio", job.ID, []any{job.SpaceID, job.ProjectID, index})
	return "avatar-inputs/" + sha + "/audio"
}
func avatarSegmentFrames(seg avatarSegment) int64 {
	return (seg.Start+seg.Samples+1999)/2000 - (seg.Start+1999)/2000
}
func validAvatarSegments(job CreativeJob) bool {
	cp := job.AvatarCheckpoint
	if !validAvatarSplitSpec(job.AvatarSplitMode, job.AvatarCutPoints, job.AvatarPlanSHA) || cp.Planner != avatarPlanner(job.AvatarSplitMode) {
		return false
	}
	if cp.Format != 2 || job.Provider != wanAvatarProvider || job.Kind != "digital_human_authorized" || !job.CloudTransferConfirmed || cp.TotalSamples < 1 || len(cp.Segments) == 0 || cp.RemoteID != "" || cp.CloudDestination == "" {
		return false
	}
	var offset int64
	priorDone := true
	for i, seg := range cp.Segments {
		if seg.ReusedFrom != "" && seg.Phase != "done" {
			return false
		}
		if seg.Start != offset || seg.Samples < 1 || seg.Samples >= 20*48000 || seg.Samples > cp.TotalSamples-offset || len(seg.AudioSHA) != 64 || avatarSegmentFrames(seg) < 1 {
			return false
		}
		switch seg.Phase {
		case "queued", "dispatching":
			if seg.RemoteID != "" || seg.Video != nil {
				return false
			}
		case "remote_pending":
			if !wanTaskID.MatchString(seg.RemoteID) || seg.Video != nil {
				return false
			}
		case "done":
			if !wanTaskID.MatchString(seg.RemoteID) || seg.Video == nil || !validBlobInfo(*seg.Video) || seg.Video.Key != avatarSegmentExpectedKey(job, i, seg) {
				return false
			}
		default:
			return false
		}
		// A later segment is never submitted until every predecessor is confirmed.
		if !priorDone && seg.Phase != "queued" && !(seg.Phase == "done" && seg.ReusedFrom != "") {
			return false
		}
		priorDone = priorDone && seg.Phase == "done"
		offset += seg.Samples
	}
	if offset != cp.TotalSamples {
		return false
	}
	if cp.Phase == "segments" {
		return cp.Engine == nil && cp.Composed == nil
	}
	for _, seg := range cp.Segments {
		if seg.Phase != "done" {
			return false
		}
	}
	return (cp.Phase == "engine_done" && cp.Engine != nil && cp.Composed == nil) || (cp.Phase == "composed" && cp.Engine != nil && cp.Composed != nil)
}
func validSegmentAdvance(prior, next *avatarCheckpoint) bool {
	if prior.Planner != next.Planner || prior.TotalSamples != next.TotalSamples || len(prior.Segments) != len(next.Segments) {
		return false
	}
	changes := 0
	for i, a := range prior.Segments {
		b := next.Segments[i]
		if a.ReusedFrom != b.ReusedFrom || a.CutReason != b.CutReason || a.Start != b.Start || a.Samples != b.Samples || a.AudioSHA != b.AudioSHA {
			return false
		}
		if a.Phase == b.Phase {
			if a.RemoteID != b.RemoteID || !sameAvatarBlob(a.Video, b.Video) {
				return false
			}
			continue
		}
		changes++
		if !(a.Phase == "queued" && b.Phase == "dispatching" || a.Phase == "dispatching" && b.Phase == "remote_pending" || a.Phase == "remote_pending" && b.Phase == "done") {
			return false
		}
		if a.RemoteID != "" && a.RemoteID != b.RemoteID {
			return false
		}
	}
	return changes == 1 && prior.Phase == "segments" && next.Phase == "segments" || changes == 0 && prior.Phase != next.Phase
}
func sameAvatarBlob(a, b *BlobInfo) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}
func cloneAvatarCheckpoint(cp *avatarCheckpoint) *avatarCheckpoint {
	if cp == nil {
		return nil
	}
	out := *cp
	out.Segments = append([]avatarSegment(nil), cp.Segments...)
	return &out
}
func avatarSegmentsRecoverable(job CreativeJob) bool {
	cp := job.AvatarCheckpoint
	if cp == nil || cp.Format != 2 || !validAvatarCheckpoint(job) {
		return false
	}
	for _, s := range cp.Segments {
		if s.Phase == "dispatching" {
			return false
		}
	}
	return true
}

// The decoded sample count, not rounded container duration, fixes all boundaries.
// PCM stays private on disk; splitting and upload never read the whole audio.
func prepareAvatarSegments(ctx context.Context, work *avatarWork, options ...avatarSplitSpec) ([]avatarSegment, []string, int64, error) {
	dir := filepath.Dir(work.videoPath)
	pcm := filepath.Join(dir, "speech.s16")
	if err := ffmpeg(ctx, "-protocol_whitelist", "file,pipe", "-i", work.audioPath, "-map", "0:a:0", "-vn", "-ar", "48000", "-ac", "1", "-c:a", "pcm_s16le", "-f", "s16le", pcm); err != nil {
		return nil, nil, 0, err
	}
	f, err := openLocalBlob(pcm)
	if err != nil {
		return nil, nil, 0, err
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil || stat.Size()%2 != 0 || stat.Size() < 2 {
		return nil, nil, 0, ErrInvalidProjectInput
	}
	total := stat.Size() / 2
	spec := avatarSplitSpec{}
	if len(options) > 0 {
		spec = options[0]
	}
	ends, reasons, err := avatarCutBounds(f, total, spec)
	if err != nil {
		return nil, nil, 0, err
	}
	segments := []avatarSegment{}
	paths := []string{}
	offset := int64(0)
	for index, end := range ends {
		samples := end - offset
		path := filepath.Join(dir, fmt.Sprintf("speech-%d.wav", len(paths)))
		out, e := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			return nil, nil, 0, e
		}
		header := make([]byte, 44)
		copy(header, "RIFF")
		binary.LittleEndian.PutUint32(header[4:], uint32(36+samples*2))
		copy(header[8:], "WAVEfmt ")
		binary.LittleEndian.PutUint32(header[16:], 16)
		binary.LittleEndian.PutUint16(header[20:], 1)
		binary.LittleEndian.PutUint16(header[22:], 1)
		binary.LittleEndian.PutUint32(header[24:], 48000)
		binary.LittleEndian.PutUint32(header[28:], 96000)
		binary.LittleEndian.PutUint16(header[32:], 2)
		binary.LittleEndian.PutUint16(header[34:], 16)
		copy(header[36:], "data")
		binary.LittleEndian.PutUint32(header[40:], uint32(samples*2))
		_, e = out.Write(header)
		if e == nil {
			_, e = io.CopyN(out, contextBlobReader{ctx, f}, samples*2)
		}
		e = errors.Join(e, out.Close())
		if e != nil {
			return nil, nil, 0, e
		}
		info, e := localBlobInfo(ctx, "segment-audio", path)
		if e != nil {
			return nil, nil, 0, e
		}
		segments = append(segments, avatarSegment{Start: offset, Samples: samples, AudioSHA: info.SHA256, Phase: "queued", CutReason: reasons[index]})
		paths = append(paths, path)
		offset += samples
	}
	return segments, paths, total, nil
}
func (s *FileStore) saveAvatarSegmentVideo(ctx context.Context, job *CreativeJob, index int, path string) error {
	key := avatarSegmentKey(*job, index)
	info, err := localBlobInfo(ctx, key, path)
	if err != nil {
		return err
	}
	if s.blobs != nil {
		err = s.blobs.putFile(ctx, key, path)
	} else if s.pg == nil {
		err = publishAvatarCheckpointFile(ctx, path, filepath.Join(s.media.dir, filepath.Base(key)), info)
	} else {
		err = ErrBlobIntegrity
	}
	if err != nil {
		return err
	}
	cp := cloneAvatarCheckpoint(job.AvatarCheckpoint)
	cp.Segments[index].Phase = "done"
	cp.Segments[index].Video = &info
	return s.creative.recordAvatarCheckpoint(ctx, job, *cp)
}
func normalizeAvatarSegment(ctx context.Context, raw, out string, frames int64, w, h int) error {
	if err := validateProviderMediaFile(ctx, raw, "video/mp4"); err != nil {
		return err
	}
	// Never pad a short generation or loop frames to fake a complete segment.
	_, duration, err := validateMedia(raw, "video/mp4")
	if err != nil || duration+0.045 < float64(frames)/24 {
		return providerFailure("invalid_provider_response")
	}
	filter := fmt.Sprintf("fps=24,scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2,setsar=1,trim=end_frame=%d,setpts=N/(24*TB)", w, h, w, h, frames)
	err = ffmpeg(ctx, "-protocol_whitelist", "file,pipe", "-i", raw, "-map", "0:v:0", "-vf", filter, "-an", "-frames:v", strconv.FormatInt(frames, 10), "-c:v", "libx264", "-preset", "veryfast", "-pix_fmt", "yuv420p", "-r", "24", "-video_track_timescale", "24000", "-movflags", "+faststart", out)
	if err != nil {
		return err
	}
	probe, err := probeMedia(out)
	if err != nil {
		return err
	}
	for _, v := range probe.Streams {
		if v.CodecType == "video" {
			n, e := strconv.ParseInt(v.NBFrames, 10, 64)
			if e == nil && n == frames {
				return nil
			}
		}
	}
	return providerFailure("invalid_provider_response")
}
func (s *FileStore) executeSegmentedCloudAvatar(ctx context.Context, job *CreativeJob, work *avatarWork, p *avatarCloud) error {
	if err := p.allowed(*job); err != nil {
		return err
	}
	spec := avatarSplitSpec{Mode: job.AvatarSplitMode, Cuts: job.AvatarCutPoints, Approved: job.AvatarPlanSHA}
	plan, audio, total, err := prepareAvatarSegments(ctx, work, spec)
	if err != nil {
		return err
	}
	if spec.Approved != "" && avatarPlanDigest(job.AudioAssetID, spec, total, plan) != spec.Approved {
		return avatarPlanChanged()
	}
	sha, err := avatarSourceSHA(ctx, *job, work)
	if err != nil {
		return err
	}
	if job.AvatarCheckpoint == nil {
		opaque, _ := projectFingerprint("cloud-avatar-inputs", job.ID, []string{job.SpaceID, job.ProjectID})
		cp := avatarCheckpoint{Format: 2, Phase: "segments", SourceSHA256: sha, CloudDestination: p.fingerprint, CloudImageKey: "avatar-inputs/" + opaque + "/image", CloudAudioKey: "avatar-inputs/" + opaque + "/audio", TotalSamples: total, Segments: plan, Planner: avatarPlanner(job.AvatarSplitMode)}
		if err = s.creative.recordAvatarCheckpoint(ctx, job, cp); err != nil {
			return err
		}
	}
	cp := job.AvatarCheckpoint
	if cp.SourceSHA256 != sha || cp.TotalSamples != total || len(cp.Segments) != len(plan) {
		return providerFailure("avatar_source_changed")
	}
	for i, v := range plan {
		old := cp.Segments[i]
		if v.AudioSHA != old.AudioSHA || v.Start != old.Start || v.Samples != old.Samples {
			return providerFailure("avatar_source_changed")
		}
	}
	if !avatarSegmentsRecoverable(*job) {
		return providerFailure("execution_outcome_unknown")
	}
	work.duration = float64(total) / 48000
	work.exactAudioSamples = total
	// Validate all confirmed proof files before any remaining (possibly paid)
	// dispatch. Reused outputs are never trusted merely because JSON names them.
	for i, seg := range job.AvatarCheckpoint.Segments {
		if seg.Phase == "done" {
			if err = s.checkAvatarJob(ctx, job); err != nil {
				return err
			}
			raw := filepath.Join(filepath.Dir(work.videoPath), fmt.Sprintf("fixed-%d.mp4", i))
			if err = s.materializeAvatarMedia(ctx, *seg.Video, raw); err != nil {
				return err
			}
		}
	}
	for i := range plan {
		seg := job.AvatarCheckpoint.Segments[i]
		if seg.Phase == "done" {
			continue
		}
		if err = s.checkAvatarJob(ctx, job); err != nil {
			return err
		}
		if err = p.allowed(*job); err != nil {
			return err
		}
		if seg.Phase == "queued" {
			clip := *work
			clip.audioPath = audio[i]
			clip.audioMIME = "audio/wav"
			clip.duration = float64(seg.Samples) / 48000
			if err = p.validate(&clip); err != nil {
				return err
			}
			next := cloneAvatarCheckpoint(job.AvatarCheckpoint)
			next.Segments[i].Phase = "dispatching"
			if err = s.creative.recordAvatarCheckpoint(ctx, job, *next); err != nil {
				return err
			}
			imageURL, e := p.inputs.avatarFileURL(ctx, cp.CloudImageKey, work.referencePath, work.referenceMIME)
			if e != nil {
				return e
			}
			audioURL, e := p.inputs.avatarFileURL(ctx, avatarSegmentAudioKey(*job, i), audio[i], "audio/wav")
			if e != nil {
				return e
			}
			response, e := p.metadata(ctx, "POST", "/services/aigc/image2video/video-synthesis", map[string]any{"model": "wan2.2-s2v", "input": map[string]string{"image_url": imageURL, "audio_url": audioURL}, "parameters": map[string]string{"resolution": p.resolution}})
			if e != nil {
				return e
			}
			if response.Output.Status != "PENDING" && response.Output.Status != "RUNNING" && response.Output.Status != "SUCCEEDED" {
				return providerFailure("execution_outcome_unknown")
			}
			next = cloneAvatarCheckpoint(job.AvatarCheckpoint)
			next.Segments[i].Phase = "remote_pending"
			next.Segments[i].RemoteID = response.Output.ID
			if err = s.creative.recordAvatarCheckpoint(ctx, job, *next); err != nil {
				return err
			}
		}
		for {
			if err = s.checkAvatarJob(ctx, job); err != nil {
				return err
			}
			if err = p.allowed(*job); err != nil {
				return err
			}
			result, e := p.poll(ctx, job.AvatarCheckpoint.Segments[i].RemoteID)
			if e != nil {
				return e
			}
			if result.Output.Status == "SUCCEEDED" {
				if err = s.checkAvatarJob(ctx, job); err != nil {
					return err
				}
				raw := filepath.Join(filepath.Dir(work.videoPath), fmt.Sprintf("raw-%d.mp4", i))
				f, e := os.OpenFile(raw, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
				if e != nil {
					return e
				}
				e = p.download(ctx, result.Output.Results.URL, f)
				e = errors.Join(e, f.Close())
				if e != nil {
					return e
				}
				if e = validateProviderMediaFile(ctx, raw, "video/mp4"); e != nil {
					return e
				}
				if _, duration, e := validateMedia(raw, "video/mp4"); e != nil || duration+0.045 < float64(avatarSegmentFrames(job.AvatarCheckpoint.Segments[i]))/24 {
					return providerFailure("invalid_provider_response")
				}
				if e = s.saveAvatarSegmentVideo(ctx, job, i, raw); e != nil {
					return e
				}
				p.inputs.deleteAvatarInput(ctx, avatarSegmentAudioKey(*job, i))
				break
			}
			switch result.Output.Status {
			case "FAILED", "CANCELED":
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
	var list strings.Builder
	w, h := 0, 0
	for i, seg := range job.AvatarCheckpoint.Segments {
		if err = s.checkAvatarJob(ctx, job); err != nil {
			return err
		}
		raw := filepath.Join(filepath.Dir(work.videoPath), fmt.Sprintf("fixed-%d.mp4", i))
		if _, statErr := os.Stat(raw); os.IsNotExist(statErr) {
			if err = s.materializeAvatarMedia(ctx, *seg.Video, raw); err != nil {
				return err
			}
		} else if statErr != nil {
			return statErr
		}
		if i == 0 {
			probe, e := probeMedia(raw)
			if e != nil {
				return e
			}
			for _, v := range probe.Streams {
				if v.CodecType == "video" {
					w, h = v.Width/2*2, v.Height/2*2
					break
				}
			}
			if w < 2 || h < 2 {
				return providerFailure("invalid_provider_response")
			}
		}
		normalized := filepath.Join(filepath.Dir(work.videoPath), fmt.Sprintf("segment-%d.mp4", i))
		if err = normalizeAvatarSegment(ctx, raw, normalized, avatarSegmentFrames(seg), w, h); err != nil {
			return err
		}
		// Relative generated basenames only; never accept a client concat path.
		fmt.Fprintf(&list, "file 'segment-%d.mp4'\n", i)
	}
	manifest := filepath.Join(filepath.Dir(work.videoPath), "segments.txt")
	if err = os.WriteFile(manifest, []byte(list.String()), 0600); err != nil {
		return err
	}
	if err = ffmpeg(ctx, "-protocol_whitelist", "file,pipe", "-f", "concat", "-safe", "1", "-i", manifest, "-map", "0:v:0", "-an", "-c:v", "copy", "-movflags", "+faststart", work.videoPath); err != nil {
		return err
	}
	if err = validateProviderMediaFile(ctx, work.videoPath, "video/mp4"); err != nil {
		return err
	}
	if err = s.saveAvatarMediaCheckpoint(ctx, job, "engine_done", work.videoPath); err != nil {
		return err
	}
	p.inputs.deleteAvatarInput(ctx, cp.CloudImageKey)
	return nil
}

// Long, explicitly authorized plans receive time for each native invocation.
// This is a single worker-attempt deadline, not a project/media-duration quota.
func (s *FileStore) creativeJobTimeout(job CreativeJob) time.Duration {
	if job.Provider != wanAvatarProvider || !isAvatarKind(job.Kind) {
		return 10 * time.Minute
	}
	state, err := s.media.GetChecked(job.ProjectID, job.SpaceID)
	if err != nil {
		return 10 * time.Minute
	}
	audio, ok := mediaAsset(state, job.AudioAssetID)
	if !ok || math.IsNaN(audio.Duration) || math.IsInf(audio.Duration, 0) || audio.Duration <= 0 {
		return 10 * time.Minute
	}
	chunks := math.Ceil(audio.Duration / 18)
	if job.AvatarSplitMode == "pause" {
		chunks = math.Ceil(audio.Duration / 12) // earliest acoustic candidate
	} else if job.AvatarSplitMode == "manual" {
		chunks = float64(len(job.AvatarCutPoints)) + 1
	}
	if cp := job.AvatarCheckpoint; cp != nil && cp.Format == 2 {
		chunks = 0
		for _, seg := range cp.Segments {
			if seg.Phase != "done" {
				chunks++
			}
		}
	}
	chunks = max(1, chunks) // completed proofs still need composition/publication

	if chunks >= 144 {
		return 24 * time.Hour
	}
	return time.Duration(chunks) * 10 * time.Minute
}
