// Package seriesmigration compiles explicit, immutable episode-copy plans.
// It performs no I/O, model calls or mutations of the source project.
package seriesmigration

import (
	"encoding/json"
	"errors"
	"sort"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/media"
	"github.com/adcwb/ai-creative-studio/backend/internal/modules/projects"
	"github.com/adcwb/ai-creative-studio/backend/internal/modules/series"
)

var ErrInput = errors.New("invalid episode migration")
var ErrConflict = errors.New("migration source or target changed")
var ErrNotFound = errors.New("migration not found")
var ErrDenied = errors.New("migration denied")

type Input struct {
	Episode              int               `json:"source_episode_number"`
	SeasonID             string            `json:"target_season_id"`
	Ordinal              int64             `json:"target_ordinal"`
	ProjectVersion       int               `json:"expected_project_version"`
	MediaVersion         int               `json:"expected_media_version"`
	SeasonVersion        int64             `json:"expected_season_version"`
	OutputMode           string            `json:"output_mode"`
	BoardPolicy          string            `json:"storyboard_policy"`
	ShotAssignments      map[string]int    `json:"shot_assignments"`
	CharacterAssignments map[string]string `json:"character_assignments"`
}
type Source struct {
	Project     projects.Project
	Media       media.MediaState
	BoardSource *projects.Script
}
type SourceRef struct {
	ProjectID      string `json:"project_id"`
	Episode        int    `json:"episode_number"`
	ProjectVersion int    `json:"project_version"`
	ScriptVersion  int    `json:"script_version"`
	MediaVersion   int    `json:"media_version"`
	SHA256         string `json:"sha256"`
}
type AssetCopy struct {
	Source      media.MediaAsset `json:"source"`
	CharacterID string           `json:"character_id,omitempty"`
	Character   string           `json:"character"`
	ShotID      string           `json:"shot_id"`
}
type Issue struct {
	Code    string `json:"code"`
	Subject string `json:"subject,omitempty"`
	Message string `json:"message"`
}
type SubtitleOrigin struct {
	ClipID      string `json:"clip_id"`
	Source      string `json:"source"`
	AlignmentID string `json:"alignment_id,omitempty"`
}
type Plan struct {
	SubtitleOrigins []SubtitleOrigin            `json:"subtitle_origins"`
	ScriptProvider  string                      `json:"script_provider,omitempty"`
	Format          int                         `json:"format"`
	Input           Input                       `json:"input"`
	Source          SourceRef                   `json:"source"`
	Title           string                      `json:"title"`
	Brief           string                      `json:"brief"`
	Profile         series.Profile              `json:"profile"`
	Script          *projects.Script            `json:"script,omitempty"`
	Storyboard      *projects.Storyboard        `json:"storyboard,omitempty"`
	Mappings        []projects.StructureMapping `json:"source_mappings"`
	Assets          []AssetCopy                 `json:"assets"`
	Timeline        media.MediaTimeline         `json:"timeline"`
	Selections      []media.Selection           `json:"selections"`
	Reviews         []media.ReviewNote          `json:"reviews"`
	Bindings        []media.LibraryBinding      `json:"library_bindings"`
	KeptExports     []string                    `json:"retained_source_exports"`
	Issues          []Issue                     `json:"issues"`
	Warnings        []Issue                     `json:"warnings"`
	Digest          string                      `json:"digest"`
}

