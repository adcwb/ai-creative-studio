package story

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func ident(n int) string { return fmt.Sprintf("%032x", n) }
func version(t *testing.T, id int, in Input) Version {
	t.Helper()
	n, e := Normalize(in)
	if e != nil {
		t.Fatal(e)
	}
	v := Version{Format: 1, ID: ident(id), Space: "space", SeriesID: ident(1), VersionID: ident(id + 100), Number: 1, Title: n.Title, Kind: n.Kind, Status: n.Status, Required: n.Required, Body: n.Body, Actor: "author", CreatedAt: time.Unix(10, 0).UTC(), SHA256: ContentDigest(n)}
	if !Valid(v) {
		t.Fatal("invalid test version")
	}
	return v
}
func fixture(t *testing.T) ([]Episode, []Version) {
	t.Helper()
	order := []Episode{{ident(2), ident(8), 1, 1, 1}, {ident(3), ident(8), 1, 7, 1}, {ident(4), ident(8), 1, 120, 1}}
	b := version(t, 10, Input{Title: "圣经", Kind: "bible", Status: "active", Body: Body{Bible: &Bible{Theme: "人与记忆", WorldRules: "记忆不能恢复已消失的客观证据。", Characters: []Character{{ID: ident(20), Name: "小满", Desire: "找到真相", VoiceIntent: "低声、克制；不是克隆授权"}}}}})
	f := version(t, 11, Input{Title: "小满误认", Kind: "fact", Status: "active", Required: true, Body: Body{Fact: &Fact{Subject: "小满", Predicate: "认定", Value: "门外是父亲", Perspective: "subjective", BelieverID: ident(20), StoryTime: "过去的雨夜", AudienceFrom: ident(3), KnownBy: []Knowledge{{ident(20), ident(3)}}}}})
	ev := version(t, 12, Input{Title: "闪回中的真相", Kind: "event", Status: "active", Body: Body{Event: &Event{EpisodeID: ident(3), StoryTime: "十年前", Description: "门外其实是一位陌生人。"}}})
	return order, []Version{ev, f, b}
}
func TestStoryContextSeparatesBeliefChronologyAndKnowledge(t *testing.T) {
	order, versions := fixture(t)
	before, _ := json.Marshal(versions)
	first, e := Compile(ident(1), ident(2), order, versions, 0)
	if e != nil || len(first.Problems) != 0 || len(first.Included) != 2 || len(first.Omitted) != 1 || first.Omitted[0].Reason != "future_event" {
		t.Fatal(e, first)
	}
	belief := first.Included[1]
	if belief.Body.Fact.Perspective != "subjective" || belief.Body.Fact.BelieverID != ident(20) || belief.AudienceKnows || len(belief.KnownBy) != 0 {
		t.Fatal("belief promoted or future knowledge leaked")
	}
	next, e := Compile(ident(1), ident(3), order, versions, 0)
	if e != nil || len(next.Included) != 3 || !next.Included[1].AudienceKnows || len(next.Included[1].KnownBy) != 1 {
		t.Fatal(e, next)
	}
	after, _ := json.Marshal(versions)
	if string(before) != string(after) {
		t.Fatal("compile mutated formal material")
	}
	again, _ := Compile(ident(1), ident(3), []Episode{order[2], order[0], order[1]}, []Version{versions[2], versions[0], versions[1]}, 0)
	if next.Digest != again.Digest {
		t.Fatal("context not deterministic")
	}
}
func TestStoryContextExplainsCapacityAndRequiredOverflow(t *testing.T) {
	order, versions := fixture(t)
	all, _ := Compile(ident(1), ident(3), order, versions, 0)
	bytes, _ := json.Marshal(all.Included[:2])
	p, e := Compile(ident(1), ident(3), order, versions, len(bytes))
	if e != nil || len(p.Problems) != 0 || len(p.Omitted) != 1 || p.Omitted[0].Reason != "input_capacity" {
		t.Fatal(e, p)
	}
	p, e = Compile(ident(1), ident(3), order, versions, 1)
	if e != nil || len(p.Problems) < 2 || p.Problems[0].Code != "required_context_overflow" {
		t.Fatal("required rules silently dropped", e, p)
	}
	empty, e := Compile(ident(1), ident(2), order, nil, 0)
	if e != nil || empty.Problems[0].Code != "bible_required" {
		t.Fatal("missing formal bible accepted", e)
	}
}
func TestStoryContextDoesNotRebindStableIntervalsAfterReorder(t *testing.T) {
	order, versions := fixture(t)
	versions[1].Body.FromEpisodeID = ident(3)
	versions[1].Body.ToEpisodeID = ident(4)
	versions[1].SHA256 = ContentDigest(Input{Title: versions[1].Title, Kind: versions[1].Kind, Status: versions[1].Status, Required: versions[1].Required, Body: versions[1].Body})
	order[1].Ordinal, order[2].Ordinal = 120, 7
	p, e := Compile(ident(1), ident(2), order, versions, 0)
	if e != nil {
		t.Fatal(e)
	}
	found := false
	for _, x := range p.Problems {
		found = found || x.Code == "episode_range_reversed"
	}
	if !found {
		t.Fatal("changed order silently reinterpreted interval", p)
	}
}
func TestStoryRejectsFalseKindsIDsAndCorruption(t *testing.T) {
	_, vs := fixture(t)
	v := vs[1]
	in := Input{Title: v.Title, Kind: v.Kind, Status: v.Status, Required: v.Required, Body: v.Body}
	for name, change := range map[string]func(*Input){"false union": func(x *Input) { x.Body.Event = &Event{EpisodeID: ident(2), Description: "extra"} }, "belief without holder": func(x *Input) { x.Body.Fact.BelieverID = "" }, "invalid knowledge": func(x *Input) { x.Body.Fact.KnownBy[0].FromEpisodeID = "7" }, "control": func(x *Input) { x.Title = "bad\x00text" }} {
		t.Run(name, func(t *testing.T) {
			copy, _ := Clone(in)
			change(&copy)
			if _, e := Normalize(copy); e == nil {
				t.Fatal("invalid material accepted")
			}
		})
	}
	v.Body.Fact.Value += "changed"
	if Valid(v) {
		t.Fatal("corrupt content hash accepted")
	}
	n, e := Normalize(Input{Title: "large authored rules", Kind: "bible", Status: "active", Body: Body{Bible: &Bible{WorldRules: strings.Repeat("素材与正式规则", 10000)}}})
	if e != nil || len(n.Body.Bible.WorldRules) < 10000 {
		t.Fatal("product content quota introduced")
	}
}

