package application

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
)

// Mix on the global timeline using at most two decoder inputs at a time. Float
// PCM intermediate stems avoid repeated AAC priming at track/clip boundaries;
// the final movie applies its limiter and encodes audio once.
func (m *MediaStore) mixTimelineAudio(ctx context.Context, dir string, t MediaTimeline, base []string, duration float64) (string, error) {
	current := filepath.Join(dir, "audio-base.wav")
	args := append([]string{}, base...)
	args = append(args, "-map", "0:a:0", "-vn", "-af", "apad", "-t", seconds(duration), "-c:a", "pcm_f32le", "-ar", "48000", "-ac", "2", "-rf64", "auto", current)
	if err := ffmpeg(ctx, args...); err != nil {
		return "", err
	}
	tracks := append([]MediaAudioTrack{}, t.AudioTracks...)
	if t.MusicAssetID != "" {
		tracks = append([]MediaAudioTrack{{AssetID: t.MusicAssetID, Duration: duration, Volume: t.MusicVolume}}, tracks...)
	}
	for i, track := range tracks {
		args = []string{"-protocol_whitelist", "file,pipe", "-i", current}
		if t.MusicAssetID != "" && i == 0 {
			args = append(args, "-stream_loop", "-1")
		} else {
			args = append(args, "-ss", seconds(track.TrimStart), "-t", seconds(track.Duration))
		}
		args = append(args, "-protocol_whitelist", "file,pipe", "-i", filepath.Join(m.dir, track.AssetID))
		filters := fmt.Sprintf("[1:a]asetpts=PTS-STARTPTS,volume=%s,adelay=%dS:all=1[added];[0:a][added]amix=inputs=2:duration=first:normalize=0[out]", seconds(track.Volume), int64(math.Round(track.Start*48000)))
		next := filepath.Join(dir, fmt.Sprintf("audio-mix-%d.wav", i))
		args = append(args, "-filter_complex", filters, "-map", "[out]", "-vn", "-t", seconds(duration), "-c:a", "pcm_f32le", "-ar", "48000", "-ac", "2", "-rf64", "auto", next)
		if err := ffmpeg(ctx, args...); err != nil {
			return "", err
		}
		if err := os.Remove(current); err != nil {
			return "", err
		}
		current = next
	}
	return current, nil
}
