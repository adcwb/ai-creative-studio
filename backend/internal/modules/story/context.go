package story

import (
	"encoding/json"
	"sort"
)

type Episode struct {
	ID            string `json:"id"`
	SeasonID      string `json:"season_id"`
	SeasonOrdinal int64  `json:"season_ordinal"`
	Ordinal       int64  `json:"ordinal"`
	Version       int64  `json:"row_version"`
}
type Omission struct {
	ID        string `json:"id"`
	VersionID string `json:"version_id"`
	Reason    string `json:"reason"`
}
type Problem struct {
	Code     string `json:"code"`
	RecordID string `json:"record_id,omitempty"`
	Message  string `json:"message"`
}
type ContextItem struct {
	Summary
	Body          Body     `json:"body"`
	AudienceKnows bool     `json:"audience_knows"`
	KnownBy       []string `json:"known_by"`
}
type Preview struct {
	Compiler    int           `json:"compiler_version"`
	SeriesID    string        `json:"series_id"`
	EpisodeID   string        `json:"episode_id"`
	OrderSHA256 string        `json:"order_sha256"`
	Sources     []Summary     `json:"sources"`
	Included    []ContextItem `json:"included"`
	Omitted     []Omission    `json:"omitted"`
	Problems    []Problem     `json:"problems"`
	Bytes       int           `json:"utf8_bytes"`
	Digest      string        `json:"digest"`
}

func EpisodeRefs(b Body) []string {
	out := []string{}
	add := func(id string) {
		if id != "" {
			out = append(out, id)
		}
	}
	if b.Cast != nil {
		add(b.Cast.EpisodeID)
	}
	add(b.FromEpisodeID)
	add(b.ToEpisodeID)
	if b.Fact != nil {
		add(b.Fact.AudienceFrom)
		for _, k := range b.Fact.KnownBy {
			add(k.FromEpisodeID)
		}
	}
	if b.Event != nil {
		add(b.Event.EpisodeID)
	}
	if b.Foreshadow != nil {
		add(b.Foreshadow.SetupEpisodeID)
		add(b.Foreshadow.PayoffEpisodeID)
	}
	for _, e := range b.Evidence {
		add(e.EpisodeID)
	}
	return out
}

