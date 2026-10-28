package application

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestModerationRequiresExplicitAuthorization(t *testing.T) {
	for _, v := range []struct {
		key, model string
		authorized bool
	}{{"synthetic", "omni-moderation-latest", false}, {"", "omni-moderation-latest", true}, {"synthetic", "", true}} {
		if _, err := NewOpenAITextModerator(v.key, v.model, v.authorized); err == nil {
			t.Fatal("unsafe configuration accepted")
		}
	}
	d, e := (UnavailableModerator{}).CheckText(context.Background(), "synthetic")
	if e == nil || d.Status != "unavailable" {
		t.Fatal(d, e)
	}
}
func TestModerationSignalsAndFailures(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
		status           int
		failure          bool
	}{
		{"allow", `{"model":"explicit-revision","results":[{"flagged":false,"categories":{"violence":false}}]}`, "allow", 200, false},
		{"review", `{"model":"explicit-revision","results":[{"flagged":true,"categories":{"violence":true}}]}`, "review", 200, false},
		{"conflicting_signal", `{"model":"explicit-revision","results":[{"flagged":false,"categories":{"violence":true}}]}`, "review", 200, false},
		{"missing_flag", `{"model":"revision","results":[{"categories":{}}]}`, "unavailable", 200, true},
		{"null_flag", `{"model":"revision","results":[{"flagged":null,"categories":{}}]}`, "unavailable", 200, true},
		{"empty", `{"model":"revision","results":[]}`, "unavailable", 200, true},
		{"rate_limit", "synthetic-private-response", "unavailable", 429, true},
		{"oversize", strings.Repeat("x", 65537), "unavailable", 200, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != "POST" || r.Header.Get("Authorization") != "Bearer synthetic" {
					t.Error("request mismatch")
				}
				var body map[string]string
				if json.NewDecoder(r.Body).Decode(&body) != nil || body["model"] != "omni-moderation-latest" || body["input"] != "synthetic fixture" {
					t.Error("input mismatch")
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer s.Close()
			p := newTextModerator("synthetic", "omni-moderation-latest", s.URL, s.Client())
			d, e := p.CheckText(context.Background(), "synthetic fixture")
			if d.Status != tc.want || (e != nil) != tc.failure || calls != 1 {
				t.Fatal(d, e, calls)
			}
			if e != nil && strings.Contains(e.Error(), "synthetic-private") {
				t.Fatal("leaked response")
			}
		})
	}
}
func TestModerationNeverFollowsRedirectOrInvalidInput(t *testing.T) {
	calls := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Location", "/again")
		w.WriteHeader(307)
	}))
	defer s.Close()
	p := newTextModerator("synthetic", "omni-moderation-latest", s.URL, s.Client())
	if d, e := p.CheckText(context.Background(), "synthetic"); e == nil || d.Status != "unavailable" || calls != 1 {
		t.Fatal(d, e, calls)
	}
	for _, in := range []string{"", strings.Repeat("a", 32769), string([]byte{255})} {
		if _, e := p.CheckText(context.Background(), in); e == nil {
			t.Fatal("invalid input accepted")
		}
	}
	if calls != 1 {
		t.Fatal("invalid input sent")
	}
}
