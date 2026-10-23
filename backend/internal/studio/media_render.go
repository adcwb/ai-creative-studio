package studio

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type mediaProbe struct {
	Streams []struct {
		CodecType string `json:"codec_type"`
		CodecName string `json:"codec_name"`
		Width     int    `json:"width"`
		Height    int    `json:"height"`
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
			if s.Width > 3840 || s.Height > 2160 || s.Width < 1 || s.Height < 1 || s.CodecName != "h264" {
				return "", 0, mediaInvalid("仅支持至 3840×2160 的 H.264 视频")
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
func (m *MediaStore) StartExport(project, space, key string, version int) (MediaExport, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d := m.clone()
	k := mediaKey(space, project)
	p := mediaState(d, k)
	digest := mediaDigest([]any{"export", version})
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
	if err := validateTimeline(p, p.Timeline); err != nil {
		return MediaExport{}, err
	}
	active := 0
	for _, pp := range d.Projects {
		for _, e := range pp.Exports {
			if e.Status == "running" || e.Status == "queued" {
				active++
			}
		}
	}
	if active >= 2 || len(p.Exports) >= 30 || m.overQuota(128<<20) {
		return MediaExport{}, errMediaLimit
	}
	e := MediaExport{ID: mediaID(), Status: "running", Timeline: p.Timeline}
	for _, c := range e.Timeline.Clips {
		e.Duration += c.Duration
	}
	p.Exports = append(p.Exports, e)
	d.Projects[k] = p
	d.Receipts[k+":"+key] = mediaReceipt{Digest: digest, ExportID: e.ID}
	if err := m.commit(d); err != nil {
		return MediaExport{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	m.cancels[e.ID] = cancel
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		defer cancel()
		err := m.render(ctx, p, e)
		m.finishExport(k, e.ID, space, project, err)
	}()
	return e, nil
}
func (m *MediaStore) finishExport(k, id, space, project string, renderErr error) {
	m.mu.Lock()
	defer m.mu.Unlock()
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
			e.Error = "本地合成失败或超时；请检查素材与 FFmpeg"
		} else {
			e.Status = "succeeded"
			e.FileURL = mediaURL(space, project, "exports/"+id+"/file")
			e.SubtitleURL = mediaURL(space, project, "exports/"+id+"/subtitles")
			e.SubtitleVTTURL = mediaURL(space, project, "exports/"+id+"/subtitles.vtt")
		}
	}
	d.Projects[k] = p
	if err := m.commit(d); err != nil {
		slog.Error("media export state persistence failed; restart required", "export_id", id, "error", err)
		// Keep the persisted running state; restart reports interruption rather than success.
		return
	}
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
			e.Status = "cancelled"
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
func (m *MediaStore) render(ctx context.Context, p MediaState, e MediaExport) error {
	dir, err := os.MkdirTemp(m.dir, "render-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	var concat, subs, vtt strings.Builder
	vtt.WriteString("WEBVTT\n\n")
	elapsed := 0.0
	subtitleCount := 0
	for i, c := range e.Timeline.Clips {
		a, _ := mediaAsset(p, c.AssetID)
		input := filepath.Join(m.dir, a.ID)
		args := []string{}
		if a.Kind == "image" {
			args = append(args, "-loop", "1", "-framerate", "24")
		} else {
			args = append(args, "-ss", seconds(c.TrimStart))
		}
		args = append(args, "-protocol_whitelist", "file,pipe", "-i", input)
		if c.AudioAssetID != "" {
			args = append(args, "-protocol_whitelist", "file,pipe", "-i", filepath.Join(m.dir, c.AudioAssetID))
		} else {
			args = append(args, "-f", "lavfi", "-i", "anullsrc=r=48000:cl=stereo")
		}
		vf := ""
		if c.CropWidth > 0 && c.CropHeight > 0 {
			vf = fmt.Sprintf("crop=iw*%f:ih*%f:iw*%f:ih*%f,", c.CropWidth, c.CropHeight, c.CropX, c.CropY)
		}
		vf += "scale=640:360:force_original_aspect_ratio=decrease,pad=640:360:(ow-iw)/2:(oh-ih)/2,setsar=1,fps=24,format=yuv420p"
		clipPath := filepath.Join(dir, fmt.Sprintf("clip-%03d.mp4", i))
		args = append(args, "-map", "0:v:0", "-map", "1:a:0", "-vf", vf, "-af", "apad,volume="+seconds(c.AudioVolume), "-t", seconds(c.Duration), "-c:v", "libx264", "-preset", "ultrafast", "-threads", "1", "-c:a", "aac", "-ar", "48000", "-ac", "2", "-movflags", "+faststart", clipPath)
		if err = ffmpeg(ctx, args...); err != nil {
			return err
		}
		fmt.Fprintf(&concat, "file '%s'\n", clipPath)
		for _, cue := range clipSubtitleCues(c) {
			subtitleCount++
			text := strings.ReplaceAll(strings.ReplaceAll(cue.Text, "\r", " "), "\n", " ")
			text = strings.ReplaceAll(text, "-->", "→")
			fmt.Fprintf(&subs, "%d\n%s --> %s\n%s\n\n", subtitleCount, srtTime(elapsed+cue.Start), srtTime(elapsed+cue.End), text)
			escaped := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(text)
			fmt.Fprintf(&vtt, "%s --> %s\n%s\n\n", strings.ReplaceAll(srtTime(elapsed+cue.Start), ",", "."), strings.ReplaceAll(srtTime(elapsed+cue.End), ",", "."), escaped)
		}
		elapsed += c.Duration
	}
	list := filepath.Join(dir, "concat.txt")
	if err = os.WriteFile(list, []byte(concat.String()), 0600); err != nil {
		return err
	}
	srt := filepath.Join(m.dir, e.ID+".srt")
	if err = os.WriteFile(srt, []byte(subs.String()), 0600); err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(m.dir, e.ID+".vtt"), []byte(vtt.String()), 0600); err != nil {
		return err
	}
	args := []string{"-f", "concat", "-safe", "0", "-protocol_whitelist", "file,pipe", "-i", list}
	audioMap := "0:a:0"
	nextInput := 1
	if e.Timeline.MusicAssetID != "" {
		args = append(args, "-stream_loop", "-1", "-protocol_whitelist", "file,pipe", "-i", filepath.Join(m.dir, e.Timeline.MusicAssetID))
		args = append(args, "-filter_complex", fmt.Sprintf("[1:a]volume=%f[m];[0:a][m]amix=inputs=2:duration=first:normalize=0[a]", e.Timeline.MusicVolume))
		audioMap = "[a]"
		nextInput++
	}
	if subtitleCount > 0 {
		args = append(args, "-f", "srt", "-i", srt)
	}
	args = append(args, "-map", "0:v:0", "-map", audioMap)
	if subtitleCount > 0 {
		args = append(args, "-map", fmt.Sprintf("%d:s:0", nextInput), "-c:s", "mov_text", "-metadata:s:s:0", "language=zho", "-disposition:s:0", "default")
	}
	out := filepath.Join(dir, "output.mp4")
	args = append(args, "-c:v", "copy", "-c:a", "aac", "-t", seconds(elapsed), "-movflags", "+faststart", out)
	if err = ffmpeg(ctx, args...); err != nil {
		return err
	}
	probe, err := probeMedia(out)
	if err != nil || len(probe.Streams) < 2 {
		return errors.New("导出校验失败")
	}
	return os.Rename(out, filepath.Join(m.dir, e.ID+".mp4"))
}
