package studio

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
)

// All times after composition (extra tracks, overlays and subtitles) are on the
// shortened movie timeline. A crossfade belongs to the outgoing clip.
func timelineDuration(t MediaTimeline) float64 {
	total := 0.0
	for _, c := range t.Clips {
		total += c.Duration
		if c.Transition == "crossfade" {
			total -= c.TransitionDuration
		}
	}
	return total
}
func hasCrossfade(t MediaTimeline) bool {
	for _, c := range t.Clips {
		if c.Transition == "crossfade" {
			return true
		}
	}
	return false
}
func validateComposite(p MediaState, t MediaTimeline) error {
	if len(t.VideoOverlays) > 4 {
		return mediaInvalid("画面叠层最多4条")
	}
	ids := map[string]bool{}
	total := timelineDuration(t)
	for _, layer := range t.VideoOverlays {
		for _, n := range []float64{layer.Start, layer.TrimStart, layer.Duration, layer.X, layer.Y, layer.Width, layer.Height, layer.Opacity, layer.SourceAudioVolume} {
			if math.IsNaN(n) || math.IsInf(n, 0) {
				return mediaInvalid("叠层数值必须有限")
			}
		}
		a, ok := mediaAsset(p, layer.AssetID)
		if !ok || (a.Kind != "image" && a.Kind != "video") || !shotIDPattern.MatchString(layer.ID) || ids[layer.ID] || layer.Start < 0 || layer.TrimStart < 0 || layer.Duration < .1 || layer.Start+layer.Duration > total+.001 || layer.X < 0 || layer.Y < 0 || layer.Width < .1 || layer.Height < .1 || layer.X+layer.Width > 1 || layer.Y+layer.Height > 1 || layer.Opacity < 0 || layer.Opacity > 1 || layer.SourceAudioVolume < 0 || layer.SourceAudioVolume > 1 {
			return mediaInvalid("叠层素材、位置、时间或音量无效")
		}
		if (a.Kind == "video" && layer.TrimStart+layer.Duration > a.Duration+.05) || (a.Kind == "image" && (layer.TrimStart != 0 || layer.PreserveSourceAudio)) || (!layer.PreserveSourceAudio && layer.SourceAudioVolume != 0) {
			return mediaInvalid("叠层裁剪或原音设置无效")
		}
		ids[layer.ID] = true
	}
	return nil
}

func boundedComposition(ctx context.Context, args []string, output, duration, maxRate string) error {
	args = append(args, "-t", duration, "-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p", "-r", "24", "-maxrate", maxRate, "-bufsize", "16M", "-threads", "1", "-c:a", "aac", "-ar", "48000", "-ac", "2", "-movflags", "+faststart", "-fs", "134217729", output)
	if err := ffmpeg(ctx, args...); err != nil {
		return err
	}
	info, err := os.Stat(output)
	if err != nil || info.Size() > 128<<20 {
		return mediaInvalid("合成中间文件超过128MiB限制")
	}
	probe, probeErr := probeMedia(output)
	actual, parseErr := strconv.ParseFloat(probe.Format.Duration, 64)
	expected, _ := strconv.ParseFloat(duration, 64)
	if probeErr != nil || parseErr != nil || math.Abs(actual-expected) > .12 || len(probe.Streams) < 2 {
		return mediaInvalid("合成中间文件时长或音视频轨道无效")
	}
	return nil
}

