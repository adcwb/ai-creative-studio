package application

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/library"
	"github.com/adcwb/ai-creative-studio/backend/internal/persistence/libraryrepo"
)

func TestVisualPropProductSceneActuallyReachGenerationAndKeepAuthority(t *testing.T) {
	cards := map[string]library.Card{
		"prop":    {Name: "铜钥匙", Prop: &library.Prop{Appearance: "PROP_MARK_铜齿", Dimensions: "PROP_SCALE_手掌大小", Materials: "PROP_MATERIAL_黄铜", StoryFunction: "PROP_FUNCTION_打开密室", Condition: "PROP_STATE_缺失一齿", ContinuityNotes: "PROP_CONTINUITY_本集不能修复"}},
		"product": {Name: "旧钟", Product: &library.Product{Model: "PRODUCT_MODEL_七号", Dimensions: "PRODUCT_SCALE_壁钟", Materials: "PRODUCT_MATERIAL_木壳", Packaging: "PRODUCT_PACK_无包装", ExactText: "PRODUCT_TEXT_晨光"}},
		"scene":   {Name: "钟楼", Scene: &library.Scene{Layout: "SCENE_LAYOUT_东侧楼梯", Objects: "SCENE_OBJECT_缺口钟盘", Lighting: "SCENE_LIGHT_雨夜", Viewpoints: "SCENE_CAMERA_窗内向外"}},
	}
	for _, storage := range []string{"file", "postgres"} {
		for kind, card := range cards {
			t.Run(storage+"/"+kind, func(t *testing.T) {
				s := testStore(t)
				if storage == "postgres" {
					s = postgresTestStore(t, postgresTestDB(t))
				}
				card.ProhibitedChanges = []string{"不得凭空恢复缺失部件"}
				p, a, binding, ids := inputLibraryFixture(t, s, kind, card)
				inputLibraryGrant(t, s, a, 0, "visual-assets-grant", "prompt_context", "image_reference")
				var calls atomic.Int32
				output := pngFixture(t, 1536, 864)
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					if r.URL.Path != "/images/edits" {
						t.Error("reference was not sent", r.URL.Path)
					}
					if err := r.ParseMultipartForm(8 << 20); err != nil {
						t.Error(err)
						return
					}
					defer r.MultipartForm.RemoveAll()
					file, _, err := r.FormFile("image[]")
					if err != nil {
						t.Error("missing authorized reference", err)
						return
					}
					raw, _ := io.ReadAll(file)
					file.Close()
					if len(raw) == 0 {
						t.Error("empty reference")
					}
					prompt := r.FormValue("prompt")
					body, _ := json.Marshal(card)
					var fields map[string]any
					_ = json.Unmarshal(body, &fields)
					for _, v := range fields[kind].(map[string]any) {
						if text, ok := v.(string); ok && text != "" && !strings.Contains(prompt, text) {
							t.Error("authored visual field omitted", text)
						}
					}
					if !strings.Contains(prompt, "不得凭空恢复缺失部件") {
						t.Error("continuity constraint omitted")
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]string{"b64_json": base64.StdEncoding.EncodeToString(output)}}})
				}))
				defer upstream.Close()
				provider := newOpenAIProvider("synthetic", upstream.URL+"/images/generations", upstream.Client())
				SetCreativeImage(s, provider, Capabilities{Ready: true, Mode: "real", Provider: "openai-images", Model: provider.model})
				in := CreativeJobInput{Kind: "image", Prompt: "保留已固定的连续性细节", ExpectedVersion: p.Version, ExpectedProvider: "openai-images", ExpectedModel: provider.model, LibraryInputs: []libraryInputChoice{{BindingID: binding, Mode: "reference", MediaAssetIDs: ids}}}
				job, _, err := s.createCreativeJob(p.SpaceID, p.ID, "visual-asset-generation", in)
				if err != nil || len(job.LibraryInputs) != 1 || job.LibraryInputs[0].Kind != kind || job.LibraryInputs[0].ContentSHA256 != a.Version.SHA256 {
					t.Fatal("visual input not fixed", job, err)
				}
				if err = s.executeCreativeJob(context.Background(), &job); err != nil {
					t.Fatal(err)
				}
				asset, err := s.media.AssetForReceipt(p.ID, p.SpaceID, "creative-"+job.ID)
				if err != nil || len(asset.LibrarySources) != 1 || asset.LibrarySources[0].VersionID != a.Version.ID || calls.Load() != 1 {
					t.Fatal("lost source or duplicate call", asset, err, calls.Load())
				}
				intentLibraryCall(t, s, "POST", "/api/v1/library/assets/"+a.Asset.ID+"/use-authorization?space_id="+p.SpaceID, "visual-revoke", library.AuthorizationInput{Expected: 1, Status: "revoked", Reason: "撤销本次素材用途声明"}, 200)
				if _, _, err = s.createCreativeJob(p.SpaceID, p.ID, "visual-after-revoke", in); err == nil || calls.Load() != 1 {
					t.Fatal("revoked source used again", err, calls.Load())
				}
				if _, err = s.library.Get(context.Background(), "foreign", a.Asset.ID); !errors.Is(err, libraryrepo.ErrNotFound) {
					t.Fatal("foreign source exposed", err)
				}
			})
		}
	}
}

func TestPostgresPropKindUpgradeFromV2PreservesVersions(t *testing.T) {
	ctx := context.Background()
	db := postgresTestDB(t)
	s := postgresTestStore(t, db)
	old := libraryWrite{Kind: "product", Card: library.Card{Name: "旧商品", ProhibitedChanges: []string{}, Product: &library.Product{Model: "clock-v1"}}, References: []libraryReferenceInput{}}
	var original libraryrepo.Result
	if err := json.Unmarshal(intentLibraryCall(t, s, "POST", "/api/v1/library/assets?space_id=personal-demo", "prop-upgrade-old", old, 201), &original); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`ALTER TABLE studio_library_assets DROP CONSTRAINT studio_library_kind_v3; ALTER TABLE studio_library_assets ADD CONSTRAINT studio_library_kind_v2 CHECK(kind IN ('character','product','scene','style','voice'))`); err != nil {
		t.Fatal(err)
	}
	if err := CheckPostgres(ctx, db); err == nil {
		t.Fatal("v2 advertised prop readiness")
	}
	for i := 0; i < 2; i++ {
		if err := InitPostgres(ctx, db); err != nil {
			t.Fatal(err)
		}
	}
	if err := CheckPostgres(ctx, db); err != nil {
		t.Fatal(err)
	}
	saved, err := s.library.Get(ctx, "personal-demo", original.Asset.ID)
	if err != nil || !reflect.DeepEqual(saved, original) {
		t.Fatal("upgrade rewrote old immutable content", err)
	}
	prop := libraryWrite{Kind: "prop", Card: library.Card{Name: "缺齿钥匙", ProhibitedChanges: []string{}, Prop: &library.Prop{Condition: "缺失一齿", StoryFunction: "剧情关键道具"}}, References: []libraryReferenceInput{}}
	var created libraryrepo.Result
	_ = json.Unmarshal(intentLibraryCall(t, s, "POST", "/api/v1/library/assets?space_id=personal-demo", "prop-upgrade-new", prop, 201), &created)
	if created.Asset.Kind != "prop" || created.Version.Card.Prop == nil || created.Version.Card.Prop.Condition != "缺失一齿" {
		t.Fatal(created)
	}
}
