package story

import "testing"

func TestCastMaterialKeepsExplicitIdentityAndEpisodeScope(t *testing.T) {
	order, _ := fixture(t)
	b := version(t, 10, Input{Title: "圣经", Kind: "bible", Status: "active", Body: Body{Bible: &Bible{Theme: "真相与误认", Characters: []Character{{ID: ident(20), Name: "小满"}}}}})
	in := Input{Title: "本集选角", Kind: "cast", Status: "active", Body: Body{Cast: &Cast{EpisodeID: ident(2), ScriptVersion: 1, ScriptSHA256: TextHash("script"), BibleVersionID: b.VersionID, Characters: []CastCharacter{{ScriptID: ident(30), ScriptName: "化名", SeriesID: ident(20), SeriesName: "小满", Notes: "本集假装不认识父亲"}}}}}
	c := version(t, 11, in)
	p, e := Compile(ident(1), ident(2), order, []Version{b, c}, 0)
	if e != nil || len(p.Problems) > 0 || len(p.Included) != 2 {
		t.Fatal(e, p)
	}
	other, e := Compile(ident(1), ident(3), order, []Version{b, c}, 0)
	if e != nil || len(other.Problems) > 0 || len(other.Included) != 1 || other.Omitted[0].Reason != "other_episode_cast" {
		t.Fatal("cast leaked into another episode", e, other)
	}
	c.Body.Cast.BibleVersionID = ident(55)
	c.SHA256 = ContentDigest(Input{Title: c.Title, Kind: c.Kind, Status: c.Status, Required: c.Required, Body: c.Body})
	changed, e := Compile(ident(1), ident(2), order, []Version{b, c}, 0)
	if e != nil || len(changed.Problems) != 1 || changed.Problems[0].Code != "cast_bible_changed" {
		t.Fatal(e, changed)
	}
	duplicate, _ := Clone(in)
	duplicate.Body.Cast.Characters = append(duplicate.Body.Cast.Characters, duplicate.Body.Cast.Characters[0])
	if _, e := Normalize(duplicate); e == nil {
		t.Fatal("duplicate script identity accepted")
	}
	incomplete, _ := Clone(in)
	incomplete.Body.Cast.Characters[0].AssetVersionID = "v1"
	if _, e := Normalize(incomplete); e == nil {
		t.Fatal("half asset reference accepted")
	}
}

func TestModelSuggestionsCannotRewriteAuthoredCastIdentities(t *testing.T) {
	s := checkSnapshot(t)
	source := s.Context.Included[0]
	proposal := Input{Title: "选角建议", Kind: "cast", Status: "active", Body: Body{Cast: &Cast{EpisodeID: s.EpisodeID, ScriptVersion: 1, ScriptSHA256: s.ScriptSHA256, BibleVersionID: source.VersionID, Characters: []CastCharacter{}}}}
	result := CheckResult{Summary: "建议更换角色对应", Findings: []Finding{{Title: "同名可能造成误认", Kind: "ambiguity", Severity: "note", Confidence: "low", Explanation: "仅提示，不得直接改角色身份。", Evidence: []CheckEvidence{{RecordID: source.ID, VersionID: source.VersionID, Quote: source.Body.Bible.Theme}}, Proposal: &proposal}}}
	if _, e := ValidateResult(ident(90), s, result); e == nil {
		t.Fatal("AI cast rewrite accepted")
	}
	if _, e := NormalizeDecision(DecisionInput{Action: "adopt", ReviewSHA256: TextHash("review"), Proposal: &proposal}); e == nil {
		t.Fatal("cast bypassed dedicated identity review")
	}
}
