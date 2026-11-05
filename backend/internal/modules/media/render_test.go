package media

import (
	"errors"
	"math"
	"testing"
)

func TestRenderPlanGlobalRationalFramesAndOwnedHandles(t *testing.T) {
	for _, transition := range []float64{0, .37} {
		timeline := MediaTimeline{}
		for i := range 200 {
			c := MediaClip{Duration: 1.01}
			if transition > 0 && i < 199 {
				c.Transition = "crossfade"
				c.TransitionDuration = transition
			}
			timeline.Clips = append(timeline.Clips, c)
		}
		p, e := CompileRenderPlan(timeline)
		if e != nil {
			t.Fatal(e)
		}
		expected := int64(math.Round((202 - 199*transition) * 24))
		if p.Frames != expected {
			t.Fatal("global frame drift", p.Frames, expected)
		}
		cursor, count := int64(0), int64(0)
		if e = p.Walk(17, func(b RenderBlock) error {
			if b.Start != cursor || b.End <= b.Start || b.Start < p.Clips[b.Left].Start || b.End > p.Clips[b.Left].End {
				t.Fatalf("gap, overlap or missing handle: %+v after %d", b, cursor)
			}
			if b.Right >= 0 && (b.Start != p.Clips[b.Right].Start || b.End > p.Clips[b.Right].End) {
				t.Fatal("wrong right handle", b)
			}
			cursor = b.End
			count++
			return nil
		}); e != nil {
			t.Fatal(e)
		}
		if cursor != p.Frames || count != p.BlockCount(17) {
			t.Fatal("incomplete coverage", cursor, count)
		}
	}
	p, e := CompileRenderPlan(MediaTimeline{Clips: []MediaClip{{Duration: 120}}})
	if e != nil || p.Frames != 2880 || p.BlockCount(240) != 12 {
		t.Fatal(p, e)
	}
}
func TestRenderPlanRejectsNonfiniteAndWalksLongOutputsLazily(t *testing.T) {
	for _, n := range []float64{math.NaN(), math.Inf(1), -1, 1e30} {
		if _, e := CompileRenderPlan(MediaTimeline{Clips: []MediaClip{{Duration: n}}}); e == nil {
			t.Fatal("unrepresentable timestamp", n)
		}
	}
	p, e := CompileRenderPlan(MediaTimeline{Clips: []MediaClip{{Duration: 1e9}}})
	if e != nil {
		t.Fatal(e)
	}
	stop := errors.New("stop")
	calls := 0
	if e = p.Walk(240, func(RenderBlock) error { calls++; return stop }); !errors.Is(e, stop) || calls != 1 {
		t.Fatal("eager segment allocation", calls, e)
	}
	n, e := RenderSamples(.37)
	if e != nil || n != 17760 {
		t.Fatal("inexact sample clock", n, e)
	}
}
