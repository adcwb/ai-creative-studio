package application

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProviderExpectationGuardAndAcceptedKeyReplay(t *testing.T) {
	for _, caps := range []Capabilities{mockCapabilities(), realCapabilities()} {
		t.Run(caps.Provider, func(t *testing.T) {
			s, e := OpenStore(t.TempDir(), caps)
			if e != nil {
				t.Fatal(e)
			}
			defer s.Close()
			wrong := Expectation{Provider: "mock-svg"}
			right := Expectation{Provider: caps.Provider, Model: caps.Model}
			if caps.Provider == "mock-svg" {
				wrong = Expectation{Provider: "openai-images", Model: OpenAIModel}
			}
			if _, _, e = s.Create("personal-demo", "guard", realInput(), wrong); !errors.Is(e, ErrProviderChanged) || len(s.List("personal-demo")) != 0 {
				t.Fatal("changed provider queued task", e)
			}
			task, _, e := s.Create("personal-demo", "guard", realInput(), right)
			if e != nil {
				t.Fatal(e)
			}
			replay, created, e := s.Create("personal-demo", "guard", realInput(), wrong)
			if e != nil || created || replay.ID != task.ID {
				t.Fatal("guard blocked accepted-key replay")
			}
			s.Finish(task, GenerationResult{}, &ProviderError{Code: "provider_error"})
			if _, _, e = s.Retry("personal-demo", task.ID, "retry", realInput(), wrong); !errors.Is(e, ErrProviderChanged) || len(s.List("personal-demo")) != 1 {
				t.Fatal("retry bypassed guard", e)
			}
			body := `{"prompt":"Synthetic persistent PNG fixture","kind":"general","ratio":"1:1","style":"natural","expected_provider":"` + wrong.Provider + `","expected_model":"` + wrong.Model + `"}`
			r := httptest.NewRequest("POST", "/api/v1/tasks?space_id=personal-demo", strings.NewReader(body))
			r.Header.Set("Idempotency-Key", "http-guard")
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			Handler(s, "").ServeHTTP(w, r)
			if w.Code != 409 || !strings.Contains(w.Body.String(), "provider_changed") {
				t.Fatal(w.Code, w.Body.String())
			}
		})
	}
}
func TestHTTPExpectationValidationDoesNotQueue(t *testing.T) {
	s := testStore(t)
	for _, extra := range []string{`"expected_provider":null,"expected_model":""`, `"expected_provider":"mock-svg"`, `"expected_model":""`, `"expected_provider":"bad","expected_model":""`, `"expected_provider":"mock-svg","expected_model":"unexpected"`, `"expected_provider":"openai-images","expected_model":""`, `"expected_provider":"mock-svg","expected_model":null`, `"expected_provider":3,"expected_model":""`} {
		body := `{"prompt":"guard validation","kind":"general",` + extra + `}`
		req := httptest.NewRequest("POST", "/api/v1/tasks?space_id=personal-demo", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", "validation")
		rec := httptest.NewRecorder()
		Handler(s, "").ServeHTTP(rec, req)
		if rec.Code != 400 || !strings.Contains(rec.Body.String(), "invalid_input") {
			t.Fatal(extra, rec.Code, rec.Body.String())
		}
	}
	if len(s.List("personal-demo")) != 0 {
		t.Fatal("invalid expectation queued task")
	}
}
func TestProviderSecretsExcludedFromStateAndPublicErrors(t *testing.T) {
	s := realStore(t, t.TempDir())
	defer s.Close()
	task, _, _ := s.Create("personal-demo", "key", realInput())
	s.Finish(task, GenerationResult{}, errors.New(syntheticAPIKey+" upstream-private-body"))
	got, _ := s.Get("personal-demo", task.ID)
	b, _ := json.Marshal(s.data)
	if strings.Contains(string(b), syntheticAPIKey) || strings.Contains(string(b), "upstream-private-body") || got.ErrorCode != "provider_error" {
		t.Fatal("secret error persisted", got.ErrorCode)
	}
}
