package media

import (
	"fmt"
	"math"
)

type MediaTimeline struct {
	VideoOverlays    []MediaVideoOverlay `json:"video_overlays,omitempty"`
	OutputResolution string              `json:"output_resolution,omitempty"`
	AspectRatio      string              `json:"aspect_ratio,omitempty"`
	SubtitleMode     string              `json:"subtitle_mode,omitempty"`
	AudioTracks      []MediaAudioTrack   `json:"audio_tracks,omitempty"`
	Clips            []MediaClip         `json:"clips"`
	MusicAssetID     string              `json:"music_asset_id"`
	MusicVolume      float64             `json:"music_volume"`
}

func Asset(p MediaState, id string) (MediaAsset, bool) {
	for _, a := range p.Assets {
		if a.ID == id {
			return a, true
		}
	}
	return MediaAsset{}, false
}

func ValidateTimeline(p MediaState, t MediaTimeline) error {
	if _, _, _, err := OutputSpec(t); err != nil {
		return err
	}
	if t.SubtitleMode != "" && t.SubtitleMode != "soft" && t.SubtitleMode != "burn" && t.SubtitleMode != "none" {
		return Invalid("字幕模式无效")
	}
	if len(t.AudioTracks) > 8 {
		return Invalid("附加音轨最多8条")
	}
	if len(t.Clips) < 1 || len(t.Clips) > 30 || t.MusicVolume < 0 || t.MusicVolume > 1 {
		return Invalid("时间轴需包含 1–30 镜，音量须为 0–1")
	}
	duration := 0.0
	ids := map[string]bool{}
	for i, c := range t.Clips {
		for _, v := range []float64{c.Duration, c.TrimStart, c.AudioVolume, c.SourceAudioVolume, c.TransitionDuration, c.CropX, c.CropY, c.CropWidth, c.CropHeight, t.MusicVolume} {
			if math.IsNaN(v) || math.IsInf(v, 0) {
				return Invalid("时间轴数值必须有限")
			}
		}
		if c.SourceAudioVolume < 0 || c.SourceAudioVolume > 1 {
			return Invalid("原音音量须为0–1")
		}
		if c.Transition != "" && c.Transition != "fade" && !OverlappingTransition(c.Transition) {
			return Invalid("转场类型无效")
		}
		if c.Transition == "fade" && (c.TransitionDuration < .1 || c.TransitionDuration > 2 || c.TransitionDuration > c.Duration/2) {
			return Invalid("淡入淡出时长须为0.1–2秒且不超过镜头一半")
		}
		if OverlappingTransition(c.Transition) && (i+1 == len(t.Clips) || c.TransitionDuration < .1 || c.TransitionDuration > 2 || c.TransitionDuration > c.Duration/2 || c.TransitionDuration > t.Clips[i+1].Duration/2) {
			return Invalid("交叉溶解需下一镜，时长0.1–2秒且不超过相邻镜头各自一半")
		}
		if c.Transition == "" && c.TransitionDuration != 0 {
			return Invalid("未选择转场")
		}
		a, ok := Asset(p, c.AssetID)
		if !ok || (a.Kind != "image" && a.Kind != "video") || !shotIDPattern.MatchString(c.ID) || ids[c.ID] || c.Duration < 0.5 || c.Duration > 30 || c.TrimStart < 0 || len([]rune(c.Subtitle)) > 300 || c.AudioVolume < 0 || c.AudioVolume > 1 {
			return Invalid("镜头素材、时长、字幕或音量无效")
		}
		if err := ValidateSubtitleCues(c.SubtitleCues, c.Duration); err != nil {
			return err
		}
		ids[c.ID] = true
		duration += c.Duration
		if a.Kind == "video" && c.TrimStart+c.Duration > a.Duration+0.05 {
			return Invalid("镜头超过视频素材长度")
		}
		if c.AudioAssetID != "" {
			audio, ok := Asset(p, c.AudioAssetID)
			if !ok || audio.Kind != "audio" {
				return Invalid("配音素材无效")
			}
		}
		if c.CropWidth != 0 || c.CropHeight != 0 {
			if c.CropWidth < 0.1 || c.CropHeight < 0.1 || c.CropX < 0 || c.CropY < 0 || c.CropX+c.CropWidth > 1 || c.CropY+c.CropHeight > 1 {
				return Invalid("裁剪区域无效")
			}
		}
	}
	if err := ValidateComposite(p, t); err != nil {
		return err
	}
	duration = TimelineDuration(t)
	_, _, maxDuration, _ := OutputSpec(t)
	if duration > maxDuration {
		return Invalid(fmt.Sprintf("所选清晰度成片最长 %.0f 秒", maxDuration))
	}
	trackIDs := map[string]bool{}
	for _, track := range t.AudioTracks {
		for _, v := range []float64{track.Start, track.TrimStart, track.Duration, track.Volume} {
			if math.IsNaN(v) || math.IsInf(v, 0) {
				return Invalid("音轨数值必须有限")
			}
		}
		a, ok := Asset(p, track.AssetID)
		if !ok || a.Kind != "audio" || !shotIDPattern.MatchString(track.ID) || trackIDs[track.ID] || track.Start < 0 || track.TrimStart < 0 || track.Duration < .1 || track.Start+track.Duration > duration+.001 || track.TrimStart+track.Duration > a.Duration+.05 || track.Volume < 0 || track.Volume > 1 {
			return Invalid("附加音轨素材、时间或音量无效")
		}
		trackIDs[track.ID] = true
	}
	if t.MusicAssetID != "" {
		a, ok := Asset(p, t.MusicAssetID)
		if !ok || a.Kind != "audio" {
			return Invalid("音乐素材无效")
		}
	}
	return nil
}

