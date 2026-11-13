package application

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/library"
	"github.com/adcwb/ai-creative-studio/backend/internal/modules/media"
	"github.com/adcwb/ai-creative-studio/backend/internal/modules/revisions"
	"github.com/adcwb/ai-creative-studio/backend/internal/persistence/libraryrepo"
	"github.com/adcwb/ai-creative-studio/backend/internal/persistence/revisionrepo"
	"github.com/adcwb/ai-creative-studio/backend/internal/platform/nativefs"
)

func copyIdentity(job, kind, id string) string {
	return revisionrepo.Hash(job + ":" + kind + ":" + id)[:32]
}
func (s *FileStore) personalCopyAllowed(actor, source, target string) bool {
	if s.runtime == nil || s.runtime.auth == nil {
		return false
	}
	a := s.runtime.auth
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.pg != nil && a.pg.err != nil {
		return false
	}
	from, ok := a.data.Organizations[source]
	to, found := a.data.Organizations[target]
	return ok && found && spaceType(from) == "personal" && from.OwnerID == actor && spaceType(to) == "team" && a.canLocked(actor, source, "workspace.read") && a.canLocked(actor, target, "workspace.write")
}
func (s *FileStore) personalOwner(actor, space string) bool {
	if s.runtime == nil || s.runtime.auth == nil {
		return false
	}
	a := s.runtime.auth
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.pg != nil && a.pg.err != nil {
		return false
	}
	o, ok := a.data.Organizations[space]
	return ok && spaceType(o) == "personal" && o.OwnerID == actor && a.canLocked(actor, space, "workspace.read")
}

