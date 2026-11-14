package application

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"reflect"
	"strconv"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/media"
	"github.com/adcwb/ai-creative-studio/backend/internal/modules/revisions"
	"github.com/adcwb/ai-creative-studio/backend/internal/persistence/revisionrepo"
)

type revisionRestoreInput struct {
	ExpectedProjectVersion int    `json:"expected_project_version"`
	ExpectedMediaVersion   int    `json:"expected_media_version"`
	Component              string `json:"component"`
	ClipID                 string `json:"clip_id"`
	Episode                int    `json:"episode_number"`
}

type restorePreviewAsset struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	Name    string `json:"name"`
	FileURL string `json:"file_url"`
}
type revisionRestorePlan struct {
	RevisionID     string                `json:"revision_id"`
	ProjectVersion int                   `json:"project_version"`
	MediaVersion   int                   `json:"media_version"`
	Component      string                `json:"component"`
	ClipID         string                `json:"clip_id"`
	Episode        int                   `json:"episode_number"`
	Changed        bool                  `json:"changed"`
	Timeline       MediaTimeline         `json:"timeline"`
	Current        MediaTimeline         `json:"current_timeline"`
	Assets         []restorePreviewAsset `json:"assets"`
}

func revisionTimeline(v revisions.Revision, episode int) (MediaTimeline, error) {
	var t MediaTimeline
	if episode > 1 {
		var sources revisionSources
		if err := json.Unmarshal(v.Manifest.Sources, &sources); err != nil {
			return t, revisions.ErrInput
		}
		var ok bool
		t, ok = sources.EpisodeTimelines[episode]
		if !ok {
			return t, mediaInvalid("此修订没有所选分集的剪辑")
		}
	} else if err := json.Unmarshal(v.Manifest.Timeline, &t); err != nil {
		return t, revisions.ErrInput
	}
	return t, nil
}
func mergeRevisionTimeline(current, old MediaTimeline, component, clipID string) (MediaTimeline, error) {
	var next MediaTimeline
	if err := json.Unmarshal(rawRevision(current), &next); err != nil {
		return next, err
	}
	if component == "timeline" {
		if clipID != "" {
			return next, revisions.ErrInput
		}
		return old, nil
	}
	if component != "clip" && component != "audio" && component != "subtitles" {
		return next, revisions.ErrInput
	}
	var source *media.MediaClip
	for i := range old.Clips {
		if old.Clips[i].ID == clipID {
			source = &old.Clips[i]
			break
		}
	}
	if source == nil || clipID == "" {
		return next, mediaInvalid("此修订没有所选镜头")
	}
	for i := range next.Clips {
		target := &next.Clips[i]
		if target.ID != clipID {
			continue
		}
		switch component {
		case "clip":
			*target = *source
		case "audio":
			target.AudioAssetID = source.AudioAssetID
			target.AudioVolume = source.AudioVolume
			target.PreserveSourceAudio = source.PreserveSourceAudio
			target.SourceAudioVolume = source.SourceAudioVolume
			target.SourceAudioAssetID = source.SourceAudioAssetID
			target.SourceAudioTrimStart = source.SourceAudioTrimStart
		case "subtitles":
			target.Subtitle = source.Subtitle
			target.SubtitleCues = source.SubtitleCues
			target.SubtitleSource = source.SubtitleSource
			target.SubtitleAlignmentID = source.SubtitleAlignmentID
		}
		return next, nil
	}
	return next, mediaInvalid("当前剪辑已无此镜头，请选择完整剪辑恢复或重新核对")
}

// Caller holds core then media locks. A restore never inserts assets, grants,
// adopted frames, model state or publishing records from historical content.
func (s *FileStore) restoreTimelineLocked(ctx context.Context, space, project string, source revisions.Revision, in revisionRestoreInput, current MediaState) (MediaTimeline, error) {
	old, err := revisionTimeline(source, in.Episode)
	if err != nil {
		return MediaTimeline{}, err
	}
	active := current.Timeline
	if in.Episode > 1 {
		active = current.EpisodeTimelines[in.Episode]
	}
	next, err := mergeRevisionTimeline(active, old, in.Component, in.ClipID)
	if err != nil {
		return next, err
	}
	next.EpisodeNumber = in.Episode
	if err = validateTimeline(current, next); err != nil {
		return next, err
	}
	if err = s.media.validateAlignmentSources(project, space, next); err != nil {
		return next, err
	}
	if err = s.media.requireLocalEpisode(space, project, in.Episode); err != nil {
		return next, err
	}
	var sources revisionSources
	if err = json.Unmarshal(source.Manifest.Sources, &sources); err != nil || sources.Format != 1 {
		return next, revisions.ErrInput
	}
	used := map[string]bool{}
	for _, id := range renderInputIDs(next) {
		used[id] = true
	}
	for _, asset := range sources.Assets {
		if !used[asset.Asset.ID] {
			continue
		}
		path, err := s.media.localBlob(space, project, asset.Asset.ID)
		if err != nil {
			return next, err
		}
		info, err := localBlobInfo(ctx, blobKey(space, project, asset.Asset.ID), path)
		if err != nil {
			return next, err
		}
		if info.SHA256 != asset.SHA256 || info.Size != asset.Size {
			return next, ErrBlobIntegrity
		}
	}
	return next, nil
}

