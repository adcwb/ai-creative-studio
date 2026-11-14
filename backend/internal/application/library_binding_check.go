package application

import (
	"context"
	"errors"
	"github.com/adcwb/ai-creative-studio/backend/internal/modules/media"
	"github.com/adcwb/ai-creative-studio/backend/internal/persistence/libraryrepo"
	"net/http"
)

type bindingVersionCheck struct {
	BindingID string `json:"binding_id"`
	AssetID   string `json:"library_asset_id"`
	Pinned    string `json:"pinned_version_id"`
	Current   string `json:"current_version_id,omitempty"`
	Status    string `json:"status"`
}

func (s *FileStore) checkBindingVersions(ctx context.Context, space, project string) ([]bindingVersionCheck, int, error) {
	if _, err := s.GetProject(space, project); err != nil {
		return nil, 0, err
	}
	state, err := s.media.GetChecked(project, space)
	if err != nil {
		return nil, 0, err
	}
	out := []bindingVersionCheck{}
	for _, b := range state.LibraryBindings {
		item := bindingVersionCheck{BindingID: b.ID, AssetID: b.AssetID, Pinned: b.VersionID, Status: "current"}
		asset, e := s.sharedLibraryAsset(ctx, space, media.LibrarySource{ShareID: b.ShareID, AssetID: b.AssetID, VersionID: b.VersionID})
		var pf *ProviderError
		if errors.Is(e, libraryrepo.ErrNotFound) || (errors.As(e, &pf) && pf.Code == "library_use_denied") {
			item.Status = "unavailable"
		} else if e != nil {
			return nil, 0, e
		} else {
			item.Current = asset.Asset.HeadVersionID
			_, e = s.sharedLibraryVersion(ctx, space, media.LibrarySource{ShareID: b.ShareID, AssetID: b.AssetID, VersionID: b.VersionID})
			if errors.Is(e, libraryrepo.ErrNotFound) {
				item.Status = "unavailable"
			} else if e != nil {
				return nil, 0, e
			} else if asset.Asset.Status != "active" {
				item.Status = "archived"
			} else if asset.Asset.HeadVersionID != b.VersionID {
				item.Status = "outdated"
			}
		}
		out = append(out, item)
	}
	return out, state.Version, nil
}
func registerBindingCheckRoute(mux *http.ServeMux, s *FileStore) {
	mux.HandleFunc("GET /api/v1/projects/{id}/library-bindings/check", func(w http.ResponseWriter, r *http.Request) {
		space, ok := spaceID(w, r)
		if !ok {
			return
		}
		items, version, err := s.checkBindingVersions(r.Context(), space, r.PathValue("id"))
		if err != nil {
			libraryError(w, err)
			return
		}
		respond(w, 200, map[string]any{"items": items, "media_version": version})
	})
}
