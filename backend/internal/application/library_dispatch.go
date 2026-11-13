package application

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/media"
)

type libraryUsePin struct {
	Source   media.LibrarySource `json:"source"`
	Revision int64               `json:"authorization_revision"`
	Purpose  string              `json:"purpose"`
}
type libraryMediaPin struct {
	MediaID string          `json:"media_id"`
	SHA256  string          `json:"content_sha256"`
	MIME    string          `json:"mime_type"`
	Uses    []libraryUsePin `json:"uses"`
}
type libraryDispatchKey struct{}
type libraryOutputSourcesKey struct{}
type libraryRecoveryIdentityKey struct{}
type libraryRecoveryIdentity struct {
	Provider, Model string
	Upstream        func() string
}

func uniqueLibrarySources(in []media.LibrarySource) []media.LibrarySource {
	seen := map[media.LibrarySource]bool{}
	out := []media.LibrarySource{}
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].AssetID != out[j].AssetID {
			return out[i].AssetID < out[j].AssetID
		}
		return out[i].VersionID < out[j].VersionID
	})
	return out
}
func cloneLibraryPins(in []libraryMediaPin) []libraryMediaPin {
	if in == nil {
		return nil
	}
	b, _ := json.Marshal(in)
	var out []libraryMediaPin
	_ = json.Unmarshal(b, &out)
	return out
}

func creativeMediaIDs(in CreativeJobInput) []string {
	ids := append([]string(nil), in.ReferenceAssetIDs...)
	ids = append(ids, in.ReferenceAssetID, in.FirstFrameAssetID, in.LastFrameAssetID, in.MotionReferenceAssetID, in.MaskAssetID, in.AudioAssetID, in.ComparisonAssetID)
	seen := map[string]bool{}
	out := []string{}
	for _, id := range ids {
		if id != "" && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}
func libraryMediaPurpose(kind, mime string) string {
	if kind == "asr" || kind == "forced" || kind == "transcription" {
		return "audio_analysis"
	}
	if strings.HasPrefix(mime, "audio/") {
		return "voice_reference"
	}
	if kind == "video" || kind == "lip_sync" || kind == "digital_human" {
		return "video_reference"
	}
	return "image_reference"
}
func (s *FileStore) pinCreativeLibraryMedia(ctx context.Context, project, space string, in CreativeJobInput) ([]libraryMediaPin, error) {
	ctx = withLineageWalk(ctx)
	var pins []libraryMediaPin
	for _, id := range creativeMediaIDs(in) {
		sources, sha, mime, e := s.libraryMediaSources(ctx, project, space, id)
		if e != nil {
			return nil, e
		}
		if len(sources) == 0 {
			continue
		}
		pin := libraryMediaPin{MediaID: id, SHA256: sha, MIME: mime, Uses: []libraryUsePin{}}
		for _, source := range sources {
			if s.library == nil {
				return nil, errRuntimeDenied
			}
			current, e := s.library.Get(ctx, space, source.AssetID)
			if e != nil {
				return nil, e
			}
			purpose := libraryMediaPurpose(in.Kind, mime)
			// A voice contribution remains a voice contribution inside a video
			// descendant. Native-preset permission alone never authorizes reuse;
			// require the separately declared voice-reference purpose, rather than
			// asking a voice card for an impossible image/video-reference grant.
			if current.Asset.Kind == "voice" && (purpose == "image_reference" || purpose == "video_reference") {
				purpose = "voice_reference"
			}
			if !current.Asset.UseAuthorization.Allows(source.VersionID, purpose, time.Now().UTC()) {
				return nil, providerFailure("library_use_denied")
			}
			pin.Uses = append(pin.Uses, libraryUsePin{Source: source, Revision: current.Asset.UseAuthorization.Revision, Purpose: purpose})
		}
		pins = append(pins, pin)
	}
	return pins, nil
}
func creativeJobMediaInput(job *CreativeJob) CreativeJobInput {
	return CreativeJobInput{Kind: job.Kind, ReferenceAssetIDs: job.ReferenceAssetIDs, ReferenceAssetID: job.ReferenceAssetID, FirstFrameAssetID: job.FirstFrameAssetID, LastFrameAssetID: job.LastFrameAssetID, MotionReferenceAssetID: job.MotionReferenceAssetID, MaskAssetID: job.MaskAssetID, AudioAssetID: job.AudioAssetID, ComparisonAssetID: job.ComparisonAssetID}
}
func (s *FileStore) withLibraryDispatchGuard(ctx context.Context, job *CreativeJob) context.Context {
	// Snapshot trusted pins so mutable completion/upstream fields cannot broaden
	// authority. Rechecking is immediately before each mutating HTTP dispatch.
	pins := cloneLibraryPins(job.LibraryMediaPins)
	inputPins := cloneLibraryInputPins(job.LibraryInputs)
	input := creativeJobMediaInput(job)
	guard := func(ctx context.Context) error {
		if job.Kind == "digital_human" {
			if err := s.checkAvatarJob(ctx, job); err != nil {
				return err
			}
		}
		if e := s.checkLibraryInputPins(ctx, job.SpaceID, inputPins); e != nil {
			return e
		}
		fresh, e := s.pinCreativeLibraryMedia(ctx, job.ProjectID, job.SpaceID, input)
		if e != nil {
			return e
		}
		if !reflect.DeepEqual(pins, fresh) {
			return providerFailure("library_use_denied")
		}
		return nil
	}
	sources := libraryInputSources(inputPins)
	for _, pin := range pins {
		for _, use := range pin.Uses {
			sources = append(sources, use.Source)
		}
	}
	ctx = context.WithValue(ctx, libraryRecoveryIdentityKey{}, libraryRecoveryIdentity{Provider: job.Provider, Model: job.Model, Upstream: func() string { return job.UpstreamID }})
	ctx = context.WithValue(ctx, libraryOutputSourcesKey{}, uniqueLibrarySources(sources))
	ctx = withDirectorContext(ctx, job)
	ctx = context.WithValue(ctx, libraryInputContextKey{}, job.LibraryInputContext)
	return context.WithValue(ctx, libraryDispatchKey{}, guard)
}
func checkLibraryDispatch(ctx context.Context) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	if guard, ok := ctx.Value(libraryDispatchKey{}).(func(context.Context) error); ok {
		return guard(ctx)
	}
	return nil
}

