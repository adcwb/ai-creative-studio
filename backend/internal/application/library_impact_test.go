package application

import (
	"context"
	"encoding/json"
	"net/url"
	"reflect"
	"testing"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/library"
	"github.com/adcwb/ai-creative-studio/backend/internal/modules/media"
	"github.com/adcwb/ai-creative-studio/backend/internal/modules/series"
	"github.com/adcwb/ai-creative-studio/backend/internal/persistence/libraryrepo"
)

func TestLibraryImpactAcrossEpisodesIsPagedScopedAndReadOnly(t *testing.T) {
	for _, storage := range []string{"file", "postgres"} {
		t.Run(storage, func(t *testing.T) {
			s := testStore(t)
			if storage == "postgres" {
				s = postgresTestStore(t, postgresTestDB(t))
			}
			ctx := context.Background()
			season := seriesFixture(t, s)
			_, first, err := s.CreateSeriesEpisode(ctx, seriesActor, "impact-first-episode", season.ID, series.Input{Title: "钥匙出现"})
			if err != nil {
				t.Fatal(err)
			}
			_, second, err := s.CreateSeriesEpisode(ctx, seriesActor, "impact-second-episode", season.ID, series.Input{Title: "钥匙损坏"})
			if err != nil {
				t.Fatal(err)
			}
			write := libraryWrite{Kind: "prop", Card: library.Card{Name: "铜钥匙", Prop: &library.Prop{Condition: "缺失一齿"}, ProhibitedChanges: []string{"缺齿位置保持一致"}}, References: []libraryReferenceInput{}}
			query := "?space_id=" + first.SpaceID
			var original libraryrepo.Result
			_ = json.Unmarshal(intentLibraryCall(t, s, "POST", "/api/v1/library/assets"+query, "impact-prop-create", write, 201), &original)
			for _, p := range []Project{first, second} {
				state := s.media.Get(p.ID, p.SpaceID)
				intentLibraryCall(t, s, "POST", "/api/v1/projects/"+p.ID+"/library-bindings"+query, "impact-bind-"+p.ID, libraryBindingInput{ExpectedVersion: state.Version, AssetID: original.Asset.ID, VersionID: original.Version.ID, Usage: "prop"}, 201)
			}
			sourceCtx := context.WithValue(ctx, libraryOutputSourcesKey{}, []media.LibrarySource{{AssetID: original.Asset.ID, VersionID: original.Version.ID}})
			if _, err = s.media.addAssetContext(sourceCtx, first.ID, first.SpaceID, "impact-recorded-media", "合成来源素材", "reference", "", "", "synthetic", syntheticCreativePNG(), "image/png"); err != nil {
				t.Fatal(err)
			}
			write.ExpectedVersion = original.Version.ID
			write.Card.Prop.Condition = "又增加一处划痕"
			var latest libraryrepo.Result
			_ = json.Unmarshal(intentLibraryCall(t, s, "POST", "/api/v1/library/assets/"+original.Asset.ID+"/versions"+query, "impact-prop-update", write, 201), &latest)
			beforeFirst := s.media.Get(first.ID, first.SpaceID)
			beforeSecond := s.media.Get(second.ID, second.SpaceID)
			type page struct {
				Head     string          `json:"head_version_id"`
				Items    []libraryImpact `json:"items"`
				Next     string          `json:"next_cursor"`
				Coverage string          `json:"coverage"`
			}
			var one, two page
			path := "/api/v1/library/assets/" + original.Asset.ID + "/impact" + query + "&limit=1"
			if err = json.Unmarshal(intentLibraryCall(t, s, "GET", path, "", nil, 200), &one); err != nil {
				t.Fatal(err)
			}
			if len(one.Items) != 1 || one.Next == "" || one.Head != latest.Version.ID || one.Coverage != "recorded_bindings_and_media_sources" {
				t.Fatal("first impact page", one)
			}
			if err = json.Unmarshal(intentLibraryCall(t, s, "GET", path+"&cursor="+url.QueryEscape(one.Next), "", nil, 200), &two); err != nil {
				t.Fatal(err)
			}
			if len(two.Items) != 1 || two.Next != "" || two.Items[0].ProjectID == one.Items[0].ProjectID {
				t.Fatal("impact pagination", one, two)
			}
			seen := map[string]bool{}
			for _, item := range append(one.Items, two.Items...) {
				seen[item.ProjectID] = true
				if item.SeriesID != season.SeriesID || item.SeasonID != season.ID || item.EpisodeID == "" || item.EpisodeNumber < 1 || item.Bindings != 1 || !item.Outdated || !reflect.DeepEqual(item.Versions, []string{original.Version.ID}) {
					t.Fatal("lost episode or pinned version", item)
				}
				if item.ProjectID == first.ID && item.Media != 1 || item.ProjectID == second.ID && item.Media != 0 {
					t.Fatal("recorded source counts", item)
				}
			}
			if !seen[first.ID] || !seen[second.ID] {
				t.Fatal("lost episode impact")
			}
			if !reflect.DeepEqual(beforeFirst, s.media.Get(first.ID, first.SpaceID)) || !reflect.DeepEqual(beforeSecond, s.media.Get(second.ID, second.SpaceID)) {
				t.Fatal("impact query rewrote saved work")
			}
			foreignSpace := "team-demo"
			if first.SpaceID == foreignSpace {
				foreignSpace = "personal-demo"
			}
			intentLibraryCall(t, s, "GET", "/api/v1/library/assets/"+original.Asset.ID+"/impact?space_id="+foreignSpace, "", nil, 404)
			var other libraryrepo.Result
			_ = json.Unmarshal(intentLibraryCall(t, s, "POST", "/api/v1/library/assets"+query, "impact-another-prop", libraryWrite{Kind: "prop", Card: library.Card{Name: "其他道具", Prop: &library.Prop{}, ProhibitedChanges: []string{}}, References: []libraryReferenceInput{}}, 201), &other)
			intentLibraryCall(t, s, "GET", "/api/v1/library/assets/"+other.Asset.ID+"/impact"+query+"&cursor="+url.QueryEscape(one.Next), "", nil, 400)
		})
	}
}