func (s *FileStore) personalCopySnapshot(ctx context.Context, space, project string) (personalCopySnapshot, error) {
	s.mu.Lock()
	p, e := s.projectLocked(space, project)
	if e != nil {
		s.mu.Unlock()
		return personalCopySnapshot{}, e
	}
	if s.generationActiveLocked(p) {
		s.mu.Unlock()
		return personalCopySnapshot{}, ErrGenerationActive
	}
	if p.Kind != "" && p.Kind != "drama" {
		s.mu.Unlock()
		return personalCopySnapshot{}, projectInputError("当前复制支持短剧项目；图片任务历史和数字人原生档案仍留在原空间，不会创建不完整副本")
	}
	p = cloneProject(p)
	history := cloneHistory(s.data.History)[project]
	s.mu.Unlock()
	state, e := s.media.GetChecked(project, space)
	if e != nil {
		return personalCopySnapshot{}, e
	}
	for _, v := range state.Exports {
		if v.Status == "queued" || v.Status == "running" {
			return personalCopySnapshot{}, ErrGenerationActive
		}
	}
	// Operational handles, connections and resumable provider jobs never cross
	// tenants. The pinned authored work and immutable history do.
	p.LatestGenerationID = ""
	p.LatestGeneration = nil
	p.LatestStoryboardGenerationID = ""
	p.LatestStoryboardGeneration = nil
	p.PersonalCopyID = ""
	state.Social = nil
	journal, e := s.revisions.SnapshotScope(ctx, revisionrepo.Scope{Space: space, Project: project})
	if e != nil {
		return personalCopySnapshot{}, e
	}
	out := personalCopySnapshot{Project: p, Media: state, History: history, Revisions: journal, Files: []personalCopyFile{}, Sources: []media.LibrarySource{}}
	for i := range out.Media.Assets {
		a := &out.Media.Assets[i]
		sources, sha, _, err := s.libraryMediaSources(ctx, project, space, a.ID)
		if err != nil && !errors.Is(err, errMediaLineageUnknown) {
			return out, err
		}
		a.ProvenanceUnknown = errors.Is(err, errMediaLineageUnknown)
		a.ProvenanceVersion = 1
		a.ParentAssetIDs = nil
		a.LibrarySources = sources
		for _, v := range sources {
			if v.ShareID != "" {
				return out, projectInputError("此作品已有跨空间共享来源，请保留原共享链；当前不能再次转授")
			}
		}
		path, err := s.media.localBlob(space, project, a.ID)
		if err != nil {
			return out, err
		}
		proof, err := migrationFileProof(ctx, path)
		if err != nil {
			return out, err
		}
		if sha != "" && sha != proof.SHA256 {
			return out, ErrBlobIntegrity
		}
		a.ContentSHA256 = proof.SHA256
		out.Files = append(out.Files, personalCopyFile{Source: a.ID, SHA256: proof.SHA256, Size: proof.Size})
		out.Sources = append(out.Sources, sources...)
	}
	for _, b := range state.LibraryBindings {
		if b.ShareID != "" {
			return out, projectInputError("此作品已有跨空间共享来源，请保留原共享链；当前不能再次转授")
		}
		if _, e = s.library.Version(ctx, space, b.AssetID, b.VersionID); e != nil {
			return out, e
		}
		out.Sources = append(out.Sources, media.LibrarySource{AssetID: b.AssetID, VersionID: b.VersionID})
	}
	// Historical journals may pin an earlier library version. Sharing that
	// exact recorded input never grants access to the rest of the library.
	assetHashes := map[string]string{}
	for _, a := range out.Media.Assets {
		assetHashes[a.ID] = a.ContentSHA256
	}
	for _, record := range journal {
		if record.Revision == nil {
			continue
		}
		for _, ref := range record.Revision.Manifest.References {
			if ref.Kind == "media" && assetHashes[ref.ID] != ref.SHA256 {
				return out, projectInputError("制作修订引用了缺失或已变化的素材，请先修复源作品历史后再复制")
			}
			if ref.Kind == "library" {
				v, e := s.library.Version(ctx, space, ref.ID, ref.VersionID)
				if e != nil {
					return out, e
				}
				if v.SHA256 != ref.SHA256 {
					return out, ErrBlobIntegrity
				}
				out.Sources = append(out.Sources, media.LibrarySource{AssetID: ref.ID, VersionID: ref.VersionID})
			}
		}
	}
	for _, v := range state.Exports {
		if v.Status != "succeeded" {
			continue
		}
		for _, ext := range []string{"mp4", "srt", "vtt"} {
			name := v.ID + "." + ext
			path, e := s.media.localBlob(space, project, name)
			if e != nil {
				return out, e
			}
			proof, e := migrationFileProof(ctx, path)
			if e != nil {
				return out, e
			}
			out.Files = append(out.Files, personalCopyFile{Source: name, SHA256: proof.SHA256, Size: proof.Size})
		}
	}
	out.Sources = uniqueLibrarySources(out.Sources)
	sort.Slice(out.Files, func(i, j int) bool { return out.Files[i].Source < out.Files[j].Source })
	// Check capture clocks after sequential I/O. Source edits after acceptance
	// remain independent; copying always uses this explicitly selected snapshot.
	current, e := s.GetProject(space, project)
	if e != nil {
		return out, e
	}
	latest, e := s.media.GetChecked(project, space)
	if e != nil {
		return out, e
	}
	r, e := s.revisions.SnapshotScope(ctx, revisionrepo.Scope{Space: space, Project: project})
	if e != nil {
		return out, e
	}
	if current.Version != p.Version || latest.Version != state.Version || personalCopyHash(r) != personalCopyHash(journal) {
		return out, ErrVersionConflict
	}
	return out, nil
}
func personalCopyHash(v any) string {
	sha, e := revisions.Digest(revisions.Manifest{Brief: rawRevision(v), Script: json.RawMessage("null"), Storyboard: json.RawMessage("null"), Timeline: json.RawMessage("null"), Poster: json.RawMessage("null"), References: []revisions.Ref{}})
	if e != nil {
		return ""
	}
	return sha
}
func personalCopyDigest(space, target string, snap personalCopySnapshot) string {
	return personalCopyHash([]any{"personal-copy-v1", space, target, snap})
}
func (s *FileStore) createPersonalCopy(ctx context.Context, actor, space, project, target, key, digest string) (personalCopyJob, error) {
	if !s.personalCopyAllowed(actor, space, target) {
		return personalCopyJob{}, errRuntimeDenied
	}
	id := copyIdentity(actor+":"+space, "request", key)
	if old, e := s.personalCopies.get(id); e == nil {
		if old.Actor != actor || old.Space != space || old.ProjectID != project || old.TargetSpace != target || old.SHA256 != digest {
			return old, ErrIdempotencyConflict
		}
		return old, nil
	} else if !errors.Is(e, ErrNotFound) {
		return old, e
	}
	snap, e := s.personalCopySnapshot(ctx, space, project)
	if e != nil {
		return personalCopyJob{}, e
	}
	if personalCopyDigest(space, target, snap) != digest {
		return personalCopyJob{}, ErrVersionConflict
	}
	j := personalCopyJob{ID: id, Space: space, ProjectID: project, Actor: actor, TargetSpace: target, TargetID: copyIdentity(id, "project", project), Version: 1, State: "copying", SHA256: digest, Snapshot: snap, Copied: map[string]bool{}, Verified: map[string]bool{}, CreatedAt: time.Now().UTC()}
	for i := range j.Snapshot.Files {
		f := &j.Snapshot.Files[i]
		base, ext := f.Source, ""
		for _, x := range []string{".mp4", ".srt", ".vtt"} {
			if strings.HasSuffix(base, x) {
				base = strings.TrimSuffix(base, x)
				ext = x
				break
			}
		}
		f.Target = copyIdentity(id, "media", base) + ext
	}
	e = s.personalCopies.change(ctx, id, func(d *personalCopyDisk) error {
		if old, ok := d.Jobs[id]; ok {
			if old.SHA256 != digest || old.TargetSpace != target {
				return ErrIdempotencyConflict
			}
			j = old
			return nil
		}
		if !s.personalCopyAllowed(actor, space, target) {
			return errRuntimeDenied
		}
		d.Jobs[id] = j
		return nil
	})
	return j, e
}
func (s *FileStore) copyPersonalFile(ctx context.Context, j personalCopyJob, f personalCopyFile) error {
	if !s.personalCopyAllowed(j.Actor, j.Space, j.TargetSpace) {
		return errRuntimeDenied
	}
	path, e := s.media.localBlob(j.Space, j.ProjectID, f.Source)
	if e != nil {
		return e
	}
	src, e := openLocalBlob(path)
	if e != nil {
		return e
	}
	defer src.Close()
	tmp, e := os.CreateTemp(s.media.dir, ".personal-copy-")
	if e != nil {
		return e
	}
	defer os.Remove(tmp.Name())
	n, sha, e := copyDeliveryFile(ctx, tmp, src)
	if e == nil && (n != f.Size || sha != f.SHA256) {
		e = ErrBlobIntegrity
	}
	if e == nil {
		e = nativefs.SyncFile(tmp)
	}
	e = errors.Join(e, tmp.Close())
	if e != nil {
		return e
	}
	for _, a := range j.Snapshot.Media.Assets {
		if a.ID == f.Source {
			kind, duration, err := validateMedia(tmp.Name(), a.MIMEType)
			if err != nil {
				return err
			}
			if kind != a.Kind || duration-a.Duration > .05 || a.Duration-duration > .05 {
				return ErrBlobIntegrity
			}
			break
		}
	}
	dest := filepath.Join(s.media.dir, f.Target)
	if e = os.Link(tmp.Name(), dest); e != nil {
		if !os.IsExist(e) {
			return e
		}
		proof, e := migrationFileProof(ctx, dest)
		if e != nil || proof.SHA256 != f.SHA256 || proof.Size != f.Size {
			return ErrBlobIntegrity
		}
	}
	if e = syncMultipartDir(s.media.dir); e != nil {
		return e
	}
	if !s.personalCopyAllowed(j.Actor, j.Space, j.TargetSpace) {
		return errRuntimeDenied
	}
	return s.media.storeBlobContext(ctx, j.TargetSpace, j.TargetID, f.Target)
}
func personalCopyIDs(j personalCopyJob) map[string]string {
	ids := map[string]string{}
	for _, a := range j.Snapshot.Media.Assets {
		ids[a.ID] = copyIdentity(j.ID, "media", a.ID)
	}
	for _, v := range j.Snapshot.Media.Exports {
		ids[v.ID] = copyIdentity(j.ID, "media", v.ID)
	}
	return ids
}
func rewriteCopyTimeline(t *MediaTimeline, ids map[string]string) error {
	get := func(id *string) error {
		if *id == "" {
			return nil
		}
		v, ok := ids[*id]
		if !ok {
			return ErrBlobIntegrity
		}
		*id = v
		return nil
	}
	if e := get(&t.MusicAssetID); e != nil {
		return e
	}
	for i := range t.Clips {
		c := &t.Clips[i]
		for _, v := range []*string{&c.AssetID, &c.AudioAssetID, &c.SourceAudioAssetID} {
			if e := get(v); e != nil {
				return e
			}
		}
		if c.SubtitleAlignmentID != "" {
			c.SubtitleAlignmentID = ""
			c.SubtitleSource = "imported"
		}
	}
	for i := range t.AudioTracks {
		if e := get(&t.AudioTracks[i].AssetID); e != nil {
			return e
		}
	}
	for i := range t.VideoOverlays {
		if e := get(&t.VideoOverlays[i].AssetID); e != nil {
			return e
		}
	}
	return nil
}
func rewrittenPersonalMedia(j personalCopyJob) (MediaState, error) {
	var state MediaState
	if e := json.Unmarshal(rawRevision(j.Snapshot.Media), &state); e != nil {
		return state, e
	}
	ids := personalCopyIDs(j)
	for i := range state.Assets {
		a := &state.Assets[i]
		a.ID = ids[a.ID]
		a.FileURL = mediaURL(j.TargetSpace, j.TargetID, "assets/"+a.ID+"/file")
		for k := range a.LibrarySources {
			a.LibrarySources[k].ShareID = j.ID
		}
	}
	if e := rewriteCopyTimeline(&state.Timeline, ids); e != nil {
		return state, e
	}
	for ep, t := range state.EpisodeTimelines {
		if e := rewriteCopyTimeline(&t, ids); e != nil {
			return state, e
		}
		state.EpisodeTimelines[ep] = t
	}
	for i := range state.LibraryBindings {
		b := &state.LibraryBindings[i]
		b.ID = copyIdentity(j.ID, "binding", b.ID)
		b.ShareID = j.ID
		for k, id := range b.MediaAssetIDs {
			next, ok := ids[id]
			if !ok {
				return state, ErrBlobIntegrity
			}
			b.MediaAssetIDs[k] = next
		}
	}
	for i := range state.Selections {
		state.Selections[i].ID = copyIdentity(j.ID, "selection", state.Selections[i].ID)
		state.Selections[i].AssetID = ids[state.Selections[i].AssetID]
	}
	for i := range state.Reviews {
		state.Reviews[i].ID = copyIdentity(j.ID, "review", state.Reviews[i].ID)
		state.Reviews[i].AssetID = ids[state.Reviews[i].AssetID]
	}
	for i := range state.Exports {
		v := &state.Exports[i]
		v.ID = ids[v.ID]
		v.TraceParent = ""
		v.RenderProgress = nil
		v.ResumeAvailable = false
		v.RenderVersion = 0
		if e := rewriteCopyTimeline(&v.Timeline, ids); e != nil {
			return state, e
		}
		v.FileURL = ""
		v.SubtitleURL = ""
		v.SubtitleVTTURL = ""
		if v.Status == "succeeded" {
			v.FileURL = mediaURL(j.TargetSpace, j.TargetID, "exports/"+v.ID+"/file")
			v.SubtitleURL = mediaURL(j.TargetSpace, j.TargetID, "exports/"+v.ID+"/subtitles")
			v.SubtitleVTTURL = mediaURL(j.TargetSpace, j.TargetID, "exports/"+v.ID+"/subtitles.vtt")
		}
	}
	for i := range state.DeliveryMetadata {
		v := &state.DeliveryMetadata[i]
		v.ID = copyIdentity(j.ID, "release", v.ID)
		v.ExportID = ids[v.ExportID]
		if v.CoverAssetID != "" {
			v.CoverAssetID = ids[v.CoverAssetID]
			if v.CoverAssetID == "" {
				return state, ErrBlobIntegrity
			}
		}
	}
	if state.Poster != nil {
		for i := range state.Poster.Layers {
			l := &state.Poster.Layers[i]
			if l.AssetID != "" {
				l.AssetID = ids[l.AssetID]
				if l.AssetID == "" {
					return state, ErrBlobIntegrity
				}
			}
		}
	}
	// Preserve the already applied recipe as owned work history. Original
	// template IDs remain provenance; their repository permissions do not move.
	for i := range state.PromptApplications {
		a := &state.PromptApplications[i]
		old := a.ID
		a.ID = copyIdentity(j.ID, "prompt-application", old)
		if a.Source.ApplicationID == old {
			a.Source.ApplicationID = a.ID
		}
		for k, id := range a.ReferenceAssetIDs {
			next, ok := ids[id]
			if !ok {
				return state, ErrBlobIntegrity
			}
			a.ReferenceAssetIDs[k] = next
		}
	}
	state.Social = nil
	state.Version = 1
	if len(state.Timeline.Clips) > 0 {
		if e := media.ValidateTimeline(state, state.Timeline); e != nil {
			return state, e
		}
	}
	for _, t := range state.EpisodeTimelines {
		if len(t.Clips) > 0 {
			if e := media.ValidateTimeline(state, t); e != nil {
				return state, e
			}
		}
	}
	return state, nil
}
func rewritePersonalManifest(j personalCopyJob, m revisions.Manifest) (revisions.Manifest, error) {
	ids := personalCopyIDs(j)
	var t MediaTimeline
	if e := jsonUnmarshal(m.Timeline, &t); e != nil {
		return m, e
	}
	if e := rewriteCopyTimeline(&t, ids); e != nil {
		return m, e
	}
	m.Timeline = rawRevision(t)
	var src revisionSources
	if len(m.Sources) > 0 {
		if e := jsonUnmarshal(m.Sources, &src); e != nil {
			return m, e
		}
		for ep, t := range src.EpisodeTimelines {
			if e := rewriteCopyTimeline(&t, ids); e != nil {
				return m, e
			}
			src.EpisodeTimelines[ep] = t
		}
		src.SeriesEpisodeID = ""
		// These are immutable authored snapshots, not credentials, live jobs or
		// grants to their original template/story repositories.
		for i := range src.PromptApplications {
			a := &src.PromptApplications[i]
			old := a.ID
			a.ID = copyIdentity(j.ID, "prompt-application", old)
			if a.Source.ApplicationID == old {
				a.Source.ApplicationID = a.ID
			}
			for k, id := range a.ReferenceAssetIDs {
				next, ok := ids[id]
				if !ok {
					return m, ErrBlobIntegrity
				}
				a.ReferenceAssetIDs[k] = next
			}
		}
		for i := range src.Jobs {
			v := &src.Jobs[i]
			if v.AssetID != "" {
				v.AssetID = ids[v.AssetID]
			}
			for k, id := range v.ReferenceIDs {
				v.ReferenceIDs[k] = ids[id]
			}
			for _, id := range []*string{&v.FirstFrameID, &v.LastFrameID, &v.MotionID} {
				if *id != "" {
					*id = ids[*id]
				}
			}
			for k := range v.LibraryVersions {
				v.LibraryVersions[k].ShareID = j.ID
			}
		}
		for i := range src.Assets {
			a := &src.Assets[i].Asset
			next, ok := ids[a.ID]
			if !ok {
				return m, ErrBlobIntegrity
			}
			for _, current := range j.Snapshot.Media.Assets {
				if current.ID == a.ID {
					a.ProvenanceVersion = current.ProvenanceVersion
					a.ProvenanceUnknown = current.ProvenanceUnknown
					a.ContentSHA256 = current.ContentSHA256
					a.LibrarySources = append([]media.LibrarySource(nil), current.LibrarySources...)
					break
				}
			}
			a.ID = next
			a.ParentAssetIDs = nil
			a.FileURL = mediaURL(j.TargetSpace, j.TargetID, "assets/"+next+"/file")
			for k := range a.LibrarySources {
				a.LibrarySources[k].ShareID = j.ID
			}
		}
		for i := range src.LibraryBindings {
			b := &src.LibraryBindings[i]
			b.ID = copyIdentity(j.ID, "binding", b.ID)
			b.ShareID = j.ID
			for k, v := range b.MediaAssetIDs {
				b.MediaAssetIDs[k] = ids[v]
			}
		}
		for i := range src.Selections {
			src.Selections[i].AssetID = ids[src.Selections[i].AssetID]
		}
		m.Sources = rawRevision(src)
	}
	var poster *media.PosterDocument
	if e := jsonUnmarshal(m.Poster, &poster); e != nil {
		return m, e
	}
	if poster != nil {
		for i := range poster.Layers {
			if poster.Layers[i].AssetID != "" {
				poster.Layers[i].AssetID = ids[poster.Layers[i].AssetID]
			}
		}
	}
	m.Poster = rawRevision(poster)
	for i := range m.References {
		r := &m.References[i]
		if r.Kind == "media" || r.Kind == "media-asset" {
			next, ok := ids[r.ID]
			if !ok {
				return m, ErrBlobIntegrity
			}
			r.ID = next
		}
	}
	return m, nil
}

