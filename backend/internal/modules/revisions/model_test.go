package revisions_test

import (
	"encoding/json"
	"errors"
	"github.com/adcwb/ai-creative-studio/backend/internal/modules/revisions"
	"strings"
	"testing"
)

func TestBranchCompareAndSwap(t *testing.T) {
	m := revisions.Manifest{Brief: json.RawMessage(`{"title":"test"}`), Script: json.RawMessage(`null`), Storyboard: json.RawMessage(`null`), Timeline: json.RawMessage(`{}`), Poster: json.RawMessage(`null`), References: []revisions.Ref{}}
	h, err := revisions.Digest(m)
	if err != nil {
		t.Fatal(err)
	}
	b := revisions.Branch{ID: "main", SpaceID: "space", ProjectID: "p", HeadRevisionID: "r1", Version: 1}
	r := revisions.Revision{ID: "r2", SpaceID: "space", ProjectID: "p", ParentID: "r1", Manifest: m, SHA256: h}
	next, err := revisions.Advance(b, 1, "r1", r)
	if err != nil || next.Version != 2 || b.Version != 1 {
		t.Fatal("advance", err)
	}
	if _, err = revisions.Advance(next, 1, "r1", r); !errors.Is(err, revisions.ErrConflict) {
		t.Fatal("stale head accepted")
	}
	r.SpaceID = "other"
	if _, err = revisions.Advance(b, 1, "r1", r); err == nil {
		t.Fatal("cross tenant accepted")
	}
	r.SpaceID = "space"
	r.Manifest.Brief = json.RawMessage(`{"title":"changed"}`)
	if _, err = revisions.Advance(b, 1, "r1", r); err == nil {
		t.Fatal("digest mismatch accepted")
	}
}

func TestVersionDependencyCycles(t *testing.T) {
	ref := func(v string) revisions.Ref {
		return revisions.Ref{Kind: "image", ID: "asset", VersionID: v, SHA256: strings.Repeat("a", 64)}
	}
	edges := []revisions.Edge{{Consumer: ref("v2"), Source: ref("v1"), Usage: "reference"}}
	if err := revisions.ValidateEdges(edges); err != nil {
		t.Fatal("same identity new version rejected", err)
	}
	edges = append(edges, revisions.Edge{Consumer: ref("v1"), Source: ref("v2"), Usage: "reference"})
	if revisions.ValidateEdges(edges) == nil {
		t.Fatal("cycle accepted")
	}
}
