package application

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http/httptest"
	"os"
	"reflect"
	"testing"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/publishing"
	"github.com/adcwb/ai-creative-studio/backend/internal/modules/series"
)

func TestDeliveryMetadataVersionsReplayAndFixedCover(t *testing.T) {
	for _, mode := range []string{"file", "postgres"} {
		t.Run(mode, func(t *testing.T) {
			s := testStore(t)
			if mode == "postgres" {
				s = postgresTestStore(t, postgresTestDB(t))
			}
			p, _, e := s.CreateProject("personal-demo", "release-project", ProjectInput{Title: "保留这次发布", Brief: "元数据固定到成片，后续变动不能改写已选择的版本。"})
			if e != nil {
				t.Fatal(e)
			}
			export := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
			installSeasonDeliveryFixture(t, s, p, export)
			// A second local export remains a separate release target.
			otherExport := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
			installSeasonDeliveryFixture(t, s, p, otherExport)
			state, e := s.media.AddAsset(p.ID, p.SpaceID, "release-cover", "封面", "shot", "", "", "upload", miniMaxTestPNG(), "image/png")
			if e != nil {
				t.Fatal(e)
			}
			cover := state.Assets[0]
			in := publishing.DeliveryInput{Title: "第一版标题", Description: "有明确来源的封面", Tags: []string{" 短剧 ", "雨夜"}, CoverAssetID: cover.ID, RightsConfirmed: true}
			v1, e := s.media.saveDeliveryMetadata(context.Background(), p.SpaceID, p.ID, export, "release-one", "creator", in)
			if e != nil {
				t.Fatal(e)
			}
			if v1.Version != 1 || len(v1.CoverSHA256) != 64 || v1.Tags[0] != "短剧" {
				t.Fatal(v1)
			}
			in.ExpectedMetadataVersion = 1
			in.Title = "第二版标题"
			v2, e := s.media.saveDeliveryMetadata(context.Background(), p.SpaceID, p.ID, export, "release-two", "creator", in)
			if e != nil {
				t.Fatal(e)
			}
			if v2.Version != 2 {
				t.Fatal(v2)
			}
			in.ExpectedMetadataVersion = 0
			in.Title = v1.Title
			prior, e := s.media.saveDeliveryMetadata(context.Background(), p.SpaceID, p.ID, export, "release-one", "creator", in)
			if e != nil || !reflect.DeepEqual(prior, v1) {
				t.Fatal("original replay", prior, e)
			}
			if _, e = s.media.saveDeliveryMetadata(context.Background(), p.SpaceID, p.ID, export, "release-stale", "creator", in); !errors.Is(e, errMediaVersion) {
				t.Fatal("stale overwrite", e)
			}
			if _, e = s.media.saveDeliveryMetadata(context.Background(), p.SpaceID, p.ID, otherExport, "release-one", "creator", in); !errors.Is(e, errMediaKey) {
				t.Fatal("receipt can cross exports", e)
			}
			in.RightsConfirmed = false
			if _, e = s.media.saveDeliveryMetadata(context.Background(), p.SpaceID, p.ID, export, "release-unconfirmed", "creator", in); !errors.Is(e, errMediaInput) {
				t.Fatal("unconfirmed rights", e)
			}
			in.RightsConfirmed = true
			in.ExpectedMetadataVersion = 2
			in.CoverAssetID = "cccccccccccccccccccccccccccccccc"
			if _, e = s.media.saveDeliveryMetadata(context.Background(), p.SpaceID, p.ID, export, "release-foreign-cover", "creator", in); !errors.Is(e, errMediaInput) {
				t.Fatal("foreign cover", e)
			}
			h := Handler(s, "")
			url := "/api/v1/projects/" + p.ID + "/media/exports/" + export + "/delivery.zip?space_id=" + p.SpaceID
			read := func(path string, status int) []byte {
				t.Helper()
				w := httptest.NewRecorder()
				h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
				if w.Code != status {
					t.Fatal(w.Code, w.Body.String())
				}
				return w.Body.Bytes()
			}
			packed := read(url+"&metadata_version=1", 200)
			z, e := zip.NewReader(bytes.NewReader(packed), int64(len(packed)))
			if e != nil {
				t.Fatal(e)
			}
			contents := map[string][]byte{}
			for _, f := range z.File {
				r, e := f.Open()
				if e != nil {
					t.Fatal(e)
				}
				contents[f.Name], e = io.ReadAll(r)
				r.Close()
				if e != nil {
					t.Fatal(e)
				}
			}
			var manifest deliveryManifest
			if json.Unmarshal(contents["manifest.json"], &manifest) != nil || manifest.Metadata == nil || !reflect.DeepEqual(*manifest.Metadata, v1) {
				t.Fatal("version swapped", manifest)
			}
			if len(manifest.Files) != 4 || len(contents["cover.png"]) == 0 {
				t.Fatal("missing fixed cover", contents)
			}
			for _, f := range manifest.Files {
				sum := sha256.Sum256(contents[f.Name])
				if hex.EncodeToString(sum[:]) != f.SHA256 || int64(len(contents[f.Name])) != f.Size {
					t.Fatal(f)
				}
			}
			read(url+"&metadata_version=99", 404)
			read(url+"&metadata_version=0", 400)
			read("/api/v1/projects/"+p.ID+"/media/exports/cccccccccccccccccccccccccccccccc/delivery-metadata?space_id="+p.SpaceID, 404)
			read("/api/v1/projects/"+p.ID+"/media/exports/"+export+"/delivery-metadata?space_id=team-demo", 404)
			path, e := s.media.localBlob(p.SpaceID, p.ID, cover.ID)
			if e != nil {
				t.Fatal(e)
			}
			prepared, e := openDeliveryCover(s.media, context.Background(), p.SpaceID, p.ID, v1)
			if e != nil {
				t.Fatal(e)
			}
			if e = os.WriteFile(path, []byte("changed"), 0600); e != nil {
				t.Fatal(e)
			}
			if _, e = openDeliveryCover(s.media, context.Background(), p.SpaceID, p.ID, v1); e == nil {
				t.Fatal("changed cover accepted")
			}
			var partial bytes.Buffer
			e = writeDelivery(context.Background(), &partial, p.ID, MediaExport{ID: export}, []deliveryInput{prepared}, &v1)
			prepared.file.Close()
			if !errors.Is(e, ErrBlobIntegrity) {
				t.Fatal("cover changed between preparation and streaming", e)
			}
			if len(s.media.Get(p.ID, p.SpaceID).DeliveryMetadata) != 2 {
				t.Fatal("download or rejection changed history")
			}
		})
	}
}

