package application

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/media"
)

func frameSeconds(n int64) string  { return strconv.FormatFloat(float64(n)/24, 'f', 9, 64) }
func sampleSeconds(n int64) string { return strconv.FormatFloat(float64(n)/48000, 'f', 9, 64) }
func renderVideoArgs(args []string, out string, frames int64, rate string) []string {
	return append(args, "-an", "-frames:v", strconv.FormatInt(frames, 10), "-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p", "-r", "24", "-g", "24", "-keyint_min", "24", "-sc_threshold", "0", "-maxrate", rate, "-bufsize", "16M", "-threads", "1", "-video_track_timescale", "24000", "-movie_timescale", "48000", "-movflags", "+faststart", out)
}
func renderRate(t MediaTimeline) string {
	switch t.OutputResolution {
	case "720p":
		return "6M"
	case "1080p":
		return "8M"
	case "1440p":
		return "12M"
	case "1800p":
		return "18M"
	case "2160p":
		return "24M"
	}
	return "4M"
}
func renderPCM(ctx context.Context, args []string, filter, out string, samples int64) error {
	return ffmpeg(ctx, append(args, "-filter_complex", filter, "-map", "[out]", "-vn", "-t", sampleSeconds(samples), "-c:a", "pcm_f32le", "-ar", "48000", "-ac", "2", "-rf64", "auto", out)...)
}
func (m *MediaStore) renderClipVideo(ctx context.Context, p MediaState, c MediaClip, offset, frames int64, out string, w, h int, rate string) error {
	a, ok := mediaAsset(p, c.AssetID)
	if !ok {
		return ErrNotFound
	}
	args := []string{}
	if a.Kind == "image" {
		args = append(args, "-loop", "1", "-framerate", "24")
	} else {
		args = append(args, "-ss", strconv.FormatFloat(c.TrimStart+float64(offset)/24, 'f', 9, 64))
	}
	args = append(args, "-protocol_whitelist", "file,pipe", "-i", filepath.Join(m.dir, a.ID))
	vf := ""
	if c.CropWidth > 0 && c.CropHeight > 0 {
		vf = fmt.Sprintf("crop=iw*%f:ih*%f:iw*%f:ih*%f,", c.CropWidth, c.CropHeight, c.CropX, c.CropY)
	}
	vf += fmt.Sprintf("scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2,setsar=1,fps=24,format=yuv420p,setpts=PTS-STARTPTS", w, h, w, h)
	if c.Transition == "fade" {
		vf += fmt.Sprintf("+%s/TB,fade=t=in:st=0:d=%s,fade=t=out:st=%s:d=%s,setpts=PTS-STARTPTS", frameSeconds(offset), seconds(c.TransitionDuration), seconds(c.Duration-c.TransitionDuration), seconds(c.TransitionDuration))
	}
	return ffmpeg(ctx, renderVideoArgs(append(args, "-map", "0:v:0", "-vf", vf), out, frames, rate)...)
}
func (m *MediaStore) renderClipAudio(ctx context.Context, p MediaState, c MediaClip, offset, frames int64, out string) error {
	samples := frames * 2000
	args, filters, labels := []string{}, []string{}, []string{}
	add := func(id string, start int64, volume float64) error {
		if id == "" {
			return nil
		}
		path := filepath.Join(m.dir, id)
		probe, e := probeMedia(path)
		if e != nil {
			return e
		}
		has := false
		for _, stream := range probe.Streams {
			if stream.CodecType == "audio" {
				has = true
			}
		}
		if !has {
			return nil
		}
		index := len(labels)
		args = append(args, "-protocol_whitelist", "file,pipe", "-i", path)
		label := fmt.Sprintf("a%d", index)
		filters = append(filters, fmt.Sprintf("[%d:a:0]aresample=48000,aformat=sample_fmts=flt:channel_layouts=stereo,atrim=start_sample=%d:end_sample=%d,asetpts=PTS-STARTPTS,apad=whole_len=%d,atrim=end_sample=%d,volume=%s[%s]", index, start, start+samples, samples, samples, seconds(volume), label))
		labels = append(labels, "["+label+"]")
		return nil
	}
	if e := add(c.AudioAssetID, offset*2000, c.AudioVolume); e != nil {
		return e
	}
	if c.PreserveSourceAudio {
		id, trim := c.AssetID, c.TrimStart
		if c.SourceAudioAssetID != "" {
			id, trim = c.SourceAudioAssetID, c.SourceAudioTrimStart
		}
		n, e := media.RenderSamples(trim)
		if e != nil {
			return e
		}
		if e := add(id, n+offset*2000, c.SourceAudioVolume); e != nil {
			return e
		}
	}
	if len(labels) == 0 {
		args = append(args, "-f", "lavfi", "-i", "anullsrc=r=48000:cl=stereo")
		filters = append(filters, fmt.Sprintf("[0:a]atrim=end_sample=%d[out]", samples))
	} else if len(labels) == 1 {
		filters = append(filters, labels[0]+"anull[out]")
	} else {
		filters = append(filters, strings.Join(labels, "")+fmt.Sprintf("amix=inputs=%d:duration=first:normalize=0[out]", len(labels)))
	}
	return renderPCM(ctx, args, strings.Join(filters, ";"), out, samples)
}
func (m *MediaStore) renderLayer(ctx context.Context, p MediaState, base string, layer MediaVideoOverlay, block media.RenderBlock, w, h int, rate, out string) error {
	start, end := float64(block.Start)/24, float64(block.End)/24
	from, to := max(start, layer.Start), min(end, layer.Start+layer.Duration)
	if from >= to {
		return errors.New("nonintersecting render layer")
	}
	offset := from - layer.Start
	asset, _ := mediaAsset(p, layer.AssetID)
	args := []string{"-protocol_whitelist", "file,pipe", "-i", base}
	if asset.Kind == "image" {
		args = append(args, "-loop", "1", "-framerate", "24")
	} else {
		args = append(args, "-ss", strconv.FormatFloat(layer.TrimStart+offset, 'f', 9, 64))
	}
	args = append(args, "-t", strconv.FormatFloat(to-from, 'f', 9, 64), "-protocol_whitelist", "file,pipe", "-i", filepath.Join(m.dir, asset.ID))
	width, height := int(float64(w)*layer.Width)/2*2, int(float64(h)*layer.Height)/2*2
	clock := "t+" + strconv.FormatFloat(start-layer.Start, 'f', 9, 64)
	x, y := keyframeExpression(layer, "x", clock), keyframeExpression(layer, "y", clock)
	alpha := "colorchannelmixer=aa=" + seconds(layer.Opacity)
	if len(layer.Keyframes) > 0 {
		alpha = fmt.Sprintf("geq=r='r(X,Y)':g='g(X,Y)':b='b(X,Y)':a='alpha(X,Y)*(%s)'", keyframeExpression(layer, "opacity", "T+"+strconv.FormatFloat(offset, 'f', 9, 64)))
	}
	vf := fmt.Sprintf("[1:v]setpts=PTS-STARTPTS,scale=%d:%d:force_original_aspect_ratio=decrease,format=rgba,pad=%d:%d:(ow-iw)/2:(oh-ih)/2:color=black@0,%s,setpts=PTS+%s/TB[over];[0:v][over]overlay=x='2*floor(%d*(%s)/2)':y='2*floor(%d*(%s)/2)':eof_action=pass:repeatlast=0:enable='gte(t,%s)*lt(t,%s)'[v]", width, height, width, height, alpha, strconv.FormatFloat(from-start, 'f', 9, 64), w, x, h, y, strconv.FormatFloat(from-start, 'f', 9, 64), strconv.FormatFloat(to-start, 'f', 9, 64))
	return ffmpeg(ctx, renderVideoArgs(append(args, "-filter_complex", vf, "-map", "[v]"), out, block.End-block.Start, rate)...)
}
func (m *MediaStore) mixRenderBlock(ctx context.Context, p MediaState, t MediaTimeline, block media.RenderBlock, base, dir string) (string, error) {
	tracks := append([]MediaAudioTrack{}, t.AudioTracks...)
	if t.MusicAssetID != "" {
		tracks = append([]MediaAudioTrack{{ID: "music", AssetID: t.MusicAssetID, Duration: float64(block.End) / 24, Volume: t.MusicVolume}}, tracks...)
	}
	for _, layer := range t.VideoOverlays {
		if layer.PreserveSourceAudio {
			tracks = append(tracks, MediaAudioTrack{ID: layer.ID, AssetID: layer.AssetID, Start: layer.Start, TrimStart: layer.TrimStart, Duration: layer.Duration, Volume: layer.SourceAudioVolume})
		}
	}
	current, start, end := base, block.Start*2000, block.End*2000
	for i, track := range tracks {
		a, e := media.RenderSamples(track.Start)
		if e != nil {
			return "", e
		}
		d, e := media.RenderSamples(track.Duration)
		if e != nil {
			return "", e
		}
		from, to := max(start, a), min(end, a+d)
		if from >= to {
			continue
		}
		source := filepath.Join(m.dir, track.AssetID)
		probe, e := probeMedia(source)
		if e != nil {
			return "", e
		}
		has := false
		for _, s := range probe.Streams {
			if s.CodecType == "audio" {
				has = true
			}
		}
		if !has {
			continue
		}
		trim, e := media.RenderSamples(track.TrimStart)
		if e != nil {
			return "", e
		}
		offset := trim + from - a
		args := []string{"-protocol_whitelist", "file,pipe", "-i", current}
		if track.ID == "music" && t.MusicAssetID != "" && i == 0 {
			args = append(args, "-stream_loop", "-1")
		}
		args = append(args, "-protocol_whitelist", "file,pipe", "-i", source)
		filter := fmt.Sprintf("[1:a]aresample=48000,aformat=sample_fmts=flt:channel_layouts=stereo,atrim=start_sample=%d:end_sample=%d,asetpts=PTS-STARTPTS,volume=%s,adelay=%dS:all=1[added];[0:a][added]amix=inputs=2:duration=first:normalize=0[out]", offset, offset+to-from, seconds(track.Volume), from-start)
		next := filepath.Join(dir, fmt.Sprintf("mix-%d.wav", i))
		if e = renderPCM(ctx, args, filter, next, end-start); e != nil {
			return "", e
		}
		if current != base {
			if e = os.Remove(current); e != nil {
				return "", e
			}
		}
		current = next
	}
	return current, nil
}
func renderSubtitles(t MediaTimeline, plan media.RenderPlan) ([]SubtitleCue, string, string) {
	cues := []SubtitleCue{}
	for i, c := range t.Clips {
		for _, cue := range clipSubtitleCues(c) {
			cue.Start += float64(plan.Clips[i].Start) / 24
			cue.End += float64(plan.Clips[i].Start) / 24
			cue.End = min(cue.End, float64(plan.Frames)/24)
			if cue.End > cue.Start {
				cues = append(cues, cue)
			}
		}
	}
	sort.SliceStable(cues, func(i, j int) bool { return cues[i].Start < cues[j].Start })
	srt, vtt := subtitleText(cues)
	return cues, srt, vtt
}
func subtitleText(cues []SubtitleCue) (string, string) {
	var srt, vtt strings.Builder
	vtt.WriteString("WEBVTT\n\n")
	for i, c := range cues {
		text := strings.NewReplacer("\r", " ", "\n", " ", "-->", "→").Replace(c.Text)
		fmt.Fprintf(&srt, "%d\n%s --> %s\n%s\n\n", i+1, srtTime(c.Start), srtTime(c.End), text)
		text = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(text)
		fmt.Fprintf(&vtt, "%s --> %s\n%s\n\n", strings.ReplaceAll(srtTime(c.Start), ",", "."), strings.ReplaceAll(srtTime(c.End), ",", "."), text)
	}
	return srt.String(), vtt.String()
}
func subtitleFilter(path string) string {
	escaped := strings.NewReplacer("\\", "\\\\", "'", "'\\''", ":", "\\:", ",", "\\,", "[", "\\[", "]", "\\]").Replace(path)
	return "subtitles=filename='" + escaped + "'"
}
func (m *MediaStore) renderVideoBlock(ctx context.Context, p MediaState, t MediaTimeline, plan media.RenderPlan, block media.RenderBlock, cues []SubtitleCue, w, h int, rate, dir, out string) error {
	frames := block.End - block.Start
	left := filepath.Join(dir, "left.mp4")
	if err := m.renderClipVideo(ctx, p, t.Clips[block.Left], block.Start-plan.Clips[block.Left].Start, frames, left, w, h, rate); err != nil {
		return err
	}
	base := left
	if block.Right >= 0 {
		right := filepath.Join(dir, "right.mp4")
		if err := m.renderClipVideo(ctx, p, t.Clips[block.Right], block.Start-plan.Clips[block.Right].Start, frames, right, w, h, rate); err != nil {
			return err
		}
		base = filepath.Join(dir, "transition.mp4")
		args := []string{"-i", left, "-i", right, "-filter_complex", fmt.Sprintf("[0:v]settb=AVTB,setpts=PTS-STARTPTS,fps=24[v0];[1:v]settb=AVTB,setpts=PTS-STARTPTS,fps=24[v1];[v0][v1]xfade=transition=%s:duration=%s:offset=0[v]", xfadeName(t.Clips[block.Left].Transition), frameSeconds(frames)), "-map", "[v]"}
		if err := ffmpeg(ctx, renderVideoArgs(args, base, frames, rate)...); err != nil {
			return err
		}
	}
	for i, layer := range t.VideoOverlays {
		if layer.Start >= float64(block.End)/24 || layer.Start+layer.Duration <= float64(block.Start)/24 {
			continue
		}
		next := filepath.Join(dir, fmt.Sprintf("layer-%d.mp4", i))
		if err := m.renderLayer(ctx, p, base, layer, block, w, h, rate, next); err != nil {
			return err
		}
		if base != left {
			os.Remove(base)
		}
		base = next
	}
	if t.SubtitleMode == "burn" && len(cues) > 0 {
		local := []SubtitleCue{}
		for _, cue := range cues {
			cue.Start = max(0, cue.Start-float64(block.Start)/24)
			cue.End = min(float64(frames)/24, cue.End-float64(block.Start)/24)
			if cue.End > cue.Start {
				local = append(local, cue)
			}
		}
		if len(local) > 0 {
			text, _ := subtitleText(local)
			path := filepath.Join(dir, "subtitles.srt")
			if err := os.WriteFile(path, []byte(text), 0600); err != nil {
				return err
			}
			return ffmpeg(ctx, renderVideoArgs([]string{"-i", base, "-vf", subtitleFilter(path)}, out, frames, rate)...)
		}
	}
	return copyRenderFile(base, out)
}
func (m *MediaStore) renderAudioBlock(ctx context.Context, p MediaState, t MediaTimeline, plan media.RenderPlan, block media.RenderBlock, dir, out string) error {
	frames := block.End - block.Start
	left := filepath.Join(dir, "left.wav")
	if err := m.renderClipAudio(ctx, p, t.Clips[block.Left], block.Start-plan.Clips[block.Left].Start, frames, left); err != nil {
		return err
	}
	base := left
	if block.Right >= 0 {
		right := filepath.Join(dir, "right.wav")
		if err := m.renderClipAudio(ctx, p, t.Clips[block.Right], block.Start-plan.Clips[block.Right].Start, frames, right); err != nil {
			return err
		}
		base = filepath.Join(dir, "transition.wav")
		if err := renderPCM(ctx, []string{"-i", left, "-i", right}, fmt.Sprintf("[0:a]afade=t=out:ss=0:ns=%d:curve=tri[a];[1:a]afade=t=in:ss=0:ns=%d:curve=tri[b];[a][b]amix=inputs=2:duration=first:normalize=0[out]", frames*2000, frames*2000), base, frames*2000); err != nil {
			return err
		}
	}
	mixed, err := m.mixRenderBlock(ctx, p, t, block, base, dir)
	if err != nil {
		return err
	}
	return copyRenderFile(mixed, out)
}
func (m *MediaStore) render(ctx context.Context, p MediaState, e MediaExport) error {
	return m.withRenderSession(ctx, p, e, func(s *renderSession) error {
		scratch, err := os.MkdirTemp(s.dir, ".assemble-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(scratch)
		w, h, err := outputSpec(e.Timeline)
		if err != nil {
			return err
		}
		rate, plan := renderRate(e.Timeline), s.manifest.Plan
		cues, srt, vtt := renderSubtitles(e.Timeline, plan)
		videoList, audioList := filepath.Join(scratch, "video.txt"), filepath.Join(scratch, "audio.txt")
		videoFile, err := os.Create(videoList)
		if err != nil {
			return err
		}
		defer videoFile.Close()
		audioFile, err := os.Create(audioList)
		if err != nil {
			return err
		}
		defer audioFile.Close()
		err = plan.Walk(s.manifest.SegmentFrames, func(block media.RenderBlock) error {
			frames := block.End - block.Start
			prefix := fmt.Sprintf("block-%d-%d-%d", block.Index, block.Start, block.End)
			video, err := s.artifact(prefix+"-video", "video", frames, func(out string) error {
				dir, err := os.MkdirTemp(scratch, "video-")
				if err != nil {
					return err
				}
				defer os.RemoveAll(dir)
				return m.renderVideoBlock(ctx, p, e.Timeline, plan, block, cues, w, h, rate, dir, out)
			})
			if err != nil {
				return err
			}
			audio, err := s.artifact(prefix+"-audio", "pcm", frames, func(out string) error {
				dir, err := os.MkdirTemp(scratch, "audio-")
				if err != nil {
					return err
				}
				defer os.RemoveAll(dir)
				return m.renderAudioBlock(ctx, p, e.Timeline, plan, block, dir, out)
			})
			if err != nil {
				return err
			}
			if _, err = fmt.Fprint(videoFile, renderConcatEntry(video, block.Start, block.End)); err != nil {
				return err
			}
			_, err = fmt.Fprint(audioFile, renderConcatEntry(audio, block.Start, block.End))
			return err
		})
		if err != nil {
			return err
		}
		if err = errors.Join(videoFile.Sync(), audioFile.Sync()); err != nil {
			return err
		}

		stem, err := s.artifact("global-pcm", "pcm", plan.Frames, func(out string) error {
			return ffmpeg(ctx, "-f", "concat", "-safe", "0", "-protocol_whitelist", "file,pipe", "-i", audioList, "-c:a", "copy", "-rf64", "auto", out)
		})
		if err != nil {
			return err
		}
		subtitlePath := filepath.Join(scratch, "subtitles.srt")
		if err = os.WriteFile(subtitlePath, []byte(srt), 0600); err != nil {
			return err
		}
		output, err := s.artifact("final", "final", plan.Frames, func(out string) error {
			args := []string{"-f", "concat", "-safe", "0", "-protocol_whitelist", "file,pipe", "-i", videoList, "-protocol_whitelist", "file,pipe", "-i", stem}
			soft := len(cues) > 0 && (e.Timeline.SubtitleMode == "" || e.Timeline.SubtitleMode == "soft")
			if soft {
				args = append(args, "-f", "srt", "-i", subtitlePath)
			}
			args = append(args, "-map", "0:v:0", "-map", "1:a:0")
			if soft {
				args = append(args, "-map", "2:s:0", "-c:s", "mov_text", "-metadata:s:s:0", "language=zho", "-disposition:s:0", "default")
			}
			args = append(args, "-c:v", "copy", "-af", fmt.Sprintf("alimiter=limit=0.95:latency=1,atrim=end_sample=%d", plan.Frames*2000), "-c:a", "aac", "-ar", "48000", "-ac", "2", "-t", frameSeconds(plan.Frames), "-movie_timescale", "48000", "-movflags", "+faststart", out)
			return ffmpeg(ctx, args...)
		})
		if err != nil {
			return err
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		for ext, data := range map[string]string{".srt": srt, ".vtt": vtt} {
			f, err := os.CreateTemp(m.dir, ".subtitle-")
			if err != nil {
				return err
			}
			_, err = f.WriteString(data)
			if err == nil {
				err = f.Sync()
			}
			err = errors.Join(err, f.Close())
			if err == nil {
				err = os.Rename(f.Name(), filepath.Join(m.dir, e.ID+ext))
			}
			os.Remove(f.Name())
			if err != nil {
				return err
			}
		}
		temp := filepath.Join(scratch, "publish.mp4")
		if err = copyRenderFile(output, temp); err != nil {
			return err
		}
		if err = os.Rename(temp, filepath.Join(m.dir, e.ID+".mp4")); err != nil {
			return err
		}
		return syncMultipartDir(m.dir)
	})
}
func copyRenderFile(from, to string) error {
	in, e := openLocalBlob(from)
	if e != nil {
		return e
	}
	defer in.Close()
	out, e := os.OpenFile(to, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	_, e = io.CopyBuffer(out, in, make([]byte, 64<<10))
	if e == nil {
		e = out.Sync()
	}
	return errors.Join(e, out.Close())
}

// Compensate ffconcat's microsecond clock at global boundaries instead of
// rounding each segment duration independently. Rescaling to the 1/24000 video
// timebase then recovers exact frame timestamps without cumulative drift.
func renderConcatEntry(path string, start, end int64) string {
	micros := (end/24-start/24)*1000000 + (end%24*1000000+12)/24 - (start%24*1000000+12)/24
	return fmt.Sprintf("file '%s'\nduration %d.%06d\n", strings.ReplaceAll(path, "'", "'\\''"), micros/1000000, micros%1000000)
}
