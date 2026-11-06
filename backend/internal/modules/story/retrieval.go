package story

import (
	"encoding/json"
	"github.com/adcwb/ai-creative-studio/backend/internal/modules/projects"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// ContextBytes is a per-request transport working set, never a project allowance
// or a claim about a provider's native token context window.
const ContextBytes = 2 << 20

type Selection struct {
	Query     string   `json:"query"`
	RecordIDs []string `json:"record_ids"`
}

func NormalizeSelection(in Selection) (Selection, error) {
	in.Query = strings.TrimSpace(in.Query)
	if !utf8.ValidString(in.Query) {
		return in, ErrInput
	}
	ids := map[string]bool{}
	out := Selection{Query: in.Query, RecordIDs: []string{}}
	for _, id := range in.RecordIDs {
		if !ID.MatchString(id) || ids[id] {
			return out, ErrInput
		}
		ids[id] = true
		out.RecordIDs = append(out.RecordIDs, id)
	}
	sort.Strings(out.RecordIDs)
	return out, nil
}

type Retrieval struct {
	Rule          int       `json:"retrieval_version"`
	Selection     Selection `json:"selection"`
	SourcesSHA256 string    `json:"sources_sha256"`
	Preview
}

// Retrieve consumes one version at a time in bible/required/id order. It retains
// summaries for provenance and only a bounded selection of bodies. Visit must
// hold one consistent head view for the whole callback sequence.
func Retrieve(root, ep string, order []Episode, selection Selection, visit func(func(Version) error) error) (Retrieval, error) {
	out := Retrieval{Rule: 1}
	var err error
	out.Selection, err = NormalizeSelection(selection)
	if err != nil {
		return out, err
	}
	requested := map[string]bool{}
	for _, id := range out.Selection.RecordIDs {
		requested[id] = false
	}
	sources := []Summary{}
	omitted := []Omission{}
	problems := []Problem{}
	selected := []Version{}
	var bible *Version
	used := 2
	last := ""
	terms := strings.Fields(strings.ToLower(out.Selection.Query))
	err = visit(func(v Version) error {
		if !Valid(v) || v.SeriesID != root {
			return ErrInput
		}
		priority := "2"
		if v.Required {
			priority = "1"
		}
		if v.Kind == "bible" {
			priority = "0"
		}
		key := priority + v.ID
		if key <= last {
			return ErrInput
		}
		last = key
		sources = append(sources, Summarize(v))
		_, explicit := requested[v.ID]
		if explicit {
			requested[v.ID] = true
		}
		include := v.Required || v.Kind == "bible" || explicit || len(terms) == 0
		if !include {
			text := strings.ToLower(searchable(v))
			for _, term := range terms {
				if strings.Contains(text, term) {
					include = true
					break
				}
			}
		}
		reason := "not_retrieved"
		if v.Status != "active" {
			include = false
			reason = "archived"
		}
		if include {
			// Apply narrative scope before consuming transport capacity: a future
			// flashback must neither leak nor crowd out this episode's material.
			candidates := []Version{v}
			if v.Kind == "bible" {
				copy := v
				bible = &copy
			} else if bible != nil {
				candidates = append(candidates, *bible)
			}
			candidate, e := Compile(root, ep, order, candidates, 0)
			if e != nil {
				return e
			}
			var item *ContextItem
			for i := range candidate.Included {
				if candidate.Included[i].ID == v.ID {
					item = &candidate.Included[i]
					break
				}
			}
			if item == nil {
				for _, p := range candidate.Problems {
					if p.RecordID == v.ID {
						problems = append(problems, p)
					}
				}
				for _, o := range candidate.Omitted {
					if o.ID == v.ID {
						omitted = append(omitted, o)
					}
				}
				return nil
			}
			raw, _ := json.Marshal(item)
			size := len(raw) + 1
			if used+size > ContextBytes {
				include = false
				reason = "input_capacity"
				if v.Required || v.Kind == "bible" {
					problems = append(problems, Problem{"required_context_overflow", v.ID, "必需资料超过本次输入容量，未静默截断；请调整资料或模型能力。"})
				}
			} else {
				used += size
				selected = append(selected, v)
			}
		}
		if !include {
			omitted = append(omitted, Omission{v.ID, v.VersionID, reason})
		}
		return nil
	})
	if err != nil {
		return out, err
	}
	for _, found := range requested {
		if !found {
			return out, ErrInput
		}
	}
	p, err := Compile(root, ep, order, selected, ContextBytes)
	if err != nil {
		return out, err
	}
	out.SourcesSHA256 = Digest(sources)
	p.Sources = sources
	p.Omitted = append(p.Omitted, omitted...)
	p.Problems = append(p.Problems, problems...)
	// All manifests have deterministic order, independent of callback internals.
	sort.Slice(p.Omitted, func(i, j int) bool { return p.Omitted[i].ID < p.Omitted[j].ID })
	p.Digest = ""
	p.Digest = Digest(p)
	out.Preview = p
	return out, nil
}

type ContextSnapshot struct {
	Format          int              `json:"format"`
	ID              string           `json:"id"`
	Space           string           `json:"space_id"`
	SeriesID        string           `json:"series_id"`
	EpisodeID       string           `json:"episode_id"`
	Context         Retrieval        `json:"context"`
	DirectorySHA256 string           `json:"directory_sha256"`
	ProjectVersion  int              `json:"project_version"`
	ScriptVersion   int              `json:"script_version"`
	Script          *projects.Script `json:"script"`
	ScriptSHA256    string           `json:"script_sha256"`
	Actor           string           `json:"created_by"`
	CreatedAt       time.Time        `json:"created_at"`
	SHA256          string           `json:"content_sha256"`
}

func SnapshotDigest(s ContextSnapshot) string { s.SHA256 = ""; return Digest(s) }
func ValidSnapshot(s ContextSnapshot) bool {
	if s.Format != 1 || !ID.MatchString(s.ID) || !Scope.MatchString(s.Space) || !ID.MatchString(s.SeriesID) || !ID.MatchString(s.EpisodeID) || !Scope.MatchString(s.Actor) || s.CreatedAt.IsZero() || s.ProjectVersion < 1 || s.ScriptVersion < 0 || !Hash.MatchString(s.DirectorySHA256) || !Hash.MatchString(s.ScriptSHA256) || s.Context.Rule != 1 || s.Context.SeriesID != s.SeriesID || s.Context.EpisodeID != s.EpisodeID || s.Context.Compiler != 1 || !Hash.MatchString(s.Context.SourcesSHA256) || s.SHA256 != SnapshotDigest(s) {
		return false
	}
	if Digest(s.Script) != s.ScriptSHA256 {
		return false
	}
	normalized, err := NormalizeSelection(s.Context.Selection)
	if err != nil || Digest(normalized) != Digest(s.Context.Selection) {
		return false
	}
	preview := s.Context.Preview
	expected := preview.Digest
	preview.Digest = ""
	return Digest(preview) == expected && Digest(s.Context.Sources) == s.Context.SourcesSHA256
}

type ContextSummary struct {
	ID            string    `json:"id"`
	EpisodeID     string    `json:"episode_id"`
	ScriptVersion int       `json:"script_version"`
	CreatedAt     time.Time `json:"created_at"`
	Included      int       `json:"included_count"`
	Problems      int       `json:"problem_count"`
	SHA256        string    `json:"content_sha256"`
}

func SummarizeContext(s ContextSnapshot) ContextSummary {
	return ContextSummary{s.ID, s.EpisodeID, s.ScriptVersion, s.CreatedAt, len(s.Context.Included), len(s.Context.Problems), s.SHA256}
}

// Search authored prose, not JSON field names, stable IDs or implicit prompts.
func authoredTexts(v Version) []string {
	texts := []string{v.Title}
	if x := v.Body.Outline; x != nil {
		texts = append(texts, x.Goal, x.Opening, x.Conflict, x.Turn, x.Hook)
	}
	if c := v.Body.Cast; c != nil {
		for _, x := range c.Characters {
			texts = append(texts, x.ScriptName, x.SeriesName, x.AssetName, x.Notes)
		}
	}
	if b := v.Body.Bible; b != nil {
		texts = append(texts, b.Theme, b.WorldRules, b.StoryArc, b.VisualIntent, b.ImmutableRules)
		for _, c := range b.Characters {
			texts = append(texts, c.Name, c.Desire, c.Flaw, c.Arc, c.Relationships, c.VisualIntent, c.VoiceIntent)
		}
	}
	if f := v.Body.Fact; f != nil {
		texts = append(texts, f.Subject, f.Predicate, f.Value, f.StoryTime)
	}
	if e := v.Body.Event; e != nil {
		texts = append(texts, e.StoryTime, e.Description, e.StateChange)
	}
	if f := v.Body.Foreshadow; f != nil {
		texts = append(texts, f.Intent)
	}
	for _, e := range v.Body.Evidence {
		texts = append(texts, e.Quote)
	}
	return texts
}

func searchable(v Version) string { return strings.Join(authoredTexts(v), "\n") }
