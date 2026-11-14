package application

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
)

func TestScriptStructuredOutputCompatibilityAndFailures(t *testing.T) {
	valid, _ := json.Marshal(scriptFixture(t))
	for _, tc := range []struct {
		name, content, finish, code string
	}{
		{"plain-json", string(valid), "stop", ""},
		{"json-fence", "```json\n" + string(valid) + "\n```", "stop", ""},
		{"crlf-fence", "```json\r\n" + string(valid) + "\r\n```", "stop", ""},
		{"untyped-fence", "```\n" + string(valid) + "\n```", "stop", ""},
		{"truncated", string(valid), "length", "provider_output_truncated"},
		{"invalid-structure", `{"title":"没有场次"}`, "stop", "invalid_provider_output"},
		{"invalid-json", `{"title":`, "stop", "invalid_provider_output"},
		{"extra-prose", "Here is your script:\n" + string(valid), "stop", "invalid_provider_output"},
		{"extra-object", string(valid) + `{}`, "stop", "invalid_provider_output"},
		{"refusal", string(valid), "content_filter", "invalid_provider_response"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"finish_reason": tc.finish, "message": map[string]string{"content": tc.content}}}})
			}))
			defer upstream.Close()
			p := newCreativeProvider("synthetic", upstream.URL, upstream.Client())
			result, err := p.Script(context.Background(), ProjectInput{Title: "测试剧本", Brief: "修钟师寻找旧信的主人"})
			if tc.code == "" {
				if err != nil || !reflect.DeepEqual(result, scriptFixture(t)) {
					t.Fatal("valid structured script rejected", err)
				}
			} else {
				var pe *ProviderError
				if !errors.As(err, &pe) || pe.Code != tc.code {
					t.Fatalf("expected %s, got %v", tc.code, err)
				}
				code, message := managedGenerationFailure(err)
				if code != tc.code || strings.Contains(message, "synthetic") {
					t.Fatal("unsafe or missing diagnosis", code, message)
				}
			}
			if calls != 1 {
				t.Fatal("unexpected automatic model retry", calls)
			}
		})
	}
}

func TestDirectScriptRegenerationKeepsHistoryAndFailedDraft(t *testing.T) {
	s := testStore(t)
	before := boardProject(t, s)
	g, _, err := s.CreateScriptGeneration(before.SpaceID, before.ID, "direct-regenerate", ScriptGenerationInput{ExpectedVersion: before.Version})
	if err != nil {
		t.Fatal(err)
	}
	output := scriptFixture(t)
	output.Title = "直接生成的新剧本"
	if err = s.FinishScriptGeneration(g, output, nil); err != nil {
		t.Fatal(err)
	}
	after, err := s.GetProject(before.SpaceID, before.ID)
	if err != nil || after.Script.Title != output.Title || after.ScriptVersion != before.ScriptVersion+1 || !after.StoryboardStale || !reflect.DeepEqual(after.Storyboard, before.Storyboard) {
		t.Fatal("direct generation did not preserve storyboard and update draft", err)
	}
	history, err := s.ProjectHistory(before.SpaceID, before.ID)
	if err != nil || len(history) == 0 || !reflect.DeepEqual(history[0].Script, before.Script) {
		t.Fatal("previous draft not recoverable", err)
	}
	failed, _, err := s.CreateScriptGeneration(after.SpaceID, after.ID, "direct-regenerate-fail", ScriptGenerationInput{ExpectedVersion: after.Version})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.FinishScriptGeneration(failed, Script{}, providerFailure("provider_output_truncated")); err != nil {
		t.Fatal(err)
	}
	preserved, err := s.GetProject(after.SpaceID, after.ID)
	if err != nil || preserved.Version != after.Version || !reflect.DeepEqual(preserved.Script, after.Script) || preserved.LatestGeneration.ErrorCode != "provider_output_truncated" {
		t.Fatal("failed generation changed saved draft", err)
	}
	restored, err := s.RestoreHistory(after.SpaceID, after.ID, history[0].ID, "direct-restore", ScriptGenerationInput{ExpectedVersion: after.Version})
	if err != nil || !reflect.DeepEqual(restored.Script, before.Script) {
		t.Fatal("old draft restoration failed", err)
	}
}

