package application

// These are observable production facts, not the creator's manually selected
// release status, an automatic approval, or proof of model output quality.
type episodeProduction struct {
	ProjectVersion  int    `json:"project_version"`
	MediaVersion    int    `json:"media_version"`
	Script          bool   `json:"script"`
	Storyboard      bool   `json:"storyboard"`
	StoryboardStale bool   `json:"storyboard_stale"`
	Shots           int    `json:"shots"`
	Adopted         int    `json:"adopted_shots"`
	OpenReviews     int    `json:"open_reviews"`
	ExportID        string `json:"export_id,omitempty"`
}

func (s *FileStore) episodeProduction(space, project string) (episodeProduction, error) {
	p, err := s.GetProject(space, project)
	if err != nil {
		return episodeProduction{}, err
	}
	state, err := s.media.GetChecked(project, space)
	if err != nil {
		return episodeProduction{}, err
	}
	out := episodeProduction{ProjectVersion: p.Version, MediaVersion: state.Version, Script: p.Script != nil, Storyboard: p.Storyboard != nil, StoryboardStale: p.StoryboardStale}
	selected := map[string]bool{}
	for _, selection := range state.Selections {
		if _, ok := mediaAsset(state, selection.AssetID); ok {
			selected[selection.ShotID] = true
		}
	}
	if p.Storyboard != nil {
		out.Shots = len(p.Storyboard.Shots)
		for _, shot := range p.Storyboard.Shots {
			if selected[shot.ID] {
				out.Adopted++
			}
		}
	}
	for _, review := range state.Reviews {
		if !review.Resolved {
			out.OpenReviews++
		}
	}
	for _, export := range state.Exports {
		if export.Purpose == "" && export.Status == "succeeded" && (export.EpisodeNumber == 0 || export.EpisodeNumber == 1) {
			out.ExportID = export.ID
		}
	}
	return out, nil
}
