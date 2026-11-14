package workflows

import (
	"strings"
	"testing"
)

func TestRuntimeInputsAreExplicitLiteralAndBoundedByTextContract(t *testing.T) {
	g := fixture()
	g.Variables = []Variable{{Name: "subject", Required: true}, {Name: "scene", Default: "garden"}}
	if _, err := ResolveInputs(g, nil); err == nil {
		t.Fatal("missing required")
	}
	values, err := ResolveInputs(g, map[string]string{"subject": "{{not_evaluated}}"})
	if err != nil || values["scene"] != "garden" {
		t.Fatal(values, err)
	}
	if _, err = ResolveInputs(g, map[string]string{"subject": "a", "credential": "no"}); err == nil {
		t.Fatal("undeclared input")
	}
	g.Variables = append(g.Variables, Variable{Name: "subject"})
	if _, err = Validate(g, false); err == nil {
		t.Fatal("duplicate declaration")
	}
	g.Variables = []Variable{{Name: "input"}}
	if _, err = Validate(g, false); err == nil {
		t.Fatal("reserved input")
	}
	g.Variables = []Variable{{Name: "subject", Default: "bad\x00default"}}
	if _, err = ResolveInputs(g, map[string]string{"subject": "override"}); err == nil {
		t.Fatal("invalid declaration must fail even when runtime value overrides default")
	}
	g.Variables = []Variable{{Name: "subject"}}
	if _, err = ResolveInputs(g, map[string]string{"subject": strings.Repeat("x", 2001)}); err == nil {
		t.Fatal("downstream text window")
	}
	if _, err = TemplateValues("{{subject.secret}}", values, ""); err == nil {
		t.Fatal("property expression")
	}
}
