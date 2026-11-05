package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/adcwb/ai-creative-studio/backend/internal/modules/library"
	"github.com/adcwb/ai-creative-studio/backend/internal/modules/media"
	"github.com/adcwb/ai-creative-studio/backend/internal/modules/prompts"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

func (s *FileStore) promptReferences(ctx context.Context, space string, recipe prompts.Recipe) ([]library.Reference, error) {
	refs := []library.Reference{}
	for _, binding := range recipe.ReferenceBindings {
		asset, e := s.library.Get(ctx, space, binding.AssetID)
		if e != nil {
			return nil, prompts.ErrNotFound
		}
		v, e := s.library.Version(ctx, space, binding.AssetID, binding.VersionID)
		if e != nil {
			return nil, prompts.ErrNotFound
		}
		if e = library.CanBind(asset.Asset, v, space); e != nil {
			return nil, prompts.ErrInput
		}
		found := false
		for _, ref := range v.References {
			if ref.ID == binding.ReferenceID {
				refs = append(refs, ref)
				found = true
				break
			}
		}
		if !found {
			return nil, prompts.ErrNotFound
		}
	}
	return refs, nil
}

type promptApplyInput struct {
	TemplateID string            `json:"template_id"`
	VersionID  string            `json:"version_id"`
	Variables  map[string]string `json:"variables,omitempty"`
	Expected   int               `json:"expected_media_version"`
}

