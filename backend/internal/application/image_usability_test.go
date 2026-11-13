package application

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTaskPaginationCompatibilityBoundariesAndIsolation(t *testing.T) {
	s := imageUsabilityStore(t)
	for i := 0; i < 8; i++ {
		id := fmt.Sprintf("%032d", i)
		s.data.Tasks[id] = Task{ID: id, SpaceID: "personal-demo", Status: "succeeded", CreatedAt: time.Unix(int64(i), 0), Input: Input{Prompt: "Synthetic task", Kind: "general"}}
	}
	s.data.Tasks["foreign"] = Task{ID: "foreign", SpaceID: "team-demo", Status: "running"}
	h := Handler(s, "http://localhost:5173")
	read := func(query string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/tasks?space_id=personal-demo"+query, nil))
		return w
	}
	var old []Task
	if w := read(""); w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &old) != nil || len(old) != 8 {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, test := range []struct {
		query       string
		page, count int
	}{{"&page=1&page_size=6", 1, 6}, {"&page=2&page_size=6", 2, 2}, {"&page=999999999&page_size=6", 2, 2}, {"&page_size=100", 1, 8}} {
		w := read(test.query)
		var page TaskPage
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &page) != nil || page.Page != test.page || len(page.Items) != test.count || page.Total != 8 || page.Running != 0 {
			t.Fatal(test, w.Code, w.Body.String())
		}
		if page.Page == 1 && page.Items[0].ID != fmt.Sprintf("%032d", 7) {
			t.Fatal("wrong ordering")
		}
	}
	for _, q := range []string{"&page=0", "&page=-1", "&page=oops", "&page_size=0", "&page_size=101", "&page_size="} {
		if w := read(q); w.Code != 400 {
			t.Fatal(q, w.Code)
		}
	}
}

func TestOriginalDownloadPreservesBytesAndSpaceAuthorization(t *testing.T) {
	s := imageUsabilityStore(t)
	id := "0123456789abcdef0123456789abcdef"
	svg := `<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10"></svg>`
	s.data.Tasks[id] = Task{ID: id, SpaceID: "personal-demo", Status: "succeeded", SVG: svg, Provider: "mock-svg", ResultMIME: "image/svg+xml"}
	h := Handler(s, "http://localhost:5173")
	for _, space := range []string{"personal-demo", "team-demo"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/tasks/"+id+"/result?space_id="+space+"&download=1", nil))
		if space == "personal-demo" {
			if w.Code != 200 || w.Body.String() != svg || !strings.Contains(w.Header().Get("Content-Disposition"), "creation-"+id+".svg") {
				t.Fatal(w.Code, w.Body.String(), w.Header())
			}
		} else if w.Code != 404 || w.Header().Get("Content-Disposition") != "" {
			t.Fatal("foreign download allowed", w.Code)
		}
	}
}

