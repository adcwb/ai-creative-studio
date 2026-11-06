package application

import (
	"context"
	"encoding/json"
)

// DirectorSnapshot is authored source, not native control or proof of consistency.
// Only this shot and its immediate neighbours are pinned, never the whole script.
type DirectorSnapshot struct {
	OmittedNeighbours []string        `json:"omitted_neighbours,omitempty"`
	ProjectVersion    int             `json:"project_version"`
	ScriptVersion     int             `json:"script_version"`
	SHA256            string          `json:"sha256"`
	Kind              string          `json:"kind"`
	Current           StoryboardShot  `json:"current"`
	Previous          *StoryboardShot `json:"previous,omitempty"`
	Next              *StoryboardShot `json:"next,omitempty"`
	OmittedFields     []string        `json:"omitted_fields"`
}

func cloneDirectorSnapshot(in *DirectorSnapshot) *DirectorSnapshot {
	if in == nil {
		return nil
	}
	out := *in
	if in.Previous != nil {
		v := *in.Previous
		out.Previous = &v
	}
	if in.Next != nil {
		v := *in.Next
		out.Next = &v
	}
	out.OmittedFields = append([]string{}, in.OmittedFields...)
	out.OmittedNeighbours = append([]string(nil), in.OmittedNeighbours...)
	return &out
}
func prepareDirectorSnapshot(p Project, in CreativeJobInput) (*DirectorSnapshot, error) {
	if !in.UseDirectorContext {
		return nil, nil
	}
	if (in.Kind != "image" && in.Kind != "video") || in.ShotID == "" || p.Storyboard == nil {
		return nil, projectInputError("导演镜头资料只适用于已保存镜头的图片或视频候选")
	}
	// The caller holds core/creative locks and has checked project CAS.
	for i, shot := range p.Storyboard.Shots {
		if shot.ID != in.ShotID {
			continue
		}
		if p.Storyboard.SourceScriptVersion != p.ScriptVersion {
			return nil, projectInputError("分镜来源剧本已变化，请核对镜头计划后再使用导演资料")
		}
		out := &DirectorSnapshot{ProjectVersion: p.Version, ScriptVersion: p.Storyboard.SourceScriptVersion, Kind: in.Kind, Current: shot, OmittedFields: []string{"sound", "dialogue"}}
		if i > 0 {
			v := p.Storyboard.Shots[i-1]
			if ep := directorEpisode(p, shot); ep > 0 && ep == directorEpisode(p, v) {
				out.Previous = &v
			} else {
				out.OmittedNeighbours = append(out.OmittedNeighbours, "previous")
			}
		}
		if i+1 < len(p.Storyboard.Shots) {
			v := p.Storyboard.Shots[i+1]
			if ep := directorEpisode(p, shot); ep > 0 && ep == directorEpisode(p, v) {
				out.Next = &v
			} else {
				out.OmittedNeighbours = append(out.OmittedNeighbours, "next")
			}
		}
		if in.Kind == "image" {
			out.OmittedFields = append(out.OmittedFields, "duration_seconds")
		}
		// The digest identifies the complete local context and its source versions.
		out.SHA256 = mediaDigest(out)
		return out, nil
	}
	return nil, projectInputError("导演镜头不存在")
}
func visualDirectorShot(shot *StoryboardShot, kind string) any {
	if shot == nil {
		return nil
	}
	v := map[string]any{"shot_id": shot.ID, "description": shot.Description, "shot_size": shot.ShotSize, "camera_movement": shot.CameraMovement, "purpose": shot.Purpose, "performance": shot.Performance, "continuity": shot.Continuity, "angle": shot.Angle, "composition": shot.Composition, "axis": shot.Axis, "eyeline": shot.Eyeline, "blocking": shot.Blocking, "lighting": shot.Lighting}
	if kind == "video" {
		v["duration_intent_seconds"] = shot.DurationSeconds
	}
	return v
}

type directorContextKey struct{}

func withDirectorContext(ctx context.Context, job *CreativeJob) context.Context {
	if job.DirectorContext == nil {
		return ctx
	}
	v := job.DirectorContext
	payload := map[string]any{"source_sha256": v.SHA256, "current": visualDirectorShot(&v.Current, v.Kind), "previous": visualDirectorShot(v.Previous, v.Kind), "next": visualDirectorShot(v.Next, v.Kind)}
	raw, _ := json.Marshal(payload)
	return context.WithValue(ctx, directorContextKey{}, string(raw))
}
func appendDirectorContext(ctx context.Context, prompt string) string {
	value, _ := ctx.Value(directorContextKey{}).(string)
	if value == "" {
		return prompt
	}
	return prompt + "\n\nDirector's authored visual direction (prompt guidance, not exact native camera, identity or timing controls). Generate only CURRENT shot. Neighbours guide continuity and must not be combined into a collage. Do not render dialogue or sound notes as text. Sound/dialogue are omitted; this does not request audio synthesis:\n" + value
}

// Report only the transport controls this selected, resolved adapter actually
// sends. A camera intention in text is never promoted to a native parameter.
func directorSupport(kind, provider string, reference bool, controls CreativeControls) map[string]string {
	out := map[string]string{}
	for _, key := range []string{"purpose", "performance", "continuity", "angle", "composition", "axis", "eyeline", "blocking", "lighting", "sound", "shot_size", "camera_movement", "duration_seconds", "image_reference", "first_frame", "last_frame", "motion_reference", "native_voice"} {
		out[key] = "unsupported"
	}
	if visualLibrarySupported(kind, provider) {
		for _, key := range []string{"purpose", "performance", "continuity", "angle", "composition", "axis", "eyeline", "blocking", "lighting", "shot_size", "camera_movement"} {
			out[key] = "prompt_only"
		}
		if kind == "video" {
			out["duration_seconds"] = "prompt_only"
		}
		if reference {
			out["image_reference"] = "reference"
		}
		if controls.Frames {
			out["first_frame"], out["last_frame"] = "native", "native"
		}
		if controls.MotionReference {
			out["motion_reference"] = "reference"
		}
	}
	if kind == "speech" && nativePresetSupported(provider) {
		out["native_voice"] = "native"
	}
	return out
}

func validateDirectorSnapshot(job *CreativeJob) error {
	v := job.DirectorContext
	if v == nil {
		if job.DirectorSourceVersion != 0 || job.DirectorSourceSHA256 != "" {
			return ErrExecutionGovernance
		}
		return nil
	}
	copy := cloneDirectorSnapshot(v)
	copy.SHA256 = ""
	if (v.Kind != "image" && v.Kind != "video") || v.Kind != job.Kind || v.ProjectVersion != job.SourceVersion || v.ProjectVersion != job.DirectorSourceVersion || v.Current.ID != job.ShotID || v.SHA256 != job.DirectorSourceSHA256 || v.SHA256 != mediaDigest(copy) {
		return ErrExecutionGovernance
	}
	return nil
}

func directorEpisode(p Project, shot StoryboardShot) int {
	if p.SeriesEpisodeID != "" || p.Script == nil {
		return 1
	}
	if shot.SceneIndex < 1 || shot.SceneIndex > len(p.Script.Scenes) {
		return 0
	}
	ep := p.Script.Scenes[shot.SceneIndex-1].EpisodeNumber
	if ep < 1 {
		return 1
	}
	return ep
}
