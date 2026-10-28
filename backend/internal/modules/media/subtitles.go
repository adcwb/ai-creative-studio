package media

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"unicode"
	"unicode/utf8"
)

// SubtitleCue times are seconds relative to the beginning of one timeline clip.
// They are not source-video timestamps; trim_start never offsets these values.
type SubtitleCue struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Text  string  `json:"text"`
}

func ValidateSubtitleCues(cues []SubtitleCue, duration float64) error {
	if len(cues) > 60 || math.IsNaN(duration) || math.IsInf(duration, 0) || duration <= 0 {
		return Invalid("每镜最多60句字幕，时长必须有限")
	}
	previous := 0.0
	total := 0
	for _, cue := range cues {
		length := utf8.RuneCountInString(strings.TrimSpace(cue.Text))
		total += length
		if math.IsNaN(cue.Start) || math.IsNaN(cue.End) || math.IsInf(cue.Start, 0) || math.IsInf(cue.End, 0) || cue.Start < previous || cue.Start < 0 || cue.End <= cue.Start || math.Round(cue.End*1000) <= math.Round(cue.Start*1000) || cue.End > duration || length < 1 || length > 300 || strings.ContainsRune(cue.Text, 0) {
			return Invalid("字幕需按时间排序且不重叠，起止在镜头范围内，每句1–300字")
		}
		previous = cue.End
	}
	if total > 2000 {
		return Invalid("每镜字幕合计最多2000字")
	}
	return nil
}

func ClipSubtitleCues(c MediaClip) []SubtitleCue {
	if len(c.SubtitleCues) > 0 {
		return c.SubtitleCues
	}
	if c.Subtitle != "" {
		return []SubtitleCue{{Start: 0, End: c.Duration, Text: c.Subtitle}}
	}
	return nil
}

func EstimateSubtitleCues(text string, duration float64) ([]SubtitleCue, error) {
	text = strings.TrimSpace(text)
	if utf8.RuneCountInString(text) < 1 || utf8.RuneCountInString(text) > 2000 {
		return nil, Invalid("估算文本需为1–2000字")
	}
	lines := []string{}
	var line strings.Builder
	flush := func() {
		if value := strings.TrimSpace(line.String()); value != "" {
			lines = append(lines, value)
		}
		line.Reset()
	}
	runes := []rune(text)
	for i, r := range runes {
		line.WriteRune(r)
		if strings.ContainsRune("。！？!?；;\n", r) || (r == '.' && (i == len(runes)-1 || unicode.IsSpace(runes[i+1]))) {
			flush()
		}
	}
	flush()
	if len(lines) > 60 {
		return nil, Invalid("最多60句，请合并过短句子")
	}
	total := 0
	weights := make([]int, len(lines))
	for i, line := range lines {
		for _, r := range line {
			if !unicode.IsSpace(r) {
				weights[i]++
			}
		}
		total += weights[i]
	}
	cues := make([]SubtitleCue, 0, len(lines))
	elapsed := 0.0
	for i, line := range lines {
		end := elapsed + duration*float64(weights[i])/float64(total)
		if i == len(lines)-1 {
			end = duration
		}
		cues = append(cues, SubtitleCue{Start: elapsed, End: end, Text: line})
		elapsed = end
	}
	if err := ValidateSubtitleCues(cues, duration); err != nil {
		return nil, err
	}
	return cues, nil
}

func (c *SubtitleCue) UnmarshalJSON(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	for _, name := range []string{"start", "end", "text"} {
		value, ok := fields[name]
		if !ok || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return fmt.Errorf("subtitle cue %s is required", name)
		}
	}
	type plain SubtitleCue
	var decoded plain
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(&decoded); err != nil {
		return err
	}
	*c = SubtitleCue(decoded)
	return nil
}