// Also covers direct official adapters that are not built by the managed gateway.
func libraryGuardedRequest(client *http.Client, req *http.Request) (*http.Response, error) {
	if !libraryReadOnlyRequest(req) {
		if e := checkLibraryDispatch(req.Context()); e != nil {
			return nil, e
		}
	}
	res, err := client.Do(req)
	return observeProviderResponse(req, res, err)
}

// Native Jimeng status queries use POST. Only the exact signed query envelope
// for this job's acknowledged upstream ID is a read; arbitrary POSTs still gate.
func libraryReadOnlyRequest(req *http.Request) bool {
	if req.Method == http.MethodGet || req.Method == http.MethodHead {
		return true
	}
	identity, ok := req.Context().Value(libraryRecoveryIdentityKey{}).(libraryRecoveryIdentity)
	if !ok || identity.Provider != "jimeng-images" || identity.Upstream == nil || identity.Upstream() == "" {
		return false
	}
	return jimengReadOnlyRequestFor(identity.Model, func(context.Context) string { return identity.Upstream() })(req)
}

// Legacy acknowledged jobs may lack authority pins. Recovering their result
// must still record source ancestry; it never manufactures an old grant or
// permits another submission. Existing receipts keep their original identity.
func (s *FileStore) libraryResultContext(ctx context.Context, job *CreativeJob) (context.Context, error) {
	if len(job.LibraryMediaPins) > 0 || len(job.LibraryInputs) > 0 || job.UpstreamID == "" {
		return ctx, nil
	}
	sources := []media.LibrarySource{}
	for _, id := range creativeMediaIDs(creativeJobMediaInput(job)) {
		inherited, _, _, e := s.libraryMediaSources(ctx, job.ProjectID, job.SpaceID, id)
		if e != nil {
			return ctx, e
		}
		sources = append(sources, inherited...)
	}
	return context.WithValue(ctx, libraryOutputSourcesKey{}, uniqueLibrarySources(sources)), nil
}