func TestOutlinePlansStayInTheirSeasonAndEpisode(t *testing.T) {
	order, versions := fixture(t)
	for i, x := range []Outline{{SeasonID: ident(8), Goal: "本季人物弧线"}, {SeasonID: ident(8), EpisodeID: ident(2), Goal: "本集明确选择"}, {SeasonID: ident(8), EpisodeID: ident(3), Goal: "不要泄漏下一集节拍"}, {SeasonID: ident(99), Goal: "其他季的节拍"}} {
		versions = append(versions, version(t, 40+i, Input{Title: "编剧计划", Kind: "outline", Status: "active", Body: Body{Outline: &x}}))
	}
	p, e := Compile(ident(1), ident(2), order, versions, 0)
	if e != nil {
		t.Fatal(e)
	}
	count := 0
	for _, v := range p.Included {
		if v.Body.Outline != nil {
			count++
			if !v.Required || v.Body.Outline.Goal == "不要泄漏下一集节拍" || v.Body.Outline.SeasonID != ident(8) {
				t.Fatal("wrong plan scope", v)
			}
		}
	}
	if count != 2 {
		t.Fatal("season/episode plan not both included", count)
	}
	for _, bad := range []Outline{{SeasonID: "invalid", Goal: "目标"}, {SeasonID: ident(8), EpisodeID: "other", Goal: "目标"}, {SeasonID: ident(8), Goal: " "}} {
		if _, e = Normalize(Input{Title: "计划", Kind: "outline", Status: "active", Body: Body{Outline: &bad}}); e == nil {
			t.Fatal("invalid outline accepted")
		}
	}
}

func TestAuthoredProjectIdentitiesAreNotCanonicalRecordIDs(t *testing.T) {
	scene := "b51e71e7-a8a9-4b7b-a6c9-52688d13c117"
	input := Input{Title: "原文来源", Kind: "event", Status: "active", Body: Body{Event: &Event{EpisodeID: ident(2), Description: "保留历史来源"}, Evidence: []Evidence{{EpisodeID: ident(2), SceneID: scene, ScriptVersion: 1, ScriptSHA256: strings.Repeat("a", 64), Quote: "原文"}}}}
	if _, e := Normalize(input); e != nil {
		t.Fatal("valid editor scene ID refused", e)
	}
	input.Body.Evidence[0].SceneID = "invalid scene with spaces"
	if _, e := Normalize(input); e == nil {
		t.Fatal("invalid authored scene ID accepted")
	}
	cast := &Cast{EpisodeID: ident(2), ScriptVersion: 1, ScriptSHA256: strings.Repeat("a", 64), BibleVersionID: ident(10), Characters: []CastCharacter{{ScriptID: scene, ScriptName: "手写人物", SeriesID: ident(20), SeriesName: "系列人物"}}}
	if !normalizeCast(cast) {
		t.Fatal("valid editor role ID refused")
	}
	cast.Characters[0].SeriesID = scene
	if normalizeCast(cast) {
		t.Fatal("canonical series identity grammar weakened")
	}
}
