package workflows

import (
	"math"
	"testing"
)

func fixture() Graph {
	return Graph{Nodes: []Node{{ID: "text", Kind: "text", Text: "A quiet room"}, {ID: "image", Kind: "image", ModelID: "model", ModelVersion: 1}, {ID: "review", Kind: "review"}, {ID: "output", Kind: "output"}}, Edges: []Edge{{"text", "image", "prompt"}, {"image", "review", "media"}, {"review", "output", "media"}}}
}
func TestTypedGraphPlanAndFailureBoundaries(t *testing.T) {
	g := fixture()
	order, e := Validate(g, true)
	if e != nil || len(order) != 4 || order[0] != "text" || order[3] != "output" {
		t.Fatal(order, e)
	}
	bad := fixture()
	bad.Edges = append(bad.Edges, Edge{"output", "image", "reference"})
	if _, e = Validate(bad, true); e == nil {
		t.Fatal("cycle")
	}
	bad = fixture()
	bad.Edges[0].Source = "image"
	if _, e = Validate(bad, true); e == nil {
		t.Fatal("self edge")
	}
	bad = fixture()
	bad.Edges = append(bad.Edges, Edge{"text", "review", "media"})
	if _, e = Validate(bad, true); e == nil {
		t.Fatal("duplicate port")
	}
	bad = fixture()
	bad.Nodes[1].ModelID = ""
	if _, e = Validate(bad, false); e != nil {
		t.Fatal("draft should save", e)
	}
	if _, e = Validate(bad, true); e == nil {
		t.Fatal("unconfigured model")
	}
	bad = fixture()
	bad.Nodes = append(bad.Nodes, Node{ID: "orphan", Kind: "speech", ModelID: "model", ModelVersion: 1})
	bad.Edges = append(bad.Edges, Edge{"text", "orphan", "prompt"})
	if _, e = Validate(bad, true); e == nil {
		t.Fatal("unused paid node")
	}
	bad = fixture()
	bad.Nodes[0].X = math.NaN()
	if _, e = Validate(bad, false); e == nil {
		t.Fatal("nonfinite position")
	}
	bad = fixture()
	bad.Nodes[0].Kind = "shell"
	if _, e = Validate(bad, false); e == nil {
		t.Fatal("arbitrary execution node")
	}
}
