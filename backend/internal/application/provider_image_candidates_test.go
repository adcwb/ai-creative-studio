package application

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestSeedreamFlashCandidateSingleImageContract(t *testing.T) {
	fixture := nativeTestImage(t, "png")
	var calls atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := calls.Add(1)
		var body map[string]any
		if r.Method != "POST" || r.URL.Path != "/api/v3/images/generations" || r.Header.Get("Authorization") != "Bearer synthetic" || json.NewDecoder(r.Body).Decode(&body) != nil {
			t.Error("invalid image target or authentication")
		}
		if body["model"] != "doubao-seedream-5-0-flash-260915" || body["size"] != "2K" || body["sequential_image_generation"] != "disabled" || body["output_format"] != "png" || body["response_format"] != "b64_json" || body["watermark"] != true || body["stream"] != false {
			t.Errorf("wrong single-image contract: %#v", body)
		}
		if (call == 1 && body["image"] != nil) || (call == 2 && body["image"] != "data:image/png;base64,"+base64.StdEncoding.EncodeToString(fixture)) {
			t.Error("incorrect reference image")
		}
		_ = json.NewEncoder(w).Encode(nativeTestResponse("ark-images", fixture))
	}))
	defer s.Close()
	p := newNativeImageProvider("ark-images", "synthetic", "doubao-seedream-5-0-flash-260915", s.URL, s.Client())
	input := Input{Prompt: "synthetic scene", Kind: "general", Ratio: "1:1", Style: "natural"}
	if _, err := p.Generate(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	if _, err := p.GenerateWithReference(context.Background(), input, fixture, "image/png"); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatal("unexpected submission count")
	}
}

func TestSeedreamFlashCandidateQuoteSpecification(t *testing.T) {
	const model = "doubao-seedream-5-0-flash-260915"
	imageRequest := imageExecutionRequest(Task{Provider: "ark-images", Model: model})
	store := testStore(t)
	creativeRequest, err := store.creativeExecutionRequest(CreativeJob{Kind: "image", Provider: "ark-images", Model: model, RuntimeSelection: &RuntimeSelection{Capability: "image"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range []ExecutionRequest{imageRequest, creativeRequest} {
		var parameters struct {
			Specification *reviewedImageSpecification `json:"image_specification"`
		}
		if json.Unmarshal(r.Parameters, &parameters) != nil || parameters.Specification == nil || *parameters.Specification != *imageCandidateSpecification("ark-images", model) {
			t.Fatalf("missing exact image wire specification: %s", r.Parameters)
		}
		old := r
		var p map[string]any
		_ = json.Unmarshal(r.Parameters, &p)
		delete(p, "image_specification")
		old.Parameters, _ = json.Marshal(p)
		if ExecutionRequestDigest(r) == ExecutionRequestDigest(old) {
			t.Fatal("old quote reused for changed wire specification")
		}
	}
	if imageCandidateSpecification("ark-images", model+"-unreviewed") != nil || imageCandidateSpecification("other-provider", model) != nil {
		t.Fatal("candidate specification applied to an unreviewed model/provider")
	}
}