// Output presets bound both dimensions and encoder work. Missing fields preserve legacy exports.
func OutputSpec(t MediaTimeline) (int, int, float64, error) {
	w, h, limit := 640, 360, 180.0
	switch t.OutputResolution {
	case "", "360p":
	case "720p":
		w, h, limit = 1280, 720, 120
	case "1080p":
		w, h, limit = 1920, 1080, 60
	case "2160p":
		w, h, limit = 3840, 2160, 15
	default:
		return 0, 0, 0, Invalid("不支持的输出清晰度")
	}
	switch t.AspectRatio {
	case "", "16:9":
	case "9:16":
		w, h = h, w
	case "1:1":
		w = h
	default:
		return 0, 0, 0, Invalid("不支持的画面比例")
	}
	return w, h, limit, nil
}

// All times after composition (extra tracks, overlays and subtitles) are on the
// shortened movie timeline. An overlapping transition belongs to the outgoing clip.
func TimelineDuration(t MediaTimeline) float64 {
	total := 0.0
	for _, c := range t.Clips {
		total += c.Duration
		if OverlappingTransition(c.Transition) {
			total -= c.TransitionDuration
		}
	}
	return total
}

func HasCrossfade(t MediaTimeline) bool {
	for _, c := range t.Clips {
		if OverlappingTransition(c.Transition) {
			return true
		}
	}
	return false
}

func ValidateComposite(p MediaState, t MediaTimeline) error {
	if len(t.VideoOverlays) > 4 {
		return Invalid("画面叠层最多4条")
	}
	ids := map[string]bool{}
	total := TimelineDuration(t)
	for _, layer := range t.VideoOverlays {
		for _, n := range []float64{layer.Start, layer.TrimStart, layer.Duration, layer.X, layer.Y, layer.Width, layer.Height, layer.Opacity, layer.SourceAudioVolume} {
			if math.IsNaN(n) || math.IsInf(n, 0) {
				return Invalid("叠层数值必须有限")
			}
		}
		a, ok := Asset(p, layer.AssetID)
		if !ok || (a.Kind != "image" && a.Kind != "video") || !shotIDPattern.MatchString(layer.ID) || ids[layer.ID] || layer.Start < 0 || layer.TrimStart < 0 || layer.Duration < .1 || layer.Start+layer.Duration > total+.001 || layer.X < 0 || layer.Y < 0 || layer.Width < .1 || layer.Height < .1 || layer.X+layer.Width > 1 || layer.Y+layer.Height > 1 || layer.Opacity < 0 || layer.Opacity > 1 || layer.SourceAudioVolume < 0 || layer.SourceAudioVolume > 1 {
			return Invalid("叠层素材、位置、时间或音量无效")
		}
		if (a.Kind == "video" && layer.TrimStart+layer.Duration > a.Duration+.05) || (a.Kind == "image" && (layer.TrimStart != 0 || layer.PreserveSourceAudio)) || (!layer.PreserveSourceAudio && layer.SourceAudioVolume != 0) {
			return Invalid("叠层裁剪或原音设置无效")
		}
		if err := ValidateKeyframes(layer); err != nil {
			return err
		}
		ids[layer.ID] = true
	}
	return nil
}
