package application

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/library"
	"github.com/adcwb/ai-creative-studio/backend/internal/modules/media"
	"github.com/adcwb/ai-creative-studio/backend/internal/modules/prompts"
	"github.com/adcwb/ai-creative-studio/backend/internal/modules/revisions"
	"github.com/adcwb/ai-creative-studio/backend/internal/modules/story"
	"github.com/adcwb/ai-creative-studio/backend/internal/persistence/revisionrepo"
)

type revisionCaptureInput struct {
	ExpectedProjectVersion int    `json:"expected_project_version"`
	ExpectedMediaVersion   int    `json:"expected_media_version"`
	ExpectedBranchVersion  int    `json:"expected_branch_version"`
	ExpectedHead           string `json:"expected_head_revision_id"`
	Label                  string `json:"label"`
}
type revisionForkInput struct {
	SourceID string `json:"source_revision_id"`
	Name     string `json:"name"`
}

func (in *revisionCaptureInput) UnmarshalJSON(raw []byte) error {
	type plain revisionCaptureInput
	var fields map[string]json.RawMessage
	if e := json.Unmarshal(raw, &fields); e != nil {
		return e
	}
	for _, k := range []string{"expected_project_version", "expected_media_version", "expected_branch_version", "expected_head_revision_id", "label"} {
		if _, ok := fields[k]; !ok {
			return errors.New("revision source fields required")
		}
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	return d.Decode((*plain)(in))
}

type revisionAsset struct {
	Asset  MediaAsset `json:"asset"`
	SHA256 string     `json:"sha256"`
	Size   int64      `json:"size"`
}

// This projection contains authored inputs only. Never serialize CreativeJob:
// its runtime selection, dispatch state and voice consent are not restorable.
type revisionJobSource struct {
	ID              string                `json:"id"`
	AssetID         string                `json:"asset_id"`
	Kind            string                `json:"kind"`
	ShotID          string                `json:"shot_id"`
	Prompt          string                `json:"prompt"`
	PromptSource    *prompts.Source       `json:"prompt_source,omitempty"`
	Director        *DirectorSnapshot     `json:"director_context,omitempty"`
	VisualContext   string                `json:"visual_context,omitempty"`
	ReferenceIDs    []string              `json:"reference_asset_ids,omitempty"`
	FirstFrameID    string                `json:"first_frame_asset_id,omitempty"`
	LastFrameID     string                `json:"last_frame_asset_id,omitempty"`
	MotionID        string                `json:"motion_reference_asset_id,omitempty"`
	LibraryVersions []media.LibrarySource `json:"library_versions"`
}
type revisionSources struct {
	Format             int                     `json:"format"`
	StructureVersion   int                     `json:"structure_version"`
	ScriptVersion      int                     `json:"script_version"`
	ScriptBrief        string                  `json:"script_brief"`
	SeriesEpisodeID    string                  `json:"series_episode_id,omitempty"`
	EpisodeTimelines   map[int]MediaTimeline   `json:"episode_timelines,omitempty"`
	Selections         []media.Selection       `json:"selections"`
	LibraryBindings    []media.LibraryBinding  `json:"library_bindings"`
	LibraryVersions    []library.Version       `json:"library_versions"`
	PromptApplications []prompts.Application   `json:"prompt_applications"`
	Assets             []revisionAsset         `json:"assets"`
	Jobs               []revisionJobSource     `json:"jobs"`
	Contexts           []story.ContextSnapshot `json:"contexts"`
	ScriptOrigin       string                  `json:"script_origin"`
}

func rawRevision(v any) json.RawMessage { b, _ := json.Marshal(v); return b }
func (s *FileStore) CaptureProductionRevision(ctx context.Context, space, project, branch, actor, key string, in revisionCaptureInput, authorize func() bool) (revisionrepo.Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, e := s.projectLocked(space, project)
	if e != nil {
		return revisionrepo.Record{}, e
	}
	q := revisionrepo.Scope{Space: space, Project: project}
	digest := mediaDigest([]any{"capture", branch, in})
	if r, replay, e := s.revisions.Replay(ctx, q, branch, actor, key, digest); e != nil || replay {
		return r, e
	}
	if in.ExpectedProjectVersion < 1 || in.ExpectedMediaVersion < 1 || in.ExpectedBranchVersion < 0 {
		return revisionrepo.Record{}, revisions.ErrInput
	}
	if p.Version != in.ExpectedProjectVersion {
		return revisionrepo.Record{}, ErrVersionConflict
	}
	s.creative.mu.Lock()
	defer s.creative.mu.Unlock()
	if s.creative.pg != nil && s.creative.pg.err != nil {
		return revisionrepo.Record{}, s.creative.pg.err
	}
	m := s.media
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failure != nil {
		return revisionrepo.Record{}, m.failure
	}
	if m.pg != nil && m.pg.err != nil {
		return revisionrepo.Record{}, m.pg.err
	}
	state := mediaState(m.data, mediaKey(space, project))
	if state.Version != in.ExpectedMediaVersion {
		return revisionrepo.Record{}, errMediaVersion
	}
	if _, ok := m.data.Projects[mediaKey(space, project)]; !ok {
		if e = m.initializeSeriesProfile(mediaKey(space, project), &state); e != nil {
			return revisionrepo.Record{}, e
		}
	}
	manifest, e := s.productionManifestLocked(ctx, p, state)
	if e != nil {
		return revisionrepo.Record{}, e
	}
	if authorize != nil && !authorize() {
		return revisionrepo.Record{}, errRuntimeDenied
	}
	var tx *sql.Tx
	if s.pg != nil {
		if s.pg.err != nil {
			return revisionrepo.Record{}, s.pg.err
		}
		tx = s.pg.tx
	}
	r, e := s.revisions.Mutate(ctx, tx, q, revisionrepo.Command{Operation: "capture", Branch: branch, Actor: actor, Key: key, Digest: digest, Expected: in.ExpectedBranchVersion, Head: in.ExpectedHead, Label: in.Label, ProjectVersion: p.Version, MediaVersion: state.Version, Manifest: manifest})
	if e == nil && s.pg != nil {
		e = s.commit(s.clone())
	}
	return r, e
}
func (s *FileStore) ForkProductionRevision(ctx context.Context, space, project, actor, key string, in revisionForkInput, authorize func() bool) (revisionrepo.Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, e := s.projectLocked(space, project); e != nil {
		return revisionrepo.Record{}, e
	}
	if authorize != nil && !authorize() {
		return revisionrepo.Record{}, errRuntimeDenied
	}
	var tx *sql.Tx
	if s.pg != nil {
		if s.pg.err != nil {
			return revisionrepo.Record{}, s.pg.err
		}
		tx = s.pg.tx
	}
	r, e := s.revisions.Mutate(ctx, tx, revisionrepo.Scope{Space: space, Project: project}, revisionrepo.Command{Operation: "fork", Branch: revisionrepo.ForkID(actor, key), Actor: actor, Key: key, Digest: mediaDigest(in), SourceID: in.SourceID, Name: in.Name})
	if e == nil && s.pg != nil {
		e = s.commit(s.clone())
	}
	return r, e
}
func (s *FileStore) productionManifestLocked(ctx context.Context, p Project, state MediaState) (revisions.Manifest, error) {
	sources := revisionSources{Format: 1, StructureVersion: p.StructureVersion, ScriptVersion: p.ScriptVersion, ScriptBrief: p.ScriptBrief, SeriesEpisodeID: p.SeriesEpisodeID, EpisodeTimelines: state.EpisodeTimelines, Selections: append([]media.Selection{}, state.Selections...), LibraryBindings: append([]media.LibraryBinding{}, state.LibraryBindings...), LibraryVersions: []library.Version{}, PromptApplications: append([]prompts.Application{}, state.PromptApplications...), Assets: []revisionAsset{}, Jobs: []revisionJobSource{}, Contexts: []story.ContextSnapshot{}, ScriptOrigin: "manual_or_legacy"}
	refs := []revisions.Ref{}
	ids := map[string]bool{}
	addTimeline := func(t MediaTimeline) {
		for _, id := range renderInputIDs(t) {
			ids[id] = true
		}
	}
	addTimeline(state.Timeline)
	for _, t := range state.EpisodeTimelines {
		addTimeline(t)
	}
	for _, v := range state.Selections {
		ids[v.AssetID] = true
	}
	if state.Poster != nil {
		for _, v := range state.Poster.Layers {
			if v.AssetID != "" {
				ids[v.AssetID] = true
			}
		}
	}
	seen := map[string]bool{}
	for _, v := range state.LibraryBindings {
		for _, id := range v.MediaAssetIDs {
			ids[id] = true
		}
		identity := v.AssetID + ":" + v.VersionID
		if seen[identity] {
			continue
		}
		seen[identity] = true
		version, e := s.sharedLibraryVersion(ctx, p.SpaceID, media.LibrarySource{ShareID: v.ShareID, AssetID: v.AssetID, VersionID: v.VersionID})
		if e != nil {
			return revisions.Manifest{}, e
		}
		sources.LibraryVersions = append(sources.LibraryVersions, version)
		refs = append(refs, revisions.Ref{Kind: "library", ID: v.AssetID, VersionID: v.VersionID, SHA256: version.SHA256})
	}
	// Capture only the inputs of actually used candidates, not every generation
	// or its mutable operational history. Historical jobs remain independent.
	jobs := []CreativeJob{}
	for _, job := range s.creative.data.Jobs {
		if job.SpaceID == p.SpaceID && job.ProjectID == p.ID && ids[job.AssetID] {
			jobs = append(jobs, job)
		}
	}
	sort.Slice(jobs, func(i, j int) bool { return jobs[i].ID < jobs[j].ID })
	for _, j := range jobs {
		input := revisionJobSource{ID: j.ID, AssetID: j.AssetID, Kind: j.Kind, ShotID: j.ShotID, Prompt: j.Prompt, PromptSource: j.PromptSource, Director: j.DirectorContext, VisualContext: j.LibraryInputContext, ReferenceIDs: j.ReferenceAssetIDs, FirstFrameID: j.FirstFrameAssetID, LastFrameID: j.LastFrameAssetID, MotionID: j.MotionReferenceAssetID, LibraryVersions: []media.LibrarySource{}}
		for _, id := range append(append([]string{}, j.ReferenceAssetIDs...), j.ReferenceAssetID, j.FirstFrameAssetID, j.LastFrameAssetID, j.MotionReferenceAssetID, j.AudioAssetID, j.MaskAssetID) {
			if id != "" {
				ids[id] = true
			}
		}
		for _, pin := range j.LibraryInputs {
			for _, use := range pin.Uses {
				input.LibraryVersions = append(input.LibraryVersions, use.Source)
			}
		}
		sources.Jobs = append(sources.Jobs, input)
	}
	ordered := []string{}
	for id := range ids {
		if id != "" {
			ordered = append(ordered, id)
		}
	}
	sort.Strings(ordered)
	for _, id := range ordered {
		asset, ok := mediaAsset(state, id)
		if !ok {
			return revisions.Manifest{}, ErrNotFound
		}
		path, e := s.media.localBlob(p.SpaceID, p.ID, id)
		if e != nil {
			return revisions.Manifest{}, e
		}
		info, e := localBlobInfo(ctx, blobKey(p.SpaceID, p.ID, id), path)
		if e != nil {
			return revisions.Manifest{}, e
		}
		if s.media.blobs != nil {
			expected, e := s.media.blobs.info(ctx, info.Key)
			if e != nil {
				return revisions.Manifest{}, e
			}
			if expected != info {
				return revisions.Manifest{}, ErrBlobIntegrity
			}
		}
		sources.Assets = append(sources.Assets, revisionAsset{asset, info.SHA256, info.Size})
		refs = append(refs, revisions.Ref{Kind: "media", ID: id, VersionID: info.SHA256, SHA256: info.SHA256})
	}
	generations := []ScriptGeneration{}
	for _, g := range s.data.ScriptGenerations {
		if g.SpaceID == p.SpaceID && g.ProjectID == p.ID && g.Adoption != nil && g.Adoption.ScriptVersion == p.ScriptVersion && g.Adoption.SHA256 == mediaDigest(p.Script) {
			generations = append(generations, g)
		}
	}
	sort.Slice(generations, func(i, j int) bool { return generations[i].ID < generations[j].ID })
	for _, g := range generations {
		sources.ScriptOrigin = "adopted_candidate"
		for _, id := range []string{g.ContextID, g.Adoption.ReviewContextID} {
			if id == "" || seen["context:"+id] {
				continue
			}
			seen["context:"+id] = true
			v, e := s.writingSnapshot(ctx, p.SpaceID, p.ID, id)
			if e != nil {
				return revisions.Manifest{}, e
			}
			sources.Contexts = append(sources.Contexts, v)
			refs = append(refs, revisions.Ref{Kind: "context", ID: id, VersionID: v.SHA256, SHA256: v.SHA256})
		}
	}
	manifest := revisions.Manifest{Brief: rawRevision(map[string]string{"title": p.Title, "brief": p.Brief}), Script: rawRevision(p.Script), Storyboard: rawRevision(p.Storyboard), Timeline: rawRevision(state.Timeline), Poster: rawRevision(state.Poster), References: refs, Sources: rawRevision(sources)}
	if e := revisions.ValidateManifest(manifest); e != nil {
		return manifest, e
	}
	return manifest, nil
}