// Compile separates narrative applicability, in-world chronology, audience
// knowledge and a character's belief. It never treats a belief as objective fact.
// limit is the caller's byte capacity for the included-material JSON fragment
// after reserving the surrounding request; it does not assert token fit. It is not a project
// allowance. Zero previews all material; optional content may be omitted only
// with an explicit reason. Required material blocks rather than disappearing.
func Compile(seriesID, episodeID string, order []Episode, versions []Version, limit int) (Preview, error) {
	p := Preview{Compiler: 1, SeriesID: seriesID, EpisodeID: episodeID, Sources: []Summary{}, Included: []ContextItem{}, Omitted: []Omission{}, Problems: []Problem{}}
	if !ID.MatchString(seriesID) || !ID.MatchString(episodeID) || limit < 0 {
		return p, ErrInput
	}
	order, e := Clone(order)
	if e != nil {
		return p, ErrInput
	}
	sort.Slice(order, func(i, j int) bool {
		a, b := order[i], order[j]
		if a.SeasonOrdinal != b.SeasonOrdinal {
			return a.SeasonOrdinal < b.SeasonOrdinal
		}
		if a.Ordinal != b.Ordinal {
			return a.Ordinal < b.Ordinal
		}
		return a.ID < b.ID
	})
	ranks := map[string]int{}
	for i, x := range order {
		if !ID.MatchString(x.ID) || !ID.MatchString(x.SeasonID) || x.SeasonOrdinal < 1 || x.Ordinal < 1 || x.Version < 1 {
			return p, ErrInput
		}
		if _, ok := ranks[x.ID]; ok {
			return p, ErrInput
		}
		if i > 0 && order[i-1].SeasonOrdinal == x.SeasonOrdinal && order[i-1].Ordinal == x.Ordinal {
			return p, ErrInput
		}
		ranks[x.ID] = i
	}
	target, ok := ranks[episodeID]
	if !ok {
		return p, ErrNotFound
	}
	p.OrderSHA256 = Digest(order)
	versions, e = Clone(versions)
	if e != nil {
		return p, ErrInput
	}
	sort.Slice(versions, func(i, j int) bool {
		a, b := versions[i], versions[j]
		if (a.Kind == "bible") != (b.Kind == "bible") {
			return a.Kind == "bible"
		}
		if a.Required != b.Required {
			return a.Required
		}
		return a.ID < b.ID
	})
	chars := map[string]bool{}
	bibleVersion := ""
	bibles := 0
	ids := map[string]bool{}
	for _, v := range versions {
		if !Valid(v) || v.SeriesID != seriesID || ids[v.ID] {
			return p, ErrInput
		}
		ids[v.ID] = true
		p.Sources = append(p.Sources, Summarize(v))
		if v.Kind == "bible" && v.Status == "active" {
			bibles++
			bibleVersion = v.VersionID
			if b := v.Body.Bible; b.Theme == "" && b.WorldRules == "" && b.StoryArc == "" && b.VisualIntent == "" && b.ImmutableRules == "" && len(b.Characters) == 0 {
				p.Problems = append(p.Problems, Problem{"empty_bible", v.ID, "故事圣经尚未填写规则、主线或人物，请补充后再核对。"})
			}
			for _, c := range v.Body.Bible.Characters {
				chars[c.ID] = true
			}
		}
	}
	if bibles != 1 {
		p.Problems = append(p.Problems, Problem{Code: "bible_required", Message: "请先保存一份正式故事圣经，再核对本集上下文。"})
	}
	for _, v := range versions {
		if v.Status != "active" {
			p.Omitted = append(p.Omitted, Omission{v.ID, v.VersionID, "archived"})
			continue
		}
		if c := v.Body.Cast; c != nil {
			if c.EpisodeID != episodeID {
				p.Omitted = append(p.Omitted, Omission{v.ID, v.VersionID, "other_episode_cast"})
				continue
			}
			if c.BibleVersionID != bibleVersion {
				p.Problems = append(p.Problems, Problem{"cast_bible_changed", v.ID, "系列人物资料已更新，请核对本集选角后保存新版本。"})
				continue
			}
			missing := false
			for _, x := range c.Characters {
				missing = missing || !chars[x.SeriesID]
			}
			if missing {
				p.Problems = append(p.Problems, Problem{"character_reference_missing", v.ID, "选角引用的系列人物已不可用，请明确重新选择。"})
				continue
			}
		}
		invalid := false
		for _, id := range EpisodeRefs(v.Body) {
			if _, ok := ranks[id]; !ok {
				p.Problems = append(p.Problems, Problem{"episode_reference_missing", v.ID, "资料引用的剧集已不可用，请核对来源与系列归属。"})
				invalid = true
				break
			}
		}
		if invalid {
			continue
		}
		from, to := 0, len(order)-1
		if v.Body.FromEpisodeID != "" {
			from = ranks[v.Body.FromEpisodeID]
		}
		if v.Body.ToEpisodeID != "" {
			to = ranks[v.Body.ToEpisodeID]
		}
		if from > to {
			p.Problems = append(p.Problems, Problem{"episode_range_reversed", v.ID, "剧集顺序变化后，资料适用范围倒置，请重新确认。"})
			continue
		}
		if target < from || target > to {
			p.Omitted = append(p.Omitted, Omission{v.ID, v.VersionID, "outside_episode_range"})
			continue
		}
		if v.Body.Event != nil && ranks[v.Body.Event.EpisodeID] > target {
			p.Omitted = append(p.Omitted, Omission{v.ID, v.VersionID, "future_event"})
			continue
		}
		if x := v.Body.Foreshadow; x != nil && x.PayoffEpisodeID != "" && ranks[x.SetupEpisodeID] > ranks[x.PayoffEpisodeID] {
			p.Problems = append(p.Problems, Problem{"foreshadow_order_reversed", v.ID, "剧集顺序变化后，伏笔回收早于埋设，请重新确认。"})
			continue
		}
		item := ContextItem{Summary: Summarize(v), Body: v.Body, KnownBy: []string{}}
		if f := v.Body.Fact; f != nil {
			if f.BelieverID != "" && !chars[f.BelieverID] {
				p.Problems = append(p.Problems, Problem{"character_reference_missing", v.ID, "角色所信引用了不在当前圣经中的角色，请先核对人物版本。"})
				invalid = true
			}
			for _, k := range f.KnownBy {
				if !chars[k.CharacterID] {
					p.Problems = append(p.Problems, Problem{"character_reference_missing", v.ID, "知识边界引用了不在当前圣经中的角色，请先核对人物版本。"})
					invalid = true
				}
				if ranks[k.FromEpisodeID] <= target {
					item.KnownBy = append(item.KnownBy, k.CharacterID)
				}
			}
			item.AudienceKnows = f.AudienceFrom != "" && ranks[f.AudienceFrom] <= target
		}
		if invalid {
			continue
		}
		sort.Strings(item.KnownBy)
		candidate := append(append([]ContextItem{}, p.Included...), item)
		raw, _ := json.Marshal(candidate)
		if limit > 0 && len(raw) > limit {
			if v.Required {
				p.Problems = append(p.Problems, Problem{"required_context_overflow", v.ID, "必需资料超过本次输入容量，未静默截断；请调整资料或模型能力。"})
			} else {
				p.Omitted = append(p.Omitted, Omission{v.ID, v.VersionID, "input_capacity"})
			}
			continue
		}
		p.Included = candidate
	}
	raw, e := json.Marshal(p.Included)
	if e != nil {
		return p, ErrInput
	}
	p.Bytes = len(raw)
	p.Digest = Digest(p)
	return p, nil
}
