package libraryrepo

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/library"

	"github.com/adcwb/ai-creative-studio/backend/internal/platform/nativefs"
)

// Legacy decoding is an upgrade/offline operation only. It has no cumulative
// size admission limit, and uses a decoder rather than a second whole-file copy.
func (s *Store) readLegacy(space string) (disk, error) {
	if !library.IDPattern.MatchString(space) {
		return disk{}, library.ErrInput
	}
	f, err := nativefs.OpenFile(filepath.Join(s.dir, space+".json"), os.O_RDONLY|nativefs.NoFollow|nativefs.NonBlock, 0)
	if os.IsNotExist(err) {
		return fresh(), nil
	}
	if err != nil {
		return disk{}, ErrStorage
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		return disk{}, ErrStorage
	}
	var d disk
	dec := json.NewDecoder(f)
	if dec.Decode(&d) != nil || dec.Decode(new(any)) != io.EOF {
		return disk{}, ErrStorage
	}
	return validateDisk(space, d)
}
func validateDisk(space string, d disk) (disk, error) {
	if d.Format != 1 || d.Assets == nil || d.Versions == nil || d.Receipts == nil {
		return disk{}, ErrStorage
	}
	for id, a := range d.Assets {
		if a.ID != id || a.SpaceID != space || !library.ValidAuthorization(a.UseAuthorization) || (a.Status != "active" && a.Status != "archived") {
			return disk{}, ErrStorage
		}
		v, ok := d.Versions[a.HeadVersionID]
		if !ok || v.AssetID != id || v.SpaceID != space {
			return disk{}, ErrStorage
		}
	}
	for id, v := range d.Versions {
		if v.ID != id || v.SpaceID != space || d.Assets[v.AssetID].ID == "" {
			return disk{}, ErrStorage
		}
		if _, e := library.Normalize(d.Assets[v.AssetID].Kind, v.Card); e != nil {
			return disk{}, ErrStorage
		}
		if v.Number < 1 || library.ValidateContent(v.Card, v.References) != nil || library.ValidateReferences(v.References) != nil {
			return disk{}, ErrStorage
		}
		h, e := library.Digest(struct {
			Card       library.Card
			References []library.Reference
		}{v.Card, v.References})
		if e != nil || h != v.SHA256 {
			return disk{}, ErrStorage
		}
	}
	for _, r := range d.Receipts {
		if !validFileResult(space, r.Result.Asset.ID, r.Result) || d.Versions[r.Result.Version.ID].ID == "" {
			return disk{}, ErrStorage
		}
	}
	return d, nil
}
