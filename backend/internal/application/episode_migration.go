package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"time"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/media"
	"github.com/adcwb/ai-creative-studio/backend/internal/modules/projects"
	"github.com/adcwb/ai-creative-studio/backend/internal/modules/series"
	m "github.com/adcwb/ai-creative-studio/backend/internal/modules/seriesmigration"
	"github.com/adcwb/ai-creative-studio/backend/internal/persistence/migrationrepo"
	"github.com/adcwb/ai-creative-studio/backend/internal/persistence/seriesrepo"
)

// Preserve the exact source records and immutable revisions. The general clone
// normalizes old nil slices; migration must not rewrite those as a side effect.
func (s *FileStore) migrationCoreCloneLocked() snapshot {
	d := s.clone()
	for id, p := range s.data.Projects {
		d.Projects[id] = p
	}
	d.History = map[string][]ProjectRevision{}
	for id, list := range s.data.History {
		if list == nil {
			d.History[id] = nil
		} else {
			d.History[id] = append([]ProjectRevision{}, list...)
		}
	}
	return d
}
func (s *FileStore) migrationSnapshotLocked(space, id string) (m.Source, error) {
	if s.pg != nil && s.pg.err != nil {
		return m.Source{}, s.pg.err
	}
	if s.media.pg != nil && s.media.pg.err != nil {
		return m.Source{}, s.media.pg.err
	}
	if s.media.failure != nil {
		return m.Source{}, s.media.failure
	}
	p, e := s.projectLocked(space, id)
	if e != nil {
		return m.Source{}, e
	}
	if p.SeriesEpisodeID != "" {
		return m.Source{}, m.ErrInput
	}
	if s.generationActiveLocked(p) {
		return m.Source{}, ErrGenerationActive
	}
	src := m.Source{Project: cloneProject(p), Media: mediaState(s.media.data, mediaKey(space, id))}
	if p.Storyboard != nil && p.Storyboard.SourceScriptVersion > 0 && p.Storyboard.SourceScriptVersion != p.ScriptVersion {
		src.BoardSource, _ = s.structureSourceLocked(p, p.Storyboard.SourceScriptVersion)
	}
	return src, nil
}
func (s *FileStore) MigrationSource(space, id string) (m.Source, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.media.mu.Lock()
	defer s.media.mu.Unlock()
	src, e := s.migrationSnapshotLocked(space, id)
	if e != nil {
		return src, e
	}
	return m.Clone(src)
}
func normalizedMigrationInput(in m.Input) m.Input {
	if in.ShotAssignments == nil {
		in.ShotAssignments = map[string]int{}
	}
	if in.CharacterAssignments == nil {
		in.CharacterAssignments = map[string]string{}
	}
	return in
}
func (s *FileStore) PreviewEpisodeMigration(ctx context.Context, space, id string, in m.Input) (m.Plan, error) {
	in = normalizedMigrationInput(in)
	season, e := s.series.Get(ctx, space, "season", in.SeasonID)
	if e != nil {
		return m.Plan{}, e
	}
	root, e := s.series.Get(ctx, space, "series", season.SeriesID)
	if e != nil {
		return m.Plan{}, e
	}
	if root.Status != "active" {
		return m.Plan{}, m.ErrConflict
	}
	src, e := s.MigrationSource(space, id)
	if e != nil {
		return m.Plan{}, e
	}
	p, e := m.Preview(src, season, in)
	if e != nil {
		return p, e
	}
	available, e := s.series.OrdinalAvailable(ctx, space, season.ID, in.Ordinal)
	if e != nil {
		return m.Plan{}, e
	}
	if !available {
		p.Issues = append(p.Issues, m.Issue{Code: "target_ordinal_occupied", Message: "目标集号已被占用或正在迁移，请选择其他集号"})
	}
	p.Digest = m.Seal(p)
	return p, nil
}
func (s *FileStore) CreateEpisodeMigration(ctx context.Context, a series.Actor, id, key string, in m.Input, digest string) (m.Job, error) {
	if !a.Writable {
		return m.Job{}, m.ErrDenied
	}
	in = normalizedMigrationInput(in)
	jobID := series.NewID(a, "episode-migration", key)
	identity := migrationrepo.Identity{Space: a.Space, Project: id, ID: jobID}
	if prior, e := s.migrations.Get(ctx, identity); e == nil {
		if prior.Actor != a.User || prior.Plan.Digest != digest || series.Digest(prior.Plan.Input) != series.Digest(in) {
			return m.Job{}, m.ErrConflict
		}
		return prior, nil
	} else if !errors.Is(e, m.ErrNotFound) {
		return m.Job{}, e
	}
	p, e := s.PreviewEpisodeMigration(ctx, a.Space, id, in)
	if e != nil {
		return m.Job{}, e
	}
	if len(p.Issues) != 0 || p.Digest != digest {
		return m.Job{}, m.ErrConflict
	}
	now := time.Now().UTC()
	j := m.Job{Format: 1, ID: jobID, Space: a.Space, Actor: a.User, ProjectID: id, TargetID: m.TargetID(jobID), Version: 1, State: "accepted", Phase: "preparing", Plan: p, Copies: map[string]m.Proof{}, CreatedAt: now, UpdatedAt: now}
	e = s.migrations.With(ctx, identity, &j, func(l *migrationrepo.Locked) error { j = l.Job; return nil })
	return j, e
}
func migrationOrigin(j m.Job) *projects.MigrationSource {
	v := j.Plan.Source
	return &projects.MigrationSource{MigrationID: j.ID, ProjectID: j.ProjectID, Episode: v.Episode, ProjectVersion: v.ProjectVersion, ScriptVersion: v.ScriptVersion, MediaVersion: v.MediaVersion, SHA256: v.SHA256}
}
func (s *FileStore) checkMigrationSourceLocked(j m.Job) error {
	src, e := s.migrationSnapshotLocked(j.Space, j.ProjectID)
	if e != nil {
		return e
	}
	if m.Fingerprint(src) != j.Plan.Source.SHA256 {
		return m.ErrConflict
	}
	return nil
}
func (s *FileStore) preparationProjectLocked(j m.Job, n series.Node) error {
	if n.MigrationID != j.ID || n.ID != j.TargetID {
		return m.ErrConflict
	}
	origin := migrationOrigin(j)
	if p, ok := s.data.Projects[j.TargetID]; ok {
		if p.SpaceID != j.Space || p.SeriesEpisodeID != n.ID || !reflect.DeepEqual(p.MigrationSource, origin) {
			return m.ErrConflict
		}
		if s.pg != nil {
			return s.commit(s.migrationCoreCloneLocked())
		}
		return nil
	}
	d := s.migrationCoreCloneLocked()
	d.Projects[n.ID] = Project{ID: n.ID, SpaceID: j.Space, Kind: "drama", StructureVersion: 2, SeriesEpisodeID: n.ID, MigrationSource: origin, Title: j.Plan.Title, Brief: j.Plan.Brief, Version: 1, CreatedAt: n.CreatedAt, UpdatedAt: n.CreatedAt}
	return s.commit(d)
}
func (s *FileStore) reserveMigration(ctx context.Context, a series.Actor, j m.Job) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.media.mu.Lock()
	defer s.media.mu.Unlock()
	if e := s.checkMigrationSourceLocked(j); e != nil {
		return e
	}
	p := seriesrepo.Preparation{MigrationID: j.ID, ID: j.TargetID, SeasonID: j.Plan.Input.SeasonID, Ordinal: j.Plan.Input.Ordinal, SeasonVersion: j.Plan.Input.SeasonVersion, Input: series.Input{Title: j.Plan.Title, Summary: j.Plan.Brief, Profile: j.Plan.Profile}}
	if s.pg != nil {
		n, e := s.series.ReserveMigrationTx(ctx, s.pg.tx, a, p)
		if e != nil {
			return e
		}
		return s.preparationProjectLocked(j, n)
	}
	_, e := s.series.ReserveMigration(ctx, a, p, func(n series.Node) error { return s.preparationProjectLocked(j, n) })
	return e
}
func (s *FileStore) migrationTargetLocked(ctx context.Context, j m.Job) (series.Node, error) {
	if s.pg != nil && s.pg.err != nil {
		return series.Node{}, s.pg.err
	}
	n, e := s.series.GetPreparation(ctx, j.Space, j.TargetID)
	if e != nil {
		return n, e
	}
	if n.MigrationID != j.ID {
		return n, m.ErrConflict
	}
	p, ok := s.data.Projects[j.TargetID]
	if !ok {
		if !j.Reserved || j.Phase == "cancelling" {
			return n, series.ErrNotFound
		}
		return n, m.ErrConflict
	}
	if p.SpaceID != j.Space || p.SeriesEpisodeID != j.TargetID || !reflect.DeepEqual(p.MigrationSource, migrationOrigin(j)) {
		return n, m.ErrConflict
	}
	return n, nil
}
func migrationFileProof(ctx context.Context, path string) (m.Proof, error) {
	f, e := openLocalBlob(path)
	if e != nil {
		return m.Proof{}, e
	}
	defer f.Close()
	h := sha256.New()
	n, e := io.CopyBuffer(h, contextBlobReader{ctx, f}, make([]byte, 64<<10))
	if e != nil {
		return m.Proof{}, e
	}
	return m.Proof{SHA256: hex.EncodeToString(h.Sum(nil)), Size: n}, nil
}
func (s *FileStore) copyMigrationAsset(ctx context.Context, j m.Job, copy m.AssetCopy, authorize func() bool) (m.Proof, error) {
	if authorize != nil && !authorize() {
		return m.Proof{}, errRuntimeDenied
	}
	s.mu.Lock()
	s.media.mu.Lock()
	preflight := s.checkMigrationSourceLocked(j)
	if preflight == nil {
		n, e := s.migrationTargetLocked(ctx, j)
		preflight = e
		if e == nil && n.State != "preparing" {
			preflight = m.ErrConflict
		}
	}
	s.media.mu.Unlock()
	s.mu.Unlock()
	if preflight != nil {
		return m.Proof{}, preflight
	}
	// This is an actual byte copy, not a source URL or hard-link alias. A target
	// keeps working when the source cache is absent and owns its own blob key.
	path, e := s.media.localBlob(j.Space, j.ProjectID, copy.Source.ID)
	if e != nil {
		return m.Proof{}, e
	}
	source, e := openLocalBlob(path)
	if e != nil {
		return m.Proof{}, e
	}
	defer source.Close()
	temp, e := os.CreateTemp(s.media.dir, ".migration-copy-")
	if e != nil {
		return m.Proof{}, e
	}
	defer os.Remove(temp.Name())
	h := sha256.New()
	size, e := io.CopyBuffer(io.MultiWriter(temp, h), contextBlobReader{ctx, source}, make([]byte, 64<<10))
	if e == nil {
		e = temp.Sync()
	}
	e = errors.Join(e, temp.Close())
	if e != nil {
		return m.Proof{}, e
	}
	kind, duration, e := validateMedia(temp.Name(), copy.Source.MIMEType)
	if e != nil {
		return m.Proof{}, e
	}
	if kind != copy.Source.Kind || duration-copy.Source.Duration > .05 || copy.Source.Duration-duration > .05 {
		return m.Proof{}, ErrBlobIntegrity
	}
	id := m.AssetID(j.ID, copy.Source.ID)
	proof := m.Proof{TargetID: id, SHA256: hex.EncodeToString(h.Sum(nil)), Size: size}
	destination := filepath.Join(s.media.dir, id)
	if e = os.Link(temp.Name(), destination); e != nil {
		if !errors.Is(e, os.ErrExist) {
			return proof, e
		}
		old, e := migrationFileProof(ctx, destination)
		if e != nil || old.Size != size || old.SHA256 != proof.SHA256 {
			return proof, ErrBlobIntegrity
		}
	}
	if e = syncMultipartDir(s.media.dir); e != nil {
		return proof, e
	}
	if authorize != nil && !authorize() {
		return proof, errRuntimeDenied
	}
	if e = s.media.storeBlobContext(ctx, j.Space, j.TargetID, id); e != nil {
		return proof, e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.media.mu.Lock()
	defer s.media.mu.Unlock()
	if authorize != nil && !authorize() {
		return proof, errRuntimeDenied
	}
	if e = s.checkMigrationSourceLocked(j); e != nil {
		return proof, e
	}
	n, e := s.migrationTargetLocked(ctx, j)
	if e != nil {
		return proof, e
	}
	if n.State != "preparing" {
		return proof, m.ErrConflict
	}
	a := copy.Source
	a.ID = id
	a.Source = "migrated"
	a.CharacterID = copy.CharacterID
	a.Character = copy.Character
	a.ShotID = copy.ShotID
	a.FileURL = mediaURL(j.Space, j.TargetID, "assets/"+id+"/file")
	d := s.media.clone()
	state := mediaState(d, mediaKey(j.Space, j.TargetID))
	for _, old := range state.Assets {
		if old.ID == id {
			if !reflect.DeepEqual(old, a) {
				return proof, m.ErrConflict
			}
			return proof, nil
		}
	}
	state.Assets = append(state.Assets, a)
	state.Version++
	state.Timeline.OutputResolution = j.Plan.Timeline.OutputResolution
	state.Timeline.AspectRatio = j.Plan.Timeline.AspectRatio
	d.Projects[mediaKey(j.Space, j.TargetID)] = state
	return proof, s.media.commit(d)
}
func rewrittenMigrationMedia(j m.Job, current MediaState) (MediaState, error) {
	state, e := m.Clone(current)
	if e != nil {
		return state, e
	}
	ids := map[string]string{}
	for source, p := range j.Copies {
		ids[source] = p.TargetID
	}
	get := func(id string) string {
		if id == "" {
			return ""
		}
		return ids[id]
	}
	state.Timeline, e = m.Clone(j.Plan.Timeline)
	if e != nil {
		return state, e
	}
	t := &state.Timeline
	t.EpisodeNumber = 1
	t.MusicAssetID = get(t.MusicAssetID)
	for i := range t.Clips {
		c := &t.Clips[i]
		c.AssetID = get(c.AssetID)
		c.AudioAssetID = get(c.AudioAssetID)
		c.SourceAudioAssetID = get(c.SourceAudioAssetID)
	}
	for i := range t.AudioTracks {
		t.AudioTracks[i].AssetID = get(t.AudioTracks[i].AssetID)
	}
	for i := range t.VideoOverlays {
		t.VideoOverlays[i].AssetID = get(t.VideoOverlays[i].AssetID)
	}
	state.Selections, e = m.Clone(j.Plan.Selections)
	if e != nil {
		return state, e
	}
	for i := range state.Selections {
		state.Selections[i].AssetID = get(state.Selections[i].AssetID)
	}
	state.Reviews, e = m.Clone(j.Plan.Reviews)
	if e != nil {
		return state, e
	}
	for i := range state.Reviews {
		state.Reviews[i].AssetID = get(state.Reviews[i].AssetID)
	}
	state.LibraryBindings, e = m.Clone(j.Plan.Bindings)
	if e != nil {
		return state, e
	}
	for i := range state.LibraryBindings {
		b := &state.LibraryBindings[i]
		b.ID = series.SHA(j.ID + ":binding:" + b.ID)[:32]
		for k, id := range b.MediaAssetIDs {
			b.MediaAssetIDs[k] = get(id)
		}
	}
	state.EpisodeTimelines = nil
	state.Exports = []MediaExport{}
	state.Social = nil
	state.Poster = nil
	state.PromptApplications = nil
	if len(state.Assets) != len(j.Plan.Assets) {
		return state, m.ErrConflict
	}
	if len(t.Clips) > 0 {
		if e = media.ValidateTimeline(state, *t); e != nil {
			return state, e
		}
	}
	return state, nil
}
func (s *FileStore) verifyMigrationBytes(ctx context.Context, j m.Job) error {
	for _, a := range j.Plan.Assets {
		proof, ok := j.Copies[a.Source.ID]
		if !ok {
			return m.ErrConflict
		}
		for _, owner := range []struct{ project, id string }{{j.ProjectID, a.Source.ID}, {j.TargetID, proof.TargetID}} {
			path, e := s.media.localBlob(j.Space, owner.project, owner.id)
			if e != nil {
				return e
			}
			actual, e := migrationFileProof(ctx, path)
			if e != nil {
				return e
			}
			if actual.SHA256 != proof.SHA256 || actual.Size != proof.Size {
				return ErrBlobIntegrity
			}
		}
	}
	return nil
}
func (s *FileStore) publishMigration(ctx context.Context, a series.Actor, j m.Job, authorize func() bool) error {
	if e := s.verifyMigrationBytes(ctx, j); e != nil {
		return e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.media.mu.Lock()
	defer s.media.mu.Unlock()
	if authorize != nil && !authorize() {
		return errRuntimeDenied
	}
	n, e := s.migrationTargetLocked(ctx, j)
	if e != nil {
		return e
	}
	if n.State == "ready" {
		return nil
	}
	if n.State != "preparing" {
		return m.ErrConflict
	}
	if e = s.checkMigrationSourceLocked(j); e != nil {
		return e
	}
	md := s.media.clone()
	state, e := rewrittenMigrationMedia(j, mediaState(md, mediaKey(j.Space, j.TargetID)))
	if e != nil {
		return e
	}
	state.Version++
	md.Projects[mediaKey(j.Space, j.TargetID)] = state
	if e = s.media.commit(md); e != nil {
		return e
	}
	d := s.migrationCoreCloneLocked()
	p := d.Projects[j.TargetID]
	if p.Version > 2 {
		return m.ErrConflict
	}
	p.Script, e = m.Clone(j.Plan.Script)
	if e != nil {
		return e
	}
	p.Storyboard, e = m.Clone(j.Plan.Storyboard)
	if e != nil {
		return e
	}
	p.ScriptProvider = j.Plan.ScriptProvider
	p.ScriptBrief = j.Plan.Brief
	if p.Script != nil {
		p.ScriptVersion = 1
	}
	p.Version = 2
	p.UpdatedAt = time.Now().UTC()
	d.Projects[p.ID] = p
	if s.pg != nil {
		if e = s.series.FinishMigrationTx(ctx, s.pg.tx, a, j.TargetID, j.ID, false); e != nil {
			return e
		}
		return s.commit(d)
	}
	if e = s.commit(d); e != nil {
		return e
	}
	return s.series.FinishMigration(ctx, a, j.TargetID, j.ID, false)
}
func (s *FileStore) cancelMigration(ctx context.Context, a series.Actor, j m.Job) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.media.mu.Lock()
	defer s.media.mu.Unlock()
	n, e := s.series.GetPreparation(ctx, j.Space, j.TargetID)
	if e != nil && !errors.Is(e, series.ErrNotFound) {
		return e
	}
	if e == nil && (n.MigrationID != j.ID || n.State == "ready") {
		return m.ErrConflict
	}
	p, exists := s.data.Projects[j.TargetID]
	if exists && (p.SpaceID != j.Space || !reflect.DeepEqual(p.MigrationSource, migrationOrigin(j)) || p.Version > 2) {
		return m.ErrConflict
	}
	md := s.media.clone()
	delete(md.Projects, mediaKey(j.Space, j.TargetID))
	if e = s.media.commit(md); e != nil {
		return e
	}
	d := s.migrationCoreCloneLocked()
	delete(d.Projects, j.TargetID)
	delete(d.History, j.TargetID)
	if s.pg != nil {
		if e = s.series.FinishMigrationTx(ctx, s.pg.tx, a, j.TargetID, j.ID, true); e != nil {
			return e
		}
		return s.commit(d)
	}
	if e = s.series.FinishMigration(ctx, a, j.TargetID, j.ID, true); e != nil {
		return e
	}
	return s.commit(d)
}

// ContinueEpisodeMigration performs one durable unit: reserve, one asset copy,
// or final verification/publication. The client can leave and resume later.
// A lost step response with an old version only returns current progress.
func (s *FileStore) ContinueEpisodeMigration(ctx context.Context, a series.Actor, identity migrationrepo.Identity, version int64, cancel bool, authorize func() bool) (m.Job, error) {
	if !a.Writable || a.Space != identity.Space {
		return m.Job{}, m.ErrDenied
	}
	if version < 1 {
		return m.Job{}, m.ErrInput
	}
	var result m.Job
	err := s.migrations.With(ctx, identity, nil, func(l *migrationrepo.Locked) error {
		j := l.Job
		result = j
		if version > j.Version {
			return m.ErrConflict
		}
		if version < j.Version || j.State == "completed" || j.State == "cancelled" {
			return nil
		}
		if authorize != nil && !authorize() {
			return errRuntimeDenied
		}
		// A publish may have committed before its final job response. Recognize
		// it before copying/cancelling and never replace later user edits.
		s.mu.Lock()
		n, e := s.migrationTargetLocked(ctx, j)
		s.mu.Unlock()
		if e == nil && n.State == "ready" {
			j.State = "completed"
			j.Phase = "completed"
			j.Error = ""
			if e = l.Save(j); e != nil {
				return e
			}
			result = l.Job
			return nil
		}
		if e != nil && !errors.Is(e, series.ErrNotFound) && !errors.Is(e, ErrNotFound) {
			return e
		}
		if cancel || j.Phase == "cancelling" {
			j.State = "cancelling"
			j.Phase = "cancelling"
			if e = l.Save(j); e != nil {
				return e
			}
			j = l.Job
			if e = l.Check(); e == nil {
				e = s.cancelMigration(ctx, a, j)
			}
			if e == nil {
				j.State = "cancelled"
				j.Phase = "cancelled"
				j.Error = ""
			}
		}
		if !cancel && j.Phase != "cancelling" && j.State != "cancelled" {
			j.Error = ""
			if !j.Reserved {
				j.State = "preparing"
				j.Phase = "preparing"
				if e = l.Save(j); e != nil {
					return e
				}
				j = l.Job
				if e = l.Check(); e == nil {
					e = s.reserveMigration(ctx, a, j)
				}
				if e == nil {
					j.Reserved = true
					j.State = "copying"
					j.Phase = "copying"
				}
			} else {
				var next *m.AssetCopy
				for i := range j.Plan.Assets {
					if _, ok := j.Copies[j.Plan.Assets[i].Source.ID]; !ok {
						next = &j.Plan.Assets[i]
						break
					}
				}
				if next != nil {
					j.State = "copying"
					j.Phase = "copying"
					if e = l.Save(j); e != nil {
						return e
					}
					j = l.Job
					var proof m.Proof
					if e = l.Check(); e == nil {
						proof, e = s.copyMigrationAsset(ctx, j, *next, authorize)
					}
					if e == nil {
						j.Copies[next.Source.ID] = proof
					}
				} else {
					j.State = "validating"
					j.Phase = "validating"
					if e = l.Save(j); e != nil {
						return e
					}
					j = l.Job
					if e = l.Check(); e == nil {
						e = s.publishMigration(ctx, a, j, authorize)
					}
					if e == nil {
						j.State = "completed"
						j.Phase = "completed"
					}
				}
			}
		}
		if e != nil {
			if errors.Is(e, errRuntimeDenied) {
				return e
			}
			j.State = "paused"
			j.Error = "迁移已暂停；原项目保留。请核对源版本、素材完整性及目标目录后再继续"
			if errors.Is(e, m.ErrConflict) || errors.Is(e, series.ErrConflict) {
				j.Error = "源项目或目标目录已变化，请保留原项目，停止此迁移后重新预览"
			}
			saveErr := l.Save(j)
			result = l.Job
			return errors.Join(e, saveErr)
		}
		if e = l.Save(j); e != nil {
			return e
		}
		result = l.Job
		return nil
	})
	return result, err
}

func (s *FileStore) validPendingMigration(ctx context.Context, p Project) bool {
	if s.migrations == nil || p.MigrationSource == nil || p.SeriesEpisodeID != p.ID {
		return false
	}
	origin := p.MigrationSource
	j, e := s.migrations.Get(ctx, migrationrepo.Identity{Space: p.SpaceID, Project: origin.ProjectID, ID: origin.MigrationID})
	if e != nil || j.TargetID != p.ID || !reflect.DeepEqual(origin, migrationOrigin(j)) {
		return false
	}
	n, e := s.series.GetPreparation(ctx, p.SpaceID, p.ID)
	if j.Phase == "cancelling" && (errors.Is(e, series.ErrNotFound) || e == nil && n.State == "cancelled") {
		return true
	}
	return e == nil && n.State == "preparing" && n.MigrationID == j.ID && j.State != "completed" && j.State != "cancelled"
}
