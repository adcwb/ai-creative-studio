package application

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// Loopback rejection fixtures prove complete request construction and no retry.
// They do not claim the supplier executed a model or preserved artistic identity.
func TestLibraryVisualContextReachesEverySupportedWireAdapterOnce(t *testing.T) {
	directive := strings.Repeat("CONTINUITY_DIRECTION_COMPLETE;", 7)
	input := Input{Prompt: "Synthetic rain scene", Kind: "general", Ratio: "16:9", Style: "natural"}
	png := syntheticCreativePNG()
	for _, kind := range []string{"openai-image", "gemini-image", "ark-image", "google-interactions-image", "minimax-image", "jimeng-image", "gateway-image", "gateway-image-edit", "gateway-video", "ark-video", "ark-video-reference", "minimax-video", "minimax-video-reference", "gemini-video", "gemini-video-reference", "gemini-video-controlled"} {
		t.Run(kind, func(t *testing.T) {
			var calls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != "POST" {
					t.Error("unexpected read/poll")
				}
				seen := 0
				var inspect func(any)
				inspect = func(value any) {
					switch v := value.(type) {
					case string:
						if strings.Contains(v, directive) {
							seen++
							if strings.Count(v, directive) != 1 {
								t.Error("direction duplicated")
							}
						}
					case map[string]any:
						for _, item := range v {
							inspect(item)
						}
					case []any:
						for _, item := range v {
							inspect(item)
						}
					}
				}
				if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
					if err := r.ParseMultipartForm(10 << 20); err != nil {
						t.Error(err)
					} else {
						defer r.MultipartForm.RemoveAll()
						inspect(r.FormValue("prompt"))
					}
				} else {
					var body any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					inspect(body)
				}
				if seen != 1 {
					t.Error("complete authored direction omitted from wire request", seen)
				}
				w.WriteHeader(429)
			}))
			defer upstream.Close()
			ctx := context.WithValue(context.Background(), libraryInputContextKey{}, directive)
			var err error
			switch kind {
			case "openai-image":
				_, err = newOpenAIProvider("synthetic", upstream.URL+"/images/generations", upstream.Client()).Generate(ctx, input)
			case "gemini-image":
				_, err = newNativeImageProvider("gemini-images", "synthetic", "image-model", upstream.URL, upstream.Client()).Generate(ctx, input)
			case "ark-image":
				_, err = newNativeImageProvider("ark-images", "synthetic", "image-model", upstream.URL, upstream.Client()).Generate(ctx, input)
			case "google-interactions-image":
				p := &GoogleInteractionsImageProvider{api: newGoogleMediaProvider("gemini-interactions-images", "synthetic", "gemini-nano-banana-2.1", "", upstream.URL, upstream.Client(), nil)}
				_, err = p.Generate(ctx, input)
			case "minimax-image":
				_, err = newMiniMaxImageProvider("synthetic", "image-01", upstream.URL, upstream.Client()).Generate(ctx, input)
			case "jimeng-image":
				_, err = newJimengImageProvider("synthetic-ak", "synthetic-sk", "synthetic", upstream.URL, upstream.Client()).Generate(ctx, input)
			case "gateway-image", "gateway-image-edit":
				p := &gatewayImageProvider{api: newCreativeProvider("synthetic", upstream.URL, upstream.Client()), model: GatewayModel{Model: "vendor/image", Capabilities: []string{"image", "image_edit"}}}
				if kind == "gateway-image" {
					_, err = p.Generate(ctx, input)
				} else {
					_, err = p.GenerateWithReference(ctx, input, png, "image/png")
				}
			case "gateway-video":
				p := &gatewayCreativeProvider{OpenAICreativeProvider: newCreativeProvider("synthetic", upstream.URL, upstream.Client()), model: GatewayModel{Model: "vendor/video", VideoSeconds: 4, VideoSize: "1280x720"}}
				_, err = p.SubmitVideo(ctx, "Synthetic rain scene")
			case "ark-video", "ark-video-reference":
				p := newArkVideoProvider("synthetic", "synthetic-model", upstream.URL, upstream.Client(), nil)
				if kind == "ark-video-reference" {
					_, err = p.SubmitVideoWithImage(ctx, "Synthetic rain scene", png, "image/png")
				} else {
					_, err = p.SubmitVideo(ctx, "Synthetic rain scene")
				}
			case "minimax-video", "minimax-video-reference":
				p := newMiniMaxVideoProvider("synthetic", "synthetic-model", upstream.URL, upstream.Client(), nil)
				if kind == "minimax-video-reference" {
					_, err = p.SubmitVideoWithImage(ctx, "Synthetic rain scene", png, "image/png")
				} else {
					_, err = p.SubmitVideo(ctx, "Synthetic rain scene")
				}
			default:
				p := newGoogleMediaProvider("gemini-video", "synthetic", "gemini-omni-1.1-flash", "", upstream.URL, upstream.Client(), nil)
				switch kind {
				case "gemini-video-reference":
					_, err = p.SubmitVideoWithImage(ctx, "Synthetic rain scene", png, "image/png")
				case "gemini-video-controlled":
					_, err = p.SubmitControlledVideo(ctx, "Synthetic rain scene", []referenceMedia{{Data: png, MIME: "image/png", Role: "reference"}})
				default:
					_, err = p.SubmitVideo(ctx, "Synthetic rain scene")
				}
			}
			if err == nil || calls.Load() != 1 {
				t.Fatal("adapter skipped input or retried rejection", err, calls.Load())
			}
		})
	}
}
