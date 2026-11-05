package story

import (
	"errors"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func orderedVisit(versions []Version) func(func(Version) error) error {
	versions = append([]Version{}, versions...)
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
	return func(fn func(Version) error) error {
		for _, v := range versions {
			if e := fn(v); e != nil {
				return e
			}
		}
		return nil
	}
}
func TestRetrievalPinsRequiredAndExplainsOptionalSelection(t *testing.T) {
	order, versions := fixture(t)
	input := Selection{Query: "不存在的词", RecordIDs: []string{}}
	r, e := Retrieve(ident(1), ident(3), order, input, orderedVisit(versions))
	if e != nil || len(r.Included) != 2 || len(r.Omitted) != 1 || r.Omitted[0].Reason != "not_retrieved" || len(r.Sources) != 3 || len(r.Problems) != 0 {
		t.Fatal(e, r)
	}
	input.RecordIDs = []string{versions[0].ID}
	chosen, e := Retrieve(ident(1), ident(3), order, input, orderedVisit(versions))
	if e != nil || len(chosen.Included) != 3 {
		t.Fatal(e, chosen)
	}
	input.Query = "陌生人"
	input.RecordIDs = nil
	queried, e := Retrieve(ident(1), ident(3), order, input, orderedVisit(versions))
	if e != nil || len(queried.Included) != 3 {
		t.Fatal(e, queried)
	}
	repeat, e := Retrieve(ident(1), ident(3), order, input, orderedVisit([]Version{versions[2], versions[0], versions[1]}))
	if e != nil || !reflect.DeepEqual(repeat, queried) {
		t.Fatal("non deterministic retrieval", e)
	}
	input.RecordIDs = []string{ident(999)}
	if _, e = Retrieve(ident(1), ident(3), order, input, orderedVisit(versions)); !errors.Is(e, ErrInput) {
		t.Fatal("unknown explicit source accepted", e)
	}
	if _, e = NormalizeSelection(Selection{RecordIDs: []string{ident(3), ident(3)}}); !errors.Is(e, ErrInput) {
		t.Fatal("duplicate selection", e)
	}
}
func TestRetrievalNarrativeScopeBeforeCapacityAndNoSilentRequiredLoss(t *testing.T) {
	order, versions := fixture(t)
	future := versions[0]
	future.Required = true
	future.Body.Event.Description = strings.Repeat("future", ContextBytes/4)
	future.SHA256 = ContentDigest(Input{Title: future.Title, Kind: future.Kind, Status: future.Status, Required: true, Body: future.Body})
	r, e := Retrieve(ident(1), ident(2), order, Selection{}, orderedVisit([]Version{future, versions[1], versions[2]}))
	if e != nil || len(r.Problems) != 0 || len(r.Included) != 2 || r.Omitted[0].Reason != "future_event" {
		t.Fatal("future material consumed this episode capacity", e, r.Problems)
	}
	r, e = Retrieve(ident(1), ident(3), order, Selection{}, orderedVisit([]Version{future, versions[1], versions[2]}))
	if e != nil || len(r.Problems) != 1 || r.Problems[0].Code != "required_context_overflow" || r.Bytes > ContextBytes {
		t.Fatal("required overflow hidden", e, r.Problems)
	}
	optional := future
	optional.Required = false
	optional.SHA256 = ContentDigest(Input{Title: optional.Title, Kind: optional.Kind, Status: optional.Status, Body: optional.Body})
	r, e = Retrieve(ident(1), ident(3), order, Selection{}, orderedVisit([]Version{optional, versions[1], versions[2]}))
	if e != nil || len(r.Problems) != 0 || r.Omitted[0].Reason != "input_capacity" {
		t.Fatal(e, r.Problems)
	}
}

func TestRetrievalStreamsSeriesBodiesWithoutProjectCountQuota(t *testing.T) {
	order, base := fixture(t)
	visited := 0
	r, e := Retrieve(ident(1), ident(3), order, Selection{}, func(fn func(Version) error) error {
		if e := fn(base[2]); e != nil {
			return e
		}
		// 600 records contain over 9 MiB in total. Only selected bodies survive the
		// callback; the complete source manifest remains available for provenance.
		for i := 1000; i < 1600; i++ {
			v := version(t, i, Input{Title: "过去的证据", Kind: "event", Status: "active", Body: Body{Event: &Event{EpisodeID: ident(3), Description: strings.Repeat("证", 6000)}}})
			visited++
			if e := fn(v); e != nil {
				return e
			}
		}
		return nil
	})
	if e != nil || visited != 600 || len(r.Sources) != 601 || len(r.Omitted) == 0 || r.Bytes > ContextBytes || len(r.Problems) != 0 {
		t.Fatal("aggregate body capacity became a project record quota", e, visited, len(r.Sources), r.Bytes, r.Problems)
	}
	// Schema field names must not produce a prose match.
	r, e = Retrieve(ident(1), ident(3), order, Selection{Query: "predicate"}, orderedVisit(base))
	if e != nil || len(r.Included) != 2 {
		t.Fatal("searched schema names rather than prose", e)
	}
}
