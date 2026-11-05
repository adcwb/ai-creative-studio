package story

// Cast pins an explicitly authored relationship between two separate identities.
// Asset references select a version; they never grant voice or likeness rights.
type Cast struct {
	EpisodeID      string          `json:"episode_id"`
	ScriptVersion  int             `json:"script_version"`
	ScriptSHA256   string          `json:"script_sha256"`
	BibleVersionID string          `json:"bible_version_id"`
	Characters     []CastCharacter `json:"characters"`
}
type CastCharacter struct {
	ScriptID       string `json:"script_character_id"`
	ScriptName     string `json:"script_character_name"`
	SeriesID       string `json:"series_character_id"`
	SeriesName     string `json:"series_character_name"`
	AssetID        string `json:"library_asset_id,omitempty"`
	AssetVersionID string `json:"asset_version_id,omitempty"`
	AssetName      string `json:"asset_name,omitempty"`
	Notes          string `json:"notes"`
}

func CastID(root, episode string) string {
	return TextHash("story-cast-v1:" + root + ":" + episode)[:32]
}
func normalizeCast(c *Cast) bool {
	if c == nil || !ID.MatchString(c.EpisodeID) || !ID.MatchString(c.BibleVersionID) || !Hash.MatchString(c.ScriptSHA256) || c.ScriptVersion < 1 || int64(c.ScriptVersion) > MaxVersion {
		return false
	}
	if c.Characters == nil {
		c.Characters = []CastCharacter{}
	}
	seen := map[string]bool{}
	for i := range c.Characters {
		x := &c.Characters[i]
		if !ID.MatchString(x.ScriptID) || !ID.MatchString(x.SeriesID) || seen[x.ScriptID] || !clean(&x.ScriptName, true) || !clean(&x.SeriesName, true) || !clean(&x.Notes, false) {
			return false
		}
		seen[x.ScriptID] = true
		if x.AssetID == "" {
			if x.AssetVersionID != "" || x.AssetName != "" {
				return false
			}
		} else if !Scope.MatchString(x.AssetID) || len(x.AssetID) > 80 || !Scope.MatchString(x.AssetVersionID) || len(x.AssetVersionID) > 80 || !clean(&x.AssetName, true) {
			return false
		}
	}
	return true
}