// Fingerprint intentionally excludes mutable execution progress and secrets.
// Actual media bytes are sealed by per-asset copy proofs before publication.
func Fingerprint(src Source) string {
	p := src.Project
	return series.Digest(struct {
		Space, ID, Title, Brief string
		Version, ScriptVersion  int
		Script                  *projects.Script
		Storyboard              *projects.Storyboard
		Mappings                []projects.StructureMapping
		Media                   any
	}{p.SpaceID, p.ID, p.Title, p.Brief, p.Version, p.ScriptVersion, p.Script, p.Storyboard, p.StructureMappings,
		struct {
			Version    int
			Assets     []media.MediaAsset
			Timeline   media.MediaTimeline
			Episodes   map[int]media.MediaTimeline
			Selections []media.Selection
			Reviews    []media.ReviewNote
			Bindings   []media.LibraryBinding
		}{src.Media.Version, src.Media.Assets, src.Media.Timeline, src.Media.EpisodeTimelines, src.Media.Selections, src.Media.Reviews, src.Media.LibraryBindings}})
}
func Episodes(src Source) []int {
	seen := map[int]bool{}
	if src.Project.Script != nil {
		for _, s := range src.Project.Script.Scenes {
			seen[episode(s.EpisodeNumber)] = true
		}
	}
	if len(src.Media.Timeline.Clips) > 0 {
		seen[1] = true
	}
	for ep, t := range src.Media.EpisodeTimelines {
		if ep > 1 && len(t.Clips) > 0 {
			seen[ep] = true
		}
	}
	if len(seen) == 0 && src.Project.Storyboard != nil {
		seen[1] = true
	}
	out := []int{}
	for ep := range seen {
		out = append(out, ep)
	}
	sort.Ints(out)
	return out
}
func episode(ep int) int {
	if ep == 0 {
		return 1
	}
	return ep
}
func Clone[T any](v T) (T, error) {
	b, err := json.Marshal(v)
	var out T
	if err != nil {
		return out, err
	}
	err = json.Unmarshal(b, &out)
	return out, err
}
func Seal(p Plan) string { p.Digest = ""; return series.Digest(p) }

