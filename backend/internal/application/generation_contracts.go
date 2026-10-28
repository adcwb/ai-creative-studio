package application

import (
	"github.com/adcwb/ai-creative-studio/backend/internal/modules/generation"
)

type Asset = generation.Asset
type Expectation = generation.Expectation
type GenerationResult = generation.GenerationResult

var ErrNotFound = generation.ErrNotFound
var ErrConflict = generation.ErrConflict
var ErrProviderNotReady = generation.ErrProviderNotReady
var ErrProviderChanged = generation.ErrProviderChanged

func Normalize(in Input) (Input, error) {
	p, err := generation.Normalize(generation.Parameters{Prompt: in.Prompt, Kind: in.Kind, Ratio: in.Ratio, Style: in.Style})
	in.Prompt, in.Kind, in.Ratio, in.Style = p.Prompt, p.Kind, p.Ratio, p.Style
	return in, err
}
func withDefaults(in Input) Input {
	p := generation.WithDefaults(generation.Parameters{Prompt: in.Prompt, Kind: in.Kind, Ratio: in.Ratio, Style: in.Style})
	in.Prompt, in.Kind, in.Ratio, in.Style = p.Prompt, p.Kind, p.Ratio, p.Style
	return in
}
