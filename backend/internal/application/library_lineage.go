package application

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"

	m "github.com/adcwb/ai-creative-studio/backend/internal/modules/seriesmigration"
	"github.com/adcwb/ai-creative-studio/backend/internal/persistence/migrationrepo"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/library"
	"github.com/adcwb/ai-creative-studio/backend/internal/modules/media"
)

var errMediaLineageUnknown = errors.New("media source provenance is not recoverable")

type lineageCreativeSnapshotKey struct{}
type lineageCoreSnapshotKey struct{}
type lineageTraceKey struct{}
type lineageWalkKey struct{}
type lineageNodeKey struct {
	Space, Project, ID string
	Verify             bool
}
type lineageCached struct {
	Sources   []media.LibrarySource
	SHA, MIME string
	Err       error
}
type lineageWalk struct {
	Nodes  map[lineageNodeKey]lineageCached
	States map[string]MediaState
}

func withLineageWalk(ctx context.Context) context.Context {
	if _, ok := ctx.Value(lineageWalkKey{}).(*lineageWalk); ok {
		return ctx
	}
	return context.WithValue(ctx, lineageWalkKey{}, &lineageWalk{Nodes: map[lineageNodeKey]lineageCached{}, States: map[string]MediaState{}})
}

type lineageAssetHintsKey struct{}
type lineageTrace struct{ Recovered bool }

func markRecoveredLineage(ctx context.Context) {
	if trace, ok := ctx.Value(lineageTraceKey{}).(*lineageTrace); ok {
		trace.Recovered = true
	}
}