func TestSeasonDeliveryPinsMetadataAndMissingCover(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	season := seriesFixture(t, s)
	_, p, e := s.CreateSeriesEpisode(ctx, seriesActor, "metadata-season", season.ID, series.Input{Title: "首集"})
	if e != nil {
		t.Fatal(e)
	}
	id := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	installSeasonDeliveryFixture(t, s, p, id)
	before, e := s.seasonDelivery(ctx, p.SpaceID, season.ID)
	if e != nil {
		t.Fatal(e)
	}
	in := publishing.DeliveryInput{Title: "本集发布", Tags: []string{}, RightsConfirmed: true}
	_, e = s.media.saveDeliveryMetadata(ctx, p.SpaceID, p.ID, id, "season-meta-one", "creator", in)
	if e != nil {
		t.Fatal(e)
	}
	after, e := s.seasonDelivery(ctx, p.SpaceID, season.ID)
	if e != nil || after.SHA256 == before.SHA256 || after.Episodes[0].Metadata == nil {
		t.Fatal(after, e)
	}
	w := httptest.NewRecorder()
	Handler(s, "").ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/seasons/"+season.ID+"/delivery.zip?space_id="+p.SpaceID+"&selection_sha256="+before.SHA256, nil))
	if w.Code != 409 {
		t.Fatal("old preview did not invalidate", w.Code)
	}
	var b bytes.Buffer
	if e = s.writeSeasonDelivery(ctx, &b, p.SpaceID, after); e != nil {
		t.Fatal(e)
	}
	z, e := zip.NewReader(bytes.NewReader(b.Bytes()), int64(b.Len()))
	if e != nil {
		t.Fatal(e)
	}
	for _, f := range z.File {
		if f.Name == "manifest.json" {
			r, _ := f.Open()
			data, _ := io.ReadAll(r)
			r.Close()
			if !bytes.Contains(data, []byte("本集发布")) {
				t.Fatal("season omitted metadata")
			}
		}
	}
	state, e := s.media.AddAsset(p.ID, p.SpaceID, "season-cover", "季集封面", "shot", "", "", "upload", miniMaxTestPNG(), "image/png")
	if e != nil {
		t.Fatal(e)
	}
	in.ExpectedMetadataVersion = 1
	in.CoverAssetID = state.Assets[0].ID
	v, e := s.media.saveDeliveryMetadata(ctx, p.SpaceID, p.ID, id, "season-meta-two", "creator", in)
	if e != nil {
		t.Fatal(e)
	}
	path, e := s.media.localBlob(p.SpaceID, p.ID, v.CoverAssetID)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.Remove(path); e != nil {
		t.Fatal(e)
	}
	missing, e := s.seasonDelivery(ctx, p.SpaceID, season.ID)
	if e != nil || missing.Ready || missing.Episodes[0].Issue != "cover_file_unavailable" {
		t.Fatal("missing cover was silently omitted", missing, e)
	}
}