// Preview keeps source IDs in the content. The executor assigns target-owned
// asset IDs and rewrites every media reference using its persisted copy map.
func Preview(src Source, season series.Node, in Input) (Plan, error) {
	if src.Project.SeriesEpisodeID != "" || src.Project.Kind != "" && src.Project.Kind != "drama" || in.Episode < 1 || int64(in.Episode) > series.MaxOrdinal || !series.ID.MatchString(in.SeasonID) || season.ID != in.SeasonID || season.Kind != "season" || season.Space != src.Project.SpaceID || season.State != "ready" || season.Status != "active" || !season.Profile.Valid() || in.Ordinal < 1 || in.Ordinal >= series.MaxOrdinal {
		return Plan{}, ErrInput
	}
	if src.Project.Version != in.ProjectVersion || src.Media.Version != in.MediaVersion || season.Version != in.SeasonVersion {
		return Plan{}, ErrConflict
	}
	if in.OutputMode != "series" && in.OutputMode != "preserve" || in.BoardPolicy != "include" && in.BoardPolicy != "omit" {
		return Plan{}, ErrInput
	}
	found := false
	for _, ep := range Episodes(src) {
		if ep == in.Episode {
			found = true
		}
	}
	if !found {
		return Plan{}, ErrInput
	}
	var err error
	src, err = Clone(src)
	if err != nil {
		return Plan{}, ErrInput
	}
	in, err = Clone(in)
	if err != nil {
		return Plan{}, ErrInput
	}
	if in.ShotAssignments == nil {
		in.ShotAssignments = map[string]int{}
	}
	if in.CharacterAssignments == nil {
		in.CharacterAssignments = map[string]string{}
	}
	p := Plan{SubtitleOrigins: []SubtitleOrigin{}, ScriptProvider: src.Project.ScriptProvider, Format: 1, Input: in, Source: SourceRef{src.Project.ID, in.Episode, src.Project.Version, src.Project.ScriptVersion, src.Media.Version, Fingerprint(src)}, Title: src.Project.Title, Brief: src.Project.Brief, Profile: season.Profile, Assets: []AssetCopy{}, Mappings: []projects.StructureMapping{}, Selections: []media.Selection{}, Reviews: []media.ReviewNote{}, Bindings: []media.LibraryBinding{}, KeptExports: []string{}, Issues: []Issue{}, Warnings: []Issue{}}
	issue := func(code, subject, message string) { p.Issues = append(p.Issues, Issue{code, subject, message}) }
	var stable *projects.Script
	charNames := map[string]string{}
	aliases := map[string]map[string]bool{}
	addAlias := func(name, id string) {
		if aliases[name] == nil {
			aliases[name] = map[string]bool{}
		}
		aliases[name][id] = true
	}
	currentScenes := map[string]int{}
	localScenes := map[string]int{}
	if src.Project.Script != nil {
		v, m, err := projects.CompileStructure(*src.Project.Script, src.Project.SpaceID+":"+src.Project.ID, src.Project.ScriptVersion)
		if err != nil {
			return Plan{}, err
		}
		stable = &v
		p.Mappings = append(p.Mappings, m)
		for _, c := range v.Characters {
			charNames[c.ID] = c.Name
			addAlias(c.Name, c.ID)
		}
		for _, m := range src.Project.StructureMappings {
			for _, c := range m.Characters {
				if charNames[c.ID] != "" {
					addAlias(c.Name, c.ID)
				}
			}
		}
		selected := projects.CloneScript(v)
		selected.Scenes = []projects.ScriptScene{}
		for _, scene := range v.Scenes {
			currentScenes[scene.ID] = episode(scene.EpisodeNumber)
			if episode(scene.EpisodeNumber) == in.Episode {
				scene.EpisodeNumber = 1
				selected.Scenes = append(selected.Scenes, scene)
				localScenes[scene.ID] = len(selected.Scenes)
			}
		}
		if len(selected.Scenes) > 0 {
			p.Script = &selected
		}
	}
	selectedShots := map[string]bool{}
	allShots := map[string]bool{}
	if b := src.Project.Storyboard; b != nil {
		for _, shot := range b.Shots {
			allShots[shot.ID] = true
		}
		if in.BoardPolicy == "include" {
			if b.StructureVersion != 0 && b.StructureVersion != 2 {
				return Plan{}, ErrInput
			}
			normalized, err := projects.NormalizeStoryboardShots(b.Shots)
			if err != nil {
				return Plan{}, err
			}
			b.Shots = normalized
			ids := b.SourceSceneIDs
			if b.StructureVersion != 2 && b.SourceScriptVersion > 0 {
				var v *projects.Script
				if b.SourceScriptVersion == src.Project.ScriptVersion {
					v = stable
				} else if src.BoardSource != nil {
					x, m, err := projects.CompileStructure(*src.BoardSource, src.Project.SpaceID+":"+src.Project.ID, b.SourceScriptVersion)
					if err != nil {
						return Plan{}, err
					}
					v = &x
					p.Mappings = append(p.Mappings, m)
				}
				if v == nil {
					issue("missing_storyboard_source", "", "找不到旧分镜来源，请核对历史或选择将旧分镜留在原项目")
				} else {
					ids = projects.SceneIDs(*v)
				}
			}
			if len(ids) > 0 || b.SourceScriptVersion == 0 {
				bound, e := projects.BindStoryboard(b.Shots, ids)
				if e != nil {
					return Plan{}, e
				}
				b.Shots = bound
			}
			shots := []projects.StoryboardShot{}
			for _, shot := range b.Shots {
				if shot.SceneIndex > len(ids) {
					issue("invalid_scene_mapping", shot.ID, "旧分镜场次引用越界，请核对来源或将分镜留在原项目")
					continue
				}
				var sceneID string
				if shot.SceneIndex > 0 && shot.SceneIndex <= len(ids) {
					sceneID = ids[shot.SceneIndex-1]
				}
				if shot.SceneID != "" && shot.SceneID != sceneID {
					issue("invalid_scene_mapping", shot.ID, "旧分镜场次标识与来源索引不一致")
					continue
				}
				ep := currentScenes[sceneID]
				if ep == 0 {
					choice, ok := in.ShotAssignments[shot.ID]
					if !ok {
						issue("shot_assignment_required", shot.ID, "请为独立镜头或无法对应当前稿的旧镜头选择所属集，或留在原项目")
						continue
					}
					if choice != 0 && choice != in.Episode {
						return Plan{}, ErrInput
					}
					ep = choice
					sceneID = ""
				} else if _, ok := in.ShotAssignments[shot.ID]; ok {
					return Plan{}, ErrInput
				}
				if ep != in.Episode {
					continue
				}
				shot.SceneID, shot.SceneIndex = sceneID, localScenes[sceneID]
				shots = append(shots, shot)
				selectedShots[shot.ID] = true
			}
			for id := range in.ShotAssignments {
				if !allShots[id] {
					return Plan{}, ErrInput
				}
			}
			if len(shots) > 0 {
				version := 0
				sourceIDs := []string{}
				if p.Script != nil {
					version = 1
					sourceIDs = projects.SceneIDs(*p.Script)
				}
				p.Storyboard = &projects.Storyboard{StructureVersion: 2, SourceSceneIDs: sourceIDs, SourceScriptVersion: version, Provider: b.Provider, Shots: shots}
			}
		} else {
			p.Warnings = append(p.Warnings, Issue{"storyboard_retained", "", "旧分镜留在原项目；目标剪辑保留画面，但解除旧镜头关联"})
		}
	}
	p.Timeline = src.Media.Timeline
	if in.Episode > 1 {
		var exists bool
		p.Timeline, exists = src.Media.EpisodeTimelines[in.Episode]
		if !exists {
			p.Timeline = media.MediaTimeline{Clips: []media.MediaClip{}, MusicVolume: .2}
		}
	}
	p.Timeline.EpisodeNumber = 1
	if p.Timeline.Clips == nil {
		p.Timeline.Clips = []media.MediaClip{}
	}
	if in.OutputMode == "series" {
		p.Timeline.OutputResolution, p.Timeline.AspectRatio = season.Profile.Resolution, season.Profile.Aspect
	}
	needed := map[string]bool{}
	add := func(id string) {
		if id != "" {
			needed[id] = true
		}
	}
	add(p.Timeline.MusicAssetID)
	for i := range p.Timeline.Clips {
		c := &p.Timeline.Clips[i]
		add(c.AssetID)
		add(c.AudioAssetID)
		add(c.SourceAudioAssetID)
		if c.ShotID != "" && !selectedShots[c.ShotID] {
			if in.BoardPolicy == "omit" {
				c.ShotID = ""
			} else {
				issue("clip_shot_not_selected", c.ID, "剪辑引用了未分配给本集的镜头，请核对归属或将旧分镜留在原项目")
			}
		}
		// Cues remain exact imported content; never retain an alignment job ID
		// owned by a different project or pretend they were freshly analyzed.
		if c.SubtitleAlignmentID != "" {
			p.SubtitleOrigins = append(p.SubtitleOrigins, SubtitleOrigin{ClipID: c.ID, Source: c.SubtitleSource, AlignmentID: c.SubtitleAlignmentID})
			c.SubtitleSource = "imported"
			c.SubtitleAlignmentID = ""
		} else if c.SubtitleSource != "" && c.SubtitleSource != "manual" {
			p.SubtitleOrigins = append(p.SubtitleOrigins, SubtitleOrigin{ClipID: c.ID, Source: c.SubtitleSource})
			c.SubtitleSource = "imported"
		}
	}
	for _, t := range p.Timeline.AudioTracks {
		add(t.AssetID)
	}
	for _, t := range p.Timeline.VideoOverlays {
		add(t.AssetID)
	}
	for _, a := range src.Media.Assets {
		if selectedShots[a.ShotID] || a.CharacterID != "" && charNames[a.CharacterID] != "" || a.CharacterID == "" && a.Character != "" && len(aliases[a.Character]) > 0 {
			add(a.ID)
		}
	}
	assets := map[string]media.MediaAsset{}
	for _, a := range src.Media.Assets {
		if assets[a.ID].ID != "" {
			return Plan{}, ErrInput
		}
		assets[a.ID] = a
	}
	for _, sel := range src.Media.Selections {
		if selectedShots[sel.ShotID] {
			add(sel.AssetID)
			p.Selections = append(p.Selections, sel)
		}
	}
	for _, r := range src.Media.Reviews {
		if selectedShots[r.ShotID] {
			add(r.AssetID)
		}
	}
	ids := []string{}
	for id := range needed {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		a, ok := assets[id]
		if !ok {
			issue("missing_asset", id, "素材引用不存在，未修改源项目")
			continue
		}
		if !series.ID.MatchString(a.ID) {
			issue("invalid_asset_identity", id, "素材标识无效，未修改源项目")
			continue
		}
		switch a.MIMEType {
		case "image/png", "image/jpeg", "audio/wav", "audio/mpeg", "video/mp4":
		default:
			issue("unsupported_asset_type", id, "当前素材格式尚不支持迁移，请保留原项目并先转换素材")
			continue
		}
		c := AssetCopy{Source: a, Character: a.Character, ShotID: a.ShotID}
		if !selectedShots[c.ShotID] {
			c.ShotID = ""
		}
		if a.Character != "" || a.CharacterID != "" {
			choice, explicit := in.CharacterAssignments[id]
			if explicit {
				if choice != "" && charNames[choice] == "" {
					return Plan{}, ErrInput
				}
				c.CharacterID, c.Character = choice, charNames[choice]
			} else if a.CharacterID != "" && charNames[a.CharacterID] != "" {
				c.CharacterID, c.Character = a.CharacterID, charNames[a.CharacterID]
			} else if a.CharacterID != "" {
				issue("character_assignment_required", id, "素材角色标识已不在当前稿中，请选择角色或解除角色关联")
			} else if len(aliases[a.Character]) == 1 {
				for id := range aliases[a.Character] {
					c.CharacterID, c.Character = id, charNames[id]
				}
			} else {
				issue("character_assignment_required", id, "素材角色标签无法唯一对应当前角色，请选择角色或解除角色关联")
			}
		}
		p.Assets = append(p.Assets, c)
	}
	for id := range in.CharacterAssignments {
		if !needed[id] {
			return Plan{}, ErrInput
		}
	}
	for _, r := range src.Media.Reviews {
		if needed[r.AssetID] && (r.ShotID == "" || selectedShots[r.ShotID]) {
			p.Reviews = append(p.Reviews, r)
		}
	}
	for _, b := range src.Media.LibraryBindings {
		if b.ShotID != "" && !selectedShots[b.ShotID] {
			continue
		}
		kept := []string{}
		for _, id := range b.MediaAssetIDs {
			if needed[id] {
				kept = append(kept, id)
			}
		}
		if len(kept) > 0 || len(b.MediaAssetIDs) == 0 {
			b.MediaAssetIDs = kept
			p.Bindings = append(p.Bindings, b)
		}
	}
	for _, e := range src.Media.Exports {
		if episode(e.EpisodeNumber) == in.Episode {
			p.KeptExports = append(p.KeptExports, e.ID)
		}
	}
	if p.Script == nil && p.Storyboard == nil && len(p.Timeline.Clips) == 0 {
		issue("empty_episode", "", "本集没有可迁移的当前内容；历史成片仍保留在原项目")
	}
	if len(p.Timeline.Clips) > 0 {
		state := media.MediaState{Assets: src.Media.Assets}
		// Imported cues use a provenance label accepted by the media contract.
		if err := media.ValidateTimeline(state, p.Timeline); err != nil {
			issue("invalid_timeline", "", err.Error())
		}
	}
	p.Digest = Seal(p)
	return p, nil
}