// Old bindings are resolved from their trusted version/reference order. A display
// string or a client supplied source ID is never sufficient proof of lineage.
func (s *FileStore) libraryMediaSources(ctx context.Context, project, space, id string) ([]media.LibrarySource, string, string, error) {
	return s.libraryMediaSourcesVisited(withLineageWalk(ctx), project, space, id, map[string]bool{}, true)
}
func (s *FileStore) libraryMediaSourcesVisited(ctx context.Context, project, space, id string, visiting map[string]bool, verify bool) (outSources []media.LibrarySource, outSHA, outMIME string, resultErr error) {
	if e := ctx.Err(); e != nil {
		return nil, "", "", e
	}
	ctx = withLineageWalk(ctx)
	walk := ctx.Value(lineageWalkKey{}).(*lineageWalk)
	nodeKey := lineageNodeKey{space, project, id, verify}
	if cached, ok := walk.Nodes[nodeKey]; ok {
		return append([]media.LibrarySource(nil), cached.Sources...), cached.SHA, cached.MIME, cached.Err
	}
	defer func() {
		walk.Nodes[nodeKey] = lineageCached{append([]media.LibrarySource(nil), outSources...), outSHA, outMIME, resultErr}
	}()
	visitID := mediaKey(space, project) + ":" + id
	if visiting[visitID] {
		return nil, "", "", ErrBlobIntegrity
	}
	visiting[visitID] = true
	defer delete(visiting, visitID)
	stateKey := mediaKey(space, project)
	state, hasState := walk.States[stateKey]
	if !hasState {
		var e error
		state, e = s.media.lineageState(project, space)
		if e != nil {
			return nil, "", "", e
		}
		walk.States[stateKey] = state
	}
	var asset *MediaAsset
	for i := range state.Assets {
		if state.Assets[i].ID == id {
			asset = &state.Assets[i]
			break
		}
	}
	if asset == nil {
		if hints, ok := ctx.Value(lineageAssetHintsKey{}).(map[string]MediaAsset); ok {
			if hint, found := hints[visitID]; found {
				asset = &hint
			}
		}
	}
	if asset == nil {
		return nil, "", "", ErrNotFound
	}
	if asset.ProvenanceUnknown {
		return nil, "", "", errMediaLineageUnknown
	}
	if asset.ProvenanceVersion != 0 && asset.ProvenanceVersion != 1 {
		return nil, "", "", ErrBlobIntegrity
	}
	sources := append([]media.LibrarySource(nil), asset.LibrarySources...)
	expected := asset.ContentSHA256
	var origin *legacyCreativeOrigin
	migrationRecovered := false
	if asset.Source == "migrated" && asset.ProvenanceVersion == 0 && len(sources) == 0 {
		recovered, sha, e := s.legacyMigrationSources(ctx, project, space, *asset, visiting)
		if e != nil {
			return nil, "", "", e
		}
		sources = recovered
		expected = sha
		migrationRecovered = true
		markRecoveredLineage(ctx)
	}
	for _, parent := range asset.ParentAssetIDs {
		if !library.IDPattern.MatchString(parent) {
			return nil, "", "", ErrBlobIntegrity
		}
		inherited, _, _, e := s.libraryMediaSourcesVisited(ctx, project, space, parent, visiting, false)
		if e != nil {
			return nil, "", "", e
		}
		sources = append(sources, inherited...)
	}

	if len(sources) == 0 && !migrationRecovered {
		for _, b := range state.LibraryBindings {
			for i, mid := range b.MediaAssetIDs {
				if mid != id {
					continue
				}
				if s.library == nil {
					return nil, "", "", errRuntimeDenied
				}
				v, e := s.library.Version(ctx, space, b.AssetID, b.VersionID)
				if e != nil {
					return nil, "", "", e
				}
				if i >= len(v.References) || v.References[i].MIMEType != asset.MIMEType {
					return nil, "", "", ErrBlobIntegrity
				}
				if expected != "" && expected != v.References[i].SHA256 {
					return nil, "", "", ErrBlobIntegrity
				}
				markRecoveredLineage(ctx)
				expected = v.References[i].SHA256
				sources = append(sources, media.LibrarySource{AssetID: b.AssetID, VersionID: b.VersionID})
				inherited, e := s.referenceLineage(ctx, space, v.References[i], visiting)
				if e != nil {
					return nil, "", "", e
				}
				sources = append(sources, inherited...)
			}
		}
	}

	if len(sources) == 0 && !migrationRecovered {
		for _, app := range state.PromptApplications {
			for i, mid := range app.ReferenceAssetIDs {
				if mid != id {
					continue
				}
				if i >= len(app.Source.ReferenceBindings) || s.library == nil {
					return nil, "", "", ErrBlobIntegrity
				}
				binding := app.Source.ReferenceBindings[i]
				v, e := s.library.Version(ctx, space, binding.AssetID, binding.VersionID)
				if e != nil {
					return nil, "", "", e
				}
				var ref *library.Reference
				for j := range v.References {
					if v.References[j].ID == binding.ReferenceID {
						ref = &v.References[j]
						break
					}
				}
				if ref == nil || ref.MIMEType != asset.MIMEType || expected != "" && expected != ref.SHA256 {
					return nil, "", "", ErrBlobIntegrity
				}
				markRecoveredLineage(ctx)
				expected = ref.SHA256
				sources = append(sources, media.LibrarySource{AssetID: binding.AssetID, VersionID: binding.VersionID})
				inherited, e := s.referenceLineage(ctx, space, *ref, visiting)
				if e != nil {
					return nil, "", "", e
				}
				sources = append(sources, inherited...)
			}
		}
	}
	if len(sources) == 0 && len(asset.ParentAssetIDs) == 0 && asset.ProvenanceVersion == 0 && !migrationRecovered {
		var e error
		origin, e = s.findLegacyCreativeOrigin(ctx, project, space, *asset)
		if e != nil {
			return nil, "", "", e
		}
		if origin != nil {
			markRecoveredLineage(ctx)
			sources = append(sources, libraryInputSources(origin.Job.LibraryInputs)...)
			for _, parent := range creativeMediaIDs(creativeJobMediaInput(&origin.Job)) {
				inherited, _, _, e := s.libraryMediaSourcesVisited(ctx, project, space, parent, visiting, false)
				if e != nil {
					return nil, "", "", e
				}
				sources = append(sources, inherited...)
			}
		} else if strings.HasPrefix(asset.Source, "library:") || asset.Source == "social-photo-normalized" || asset.Source == "poster-composite" || asset.Source == "migrated" {
			return nil, "", "", errMediaLineageUnknown
		}
	}
	if len(sources) == 0 && len(asset.ParentAssetIDs) == 0 && asset.ProvenanceVersion == 0 && origin == nil && !migrationRecovered && asset.Source != "uploaded" && asset.Source != "synthetic" && !s.media.hasLegacyUploadReceipt(project, space, *asset) {
		return nil, "", "", errMediaLineageUnknown
	}
	if !verify {
		return uniqueLibrarySources(sources), expected, asset.MIMEType, nil
	}
	for _, source := range sources {
		if !library.IDPattern.MatchString(source.AssetID) || !library.IDPattern.MatchString(source.VersionID) {
			return nil, "", "", ErrBlobIntegrity
		}
	}
	path, mime, e := s.media.file(project, space, id, false, false)
	if e != nil {
		return nil, "", "", e
	}
	if mime != asset.MIMEType {
		return nil, "", "", ErrBlobIntegrity
	}
	f, e := openLocalBlob(path)
	if e != nil {
		return nil, "", "", e
	}
	defer f.Close()
	rawSHA, content, e := lineageFileDigests(ctx, f)
	if e != nil {
		return nil, "", "", e
	}
	if expected != "" && (len(expected) != 64 || expected != rawSHA) {
		return nil, "", "", ErrBlobIntegrity
	}
	if origin != nil {
		if !legacyCreativeDigestMatches(*asset, origin.Job, content, origin.Receipt.Digest) {
			return nil, "", "", ErrBlobIntegrity
		}
	} else if expected == "" {
		if len(sources) > 0 || len(asset.ParentAssetIDs) > 0 || asset.ProvenanceVersion == 1 {
			return nil, "", "", ErrBlobIntegrity
		}
		if !s.media.matchesLegacyUpload(project, space, *asset, content) {
			return nil, "", "", errMediaLineageUnknown
		}
		markRecoveredLineage(ctx)
	}
	expected = rawSHA
	return uniqueLibrarySources(sources), expected, mime, nil
}

