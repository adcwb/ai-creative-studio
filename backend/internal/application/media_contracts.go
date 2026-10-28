package application

import (
	"github.com/adcwb/ai-creative-studio/backend/internal/modules/media"
)

type MediaAsset = media.MediaAsset
type MediaClip = media.MediaClip
type MediaAudioTrack = media.MediaAudioTrack
type MediaKeyframe = media.MediaKeyframe
type MediaVideoOverlay = media.MediaVideoOverlay
type MediaTimeline = media.MediaTimeline
type MediaExport = media.MediaExport
type MediaState = media.MediaState
type PosterLayer = media.PosterLayer
type PosterDocument = media.PosterDocument
type SubtitleCue = media.SubtitleCue

var errMediaInput = media.ErrInput
var errMediaVersion = media.ErrVersion
var errMediaKey = media.ErrKey
var errMediaLimit = media.ErrLimit

func mediaInvalid(message string) error                     { return media.Invalid(message) }
func mediaAsset(p MediaState, id string) (MediaAsset, bool) { return media.Asset(p, id) }
func validateTimeline(p MediaState, t MediaTimeline) error  { return media.ValidateTimeline(p, t) }
func outputSpec(t MediaTimeline) (int, int, float64, error) { return media.OutputSpec(t) }
func validatePoster(p MediaState, v PosterDocument) error   { return media.ValidatePoster(p, v) }
func validateSubtitleCues(cues []SubtitleCue, duration float64) error {
	return media.ValidateSubtitleCues(cues, duration)
}
func clipSubtitleCues(c MediaClip) []SubtitleCue { return media.ClipSubtitleCues(c) }
func estimateSubtitleCues(text string, duration float64) ([]SubtitleCue, error) {
	return media.EstimateSubtitleCues(text, duration)
}
func overlappingTransition(name string) bool          { return media.OverlappingTransition(name) }
func xfadeName(name string) string                    { return media.XfadeName(name) }
func validateKeyframes(layer MediaVideoOverlay) error { return media.ValidateKeyframes(layer) }
func keyframeExpression(layer MediaVideoOverlay, property, clock string) string {
	return media.KeyframeExpression(layer, property, clock)
}
func keyNumber(n float64) string                            { return media.KeyNumber(n) }
func timelineDuration(t MediaTimeline) float64              { return media.TimelineDuration(t) }
func hasCrossfade(t MediaTimeline) bool                     { return media.HasCrossfade(t) }
func validateComposite(p MediaState, t MediaTimeline) error { return media.ValidateComposite(p, t) }