func (s *FileStore) continuePersonalCopy(ctx context.Context, id string, expected ...int) (personalCopyJob, error) {
	j, e := s.personalCopies.get(id)
	if e != nil {
		return j, e
	}
	if len(expected) > 0 && j.Version != expected[0] {
		return j, nil
	}
	if !s.personalCopyAllowed(j.Actor, j.Space, j.TargetSpace) {
		return j, errRuntimeDenied
	}
	if j.State == "completed" {
		return j, nil
	}
	if j.Revoked || j.State != "copying" {
		return j, errRuntimeDenied
	}
	for _, f := range j.Snapshot.Files {
		if j.Copied[f.Source] {
			continue
		}
		if e = s.copyPersonalFile(ctx, j, f); e != nil {
			return j, e
		}
		e = s.personalCopies.change(ctx, id, func(d *personalCopyDisk) error {
			v := d.Jobs[id]
			if v.Revoked {
				return errRuntimeDenied
			}
			if !v.Copied[f.Source] {
				v.Copied[f.Source] = true
				v.Version++
			}
			d.Jobs[id] = v
			j = v
			return nil
		})
		return j, e
	}
	// Verify one immutable destination per checkpoint. A large work does not
	// restart its entire verification scan whenever the browser reconnects.
	for _, f := range j.Snapshot.Files {
		if j.Verified[f.Source] {
			continue
		}
		path, e := s.media.localBlob(j.TargetSpace, j.TargetID, f.Target)
		if e != nil {
			return j, e
		}
		proof, e := migrationFileProof(ctx, path)
		if e != nil || proof.SHA256 != f.SHA256 || proof.Size != f.Size {
			return j, ErrBlobIntegrity
		}
		e = s.personalCopies.change(ctx, id, func(d *personalCopyDisk) error {
			v := d.Jobs[id]
			if v.Revoked {
				return errRuntimeDenied
			}
			if !v.Verified[f.Source] {
				v.Verified[f.Source] = true
				v.Version++
			}
			d.Jobs[id] = v
			j = v
			return nil
		})
		return j, e
	}
	state, e := rewrittenPersonalMedia(j)
	if e != nil {
		return j, e
	}
	records, e := revisionrepo.CloneRecords(j.Snapshot.Revisions, revisionrepo.Scope{Space: j.TargetSpace, Project: j.TargetID}, j.ID, func(m revisions.Manifest) (revisions.Manifest, error) { return rewritePersonalManifest(j, m) })
	if e != nil {
		return j, e
	}
	s.mu.Lock()
	if old, ok := s.data.Projects[j.TargetID]; ok {
		if old.SpaceID != j.TargetSpace || old.PersonalCopyID != j.ID {
			s.mu.Unlock()
			return j, ErrIdempotencyConflict
		}
		fresh, e := s.personalCopies.get(id)
		if e != nil || fresh.State == "completed" {
			s.mu.Unlock()
			return fresh, e
		}
	} else {
		d := s.migrationCoreCloneLocked()
		p := j.Snapshot.Project
		p.ID = j.TargetID
		p.SpaceID = j.TargetSpace
		p.PersonalCopyID = j.ID
		p.SeriesEpisodeID = ""
		p.MigrationSource = nil
		p.Version = 1
		p.CreatedAt = j.CreatedAt
		p.UpdatedAt = j.CreatedAt
		d.Projects[p.ID] = p
		d.History[p.ID] = j.Snapshot.History
		for i := range d.History[p.ID] {
			d.History[p.ID][i].ID = copyIdentity(j.ID, "history", d.History[p.ID][i].ID)
		}
		e = s.commit(d)
		if e != nil {
			s.mu.Unlock()
			return j, e
		}
	}
	s.mu.Unlock()
	if e = s.revisions.ImportCopy(ctx, revisionrepo.Scope{Space: j.TargetSpace, Project: j.TargetID}, records); e != nil {
		return j, e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	fresh, e := s.personalCopies.get(id)
	if e != nil {
		return j, e
	}
	if fresh.State == "completed" {
		return fresh, nil
	}
	if fresh.Revoked || !s.personalCopyAllowed(j.Actor, j.Space, j.TargetSpace) {
		return j, errRuntimeDenied
	}
	s.media.mu.Lock()
	d := s.media.clone()
	d.Projects[mediaKey(j.TargetSpace, j.TargetID)] = state
	e = s.media.commit(d)
	s.media.mu.Unlock()
	if e != nil {
		return j, e
	}
	e = s.personalCopies.change(ctx, id, func(d *personalCopyDisk) error {
		v := d.Jobs[id]
		if v.Revoked || !s.personalCopyAllowed(j.Actor, j.Space, j.TargetSpace) {
			return errRuntimeDenied
		}
		v.State = "completed"
		v.Version++
		d.Jobs[id] = v
		j = v
		return nil
	})
	return j, e
}

// A shared version is resolved only through a server-written copy grant scoped
// to this destination and exact immutable source. It never grants library browse
// access to the personal space. Revocation is checked again before dispatch.
func (s *FileStore) librarySourceSpace(ctx context.Context, space string, source media.LibrarySource) (string, error) {
	if e := ctx.Err(); e != nil {
		return "", e
	}
	if source.ShareID == "" {
		return space, nil
	}
	j, e := s.personalCopies.get(source.ShareID)
	if e != nil {
		return "", e
	}
	if j.TargetSpace != space || j.State != "completed" || j.Revoked {
		return "", providerFailure("library_use_denied")
	}
	for _, v := range j.Snapshot.Sources {
		if v.AssetID == source.AssetID && v.VersionID == source.VersionID && v.ShareID == "" {
			return j.Space, nil
		}
	}
	return "", providerFailure("library_use_denied")
}

// Keep the decoder typed; authored strings are never globally ID-replaced.
func jsonUnmarshal(b []byte, v any) error { return json.Unmarshal(b, v) }

func (s *FileStore) validPendingPersonalCopy(p Project) bool {
	j, e := s.personalCopies.get(p.PersonalCopyID)
	return e == nil && j.TargetID == p.ID && j.TargetSpace == p.SpaceID && j.State == "copying"
}

func (s *FileStore) sharedLibraryAsset(ctx context.Context, space string, source media.LibrarySource) (libraryrepo.Result, error) {
	origin, e := s.librarySourceSpace(ctx, space, source)
	if e != nil {
		return libraryrepo.Result{}, e
	}
	return s.library.Get(ctx, origin, source.AssetID)
}
func (s *FileStore) sharedLibraryVersion(ctx context.Context, space string, source media.LibrarySource) (library.Version, error) {
	origin, e := s.librarySourceSpace(ctx, space, source)
	if e != nil {
		return library.Version{}, e
	}
	return s.library.Version(ctx, origin, source.AssetID, source.VersionID)
}