func registerPromptApplicationRoutes(mux *http.ServeMux, s *FileStore) {
	base := "/api/v1/projects/{id}/prompt-applications"
	mux.HandleFunc("GET "+base+"/{applicationId}", func(w http.ResponseWriter, r *http.Request) {
		space, ok := spaceID(w, r)
		if !ok {
			return
		}
		if _, e := s.GetProject(space, r.PathValue("id")); e != nil {
			storeError(w, e)
			return
		}
		state := s.media.Get(r.PathValue("id"), space)
		for _, app := range state.PromptApplications {
			if app.ID == r.PathValue("applicationId") {
				respond(w, 200, app)
				return
			}
		}
		promptError(w, prompts.ErrNotFound)
	})
	mux.HandleFunc("POST "+base, func(w http.ResponseWriter, r *http.Request) {
		space, key, ok := projectWriteScope(w, r)
		if !ok {
			return
		}
		project := r.PathValue("id")
		if _, e := s.GetProject(space, project); e != nil {
			storeError(w, e)
			return
		}
		a := promptActor(r, space)
		if !a.Writable {
			promptError(w, prompts.ErrDenied)
			return
		}
		var in promptApplyInput
		if !decodeProject(w, r, &in) {
			return
		}
		if in.Expected < 1 || !prompts.ID.MatchString(in.TemplateID) || !prompts.ID.MatchString(in.VersionID) {
			promptError(w, prompts.ErrInput)
			return
		}
		digest := mediaDigest(struct {
			Operation, Actor string
			Input            promptApplyInput
		}{"prompt-apply", a.User, in})
		id := libraryStableID(space, project+":"+a.User+":"+key, "prompt-application")
		m := s.media
		m.mu.Lock()
		state := mediaState(m.data, mediaKey(space, project))
		closed := m.closed
		replay, e := m.check(m.data, mediaKey(space, project), key, digest)
		m.mu.Unlock()
		if closed {
			mediaError(w, errors.New("media store closed"))
			return
		}
		if e != nil {
			mediaError(w, e)
			return
		}
		if replay {
			for _, app := range state.PromptApplications {
				if app.ID == id {
					respond(w, 200, app)
					return
				}
			}
			promptError(w, prompts.ErrNotFound)
			return
		}
		current, e := s.prompts.Get(r.Context(), a, in.TemplateID)
		if e != nil {
			promptError(w, e)
			return
		}
		if current.Template.Status != "active" {
			promptError(w, prompts.ErrArchived)
			return
		}
		version, e := s.prompts.Version(r.Context(), a, in.TemplateID, in.VersionID)
		if e != nil {
			promptError(w, e)
			return
		}
		compiled, e := prompts.Compile(version.Recipe, in.Variables)
		if e != nil {
			promptError(w, e)
			return
		}
		refs, e := s.promptReferences(r.Context(), space, version.Recipe)
		if e != nil {
			promptError(w, e)
			return
		}
		application := prompts.Application{ID: id, Prompt: compiled.Text, Defaults: version.Defaults, Source: prompts.Source{TemplateID: in.TemplateID, VersionID: in.VersionID, CompiledSHA256: compiled.SHA256, EffectiveSHA256: compiled.SHA256, Variables: canonicalVariables(in.Variables), ApplicationID: id, ReferenceBindings: append([]prompts.ReferenceBinding(nil), version.ReferenceBindings...)}, ReferenceAssetIDs: []string{}, CreatedBy: a.User, CreatedAt: time.Now().UTC()}
		assets := []MediaAsset{}
		for i, ref := range refs {
			if e = r.Context().Err(); e != nil {
				mediaError(w, e)
				return
			}
			path, e := m.localBlob(space, "library", ref.ID)
			if e != nil {
				mediaError(w, e)
				return
			}
			data, e := safeReadLocal(path, 4<<20)
			hash := sha256.Sum256(data)
			if e != nil || hex.EncodeToString(hash[:]) != ref.SHA256 {
				mediaError(w, ErrBlobIntegrity)
				return
			}
			assetID := libraryStableID(space, project+":"+id+":"+ref.ID, "prompt-reference")
			destination := filepath.Join(m.dir, assetID)
			if old, e := safeReadLocal(destination, 4<<20); e == nil {
				h := sha256.Sum256(old)
				if h != hash {
					mediaError(w, ErrBlobIntegrity)
					return
				}
			} else if !os.IsNotExist(e) {
				mediaError(w, e)
				return
			} else if e = writePreparedLibraryCopy(destination, data); e != nil {
				mediaError(w, e)
				return
			}
			if e = m.storeBlobContext(r.Context(), space, project, assetID); e != nil {
				mediaError(w, e)
				return
			}
			application.ReferenceAssetIDs = append(application.ReferenceAssetIDs, assetID)
			sources := []media.LibrarySource{{AssetID: version.ReferenceBindings[i].AssetID, VersionID: version.ReferenceBindings[i].VersionID}}
			for _, source := range ref.LibrarySources {
				sources = append(sources, media.LibrarySource{AssetID: source.AssetID, VersionID: source.VersionID})
			}
			assets = append(assets, MediaAsset{LibrarySources: uniqueLibrarySources(sources), ContentSHA256: ref.SHA256, ID: assetID, Kind: "image", MIMEType: ref.MIMEType, Name: fmt.Sprintf("Recipe reference %d", i+1), Role: "reference", Source: "library:" + version.ReferenceBindings[i].AssetID + ":" + version.ReferenceBindings[i].VersionID, FileURL: mediaURL(space, project, "assets/"+assetID+"/file")})
		}
		if scope, ok := r.Context().Value(authScopeKey{}).(authScope); ok && scope.recheckWrite != nil && !scope.recheckWrite() {
			promptError(w, prompts.ErrDenied)
			return
		}
		m.mu.Lock()
		d := m.clone()
		k := mediaKey(space, project)
		state = mediaState(d, k)
		replay, e = m.check(d, k, key, digest)
		if e == nil && m.closed {
			e = errors.New("media store closed")
		}
		if e == nil && !replay {
			if state.Version != in.Expected {
				e = errMediaVersion
			} else {
				state.Assets = append(state.Assets, assets...)
				state.PromptApplications = append(state.PromptApplications, application)
				state.Version++
				d.Projects[k] = state
				d.Receipts[k+":"+key] = mediaReceipt{Digest: digest}
				e = m.commit(d)
			}
		}
		m.mu.Unlock()
		if e != nil {
			mediaError(w, e)
			return
		}
		if replay {
			for _, old := range state.PromptApplications {
				if old.ID == id {
					application = old
					break
				}
			}
		}
		respond(w, 201, application)
	})
}

