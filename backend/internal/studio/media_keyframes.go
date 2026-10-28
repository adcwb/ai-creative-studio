package studio

import (
	"fmt"
	"math"
	"strconv"
)

// Only these built-in transitions can reach FFmpeg; callers cannot submit filters.
func overlappingTransition(name string) bool {
	switch name {
	case "crossfade", "wipeleft", "wiperight", "slideleft", "slideright", "circleopen":
		return true
	}
	return false
}
func xfadeName(name string) string {
	if name == "crossfade" {
		return "fade"
	}
	return name
}
func validateKeyframes(layer MediaVideoOverlay) error {
	if len(layer.Keyframes) > 16 {
		return mediaInvalid("关键帧最多16个")
	}
	previous := -1.0
	for i, k := range layer.Keyframes {
		for _, n := range []float64{k.Time, k.X, k.Y, k.Opacity} {
			if math.IsNaN(n) || math.IsInf(n, 0) {
				return mediaInvalid("关键帧数值必须有限")
			}
		}
		if (i == 0 && k.Time != 0) || k.Time <= previous || k.Time > layer.Duration || k.X < 0 || k.Y < 0 || k.X+layer.Width > 1 || k.Y+layer.Height > 1 || k.Opacity < 0 || k.Opacity > 1 {
			return mediaInvalid("关键帧时间、位置或透明度无效")
		}
		previous = k.Time
	}
	return nil
}

// These expressions contain only server-generated numbers/operators, never user text.
// Position is sampled on the movie clock; opacity on the layer-local clock.
func keyframeExpression(layer MediaVideoOverlay, property, clock string) string {
	value := func(k MediaKeyframe) float64 {
		switch property {
		case "x":
			return k.X
		case "y":
			return k.Y
		default:
			return k.Opacity
		}
	}
	if len(layer.Keyframes) == 0 {
		return keyNumber(value(MediaKeyframe{X: layer.X, Y: layer.Y, Opacity: layer.Opacity}))
	}
	keys := layer.Keyframes
	expression := keyNumber(value(keys[len(keys)-1]))
	for i := len(keys) - 2; i >= 0; i-- {
		a, b := keys[i], keys[i+1]
		ramp := fmt.Sprintf("%s+(%s)*((%s)-%s)/%s", keyNumber(value(a)), keyNumber(value(b)-value(a)), clock, keyNumber(a.Time), keyNumber(b.Time-a.Time))
		expression = fmt.Sprintf("if(lt((%s),%s),%s,%s)", clock, keyNumber(b.Time), ramp, expression)
	}
	return fmt.Sprintf("if(lt((%s),0),%s,%s)", clock, keyNumber(value(keys[0])), expression)
}

func keyNumber(n float64) string { return strconv.FormatFloat(n, 'g', 17, 64) }
