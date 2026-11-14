package application

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/series"
)

func installSeasonDeliveryFixture(t *testing.T, s *FileStore, p Project, id string) {
	t.Helper()
	m := s.media
	m.mu.Lock()
	d := m.clone()
	state := d.Projects[mediaKey(p.SpaceID, p.ID)]
	state.Exports = append(state.Exports, MediaExport{ID: id, Status: "succeeded", Duration: 2, Timeline: MediaTimeline{OutputResolution: "1080p"}})
	d.Projects[mediaKey(p.SpaceID, p.ID)] = state
	err := m.commit(d)
	m.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	for _, ext := range []string{"mp4", "srt", "vtt"} {
		if err = os.WriteFile(filepath.Join(m.dir, id+"."+ext), []byte(p.ID+":"+ext), 0600); err != nil {
			t.Fatal(err)
		}
		if err = m.storeBlobContext(context.Background(), p.SpaceID, p.ID, id+"."+ext); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSeasonDeliveryPinsEveryEpisodeAndStreamsChecksums(t *testing.T) {
	for _, mode := range []string{"file", "postgres"} {
		t.Run(mode, func(t *testing.T) {
			s := testStore(t)
			if mode == "postgres" {
				s = postgresTestStore(t, postgresTestDB(t))
			}
			ctx := context.Background()
			season := seriesFixture(t, s)
			var projects []Project
			for i := 1; i <= 2; i++ {
				_, p, err := s.CreateSeriesEpisode(ctx, seriesActor, fmt.Sprintf("season-delivery-%d", i), season.ID, series.Input{Title: fmt.Sprintf("第%d幕", i)})
				if err != nil {
					t.Fatal(err)
				}
				projects = append(projects, p)
			}
			h := Handler(s, "")
			path := "/api/v1/seasons/" + season.ID + "/delivery"
			q := "?space_id=" + seriesActor.Space
			call := func(url string, status int) *httptest.ResponseRecorder {
				t.Helper()
				w := httptest.NewRecorder()
				h.ServeHTTP(w, httptest.NewRequest("GET", url, nil))
				if w.Code != status {
					t.Fatal(w.Code, w.Body.String())
				}
				return w
			}
			var missing seasonDeliveryPreview
			if err := json.Unmarshal(call(path+q, 200).Body.Bytes(), &missing); err != nil {
				t.Fatal(err)
			}
			if missing.Ready || len(missing.Episodes) != 2 || missing.Episodes[0].Issue != "missing_final_export" {
				t.Fatal(missing)
			}
			call(path+".zip"+q+"&selection_sha256="+missing.SHA256, 409)
			for i, p := range projects {
				installSeasonDeliveryFixture(t, s, p, fmt.Sprintf("%032x", i+1))
			}
			preview, err := s.seasonDelivery(ctx, seriesActor.Space, season.ID)
			if err != nil || !preview.Ready || len(preview.Episodes) != 2 {
				t.Fatal(preview, err)
			}
			before := s.media.Get(projects[0].ID, seriesActor.Space)
			response := call(path+".zip"+q+"&selection_sha256="+preview.SHA256, 200)
			if response.Header().Get("Content-Type") != "application/zip" {
				t.Fatal(response.Header())
			}
			archive, err := zip.NewReader(bytes.NewReader(response.Body.Bytes()), int64(response.Body.Len()))
			if err != nil {
				t.Fatal(err)
			}
			files := map[string][]byte{}
			for _, file := range archive.File {
				r, err := file.Open()
				if err != nil {
					t.Fatal(err)
				}
				files[file.Name], err = io.ReadAll(r)
				r.Close()
				if err != nil {
					t.Fatal(err)
				}
			}
			var manifest struct {
				Selection seasonDeliveryPreview `json:"selection"`
				Episodes  []struct {
					Files []deliveryFile `json:"files"`
				} `json:"episodes"`
			}
			if err = json.Unmarshal(files["manifest.json"], &manifest); err != nil {
				t.Fatal(err)
			}
			if len(files) != 8 || len(manifest.Episodes) != 2 || manifest.Selection.SHA256 != preview.SHA256 {
				t.Fatal("incomplete bundle", len(files), manifest)
			}
			for _, episode := range manifest.Episodes {
				if len(episode.Files) != 3 {
					t.Fatal(episode)
				}
				for _, f := range episode.Files {
					hash := sha256.Sum256(files[f.Name])
					if f.Size != int64(len(files[f.Name])) || f.SHA256 != hex.EncodeToString(hash[:]) {
						t.Fatal("checksum", f)
					}
				}
			}
			if !reflect.DeepEqual(before, s.media.Get(projects[0].ID, seriesActor.Space)) {
				t.Fatal("download changed project")
			}
			call(path+"?space_id=team-demo", 404)
			call(path+".zip"+q, 409)
			installSeasonDeliveryFixture(t, s, projects[0], "cccccccccccccccccccccccccccccccc")
			call(path+".zip"+q+"&selection_sha256="+preview.SHA256, 409)
			// Pinned export metadata remains valid if later edits exist; a newer
			// final requires a new explicit preview instead of silently changing ZIP.
			ctx, cancel := context.WithCancel(ctx)
			cancel()
			var out bytes.Buffer
			if err = s.writeSeasonDelivery(ctx, &out, seriesActor.Space, preview); err == nil {
				t.Fatal("cancel ignored")
			}
			if err = os.Remove(filepath.Join(s.media.dir, "00000000000000000000000000000002.vtt")); err != nil {
				t.Fatal(err)
			}
			unavailable, err := s.seasonDelivery(context.Background(), seriesActor.Space, season.ID)
			if err != nil || unavailable.Ready || unavailable.Episodes[1].Issue != "export_files_unavailable" {
				t.Fatal(unavailable, err)
			}
		})
	}
}

func TestSeasonDeliveryDoesNotTruncateAtDirectoryPageSize(t *testing.T) {
	s := testStore(t)
	season := seriesFixture(t, s)
	for i := 0; i < 101; i++ {
		if _, _, err := s.CreateSeriesEpisode(context.Background(), seriesActor, fmt.Sprintf("season-many-%d", i), season.ID, series.Input{Title: fmt.Sprintf("第 %d 集", i+1)}); err != nil {
			t.Fatal(err)
		}
	}
	preview, err := s.seasonDelivery(context.Background(), seriesActor.Space, season.ID)
	if err != nil || preview.Ready || len(preview.Episodes) != 101 || preview.Episodes[100].Number != 101 {
		t.Fatal("season list treated page size as a product quota", len(preview.Episodes), err)
	}
}

// A changing regular file must abort the ZIP rather than certify a partial
// render. The manifest hashes received bytes, not model quality or legal rights.
func TestDeliveryStreamRejectsMutationAndSymlink(t *testing.T) {
	path := filepath.Join(t.TempDir(), "export.mp4")
	if err := os.WriteFile(path, bytes.Repeat([]byte{9}, 1<<16), 0600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var once bool
	writer := deliveryMutationWriter{write: func(p []byte) (int, error) {
		if !once {
			once = true
			if err := os.Truncate(path, 1); err != nil {
				return 0, err
			}
		}
		return len(p), nil
	}}
	if _, _, err = copyDeliveryFile(context.Background(), writer, file); err == nil {
		t.Fatal("mutated export accepted")
	}
	s := testStore(t)
	season := seriesFixture(t, s)
	_, project, err := s.CreateSeriesEpisode(context.Background(), seriesActor, "delivery-no-symlink", season.ID, series.Input{Title: "正式交付"})
	if err != nil {
		t.Fatal(err)
	}
	id := "dddddddddddddddddddddddddddddddd"
	installSeasonDeliveryFixture(t, s, project, id)
	target := filepath.Join(s.media.dir, id+".mp4")
	if err = os.Remove(target); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(path, target); err != nil {
		t.Skip("symlink creation unavailable", err)
	}
	if _, files, err := prepareDelivery(s.media, project.SpaceID, project.ID, id); err == nil {
		closeDelivery(files)
		t.Fatal("symbolic link export accepted")
	}
}

type deliveryMutationWriter struct{ write func([]byte) (int, error) }

func (w deliveryMutationWriter) Write(p []byte) (int, error) { return w.write(p) }
