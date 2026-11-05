package story

import (
	"testing"
	"time"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/projects"
)

func checkSnapshot(t *testing.T) ContextSnapshot {
	t.Helper()
	order, versions := fixture(t)
	context, e := Retrieve(ident(1), ident(2), order, Selection{}, orderedVisit(versions))
	if e != nil {
		t.Fatal(e)
	}
	script := &projects.Script{StructureVersion: 2, Title: "雨夜", Characters: []projects.ScriptCharacter{}, Scenes: []projects.ScriptScene{{ID: ident(80), EpisodeNumber: 1, Heading: "门前", Action: "小满迟疑着推开木门。", Dialogue: []projects.ScriptDialogue{}}}}
	s := ContextSnapshot{Format: 1, ID: ident(70), Space: "space", SeriesID: ident(1), EpisodeID: ident(2), Context: context, DirectorySHA256: TextHash("directory"), ProjectVersion: 2, ScriptVersion: 1, Script: script, ScriptSHA256: Digest(script), Actor: "author", CreatedAt: time.Unix(10, 0).UTC()}
	s.SHA256 = SnapshotDigest(s)
	if !ValidSnapshot(s) {
		t.Fatal("invalid fixture snapshot")
	}
	return s
}
func TestCheckFindingsOnlyReferencePinnedIncludedProseOrStableScenes(t *testing.T) {
	s := checkSnapshot(t)
	source := s.Context.Included[0]
	valid := CheckResult{Summary: "需要作者判断的认知差异。", Findings: []Finding{{Title: "人物是否有意误认", Kind: "ambiguity", Severity: "note", Confidence: "low", Explanation: "这可能是有意的悬念，并非确定矛盾。", Evidence: []CheckEvidence{{RecordID: source.ID, VersionID: source.VersionID, Quote: source.Body.Bible.Theme}, {SceneID: ident(80), Quote: "迟疑着推开木门"}}}}}
	result, e := ValidateResult(ident(90), s, valid)
	if e != nil || !ID.MatchString(result.Findings[0].ID) || valid.Findings[0].ID != "" {
		t.Fatal("verified finding not assigned stable identity", e)
	}
	again, e := ValidateResult(ident(90), s, valid)
	if e != nil || Digest(again) != Digest(result) {
		t.Fatal("non deterministic finding identity", e)
	}
	for name, mutate := range map[string]func(*CheckResult){
		"forged quote":      func(x *CheckResult) { x.Findings[0].Evidence[0].Quote = "从未出现的原文" },
		"different version": func(x *CheckResult) { x.Findings[0].Evidence[0].VersionID = ident(999) },
		"omitted future": func(x *CheckResult) {
			x.Findings[0].Evidence[0].RecordID = ident(12)
			x.Findings[0].Evidence[0].VersionID = ident(112)
			x.Findings[0].Evidence[0].Quote = "门外其实是一位陌生人。"
		},
		"foreign scene":   func(x *CheckResult) { x.Findings[0].Evidence[1].SceneID = ident(555) },
		"server identity": func(x *CheckResult) { x.Findings[0].ID = ident(90) },
		"unproven":        func(x *CheckResult) { x.Findings[0].Evidence = nil },
		"cross-field quote": func(x *CheckResult) {
			x.Findings[0].Evidence[0].Quote = source.Body.Bible.Theme + "\n" + source.Body.Bible.WorldRules
		},
		"false numeric confidence": func(x *CheckResult) { x.Findings[0].Confidence = "100%" },
	} {
		t.Run(name, func(t *testing.T) {
			candidate, _ := Clone(valid)
			mutate(&candidate)
			if _, e := ValidateResult(ident(90), s, candidate); e == nil {
				t.Fatal("unverified output accepted")
			}
		})
	}
	empty := CheckResult{Summary: "未发现有原文支撑的问题；作者仍需审稿。", Findings: []Finding{}}
	if _, e = ValidateResult(ident(90), s, empty); e != nil {
		t.Fatal(e)
	}
}