type legacyCreativeOrigin struct {
	Job     CreativeJob
	Receipt mediaReceipt
}

func (m *MediaStore) lineageReceipts(project, space string) map[string]mediaReceipt {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string]mediaReceipt{}
	prefix := mediaKey(space, project) + ":"
	for key, r := range m.data.Receipts {
		if strings.HasPrefix(key, prefix) {
			out[strings.TrimPrefix(key, prefix)] = r
		}
	}
	return out
}
func (m *MediaStore) hasLegacyUploadReceipt(project, space string, a MediaAsset) bool {
	for key, r := range m.lineageReceipts(project, space) {
		if r.AssetID == a.ID && !strings.HasPrefix(key, "creative-") {
			return true
		}
	}
	return false
}
func (m *MediaStore) matchesLegacyUpload(project, space string, a MediaAsset, content string) bool {
	expected := mediaDigest([]string{"upload", a.Name, a.Role, a.Character, a.ShotID, a.Source, a.MIMEType, content})
	for key, r := range m.lineageReceipts(project, space) {
		if r.AssetID == a.ID && r.Digest == expected && !strings.HasPrefix(key, "creative-") {
			return true
		}
	}
	return false
}
func (s *FileStore) findLegacyCreativeOrigin(ctx context.Context, project, space string, a MediaAsset) (*legacyCreativeOrigin, error) {
	receipts := s.media.lineageReceipts(project, space)
	var snapshot creativeDisk
	if held, ok := ctx.Value(lineageCreativeSnapshotKey{}).(creativeDisk); ok {
		snapshot = held
	} else {
		s.creative.mu.Lock()
		snapshot = creativeDisk{Jobs: map[string]CreativeJob{}}
		for id, j := range s.creative.data.Jobs {
			if j.SpaceID == space && j.ProjectID == project {
				snapshot.Jobs[id] = cloneCreativeJob(j)
			}
		}
		s.creative.mu.Unlock()
	}
	var match *legacyCreativeOrigin
	for _, j := range snapshot.Jobs {
		if e := ctx.Err(); e != nil {
			return nil, e
		}
		if j.SpaceID != space || j.ProjectID != project {
			continue
		}
		r, has := receipts["creative-"+j.ID]
		if j.AssetID != a.ID && (!has || r.AssetID != a.ID) {
			continue
		}
		if !has || r.AssetID != a.ID || j.AssetID != "" && j.AssetID != a.ID {
			return nil, errMediaLineageUnknown
		}
		kind := "audio"
		switch j.Kind {
		case "image":
			kind = "image"
		case "video", "digital_human", "lip_sync":
			kind = "video"
		case "speech", "music", "sound_effect":
		default:
			return nil, ErrBlobIntegrity
		}
		if kind != a.Kind || a.Source != j.Provider {
			return nil, ErrBlobIntegrity
		}
		if match != nil {
			return nil, errMediaLineageUnknown
		}
		match = &legacyCreativeOrigin{cloneCreativeJob(j), r}
	}
	return match, nil
}
func legacyCreativeDigestMatches(a MediaAsset, j CreativeJob, content, digest string) bool {
	if digest == mediaDigest([]string{"upload", a.Name, a.Role, a.Character, a.ShotID, a.Source, a.MIMEType, content}) {
		return true
	}
	role := "shot"
	switch j.Kind {
	case "image":
		if j.Character != "" {
			role = "reference"
		}
	case "speech":
		role = "voice"
	case "music":
		role = "music"
	case "sound_effect":
		role = "effect"
	}
	name := "创作素材 · " + j.Kind
	if j.Kind == "digital_human" {
		name = "AI生成 · 数字人口播"
	}
	return digest == mediaDigest([]string{"upload", name, role, j.Character, j.ShotID, j.Provider, a.MIMEType, content})
}

