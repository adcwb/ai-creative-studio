package application

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"time"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/media"
)

type previewShotInput struct {
	ShotID       string  `json:"shot_id"`
	AssetID      string  `json:"asset_id"`
	AudioAssetID string  `json:"audio_asset_id,omitempty"`
	Duration     float64 `json:"duration"`
}
type PreviewInput struct {
	ExpectedProjectVersion int                `json:"expected_project_version"`
	ExpectedMediaVersion   int                `json:"expected_media_version"`
	EpisodeNumber          int                `json:"episode_number"`
	AspectRatio            string             `json:"aspect_ratio"`
	SoundMode              string             `json:"sound_mode"`
	Shots                  []previewShotInput `json:"shots"`
}

func previewSourceDigest(v *media.AnimaticSource, t MediaTimeline) string {
	copy := *v
	copy.SHA256 = ""
	return mediaDigest([]any{copy, t})
}
func validatePreviewExport(e MediaExport) error {
	if e.Purpose == "" && e.PreviewSource == nil {
		return nil
	}
	v := e.PreviewSource
	if e.Purpose != "animatic" || v == nil || v.SHA256 != previewSourceDigest(v, e.Timeline) || len(v.Shots) != len(e.Timeline.Clips) || len(v.Shots) < 1 || len(v.Assets) < 1 {
		return errRenderInputsChanged
	}
	return nil
}
func previewAssetMatches(v *media.AnimaticSource, id string, info BlobInfo) bool {
	for _, a := range v.Assets {
		if a.ID == id {
			return a.SHA256 == info.SHA256 && a.Size == info.Size
		}
	}
	return false
}

