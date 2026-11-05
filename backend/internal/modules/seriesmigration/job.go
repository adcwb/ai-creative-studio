package seriesmigration

import (
	"reflect"
	"regexp"
	"time"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/projects"
	"github.com/adcwb/ai-creative-studio/backend/internal/modules/series"
)

var digestID = regexp.MustCompile(`^[a-f0-9]{64}$`)

type Proof struct {
	TargetID string `json:"target_asset_id"`
	SHA256   string `json:"sha256"`
	Size     int64  `json:"size"`
}
type Job struct {
	Format    int              `json:"format"`
	ID        string           `json:"id"`
	Space     string           `json:"space_id"`
	Actor     string           `json:"created_by"`
	ProjectID string           `json:"source_project_id"`
	TargetID  string           `json:"target_project_id"`
	Version   int64            `json:"row_version"`
	State     string           `json:"state"`
	Phase     string           `json:"phase"`
	Error     string           `json:"error,omitempty"`
	Plan      Plan             `json:"plan"`
	Reserved  bool             `json:"reserved"`
	Copies    map[string]Proof `json:"copies"`
	CreatedAt time.Time        `json:"created_at"`
	UpdatedAt time.Time        `json:"updated_at"`
}

func TargetID(job string) string { return series.SHA("episode-migration-target-v1:" + job)[:32] }
func AssetID(job, source string) string {
	return series.SHA("episode-migration-asset-v1:" + job + ":" + source)[:32]
}
func ValidJob(j Job) bool {
	if j.Format != 1 || !series.ID.MatchString(j.ID) || !series.ScopeID.MatchString(j.Space) || !series.ScopeID.MatchString(j.Actor) || !series.ID.MatchString(j.ProjectID) || j.TargetID != TargetID(j.ID) || j.Version < 1 || j.Version > series.MaxOrdinal || j.Plan.Format != 1 || j.Plan.Source.ProjectID != j.ProjectID || j.Plan.Digest != Seal(j.Plan) || len(j.Plan.Digest) != 64 || len(j.Plan.Issues) != 0 || j.Copies == nil || j.CreatedAt.IsZero() || j.UpdatedAt.IsZero() {
		return false
	}
	if !digestID.MatchString(j.Plan.Source.SHA256) || !j.Plan.Profile.Valid() || j.Plan.Input.Episode != j.Plan.Source.Episode || j.Plan.Input.Episode < 1 || j.Plan.Input.Ordinal < 1 || j.Plan.Input.Ordinal >= series.MaxOrdinal || !series.ID.MatchString(j.Plan.Input.SeasonID) {
		return false
	}
	if j.Plan.Script != nil {
		if j.Plan.Script.StructureVersion != 2 {
			return false
		}
		if _, e := projects.NormalizeScript(*j.Plan.Script); e != nil {
			return false
		}
		for _, scene := range j.Plan.Script.Scenes {
			if scene.EpisodeNumber != 1 {
				return false
			}
		}
	}
	if b := j.Plan.Storyboard; b != nil {
		if b.StructureVersion != 2 {
			return false
		}
		shots, e := projects.BindStoryboard(b.Shots, b.SourceSceneIDs)
		if e != nil || !reflect.DeepEqual(shots, b.Shots) {
			return false
		}
		if j.Plan.Script == nil && b.SourceScriptVersion != 0 || j.Plan.Script != nil && (b.SourceScriptVersion != 1 || !reflect.DeepEqual(b.SourceSceneIDs, projects.SceneIDs(*j.Plan.Script))) {
			return false
		}
	}
	switch j.State {
	case "accepted", "preparing", "copying", "validating", "paused", "completed", "cancelling", "cancelled":
	default:
		return false
	}
	switch j.Phase {
	case "preparing", "copying", "validating", "completed", "cancelling", "cancelled":
	default:
		return false
	}
	if j.Plan.Input.ProjectVersion != j.Plan.Source.ProjectVersion || j.Plan.Input.MediaVersion != j.Plan.Source.MediaVersion || j.Plan.Source.ProjectVersion < 1 || j.Plan.Source.MediaVersion < 1 || j.Plan.Source.ScriptVersion < 0 {
		return false
	}
	if j.Plan.Input.BoardPolicy != "include" && j.Plan.Input.BoardPolicy != "omit" || j.Plan.Input.OutputMode != "series" && j.Plan.Input.OutputMode != "preserve" {
		return false
	}
	if !j.Reserved && len(j.Copies) != 0 {
		return false
	}
	if j.State == "completed" && (!j.Reserved || j.Phase != "completed" || len(j.Copies) != len(j.Plan.Assets)) {
		return false
	}
	if j.State == "validating" && (!j.Reserved || len(j.Copies) != len(j.Plan.Assets)) {
		return false
	}
	if j.State == "cancelled" && j.Phase != "cancelled" {
		return false
	}
	ids := map[string]bool{}
	for _, a := range j.Plan.Assets {
		if !series.ID.MatchString(a.Source.ID) || ids[a.Source.ID] {
			return false
		}
		ids[a.Source.ID] = true
	}
	for id, p := range j.Copies {
		if !ids[id] || p.TargetID != AssetID(j.ID, id) || !digestID.MatchString(p.SHA256) || p.Size < 0 {
			return false
		}
	}
	return true
}
