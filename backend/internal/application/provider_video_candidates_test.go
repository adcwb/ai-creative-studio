package application

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// These are wire-contract fixtures, not model quality or account-access tests.
func TestReviewedVideoCandidateWireSpecifications(t *testing.T) {
	for _, tc := range []struct {
		provider, model, resolution string
		seconds                     int
	}{
		{"minimax", "MiniMax-H3-Lite", "480P", 4},
		{"minimax", "MiniMax-H3", "768P", 5},
		{"minimax", "MiniMax-H3-Lite-unreviewed", "768P", 5},
		{"ark", "doubao-seedance-2-0-mini-260615", "480p", 5},
		{"ark", "other-explicit-model", "720p", 5},
		{"ark", "doubao-seedance-2-0-mini-260615-unreviewed", "720p", 5},
	} {
		t.Run(tc.model, func(t *testing.T) {
			var calls atomic.Int32
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				call := calls.Add(1)
				var body map[string]any
				if r.Method != "POST" || r.Header.Get("Authorization") != "Bearer synthetic" || json.NewDecoder(r.Body).Decode(&body) != nil {
					t.Error("invalid method, authorization or JSON")
				}
				if body["model"] != tc.model || body["resolution"] != tc.resolution || body["duration"] != float64(tc.seconds) {
					t.Errorf("candidate specification changed: %#v", body)
				}
				content, ok := body["content"].([]any)
				if !ok || len(content) != int(call) {
					t.Error("unexpected content or duplicate submission")
				} else if call == 2 {
					if ref, ok := content[1].(map[string]any); !ok || ref["role"] != "first_frame" {
						t.Error("missing image conditioning")
					}
				}
				if tc.provider == "ark" {
					if r.URL.Path != "/contents/generations/tasks" || body["watermark"] != true || body["ratio"] != "16:9" {
						t.Error("unexpected Ark contract")
					}
					_, _ = w.Write([]byte(`{"id":"cgt-candidate"}`))
				} else {
					if r.URL.Path != "/v2/video_generation" || (call == 1 && body["ratio"] != "16:9") || (call == 2 && body["ratio"] != "adaptive") {
						t.Error("unexpected MiniMax contract")
					}
					_, _ = w.Write([]byte(`{"task_id":"123"}`))
				}
			}))
			defer s.Close()
			var p interface {
				SubmitVideo(context.Context, string) (VideoJob, error)
				SubmitVideoWithImage(context.Context, string, []byte, string) (VideoJob, error)
			}
			if tc.provider == "ark" {
				p = newArkVideoProvider("synthetic", tc.model, s.URL, s.Client(), nil)
			} else {
				p = newMiniMaxVideoProvider("synthetic", tc.model, s.URL, s.Client(), nil)
			}
			if _, err := p.SubmitVideo(context.Background(), "synthetic video"); err != nil {
				t.Fatal(err)
			}
			if _, err := p.SubmitVideoWithImage(context.Background(), "synthetic video", syntheticPNG(0), "image/png"); err != nil {
				t.Fatal(err)
			}
			if calls.Load() != 2 {
				t.Fatal("unexpected submission count")
			}
		})
	}
}

func TestReviewedVideoCandidateRequiresFreshSpecificationQuote(t *testing.T) {
	for _, tc := range []struct{ provider, model string }{
		{"minimax-video", "MiniMax-H3-Lite"},
		{"ark-video", "doubao-seedance-2-0-mini-260615"},
		{"gemini-video", "gemini-omni-1.1-flash"},
	} {
		t.Run(tc.provider, func(t *testing.T) {
			store := testStore(t)
			job := CreativeJob{Kind: "video", Provider: tc.provider, Model: tc.model, Prompt: "synthetic video", RuntimeSelection: &RuntimeSelection{Capability: "video"}}
			r, err := store.creativeExecutionRequest(job)
			if err != nil {
				t.Fatal(err)
			}
			var parameters struct {
				Specification *reviewedVideoSpecification `json:"video_specification"`
			}
			if json.Unmarshal(r.Parameters, &parameters) != nil || parameters.Specification == nil || *parameters.Specification != *videoCandidateSpecification(tc.provider, tc.model, false) {
				t.Fatalf("quote lacks wire output specification: %s", r.Parameters)
			}
			old := r
			old.Parameters, _ = json.Marshal(struct{ Kind, Shot, Character string }{job.Kind, job.ShotID, job.Character})
			policy := &ExecutionPolicy{Prices: []ExecutionPriceRule{{Provider: r.Provider, Model: r.Model, Capability: "video", RequestDigest: ExecutionRequestDigest(old), Currency: "USD", MaximumMinor: 10, MaximumInputBytes: 8192, Source: "synthetic old specification", ExpiresAt: time.Now().Add(time.Hour)}}}
			if _, err := policy.QuoteExecution(context.Background(), r); err == nil {
				t.Fatal("reused old quote after wire specification changed")
			}
			policy.Prices[0].RequestDigest = ExecutionRequestDigest(r)
			if _, err := policy.QuoteExecution(context.Background(), r); err != nil {
				t.Fatal(err)
			}
			parameters.Specification.Seconds++
			changed := r
			changed.Parameters, _ = json.Marshal(struct {
				Kind, Shot, Character string
				Specification         *reviewedVideoSpecification `json:"video_specification"`
			}{job.Kind, job.ShotID, job.Character, parameters.Specification})
			if _, err := policy.QuoteExecution(context.Background(), changed); err == nil {
				t.Fatal("quote accepted changed output duration")
			}
		})
	}
}

func TestGoogleCandidateMaximumIsNotExactOrExtensionDuration(t *testing.T) {
	spec := videoCandidateSpecification("gemini-video", "gemini-omni-1.1-flash", false)
	if spec == nil || spec.Seconds != 0 || spec.MaximumSeconds != 10 || spec.Resolution != "720p" {
		t.Fatal("original generation limit confused with exact duration")
	}
	if videoCandidateSpecification("gemini-video", "gemini-omni-1.1-flash", true) != nil || videoCandidateSpecification("gemini-video", "gemini-omni-flash-preview", false) != nil {
		t.Fatal("original stable-model quote bound reused for reference or retired preview")
	}
}
