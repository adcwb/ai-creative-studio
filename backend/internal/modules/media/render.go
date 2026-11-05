package media

import (
	"errors"
	"math/big"
	"strconv"
)

const RenderFPS int64 = 24
const RenderSampleRate int64 = 48000
const SamplesPerFrame int64 = RenderSampleRate / RenderFPS

type RenderClipRange struct{ Start, End, CoreStart, CoreEnd int64 }
type RenderPlan struct {
	Frames int64
	Clips  []RenderClipRange
}
type RenderBlock struct {
	Index       int64
	Start, End  int64
	Left, Right int
}

// Decimal input durations are added as rationals. Round global boundaries once;
// independently rounding every clip would accumulate frame drift in long edits.
func renderDecimal(v float64) (*big.Rat, error) {
	r, ok := new(big.Rat).SetString(strconv.FormatFloat(v, 'f', -1, 64))
	if !ok || r.Sign() < 0 {
		return nil, errors.New("invalid render time")
	}
	return r, nil
}
func roundRenderTime(r *big.Rat, rate int64) (int64, error) {
	v := new(big.Rat).Mul(r, big.NewRat(rate, 1))
	v.Add(v, big.NewRat(1, 2))
	n := new(big.Int).Quo(v.Num(), v.Denom())
	if !n.IsInt64() || n.Sign() < 0 {
		return 0, errors.New("render timestamp outside encoder range")
	}
	return n.Int64(), nil
}
func RenderSamples(v float64) (int64, error) {
	r, e := renderDecimal(v)
	if e != nil {
		return 0, e
	}
	return roundRenderTime(r, RenderSampleRate)
}
func CompileRenderPlan(t MediaTimeline) (RenderPlan, error) {
	p := RenderPlan{Clips: make([]RenderClipRange, len(t.Clips))}
	cursor := new(big.Rat)
	for i, c := range t.Clips {
		d, e := renderDecimal(c.Duration)
		if e != nil {
			return p, e
		}
		start, e := roundRenderTime(cursor, RenderFPS)
		if e != nil {
			return p, e
		}
		endRaw := new(big.Rat).Add(cursor, d)
		end, e := roundRenderTime(endRaw, RenderFPS)
		if e != nil || end <= start || end > (1<<63-1)/SamplesPerFrame {
			return p, errors.New("invalid render frame interval")
		}
		p.Clips[i] = RenderClipRange{Start: start, End: end, CoreStart: start, CoreEnd: end}
		cursor = endRaw
		if OverlappingTransition(c.Transition) {
			d, e := renderDecimal(c.TransitionDuration)
			if e != nil {
				return p, e
			}
			cursor = new(big.Rat).Sub(cursor, d)
		}
	}
	if len(p.Clips) == 0 {
		return p, errors.New("empty render plan")
	}
	for i := range p.Clips {
		if i > 0 && OverlappingTransition(t.Clips[i-1].Transition) {
			p.Clips[i].CoreStart = p.Clips[i-1].End
		}
		if OverlappingTransition(t.Clips[i].Transition) {
			if i+1 == len(p.Clips) {
				return p, errors.New("missing transition handle")
			}
			p.Clips[i].CoreEnd = p.Clips[i+1].Start
		}
		c := p.Clips[i]
		if c.CoreStart > c.CoreEnd {
			return p, errors.New("overlapping transition handles")
		}
	}
	p.Frames = p.Clips[len(p.Clips)-1].End
	return p, nil
}

// Walk blocks lazily: admission never allocates one record per output frame or
// per segment of an arbitrarily long movie. Transition handles own their output
// interval exactly once; non-overlapping core blocks cover every other frame.
func (p RenderPlan) Walk(segmentFrames int64, fn func(RenderBlock) error) error {
	if segmentFrames < 1 {
		return errors.New("invalid segment size")
	}
	index := int64(0)
	for i, c := range p.Clips {
		for start := c.CoreStart; start < c.CoreEnd; {
			end := min(c.CoreEnd, start+segmentFrames)
			if e := fn(RenderBlock{index, start, end, i, -1}); e != nil {
				return e
			}
			index++
			start = end
		}
		if i+1 < len(p.Clips) && p.Clips[i+1].Start < c.End {
			if e := fn(RenderBlock{index, p.Clips[i+1].Start, c.End, i, i + 1}); e != nil {
				return e
			}
			index++
		}
	}
	return nil
}

func (p RenderPlan) BlockCount(segmentFrames int64) int64 {
	if segmentFrames < 1 {
		return 0
	}
	n := int64(0)
	for i, c := range p.Clips {
		n += (c.CoreEnd - c.CoreStart + segmentFrames - 1) / segmentFrames
		if i+1 < len(p.Clips) && p.Clips[i+1].Start < c.End {
			n++
		}
	}
	return n
}
