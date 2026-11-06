package revisions_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/revisions"
)

func TestCanonicalJSONBAndUnboundedReferenceHistory(t *testing.T) {
	m := revisions.Manifest{Brief: json.RawMessage(`{"z":2,"a":{"y":1,"x":4}}`), Script: json.RawMessage(`null`), Storyboard: json.RawMessage(`null`), Timeline: json.RawMessage(`{}`), Poster: json.RawMessage(`null`), Sources: json.RawMessage(`{"b":1,"a":2}`)}
	a, e := revisions.Digest(m)
	if e != nil {
		t.Fatal(e)
	}
	m.Brief = json.RawMessage(`{ "a": {"x":4, "y":1}, "z":2}`)
	m.Sources = json.RawMessage(`{"a":2,"b":1}`)
	b, e := revisions.Digest(m)
	if e != nil || a != b {
		t.Fatal("key order changed snapshot identity", e)
	}
	for i := 0; i < 600; i++ {
		m.References = append(m.References, revisions.Ref{Kind: "media", ID: fmt.Sprintf("asset-%d", i), VersionID: "version-1", SHA256: strings.Repeat("a", 64)})
	}
	if _, e = revisions.Digest(m); e != nil {
		t.Fatal("old 512 reference quota", e)
	}
	edges := []revisions.Edge{}
	for i := 0; i < 4200; i++ {
		edges = append(edges, revisions.Edge{Consumer: revisions.Ref{Kind: "image", ID: fmt.Sprint(i), VersionID: "v1", SHA256: strings.Repeat("b", 64)}, Source: revisions.Ref{Kind: "image", ID: "root", VersionID: "v1", SHA256: strings.Repeat("b", 64)}, Usage: "reference"})
	}
	if e = revisions.ValidateEdges(edges); e != nil {
		t.Fatal("old edge quota", e)
	}
	m.References = append(m.References, m.References[0])
	if _, e = revisions.Digest(m); e == nil {
		t.Fatal("duplicate reference accepted")
	}
}