// Pairwise merging bounds decoder/filter inputs at two even for 30 clips.
// Intermediate movie files are deleted as soon as the next merge succeeds.
func composeCrossfades(ctx context.Context, dir string, t MediaTimeline, maxRate string) (string, error) {
	current := filepath.Join(dir, "clip-000.mp4")
	duration := t.Clips[0].Duration
	for i := 1; i < len(t.Clips); i++ {
		next := filepath.Join(dir, fmt.Sprintf("clip-%03d.mp4", i))
		out := filepath.Join(dir, fmt.Sprintf("merge-%03d.mp4", i))
		args := []string{"-protocol_whitelist", "file,pipe", "-i", current, "-protocol_whitelist", "file,pipe", "-i", next}
		// Explicitly trim audio to the contract, removing AAC packet padding before
		// concatenation/crossfade; normalize video timebases for the xfade filter.
		filters := fmt.Sprintf("[0:v]settb=AVTB,setpts=PTS-STARTPTS,fps=24[v0];[1:v]settb=AVTB,setpts=PTS-STARTPTS,fps=24[v1];[0:a]atrim=duration=%s,asetpts=PTS-STARTPTS[a0];[1:a]atrim=duration=%s,asetpts=PTS-STARTPTS[a1];", seconds(duration), seconds(t.Clips[i].Duration))
		previous := t.Clips[i-1]
		if previous.Transition == "crossfade" {
			filters += fmt.Sprintf("[v0][v1]xfade=transition=fade:duration=%s:offset=%s[v];[a0][a1]acrossfade=d=%s:c1=tri:c2=tri[a]", seconds(previous.TransitionDuration), seconds(duration-previous.TransitionDuration), seconds(previous.TransitionDuration))
			duration -= previous.TransitionDuration
		} else {
			filters += "[v0][a0][v1][a1]concat=n=2:v=1:a=1[v][a]"
		}
		duration += t.Clips[i].Duration
		args = append(args, "-filter_complex", filters, "-map", "[v]", "-map", "[a]")
		if err := boundedComposition(ctx, args, out, seconds(duration), maxRate); err != nil {
			return "", err
		}
		if i > 1 {
			_ = os.Remove(current)
		}
		current = out
	}
	return current, nil
}

// Layers are applied in saved order; the last layer is on top. Each pass uses
// only two inputs and does not retain decoded frames for the entire film.
func (m *MediaStore) composeOverlays(ctx context.Context, dir string, p MediaState, t MediaTimeline, baseArgs []string, width, height int, maxRate string) (string, error) {
	current := ""
	for i, layer := range t.VideoOverlays {
		args := append([]string(nil), baseArgs...)
		if i > 0 {
			args = []string{"-protocol_whitelist", "file,pipe", "-i", current}
		}
		asset, ok := mediaAsset(p, layer.AssetID)
		if !ok {
			return "", ErrNotFound
		}
		source := filepath.Join(m.dir, asset.ID)
		if asset.Kind == "image" {
			args = append(args, "-loop", "1", "-framerate", "24")
		} else {
			args = append(args, "-ss", seconds(layer.TrimStart))
		}
		args = append(args, "-t", seconds(layer.Duration), "-protocol_whitelist", "file,pipe", "-i", source)
		w, h := int(float64(width)*layer.Width)/2*2, int(float64(height)*layer.Height)/2*2
		x, y := int(float64(width)*layer.X)/2*2, int(float64(height)*layer.Y)/2*2
		filters := fmt.Sprintf("[1:v]setpts=PTS-STARTPTS+%s/TB,scale=%d:%d:force_original_aspect_ratio=decrease,format=rgba,pad=%d:%d:(ow-iw)/2:(oh-ih)/2:color=black@0,colorchannelmixer=aa=%s[over];[0:v][over]overlay=x=%d:y=%d:eof_action=pass:repeatlast=0:enable='gte(t,%s)*lt(t,%s)'[v]", seconds(layer.Start), w, h, w, h, seconds(layer.Opacity), x, y, seconds(layer.Start), seconds(layer.Start+layer.Duration))
		audio := "0:a:0"
		if layer.PreserveSourceAudio && asset.Kind == "video" {
			probe, err := probeMedia(source)
			if err != nil {
				return "", err
			}
			for _, stream := range probe.Streams {
				if stream.CodecType == "audio" {
					filters += fmt.Sprintf(";[1:a]atrim=duration=%s,asetpts=PTS-STARTPTS,volume=%s,adelay=%d:all=1[oa];[0:a][oa]amix=inputs=2:duration=first:normalize=0,alimiter=limit=0.95:latency=1[a]", seconds(layer.Duration), seconds(layer.SourceAudioVolume), int(layer.Start*1000+.5))
					audio = "[a]"
					break
				}
			}
		}
		out := filepath.Join(dir, fmt.Sprintf("overlay-%03d.mp4", i))
		args = append(args, "-filter_complex", filters, "-map", "[v]", "-map", audio)
		if err := boundedComposition(ctx, args, out, seconds(timelineDuration(t)), maxRate); err != nil {
			return "", err
		}
		if current != "" {
			_ = os.Remove(current)
		}
		current = out
	}
	return current, nil
}