func TestDeliveryMetadataRequiresCurrentTenantWritePermission(t *testing.T) {
	for _, mode := range []string{"file", "postgres"} {
		t.Run(mode, func(t *testing.T) {
			s := testStore(t)
			a, e := OpenAuthStore(t.TempDir())
			if e != nil {
				t.Fatal(e)
			}
			if mode == "postgres" {
				db := postgresTestDB(t)
				s = postgresTestStore(t, db)
				a, e = OpenPostgresAuthStore(t.TempDir(), db)
				if e != nil {
					t.Fatal(e)
				}
			}
			owner, token, session, e := a.login("release-owner@example.test", "synthetic-release-password", true)
			if e != nil {
				t.Fatal(e)
			}
			viewer, viewToken, viewSession, e := a.login("release-viewer@example.test", "synthetic-release-password", true)
			if e != nil {
				t.Fatal(e)
			}
			team := testTeamSpace(t, a, owner.ID)
			h := SecureHandler(s, "http://localhost:5173", a, AuthOptions{})
			authRequest(t, h, "PUT", "/api/v1/organizations/"+team+"/members", testMemberInput(t, a, viewer.Email, "viewer", 1), token, session.CSRF, 200)
			p, _, e := s.CreateProject(team, "secure-release-project", ProjectInput{Title: "团队发布", Brief: "只允许当前团队有编辑权限的成员准备发布资料。"})
			if e != nil {
				t.Fatal(e)
			}
			id := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
			installSeasonDeliveryFixture(t, s, p, id)
			path := "/api/v1/projects/" + p.ID + "/media/exports/" + id + "/delivery-metadata?space_id=" + team
			in := `{"expected_metadata_version":0,"title":"真实保存","description":"人工确认","tags":[],"rights_confirmed":true}`
			authRequest(t, h, "POST", path, in, viewToken, viewSession.CSRF, 403)
			authRequest(t, h, "POST", path, in, token, "", 403)
			authRequest(t, h, "POST", path, in, token, session.CSRF, 200)
			authRequest(t, h, "GET", path, "", viewToken, viewSession.CSRF, 200)
			other := testTeamSpace(t, a, owner.ID)
			authRequest(t, h, "GET", "/api/v1/projects/"+p.ID+"/media/exports/"+id+"/delivery-metadata?space_id="+other, "", token, session.CSRF, 404)
			if len(s.media.Get(p.ID, p.SpaceID).DeliveryMetadata) != 1 {
				t.Fatal("rejected operation created a release")
			}
		})
	}
}