// The raw byte identity and old JSON-base64 receipt digest must describe the
// same stream. Two independent reads would permit a replacement between them.
func lineageFileDigests(ctx context.Context, r io.Reader) (string, string, error) {
	raw, legacy := sha256.New(), sha256.New()
	_, _ = legacy.Write([]byte{'"'})
	encoded := base64.NewEncoder(base64.StdEncoding, legacy)
	_, e := io.CopyBuffer(io.MultiWriter(raw, encoded), contextBlobReader{ctx, r}, make([]byte, 64<<10))
	if e != nil {
		return "", "", e
	}
	if e = encoded.Close(); e != nil {
		return "", "", e
	}
	_, _ = legacy.Write([]byte{'"'})
	return hex.EncodeToString(raw.Sum(nil)), hex.EncodeToString(legacy.Sum(nil)), nil
}

type LibraryLineageView struct {
	AssetID string                `json:"asset_id"`
	Status  string                `json:"status"`
	Sources []media.LibrarySource `json:"library_sources"`
	SHA256  string                `json:"content_sha256,omitempty"`
}

func registerLibraryLineageRoutes(mux *http.ServeMux, s *FileStore) {
	// Queue streaming inspections behind process resource slots, never an asset
	// count or account allowance. Waiting requests may cancel without mutation.
	slots := make(chan struct{}, 2)
	mux.HandleFunc("GET /api/v1/projects/{id}/media/assets/{assetId}/lineage", func(w http.ResponseWriter, r *http.Request) {
		space, ok := spaceID(w, r)
		if !ok {
			return
		}
		project, id := r.PathValue("id"), r.PathValue("assetId")
		if _, e := s.GetProject(space, project); e != nil {
			storeError(w, e)
			return
		}
		if r.Context().Err() != nil {
			return
		}
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
		case <-r.Context().Done():
			return
		}
		trace := &lineageTrace{}
		sources, sha, _, e := s.libraryMediaSources(context.WithValue(r.Context(), lineageTraceKey{}, trace), project, space, id)
		status := "recorded"
		if trace.Recovered {
			status = "recovered"
		}
		if errors.Is(e, errMediaLineageUnknown) {
			status = "unverifiable"
			sources = []media.LibrarySource{}
			sha = ""
		} else if e != nil {
			if errors.Is(e, ErrNotFound) || errors.Is(e, errRuntimeDenied) {
				mediaError(w, e)
			} else {
				problem(w, 503, "media_lineage_unavailable", "来源记录或文件字节暂时无法核对；原文件未修改，请重新读取或恢复匹配记录")
			}
			return
		}
		if sources == nil {
			sources = []media.LibrarySource{}
		}
		respond(w, 200, LibraryLineageView{AssetID: id, Status: status, Sources: sources, SHA256: sha})
	})
}

