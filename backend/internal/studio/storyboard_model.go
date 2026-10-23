package studio

import (
	"bytes"
	"encoding/json"
	"errors"
	"regexp"
	"time"
)

type StoryboardShot struct {
	ID              string `json:"id"`
	SceneIndex      int    `json:"scene_index"`
	Description     string `json:"description"`
	ShotSize        string `json:"shot_size"`
	CameraMovement  string `json:"camera_movement"`
	DurationSeconds int    `json:"duration_seconds"`
	Dialogue        string `json:"dialogue"`
}
type Storyboard struct {
	Provider            string           `json:"provider,omitempty"`
	SourceScriptVersion int              `json:"source_script_version"`
	Shots               []StoryboardShot `json:"shots"`
}
type StoryboardSaveInput struct {
	ExpectedVersion int              `json:"expected_version"`
	Shots           []StoryboardShot `json:"shots"`
}
type StoryboardGeneration struct {
	Model               string    `json:"model,omitempty"`
	ID                  string    `json:"id"`
	SpaceID             string    `json:"space_id"`
	ProjectID           string    `json:"project_id"`
	BaseVersion         int       `json:"base_version"`
	SourceScriptVersion int       `json:"source_script_version"`
	Provider            string    `json:"provider"`
	Status              string    `json:"status"`
	Progress            int       `json:"progress"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
	Error               string    `json:"error,omitempty"`
	ErrorCode           string    `json:"error_code,omitempty"`
	SourceScript        *Script   `json:"source_script,omitempty"`
}

var ErrStoryboardMissing = errors.New("请先成功生成一份模拟分镜计划")
var shotIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,40}$`)

func cloneStoryboardGeneration(g StoryboardGeneration) StoryboardGeneration {
	if g.SourceScript != nil {
		script := cloneScript(*g.SourceScript)
		g.SourceScript = &script
	}
	return g
}
func storyboardGenerationView(g StoryboardGeneration) StoryboardGeneration {
	g.SourceScript = nil
	return g
}
func NormalizeStoryboardShots(shots []StoryboardShot) ([]StoryboardShot, error) {
	if len(shots) < 1 || len(shots) > 30 {
		return nil, projectInputError("分镜需包含 1–30 个镜头")
	}
	shots = append([]StoryboardShot(nil), shots...)
	ids := map[string]bool{}
	for i := range shots {
		shot := &shots[i]
		if !shotIDPattern.MatchString(shot.ID) || ids[shot.ID] {
			return nil, projectInputError("镜头标识无效或重复")
		}
		ids[shot.ID] = true
		if shot.SceneIndex < 1 || shot.SceneIndex > 3 {
			return nil, projectInputError("场景引用需为 1–3")
		}
		var err error
		if shot.Description, err = normalizeProjectText(shot.Description, "画面描述", 1, 1000); err != nil {
			return nil, err
		}
		if shot.Dialogue, err = normalizeProjectText(shot.Dialogue, "镜头对白", 0, 2000); err != nil {
			return nil, err
		}
		if shot.DurationSeconds < 1 || shot.DurationSeconds > 60 {
			return nil, projectInputError("镜头时长需为 1–60 秒")
		}
		if shot.ShotSize != "wide" && shot.ShotSize != "medium" && shot.ShotSize != "close_up" {
			return nil, projectInputError("景别无效")
		}
		if shot.CameraMovement != "static" && shot.CameraMovement != "pan" && shot.CameraMovement != "track" {
			return nil, projectInputError("运镜无效")
		}
	}
	return shots, nil
}

// Dialogue may be empty but must be explicitly supplied, as in the HTTP contract.
func (s *StoryboardShot) UnmarshalJSON(data []byte) error {
	type plain StoryboardShot
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	value, ok := fields["dialogue"]
	if !ok || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
		return errors.New("dialogue is required")
	}
	var decoded plain
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		return err
	}
	*s = StoryboardShot(decoded)
	return nil
}