// A preview writes only the media export/receipt/queue boundary. Holding the
// existing core lock protects project CAS until that media transaction commits.
func (s *FileStore) CreateMediaPreview(ctx context.Context, space, project, key string, in PreviewInput, trace string, authorize func() bool) (MediaExport, error) {
	if in.ExpectedProjectVersion < 1 || in.ExpectedMediaVersion < 1 {
		return MediaExport{}, mediaInvalid("项目与素材来源版本需为正整数")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p, e := s.projectLocked(space, project)
	if e != nil {
		return MediaExport{}, e
	}
	m := s.media
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return MediaExport{}, errors.New("media store closed")
	}
	d := m.clone()
	state := mediaState(d, mediaKey(space, project))
	digest := mediaDigest([]any{"animatic", in})
	replay, e := m.check(d, mediaKey(space, project), key, digest)
	if e != nil {
		return MediaExport{}, e
	}
	if replay {
		for _, v := range state.Exports {
			if v.ID == d.Receipts[mediaKey(space, project)+":"+key].ExportID && v.Purpose == "animatic" {
				return v, nil
			}
		}
		return MediaExport{}, errors.New("preview receipt missing")
	}
	if in.ExpectedProjectVersion != p.Version {
		return MediaExport{}, ErrVersionConflict
	}
	if in.ExpectedMediaVersion != state.Version {
		return MediaExport{}, errMediaVersion
	}
	if in.EpisodeNumber < 1 || in.EpisodeNumber > 9007199254740991 || p.Storyboard == nil || len(in.Shots) == 0 || (in.SoundMode != "silent" && in.SoundMode != "temporary") || (in.AspectRatio != "16:9" && in.AspectRatio != "9:16" && in.AspectRatio != "1:1") {
		return MediaExport{}, mediaInvalid("请选择已保存的镜头、画幅、集数和明确的临时声音方式")
	}
	if e = m.requireLocalEpisode(space, project, in.EpisodeNumber); e != nil {
		return MediaExport{}, e
	}
	source := &media.AnimaticSource{CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), ProjectVersion: p.Version, ScriptVersion: p.ScriptVersion, StoryboardScriptVersion: p.Storyboard.SourceScriptVersion, StoryboardSHA256: mediaDigest(p.Storyboard), MediaVersion: state.Version, EpisodeNumber: in.EpisodeNumber, SoundMode: in.SoundMode, Shots: []media.AnimaticShot{}, Assets: []media.AnimaticAsset{}}
	timeline := MediaTimeline{EpisodeNumber: in.EpisodeNumber, AspectRatio: in.AspectRatio, OutputResolution: "360p", SubtitleMode: "burn", Clips: []MediaClip{}}
	episodes := s.previewShotEpisodesLocked(p)
	hasAudio := false
	for clipIndex, choice := range in.Shots {
		var shot *StoryboardShot
		index := 0
		for i, v := range p.Storyboard.Shots {
			if v.ID == choice.ShotID {
				copy := v
				shot = &copy
				index = i
				break
			}
		}
		asset, ok := mediaAsset(state, choice.AssetID)
		if shot == nil || !ok || asset.Kind != "image" || asset.Role == "mask" || (asset.MIMEType != "image/png" && asset.MIMEType != "image/jpeg") || math.IsNaN(choice.Duration) || math.IsInf(choice.Duration, 0) || choice.Duration < .5 {
			return MediaExport{}, mediaInvalid("预演需要有效的镜头、项目图片和至少0.5秒的镜头时长")
		}
		// Unknown independent-shot scope is explicitly assigned by this request;
		// known script-backed shots cannot be relabelled as another episode.
		if ep := episodes[shot.ID]; ep < 0 || (ep > 0 && ep != in.EpisodeNumber) {
			return MediaExport{}, mediaInvalid("所选镜头属于其他集，请核对本次预演范围")
		}
		if choice.AudioAssetID != "" {
			a, ok := mediaAsset(state, choice.AudioAssetID)
			if !ok || a.Kind != "audio" || in.SoundMode != "temporary" {
				return MediaExport{}, mediaInvalid("临时声轨必须是本项目音频，静音预演不能附带声轨")
			}
			hasAudio = true
		}
		label := "ANIMATIC - SILENT PREVIEW"
		if in.SoundMode == "temporary" {
			label = "ANIMATIC - TEMP AUDIO"
		}
		timeline.Clips = append(timeline.Clips, MediaClip{ID: mediaDigest([]any{choice.ShotID, clipIndex})[:32], ShotID: choice.ShotID, AssetID: choice.AssetID, AudioAssetID: choice.AudioAssetID, AudioVolume: 1, Duration: choice.Duration, Subtitle: label})
		source.Shots = append(source.Shots, media.AnimaticShot{Shot: *shot, OriginalIndex: index, Duration: choice.Duration, AssetID: choice.AssetID, AudioAssetID: choice.AudioAssetID})
	}
	if in.SoundMode == "temporary" && !hasAudio {
		return MediaExport{}, mediaInvalid("请选择至少一份临时声轨，或明确改为静音预演")
	}
	if e = validateTimeline(state, timeline); e != nil {
		return MediaExport{}, e
	}
	if _, e = media.CompileRenderPlan(timeline); e != nil {
		return MediaExport{}, mediaInvalid("预演时长超出编码器可表示范围")
	}
	for _, id := range renderInputIDs(timeline) {
		path, e := m.localBlob(space, project, id)
		if e != nil {
			return MediaExport{}, e
		}
		info, e := localBlobInfo(ctx, blobKey(space, project, id), path)
		if e != nil {
			return MediaExport{}, e
		}
		if m.blobs != nil {
			expected, e := m.blobs.info(ctx, info.Key)
			if e != nil {
				return MediaExport{}, e
			}
			if expected != info {
				return MediaExport{}, ErrBlobIntegrity
			}
		}
		asset, _ := mediaAsset(state, id)
		source.Assets = append(source.Assets, media.AnimaticAsset{Name: asset.Name, Kind: asset.Kind, ID: id, SHA256: info.SHA256, Size: info.Size})
	}
	source.SHA256 = previewSourceDigest(source, timeline)
	return m.enqueueExportLocked(d, state, project, space, key, digest, in.EpisodeNumber, timeline, "animatic", source, trace, authorize)
}