func (s *FileStore) lineageProject(ctx context.Context, project, space string) (Project, error) {
	if held, ok := ctx.Value(lineageCoreSnapshotKey{}).(map[string]Project); ok {
		p, exists := held[project]
		if !exists || p.SpaceID != space {
			return p, ErrNotFound
		}
		return p, nil
	}
	return s.GetProject(space, project)
}
func (s *FileStore) legacyMigrationSources(ctx context.Context, project, space string, a MediaAsset, visiting map[string]bool) ([]media.LibrarySource, string, error) {
	p, e := s.lineageProject(ctx, project, space)
	if e != nil {
		return nil, "", e
	}
	if p.MigrationSource == nil || s.migrations == nil {
		return nil, "", errMediaLineageUnknown
	}
	origin := p.MigrationSource
	j, e := s.migrations.Get(ctx, migrationrepo.Identity{Space: space, Project: origin.ProjectID, ID: origin.MigrationID})
	if e != nil {
		return nil, "", e
	}
	if j.TargetID != project || !reflect.DeepEqual(origin, migrationOrigin(j)) || j.State != "completed" {
		return nil, "", errMediaLineageUnknown
	}
	var copy *m.AssetCopy
	var proof m.Proof
	for i := range j.Plan.Assets {
		candidate := j.Plan.Assets[i]
		v, ok := j.Copies[candidate.Source.ID]
		if !ok || v.TargetID != a.ID {
			continue
		}
		if copy != nil || m.AssetID(j.ID, candidate.Source.ID) != a.ID {
			return nil, "", ErrBlobIntegrity
		}
		copy = &candidate
		proof = v
	}
	if copy == nil || proof.SHA256 == "" || copy.Source.MIMEType != a.MIMEType || copy.Source.Kind != a.Kind {
		return nil, "", ErrBlobIntegrity
	}
	hints := map[string]MediaAsset{}
	if prior, ok := ctx.Value(lineageAssetHintsKey{}).(map[string]MediaAsset); ok {
		for key, value := range prior {
			hints[key] = value
		}
	}
	for _, candidate := range j.Plan.Assets {
		hints[mediaKey(space, j.ProjectID)+":"+candidate.Source.ID] = candidate.Source
	}
	ctx = context.WithValue(ctx, lineageAssetHintsKey{}, hints)
	// The old plan may retain only a subset of a binding's references. Match
	// the persisted copy proof, never the subset's positional index.
	sources := []media.LibrarySource{}
	for _, binding := range j.Plan.Bindings {
		for _, id := range binding.MediaAssetIDs {
			if id != copy.Source.ID {
				continue
			}
			v, e := s.library.Version(ctx, space, binding.AssetID, binding.VersionID)
			if e != nil {
				return nil, "", e
			}
			matched := false
			for _, ref := range v.References {
				if ref.SHA256 != proof.SHA256 || ref.MIMEType != a.MIMEType {
					continue
				}
				matched = true
				sources = append(sources, media.LibrarySource{AssetID: binding.AssetID, VersionID: binding.VersionID})
				inherited, e := s.referenceLineage(ctx, space, ref, visiting)
				if e != nil {
					return nil, "", e
				}
				sources = append(sources, inherited...)
			}
			if !matched {
				return nil, "", ErrBlobIntegrity
			}
		}
	}
	if len(sources) == 0 {
		inherited, _, _, e := s.libraryMediaSourcesVisited(ctx, j.ProjectID, space, copy.Source.ID, visiting, false)
		if e != nil {
			return nil, "", e
		}
		sources = inherited
	}
	return uniqueLibrarySources(sources), proof.SHA256, nil
}

func (m *MediaStore) lineageState(project, space string) (MediaState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failure != nil {
		return MediaState{}, m.failure
	}
	if m.pg != nil && m.pg.err != nil {
		return MediaState{}, m.pg.err
	}
	return mediaState(m.clone(), mediaKey(space, project)), nil
}

// A legacy library reference retains the original owned media identity. Follow
// that server-written link before treating the new card as an authorization
// root. A recorded v1 reference already freezes the complete inherited scope.
func (s *FileStore) referenceLineage(ctx context.Context, space string, ref library.Reference, visiting map[string]bool) ([]media.LibrarySource, error) {
	sources := []media.LibrarySource{}
	for _, parent := range ref.LibrarySources {
		sources = append(sources, media.LibrarySource{AssetID: parent.AssetID, VersionID: parent.VersionID})
	}
	if ref.ProvenanceVersion == 1 {
		return uniqueLibrarySources(sources), nil
	}
	if ref.ProvenanceVersion != 0 {
		return nil, ErrBlobIntegrity
	}
	inherited, expected, mime, e := s.libraryMediaSourcesVisited(withLineageWalk(ctx), ref.SourceProjectID, space, ref.SourceAssetID, visiting, false)
	if errors.Is(e, ErrNotFound) {
		return nil, errMediaLineageUnknown
	}
	if e != nil {
		return nil, e
	}
	if mime != ref.MIMEType || expected != "" && expected != ref.SHA256 {
		return nil, ErrBlobIntegrity
	}
	markRecoveredLineage(ctx)
	return uniqueLibrarySources(append(sources, inherited...)), nil
}
func (s *FileStore) prepareReferenceLineage(ctx context.Context, space string, ref library.Reference) (library.Reference, error) {
	sources, e := s.referenceLineage(ctx, space, ref, map[string]bool{})
	if e != nil {
		return ref, e
	}
	out := ref
	out.ProvenanceVersion = 1
	out.LibrarySources = nil
	for _, source := range sources {
		out.LibrarySources = append(out.LibrarySources, library.Source{AssetID: source.AssetID, VersionID: source.VersionID})
	}
	return out, nil
}
func (s *FileStore) prepareLibraryCopyLineage(ctx context.Context, space string, v library.Version) (library.Version, error) {
	v.References = append([]library.Reference(nil), v.References...)
	for i, ref := range v.References {
		next, e := s.prepareReferenceLineage(ctx, space, ref)
		if errors.Is(e, errMediaLineageUnknown) {
			continue
		}
		if e != nil {
			return v, e
		}
		v.References[i] = next
	}
	return v, nil
}