func TestPromptOptimizationUsesGovernedVendorTextAndNoImageTask(t *testing.T) {
	for _, vendor := range []string{"openai-compatible", gatewayBailian} {
		t.Run(vendor, func(t *testing.T) {
			calls := 0
			answer := `{"prompt":"A softly lit coffee poster with empty title space"}`
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				time.Sleep(300 * time.Millisecond)
				want := "/v1/chat/completions"
				if vendor == gatewayBailian {
					want = "/compatible-mode/v1/chat/completions"
				}
				if r.URL.Path != want {
					t.Error(r.URL.Path)
				}
				var body struct {
					Messages []struct {
						Content string `json:"content"`
					} `json:"messages"`
				}
				json.NewDecoder(r.Body).Decode(&body)
				if len(body.Messages) != 2 || !strings.Contains(body.Messages[0].Content, "1000 Unicode") || !strings.Contains(body.Messages[1].Content, "coffee") {
					t.Error("missing source or instructions")
				}
				json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]string{"content": answer}}}})
			}))
			defer srv.Close()
			u, _ := url.Parse(srv.URL)
			t.Setenv("MODEL_GATEWAY_ALLOWED_HOSTS", u.Host)
			endpoint := srv.URL + "/v1"
			if vendor == gatewayBailian {
				endpoint = srv.URL + "/api/v1"
			}
			runtime, g, m := gatewayFixture(t, endpoint)
			if vendor == gatewayBailian {
				g.Vendor = vendor
				if _, err := runtime.auth.models.saveGateway("tenant-a", g.Version, g, ModelCredentialInput{Action: "keep"}); err != nil {
					t.Fatal(err)
				}
			}
			_, err := runtime.auth.models.saveFeatureBinding("tenant-a", "actor-a", 0, FeatureBinding{FeatureKey: "prompt_optimization", ModelConfigID: m.ID, Enabled: true})
			if err != nil {
				t.Fatal(err)
			}
			s := imageUsabilityStore(t)
			s.runtime = runtime
			ledger, err := OpenBudgetLedger(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			s.execution = &ExecutionGovernor{Ledger: ledger, ModerationDisabled: true}
			h := Handler(s, "http://localhost:5173")
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				scope := authScope{UserID: "actor-a", SpaceID: r.URL.Query().Get("space_id"), Role: "editor"}
				h.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), authScopeKey{}, scope)))
			}))
			// Exercise the override through a real connection: the upstream is
			// slower than this server's default write timeout.
			server.Config.WriteTimeout = 200 * time.Millisecond
			server.Start()
			defer server.Close()
			call := func(key, body string, scope authScope) *httptest.ResponseRecorder {
				r, err := http.NewRequest("POST", server.URL+"/api/v1/prompt-optimizations?space_id="+scope.SpaceID, strings.NewReader(body))
				if err != nil {
					t.Fatal(err)
				}
				r.Header.Set("Content-Type", "application/json")
				r.Header.Set("Idempotency-Key", key)
				response, err := server.Client().Do(r)
				if err != nil {
					t.Fatal(err)
				}
				defer response.Body.Close()
				w := httptest.NewRecorder()
				w.WriteHeader(response.StatusCode)
				if _, err = io.Copy(w.Body, response.Body); err != nil {
					t.Fatal(err)
				}
				return w
			}
			scope := authScope{UserID: "actor-a", SpaceID: "tenant-a", Role: "editor"}
			body := fmt.Sprintf(`{"prompt":"coffee poster","kind":"poster","ratio":"16:9","style":"natural","model_config_id":%q,"model_config_version":%d}`, m.ID, m.Version)
			w := call("synthetic-optimize", body, scope)
			if w.Code != 200 || !strings.Contains(w.Body.String(), "softly lit") || calls != 1 || len(s.List("tenant-a")) != 0 {
				t.Fatal(w.Code, w.Body.String(), calls)
			}
			if w = call("synthetic-optimize", body, scope); w.Code != 409 || calls != 1 {
				t.Fatal("duplicate dispatch", w.Code, calls)
			}
			invalid := strings.Replace(body, "coffee poster", "x", 1)
			if w = call("synthetic-invalid", invalid, scope); w.Code != 400 || calls != 1 {
				t.Fatal("invalid input dispatched", w.Code, calls)
			}
			foreign := authScope{UserID: "actor-a", SpaceID: "tenant-b", Role: "editor"}
			if w = call("synthetic-foreign", body, foreign); w.Code != 403 || calls != 1 {
				t.Fatal("cross tenant dispatched", w.Code, calls)
			}
			answer = `{"prompt":"` + strings.Repeat("x", 1001) + `"}`
			if w = call("synthetic-output", body, scope); w.Code != 502 || calls != 2 {
				t.Fatal("invalid output accepted", w.Code, w.Body.String(), calls)
			}
		})
	}
}

func imageUsabilityStore(t *testing.T) *FileStore {
	t.Helper()
	s, err := OpenStore(filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