func cloneMediaExport(in MediaExport) MediaExport {
	raw, _ := json.Marshal(in)
	var out MediaExport
	_ = json.Unmarshal(raw, &out)
	return out
}

// Resolve legacy multi-episode scope against the storyboard's exact script
// revision. A current reordered script cannot relabel historical shot indices.
func (s *FileStore) previewShotEpisodesLocked(p Project) map[string]int {
	out := map[string]int{}
	if p.Storyboard == nil {
		return out
	}
	if p.SeriesEpisodeID != "" {
		for _, shot := range p.Storyboard.Shots {
			out[shot.ID] = 1
		}
		return out
	}
	script := p.Script
	if p.Storyboard.SourceScriptVersion != p.ScriptVersion {
		script = nil
		digest := ""
		ambiguous := false
		for _, r := range s.data.History[p.ID] {
			if r.Kind == "script" && r.ScriptVersion == p.Storyboard.SourceScriptVersion && r.Script != nil {
				sha := mediaDigest(r.Script)
				if digest != "" && sha != digest {
					ambiguous = true
				}
				digest = sha
				script = r.Script
			}
		}
		if ambiguous {
			script = nil
		}
	}
	for _, shot := range p.Storyboard.Shots {
		if shot.SceneIndex == 0 {
			out[shot.ID] = 0
			continue
		}
		out[shot.ID] = -1
		if script != nil && shot.SceneIndex <= len(script.Scenes) {
			n := script.Scenes[shot.SceneIndex-1].EpisodeNumber
			if n < 1 {
				n = 1
			}
			out[shot.ID] = n
		}
	}
	return out
}

type previewProjectView struct {
	ID              string      `json:"id"`
	Version         int         `json:"version"`
	ScriptVersion   int         `json:"script_version"`
	SeriesEpisodeID string      `json:"series_episode_id,omitempty"`
	Storyboard      *Storyboard `json:"storyboard,omitempty"`
}
type previewMediaView struct {
	Version     int               `json:"version"`
	Assets      []MediaAsset      `json:"assets"`
	Selections  []media.Selection `json:"selections"`
	AspectRatio string            `json:"aspect_ratio"`
}
type PreviewOptions struct {
	Project      previewProjectView `json:"project"`
	Media        previewMediaView   `json:"media"`
	ShotEpisodes map[string]int     `json:"shot_episodes"`
}

func (s *FileStore) MediaPreviewOptions(space, project string) (PreviewOptions, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, e := s.projectLocked(space, project)
	if e != nil {
		return PreviewOptions{}, e
	}
	m := s.media
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failure != nil {
		return PreviewOptions{}, m.failure
	}
	if m.pg != nil && m.pg.err != nil {
		return PreviewOptions{}, m.pg.err
	}
	state := mediaState(m.data, mediaKey(space, project))
	if _, ok := m.data.Projects[mediaKey(space, project)]; !ok {
		if e = m.initializeSeriesProfile(mediaKey(space, project), &state); e != nil {
			return PreviewOptions{}, e
		}
	}
	// Copy only source options, never all project exports or script history.
	assets := append([]MediaAsset{}, state.Assets...)
	selections := append([]media.Selection{}, state.Selections...)
	view := PreviewOptions{Project: previewProjectView{ID: p.ID, Version: p.Version, ScriptVersion: p.ScriptVersion, SeriesEpisodeID: p.SeriesEpisodeID, Storyboard: p.Storyboard}, Media: previewMediaView{Version: state.Version, Assets: assets, Selections: selections, AspectRatio: state.Timeline.AspectRatio}, ShotEpisodes: s.previewShotEpisodesLocked(p)}
	raw, e := json.Marshal(view)
	if e != nil {
		return PreviewOptions{}, e
	}
	var copy PreviewOptions
	e = json.Unmarshal(raw, &copy)
	return copy, e
}
