package application

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"log/slog"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/media"
	"github.com/adcwb/ai-creative-studio/backend/internal/platform/jobqueue"
	"github.com/adcwb/ai-creative-studio/backend/internal/platform/tracing"
)

type mediaProbe struct {
	Streams []struct {
		NBFrames     string `json:"nb_frames"`
		AvgFrameRate string `json:"avg_frame_rate"`
		DurationTS   int64  `json:"duration_ts"`
		TimeBase     string `json:"time_base"`
		SampleRate   string `json:"sample_rate"`
		Channels     int    `json:"channels"`
		CodecType    string `json:"codec_type"`
		CodecName    string `json:"codec_name"`
		Width        int    `json:"width"`
		Height       int    `json:"height"`
	} `json:"streams"`
	Format struct {
		Duration string `json:"duration"`
		Name     string `json:"format_name"`
	} `json:"format"`
}

func probeMedia(path string) (mediaProbe, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-protocol_whitelist", "file,pipe", "-show_streams", "-show_format", "-of", "json", path)
	out, err := cmd.Output()
	var p mediaProbe
	if err != nil || len(out) > 1<<20 {
		return p, mediaInvalid("无法探测媒体")
	}
	err = json.Unmarshal(out, &p)
	return p, err
}
func validateMedia(path, mime string) (string, float64, error) {
	if mime == "image/png" || mime == "image/jpeg" {
		f, err := os.Open(path)
		if err != nil {
			return "", 0, err
		}
		defer f.Close()
		cfg, format, err := image.DecodeConfig(f)
		if err != nil || cfg.Width < 1 || cfg.Height < 1 || cfg.Width > 8192 || cfg.Height > 8192 || cfg.Width*cfg.Height > 20_000_000 || (format == "png" && mime != "image/png") || (format == "jpeg" && mime != "image/jpeg") {
			return "", 0, mediaInvalid("图片格式或尺寸无效（最多 2000 万像素）")
		}
		if _, err = f.Seek(0, 0); err != nil {
			return "", 0, err
		}
		if _, _, err = image.Decode(f); err != nil {
			return "", 0, mediaInvalid("图片数据不完整")
		}
		return "image", 0, nil
	}
	if mime != "audio/wav" && mime != "audio/mpeg" && mime != "video/mp4" {
		return "", 0, mediaInvalid("仅支持 PNG/JPEG、WAV/MP3 和 MP4")
	}
	p, err := probeMedia(path)
	if err != nil {
		return "", 0, err
	}
	duration, err := strconv.ParseFloat(p.Format.Duration, 64)
	if err != nil || duration <= 0 || duration > 180 {
		return "", 0, mediaInvalid("媒体时长需为 0–180 秒")
	}
	expected := map[string]string{"audio/wav": "wav", "audio/mpeg": "mp3", "video/mp4": "mp4"}[mime]
	if !strings.Contains(p.Format.Name, expected) {
		return "", 0, mediaInvalid("文件内容与媒体类型不匹配")
	}
	audio, video := 0, 0
	for _, s := range p.Streams {
		switch s.CodecType {
		case "audio":
			audio++
			if s.CodecName != "aac" && s.CodecName != "mp3" && !strings.HasPrefix(s.CodecName, "pcm_") {
				return "", 0, mediaInvalid("音频编码不支持")
			}
		case "video":
			video++
			if s.Width < 1 || s.Height < 1 || s.Width > 3840 || s.Height > 3840 || int64(s.Width)*int64(s.Height) > 3840*2160 || s.CodecName != "h264" {
				return "", 0, mediaInvalid("仅支持横屏或竖屏 4K 范围内的 H.264 视频")
			}
		default:
			return "", 0, mediaInvalid("上传媒体不能带字幕、附件或数据流")
		}
	}
	if mime == "video/mp4" && video == 1 && audio <= 1 {
		return "video", duration, nil
	}
	if strings.HasPrefix(mime, "audio/") && audio == 1 && video == 0 {
		return "audio", duration, nil
	}
	return "", 0, mediaInvalid("媒体流数量无效")
}
func ffmpeg(ctx context.Context, args ...string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	base := []string{"-hide_banner", "-loglevel", "error", "-nostdin", "-y", "-threads", "1", "-filter_threads", "1", "-filter_complex_threads", "1"}
	cmd := exec.CommandContext(ctx, "ffmpeg", append(base, args...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("local encoder failed: %w", err)
	}
	return nil
}
func seconds(v float64) string { return strconv.FormatFloat(v, 'f', 3, 64) }
func srtTime(t float64) string {
	ms := int(t*1000 + 0.5)
	return fmt.Sprintf("%02d:%02d:%02d,%03d", ms/3600000, ms/60000%60, ms/1000%60, ms%1000)
}
func (m *MediaStore) StartExport(project, space, key string, version int, traceParents ...string) (MediaExport, error) {
	return m.StartEpisodeExport(project, space, key, version, 0, traceParents...)
}
func (m *MediaStore) StartEpisodeExport(project, space, key string, version, episode int, traceParents ...string) (MediaExport, error) {
	if episode < 0 || episode > 9007199254740991 {
		return MediaExport{}, mediaInvalid("集数需为有效正整数")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return MediaExport{}, errors.New("media store closed")
	}
	d := m.clone()
	k := mediaKey(space, project)
	p := mediaState(d, k)
	digest := mediaDigest([]any{"export", version})
	if episode > 1 {
		digest = mediaDigest([]any{"export", version, episode})
	}
	selected := timelineForEpisode(p, episode)
	if replay, err := m.check(d, k, key, digest); err != nil {
		return MediaExport{}, err
	} else if replay {
		for _, e := range p.Exports {
			if e.ID == d.Receipts[k+":"+key].ExportID {
				return e, nil
			}
		}
		return MediaExport{}, errors.New("导出回执损坏")
	}
	if p.Version != version {
		return MediaExport{}, errMediaVersion
	}
	if err := validateTimeline(p, selected); err != nil {
		return MediaExport{}, err
	}
	if m.blobs != nil {
		ids := map[string]bool{}
		for _, clip := range selected.Clips {
			ids[clip.AssetID] = true
			if clip.SourceAudioAssetID != "" {
				ids[clip.SourceAudioAssetID] = true
			}
			if clip.AudioAssetID != "" {
				ids[clip.AudioAssetID] = true
			}
		}
		if selected.MusicAssetID != "" {
			ids[selected.MusicAssetID] = true
		}
		for _, track := range selected.AudioTracks {
			ids[track.AssetID] = true
		}
		for _, overlay := range selected.VideoOverlays {
			ids[overlay.AssetID] = true
		}
		for id := range ids {
			if _, err := m.localBlob(space, project, id); err != nil {
				return MediaExport{}, err
			}
		}
	}
	parent := ""
	if len(traceParents) == 1 && tracing.ValidParent(traceParents[0]) {
		parent = traceParents[0]
	}
	e := MediaExport{RenderVersion: 1, EpisodeNumber: episode, TraceParent: parent, ID: mediaID(), Status: "queued", Timeline: selected}
	plan, err := media.CompileRenderPlan(e.Timeline)
	if err != nil {
		return MediaExport{}, err
	}
	e.Duration = float64(plan.Frames) / 24
	p.Exports = append(p.Exports, e)
	d.Projects[k] = p
	d.Receipts[k+":"+key] = mediaReceipt{Digest: digest, ExportID: e.ID}
	if m.pg != nil {
		payload, _ := json.Marshal(map[string]string{"task_id": e.ID, "project_id": project, "render_version": "1"})
		m.pg.beforeCommit = func(ctx context.Context, tx *sql.Tx) error {
			return jobqueue.EnqueueTx(ctx, tx, jobqueue.Job{TenantID: space, ID: e.ID, TraceParent: e.TraceParent, Kind: "export", IdempotencyKey: project + ":" + key, Payload: payload})
		}
		defer func() { m.pg.beforeCommit = nil }()
	}
	if err := m.commit(d); err != nil {
		return MediaExport{}, err
	}
	if m.pg != nil {
		return e, nil
	}
	m.scheduleExportLocked(space, project, p, e)
	return e, nil
}

func (m *MediaStore) scheduleExportLocked(space, project string, p MediaState, e MediaExport) {
	if m.closed || m.cancels[e.ID] != nil {
		return
	}
	k := mediaKey(space, project)
	ctx, cancel := context.WithCancel(tracing.WithParent(context.Background(), e.TraceParent))
	m.cancels[e.ID] = cancel
	if m.attempts == nil {
		m.attempts = map[string]string{}
	}
	attempt := mediaID()
	m.attempts[e.ID] = attempt
	if m.jobs == nil {
		m.jobs = newLocalJobs(2)
	}
	m.jobs.submit(func() {
		defer cancel()
		m.mu.Lock()
		if m.closed || ctx.Err() != nil {
			m.mu.Unlock()
			return
		}
		d := m.clone()
		current := d.Projects[k]
		found := false
		for i := range current.Exports {
			if current.Exports[i].ID == e.ID && current.Exports[i].Status == "queued" {
				current.Exports[i].Status = "running"
				found = true
				break
			}
		}
		if !found {
			delete(m.cancels, e.ID)
			m.mu.Unlock()
			return
		}
		d.Projects[k] = current
		err := m.commit(d)
		m.mu.Unlock()
		if err != nil {
			return
		}
		renderCtx := context.WithValue(ctx, renderScopeKey{}, renderScope{Space: space, Project: project})
		err = m.exportInputs(space, project, p, e)
		if err == nil {
			err = m.render(renderCtx, p, e)
		}
		if err == nil && m.blobs != nil {
			for _, ext := range []string{".mp4", ".srt", ".vtt"} {
				if err = m.storeBlobContext(ctx, space, project, e.ID+ext); err != nil {
					break
				}
			}
		}
		m.finishExport(k, e.ID, space, project, err, attempt)
	})
}
func (m *MediaStore) finishExport(k, id, space, project string, renderErr error, attempt string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || m.attempts[id] != attempt {
		return
	}
	delete(m.attempts, id)
	if err := m.finishExportLocked(k, id, space, project, renderErr); err != nil {
		slog.Error("media export state persistence failed; restart required", "export_id", id)
	}
}
func (m *MediaStore) finishExportLocked(k, id, space, project string, renderErr error) error {
	delete(m.cancels, id)
	d := m.clone()
	p := d.Projects[k]
	for i := range p.Exports {
		e := &p.Exports[i]
		if e.ID != id || e.Status == "cancelled" {
			continue
		}
		if renderErr != nil {
			e.Status = "failed"
			e.ResumeAvailable = e.RenderVersion == 1 && !errors.Is(renderErr, errRenderInputsChanged)
			e.Error = "本地合成失败或超时；请检查素材与 FFmpeg"
			if e.ResumeAvailable {
				e.Error = "合成未完成，进度已保留。请检查素材、存储服务和 FFmpeg 后继续"
			}
			if errors.Is(renderErr, errRenderInputsChanged) {
				e.Error = "素材或合成环境已变更，请重新导出"
			}
		} else {
			e.Status = "succeeded"
			e.ResumeAvailable = false
			e.FileURL = mediaURL(space, project, "exports/"+id+"/file")
			e.SubtitleURL = mediaURL(space, project, "exports/"+id+"/subtitles")
			e.SubtitleVTTURL = mediaURL(space, project, "exports/"+id+"/subtitles.vtt")
		}
	}
	d.Projects[k] = p
	return m.commit(d)
}
func (m *MediaStore) CancelExport(project, space, key, id string) (MediaExport, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d := m.clone()
	k := mediaKey(space, project)
	p := mediaState(d, k)
	digest := mediaDigest([]string{"cancel", id})
	replay, err := m.check(d, k, key, digest)
	if err != nil {
		return MediaExport{}, err
	}
	for i := range p.Exports {
		e := &p.Exports[i]
		if e.ID != id {
			continue
		}
		if replay {
			return *e, nil
		}
		if e.Status == "running" || e.Status == "queued" {
			if m.pg != nil {
				m.pg.beforeCommit = func(ctx context.Context, tx *sql.Tx) error {
					return CancelQueuedPostgresTx(ctx, tx, space, id, "export")
				}
				defer func() { m.pg.beforeCommit = nil }()
			}
			e.Status = "cancelled"
			e.ResumeAvailable = e.RenderVersion == 1
			e.Error = "已取消本地合成"
		}
		d.Projects[k] = p
		d.Receipts[k+":"+key] = mediaReceipt{Digest: digest, ExportID: id}
		if err = m.commit(d); err != nil {
			return MediaExport{}, err
		}
		if cancel := m.cancels[id]; cancel != nil {
			cancel()
		}
		return *e, nil
	}
	return MediaExport{}, ErrNotFound
}