func TestResponsesOutputLimitHasActionableFailure(t *testing.T) {
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_ = json.NewEncoder(w).Encode(map[string]any{"object": "response", "status": "incomplete", "incomplete_details": map[string]string{"reason": "max_output_tokens"}, "output": []any{}})
	}))
	defer upstream.Close()
	p := newCreativeProvider("synthetic", upstream.URL, upstream.Client())
	p.textProtocol = "responses"
	_, err := p.Script(context.Background(), ProjectInput{Title: "测试剧本", Brief: "修钟师寻找旧信的主人"})
	var pe *ProviderError
	if !errors.As(err, &pe) || pe.Code != "provider_output_truncated" || calls != 1 {
		t.Fatal(err, calls)
	}
}

func TestDirectScriptGenerationRetainsCanonicalWritingContext(t *testing.T) {
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var envelope struct{ Messages []struct{ Content string } }
		if err := json.NewDecoder(r.Body).Decode(&envelope); err != nil || len(envelope.Messages) != 2 {
			t.Error("missing text request", err)
		}
		if len(envelope.Messages) == 2 && !strings.Contains(envelope.Messages[1].Content, "本集明确节拍") {
			t.Error("direct generation omitted canonical writing material")
		}
		out, _ := json.Marshal(scriptFixture(t))
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]string{"content": string(out)}}}})
	}))
	defer upstream.Close()
	endpoint, _ := url.Parse(upstream.URL)
	t.Setenv("MODEL_GATEWAY_ALLOWED_HOSTS", endpoint.Host)
	s := testStore(t)
	snapshot, model, _ := writingFixture(t, s, upstream.URL+"/v1")
	before, err := s.GetProject(checkActor.Space, snapshot.EpisodeID)
	if err != nil {
		t.Fatal(err)
	}
	g, _, err := s.CreateScriptGeneration(checkActor.Space, snapshot.EpisodeID, "direct-context-generation", ScriptGenerationInput{ExpectedVersion: before.Version, ContextID: snapshot.ID, ModelRequest: model})
	if err != nil {
		t.Fatal(err)
	}
	output, err := s.managedScript(context.Background(), g, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.FinishScriptGeneration(g, output, nil); err != nil {
		t.Fatal(err)
	}
	after, err := s.GetProject(checkActor.Space, snapshot.EpisodeID)
	if err != nil || after.ScriptVersion != before.ScriptVersion+1 || after.LatestGeneration.Status != "succeeded" || after.LatestGeneration.ContextSHA256 != snapshot.SHA256 || calls != 1 {
		t.Fatal("direct generation lost canonical context or did not update current script", err, calls)
	}
}

func TestDirectScriptGenerationHTTPReplaysBeforeModelResolution(t *testing.T) {
	s := testStore(t)
	p := createProject(t, s)
	in := ScriptGenerationInput{ExpectedVersion: p.Version}
	g, _, err := s.CreateScriptGeneration(p.SpaceID, p.ID, "direct-http-receipt", in)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.FinishScriptGeneration(g, scriptFixture(t), nil); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(in)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/projects/"+p.ID+"/gateway-generations?space_id="+p.SpaceID, strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "direct-http-receipt")
	w := httptest.NewRecorder()
	Handler(s, "http://localhost:5173").ServeHTTP(w, req)
	if w.Code != 200 || !strings.Contains(w.Body.String(), g.ID) {
		t.Fatal("original receipt required another model selection", w.Code, w.Body.String())
	}
	list, err := s.ListScriptGenerations(p.SpaceID, p.ID)
	if err != nil || len(list) != 1 {
		t.Fatal("checking original request created another generation", err)
	}
}
