package application

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/adcwb/ai-creative-studio/backend/internal/modules/publishing"
	"io"
	"net/http"
	"os"

	"github.com/adcwb/ai-creative-studio/backend/internal/platform/nativefs"
)

type deliveryFile struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}
type deliveryManifest struct {
	Metadata      *publishing.DeliveryMetadata `json:"publishing_metadata,omitempty"`
	Format        int                          `json:"format"`
	ExportID      string                       `json:"export_id"`
	ProjectID     string                       `json:"project_id"`
	Episode       int                          `json:"episode_number"`
	Timeline      MediaTimeline                `json:"timeline"`
	SeriesEpisode any                          `json:"series_episode,omitempty"`
	Files         []deliveryFile               `json:"files"`
}
type deliveryInput struct {
	name string
	file *os.File
}
type deliveryReader struct {
	ctx context.Context
	r   io.Reader
}

func (r deliveryReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}

func copyDeliveryFile(ctx context.Context, dest io.Writer, file *os.File) (int64, string, error) {
	before, err := file.Stat()
	if err != nil || !before.Mode().IsRegular() {
		return 0, "", ErrBlobIntegrity
	}
	hash := sha256.New()
	size, err := io.Copy(io.MultiWriter(dest, hash), deliveryReader{ctx, file})
	if err != nil {
		return 0, "", err
	}
	after, err := file.Stat()
	if err != nil || size != before.Size() || after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) {
		return 0, "", ErrBlobIntegrity
	}
	return size, hex.EncodeToString(hash.Sum(nil)), nil
}

func prepareDelivery(m *MediaStore, space, project, id string) (MediaExport, []deliveryInput, error) {
	state, err := m.GetChecked(project, space)
	if err != nil {
		return MediaExport{}, nil, err
	}
	var chosen *MediaExport
	for _, v := range state.Exports {
		if v.ID == id {
			v := v
			chosen = &v
			break
		}
	}
	if chosen == nil {
		return MediaExport{}, nil, ErrNotFound
	}
	if chosen.Purpose != "" || chosen.Status != "succeeded" {
		return MediaExport{}, nil, mediaInvalid("只有已完成的正式成片可以制作交付包")
	}
	files := []deliveryInput{}
	for _, ext := range []string{"mp4", "srt", "vtt"} {
		path, err := m.localBlob(space, project, id+"."+ext)
		var file *os.File
		if err == nil {
			file, err = nativefs.OpenFile(path, os.O_RDONLY|nativefs.NoFollow|nativefs.NonBlock, 0)
		}
		if err != nil {
			for _, f := range files {
				_ = f.file.Close()
			}
			return MediaExport{}, nil, ErrNotFound
		}
		info, err := file.Stat()
		if err != nil || !info.Mode().IsRegular() {
			_ = file.Close()
			for _, f := range files {
				_ = f.file.Close()
			}
			return MediaExport{}, nil, ErrNotFound
		}
		files = append(files, deliveryInput{"episode." + ext, file})
	}
	return *chosen, files, nil
}
func writeDelivery(ctx context.Context, w io.Writer, project string, export MediaExport, files []deliveryInput, metadata ...*publishing.DeliveryMetadata) error {
	archive := zip.NewWriter(w)
	manifest := deliveryManifest{Format: 1, ExportID: export.ID, ProjectID: project, Episode: export.EpisodeNumber, Timeline: export.Timeline, SeriesEpisode: export.SeriesEpisode, Files: []deliveryFile{}}
	if len(metadata) > 0 {
		manifest.Metadata = metadata[0]
	}
	if manifest.Episode == 0 {
		manifest.Episode = 1
	}
	for _, f := range files {
		entry, err := archive.CreateHeader(&zip.FileHeader{Name: f.name, Method: zip.Store})
		if err != nil {
			return err
		}
		size, hash, err := copyDeliveryFile(ctx, entry, f.file)
		if err != nil {
			return err
		}
		if manifest.Metadata != nil && f.name != "episode.mp4" && f.name != "episode.srt" && f.name != "episode.vtt" && hash != manifest.Metadata.CoverSHA256 {
			return ErrBlobIntegrity
		}
		manifest.Files = append(manifest.Files, deliveryFile{f.name, size, hash})
	}
	entry, err := archive.Create("manifest.json")
	if err != nil {
		return err
	}
	if err = json.NewEncoder(entry).Encode(manifest); err != nil {
		return err
	}
	entry, err = archive.Create("README.txt")
	if err != nil {
		return err
	}
	if _, err = fmt.Fprint(entry, "This delivery contains the selected completed export and its fixed timeline. SHA-256 values describe the included bytes. It does not certify licensing, consistency, native model resolution, or publishing approval. Files have not been published to any external platform.\n"); err != nil {
		return err
	}
	return archive.Close()
}
func registerDeliveryRoute(mux *http.ServeMux, s *FileStore, scope func(http.ResponseWriter, *http.Request, bool) (string, string, bool)) {
	mux.HandleFunc("GET /api/v1/projects/{id}/media/exports/{export_id}/delivery.zip", func(w http.ResponseWriter, r *http.Request) {
		space, _, ok := scope(w, r, false)
		if !ok {
			return
		}
		project := r.PathValue("id")
		export, files, err := prepareDelivery(s.media, space, project, r.PathValue("export_id"))
		if err != nil {
			mediaError(w, err)
			return
		}
		defer func() {
			for _, f := range files {
				_ = f.file.Close()
			}
		}()
		version, err := deliveryMetadataQuery(r)
		if err != nil {
			mediaError(w, err)
			return
		}
		state, err := s.media.GetChecked(project, space)
		if err != nil {
			mediaError(w, err)
			return
		}
		metadata, err := deliveryMetadata(state, export.ID, version)
		if err != nil {
			mediaError(w, err)
			return
		}
		if metadata != nil && metadata.CoverAssetID != "" {
			cover, e := openDeliveryCover(s.media, r.Context(), space, project, *metadata)
			if e != nil {
				mediaError(w, mediaInvalid("封面文件不可读取或已变化，请重新选择并保存发布资料"))
				return
			}
			files = append(files, cover)
		}
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", `attachment; filename="episode-delivery.zip"`)
		w.Header().Set("Cache-Control", "private, no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		// No JSON or second status after streaming starts. An incomplete archive is
		// deliberately invalid, never a falsely successful delivery.
		if err = writeDelivery(r.Context(), w, project, export, files, metadata); err != nil {
			panic(http.ErrAbortHandler)
		}
	})
}
