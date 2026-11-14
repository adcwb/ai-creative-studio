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

func TestSavedViewportRejectsCorruptCoordinates(t *testing.T) {
	for _, v := range []Viewport{{X: math.Inf(1), Zoom: 1}, {Y: math.NaN(), Zoom: 1}, {Zoom: 0}, {Zoom: 2.1}, {Zoom: math.NaN()}} {
		g := fixture()
		g.Viewport = &v
		if _, err := Validate(g, false); err == nil {
			t.Fatal("invalid viewport accepted", v)
		}
	}
	g := fixture()
	g.Viewport = &Viewport{X: -120, Y: 40, Zoom: .7}
	if _, err := Validate(g, true); err != nil {
		t.Fatal(err)
	}
}

func TestTemplateNodeUsesTypedLiteralTextAndRequiresInput(t *testing.T) {
	g := fixture()
	g.Nodes = append(g.Nodes, Node{ID: "template", Kind: "template", Text: "Poster: {{input}}"})
	g.Edges[0] = Edge{Source: "template", Target: "image", Input: "prompt"}
	g.Edges = append(g.Edges, Edge{Source: "text", Target: "template", Input: "prompt"})
	if _, err := Validate(g, true); err != nil {
		t.Fatal(err)
	}
	g.Nodes[len(g.Nodes)-1].Text = "{{input.secret}}"
	if _, err := Validate(g, false); err == nil {
		t.Fatal("expression accepted")
	}
	g.Nodes[len(g.Nodes)-1].Text = "{{input}}"
	g.Edges = g.Edges[:len(g.Edges)-1]
	if _, err := Validate(g, true); err == nil {
		t.Fatal("missing input accepted")
	}
}