func (s *FileStore) productionRestore(ctx context.Context, space, project, revision, key string, in revisionRestoreInput, apply bool, authorize func() bool) (revisionRestorePlan, MediaState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, err := s.projectLocked(space, project)
	if err != nil {
		return revisionRestorePlan{}, MediaState{}, err
	}
	source, err := s.revisions.Get(ctx, revisionrepo.Scope{Space: space, Project: project}, revision)
	if err != nil {
		return revisionRestorePlan{}, MediaState{}, err
	}
	if apply && (in.ExpectedProjectVersion < 1 || in.ExpectedMediaVersion < 1) {
		return revisionRestorePlan{}, MediaState{}, revisions.ErrInput
	}
	if in.Episode < 0 {
		return revisionRestorePlan{}, MediaState{}, revisions.ErrInput
	}
	if in.Episode == 0 {
		in.Episode = 1
	}
	m := s.media
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failure != nil {
		return revisionRestorePlan{}, MediaState{}, m.failure
	}
	if m.pg != nil && m.pg.err != nil {
		return revisionRestorePlan{}, MediaState{}, m.pg.err
	}
	d := m.clone()
	k := mediaKey(space, project)
	state := mediaState(d, k)
	digest := mediaDigest([]any{"revision-restore", revision, source.SHA256, in})
	if authorize != nil && !authorize() {
		return revisionRestorePlan{}, state, errRuntimeDenied
	}
	if apply {
		if replay, err := m.check(d, k, key, digest); err != nil || replay {
			return revisionRestorePlan{}, state, err
		}
		if in.ExpectedProjectVersion != p.Version {
			return revisionRestorePlan{}, state, ErrVersionConflict
		}
		if in.ExpectedMediaVersion != state.Version {
			return revisionRestorePlan{}, state, errMediaVersion
		}
	}
	next, err := s.restoreTimelineLocked(ctx, space, project, source, in, state)
	if err != nil {
		return revisionRestorePlan{}, state, err
	}
	active := state.Timeline
	if in.Episode > 1 {
		active = state.EpisodeTimelines[in.Episode]
	}
	if active.EpisodeNumber == 0 {
		active.EpisodeNumber = 1
	}
	plan := revisionRestorePlan{RevisionID: revision, ProjectVersion: p.Version, MediaVersion: state.Version, Component: in.Component, ClipID: in.ClipID, Episode: in.Episode, Changed: !reflect.DeepEqual(active, next), Timeline: next, Current: active}
	if !apply {
		used := map[string]bool{}
		for _, timeline := range []MediaTimeline{active, next} {
			for _, id := range renderInputIDs(timeline) {
				used[id] = true
			}
		}
		plan.Assets = []restorePreviewAsset{}
		for _, a := range state.Assets {
			if used[a.ID] {
				plan.Assets = append(plan.Assets, restorePreviewAsset{a.ID, a.Kind, a.Name, a.FileURL})
			}
		}
		return plan, state, nil
	}
	if authorize != nil && !authorize() {
		return plan, state, errRuntimeDenied
	}
	if in.Episode > 1 {
		if state.EpisodeTimelines == nil {
			state.EpisodeTimelines = map[int]MediaTimeline{}
		}
		state.EpisodeTimelines[in.Episode] = next
	} else {
		state.Timeline = next
	}
	state.Version++
	d.Projects[k] = state
	d.Receipts[k+":"+key] = mediaReceipt{Digest: digest}
	result, err := m.commitState(d, k)
	return plan, result, err
}

func registerRevisionRestoreRoutes(mux *http.ServeMux, s *FileStore) {
	base := "/api/v1/projects/{id}/revisions/{revision_id}"
	mux.HandleFunc("GET "+base+"/restore-plan", func(w http.ResponseWriter, r *http.Request) {
		space, ok := spaceID(w, r)
		if !ok {
			return
		}
		episode := 1
		if raw := r.URL.Query().Get("episode_number"); raw != "" {
			var err error
			episode, err = strconv.Atoi(raw)
			if err != nil || episode < 1 {
				revisionError(w, revisions.ErrInput)
				return
			}
		}
		in := revisionRestoreInput{Component: r.URL.Query().Get("component"), ClipID: r.URL.Query().Get("clip_id"), Episode: episode}
		plan, _, err := s.productionRestore(r.Context(), space, r.PathValue("id"), r.PathValue("revision_id"), "", in, false, nil)
		if err != nil {
			revisionRestoreError(w, err)
			return
		}
		respond(w, 200, plan)
	})
	mux.HandleFunc("POST "+base+"/restore-timeline", func(w http.ResponseWriter, r *http.Request) {
		space, key, ok := projectWriteScope(w, r)
		if !ok {
			return
		}
		var in revisionRestoreInput
		if !decodeProject(w, r, &in) {
			return
		}
		var authorize func() bool
		if a, ok := r.Context().Value(authScopeKey{}).(authScope); ok {
			authorize = a.recheckWrite
		}
		_, state, err := s.productionRestore(r.Context(), space, r.PathValue("id"), r.PathValue("revision_id"), key, in, true, authorize)
		if err != nil {
			revisionRestoreError(w, err)
			return
		}
		respond(w, 200, map[string]any{"id": r.PathValue("revision_id"), "media_version": state.Version})
	})
}

func revisionRestoreError(w http.ResponseWriter, err error) {
	if errors.Is(err, errMediaInput) || errors.Is(err, errMediaVersion) || errors.Is(err, errMediaKey) {
		mediaError(w, err)
		return
	}
	if errors.Is(err, ErrBlobIntegrity) || errors.Is(err, os.ErrNotExist) {
		problem(w, 409, "revision_asset_unavailable", "历史素材缺失或字节已变化，未恢复剪辑")
		return
	}
	revisionError(w, err)
}