// Resolve source snapshots from an already accessible project application. The
// underlying image references remain immutable project-owned objects.
func (s *FileStore) resolveCreativePrompt(w http.ResponseWriter, r *http.Request, space, project, key string, in *CreativeJobInput) bool {
	if in.PromptSource == nil {
		return true
	}
	source := in.PromptSource
	in.Prompt = strings.TrimSpace(in.Prompt)
	if utf8.RuneCountInString(in.Prompt) > 1000 {
		promptError(w, prompts.ErrInput)
		return false
	}
	if in.Kind != "image" {
		promptError(w, prompts.ErrInput)
		return false
	}
	// Accepted receipts replay before template archival/revocation; current project
	// membership is still enforced by the secure middleware and store.
	s.creative.mu.Lock()
	receipt, ok := s.creative.data.Receipts[projectReceiptKey(space, key)]
	old := s.creative.data.Jobs[receipt.JobID]
	s.creative.mu.Unlock()
	if ok && old.ProjectID == project && old.PromptSource != nil && old.PromptSource.TemplateID == source.TemplateID && old.PromptSource.VersionID == source.VersionID && old.PromptSource.ApplicationID == source.ApplicationID {
		in.promptSnapshot = old.PromptSource
		if in.ImageParameters == nil {
			in.ImageParameters = old.ImageParameters
		}
		return true
	}
	a := promptActor(r, space)
	current, e := s.prompts.Get(r.Context(), a, source.TemplateID)
	if e != nil {
		promptError(w, e)
		return false
	}
	if current.Template.Status != "active" {
		promptError(w, prompts.ErrArchived)
		return false
	}
	version, e := s.prompts.Version(r.Context(), a, source.TemplateID, source.VersionID)
	if e != nil {
		promptError(w, e)
		return false
	}
	compiled, e := prompts.Compile(version.Recipe, source.Variables)
	if e != nil {
		promptError(w, e)
		return false
	}
	if in.Prompt != compiled.Text && !source.Edited {
		promptError(w, prompts.ErrInput)
		return false
	}
	snapshot := prompts.Source{TemplateID: source.TemplateID, VersionID: source.VersionID, Variables: canonicalVariables(source.Variables), CompiledSHA256: compiled.SHA256, EffectiveSHA256: prompts.TextSHA(in.Prompt), Edited: in.Prompt != compiled.Text, ApplicationID: source.ApplicationID, ReferenceBindings: append([]prompts.ReferenceBinding(nil), version.ReferenceBindings...)}
	if len(version.ReferenceBindings) > 0 || source.ApplicationID != "" {
		found := false
		for _, app := range s.media.Get(project, space).PromptApplications {
			if app.ID == source.ApplicationID && app.Source.TemplateID == source.TemplateID && app.Source.VersionID == source.VersionID && prompts.Digest(app.Source.Variables) == prompts.Digest(canonicalVariables(source.Variables)) {
				refs := append([]string{}, in.ReferenceAssetIDs...)
				if in.ReferenceAssetID != "" {
					refs = append(refs, in.ReferenceAssetID)
				}
				if prompts.Digest(refs) != prompts.Digest(app.ReferenceAssetIDs) {
					promptError(w, prompts.ErrInput)
					return false
				}
				found = true
				break
			}
		}
		if !found {
			promptError(w, prompts.ErrNotFound)
			return false
		}
	}
	if in.ImageParameters == nil {
		copy := version.Defaults
		in.ImageParameters = &copy
	}
	in.promptSnapshot = &snapshot
	return true
}
