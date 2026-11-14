// Package workflows defines a typed, versioned creative graph. It cannot run
// arbitrary programs or URLs; model work is delegated to existing owned jobs.
package workflows

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/adcwb/ai-creative-studio/backend/internal/modules/prompts"
	"math"
	"regexp"
	"sort"
	"strings"
)

var ErrInput = errors.New("invalid workflow")
var ErrConflict = errors.New("workflow version conflict")
var ID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,80}$`)

type Node struct {
	ID           string  `json:"id"`
	Kind         string  `json:"kind"`
	Label        string  `json:"label"`
	X            float64 `json:"x"`
	Y            float64 `json:"y"`
	Text         string  `json:"text,omitempty"`
	Variable     string  `json:"variable_name,omitempty"`
	AssetID      string  `json:"asset_id,omitempty"`
	AssetKind    string  `json:"asset_kind,omitempty"`
	ModelID      string  `json:"model_config_id,omitempty"`
	ModelVersion int64   `json:"model_config_version,omitempty"`
	Voice        string  `json:"voice,omitempty"`
	Ratio        string  `json:"ratio,omitempty"`
}
type Edge struct {
	Source string `json:"source"`
	Target string `json:"target"`
	Input  string `json:"input"`
}
type Viewport struct {
	X    float64 `json:"x"`
	Y    float64 `json:"y"`
	Zoom float64 `json:"zoom"`
}
type Graph struct {
	Variables []Variable `json:"variables,omitempty"`
	Viewport  *Viewport  `json:"viewport,omitempty"`
	Nodes     []Node     `json:"nodes"`
	Edges     []Edge     `json:"edges"`
}
type Revision struct {
	ID        string `json:"id"`
	Number    int    `json:"number"`
	SHA256    string `json:"sha256"`
	Graph     Graph  `json:"graph"`
	CreatedAt string `json:"created_at"`
	Actor     string `json:"created_by"`
}
type Workflow struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Version   int        `json:"version"`
	Head      string     `json:"head_revision_id"`
	Revisions []Revision `json:"revisions"`
}
type Result struct {
	Text      string `json:"text,omitempty"`
	AssetID   string `json:"asset_id,omitempty"`
	AssetKind string `json:"asset_kind,omitempty"`
}
type NodeRun struct {
	ReusedFrom string `json:"reused_from_run_id,omitempty"`
	ReviewedBy string `json:"reviewed_by,omitempty"`
	ReviewedAt string `json:"reviewed_at,omitempty"`
	NodeID     string `json:"node_id"`
	Status     string `json:"status"`
	JobID      string `json:"job_id,omitempty"`
	Result     Result `json:"result"`
	ErrorCode  string `json:"error_code,omitempty"`
}
type Run struct {
	FromRunID      string             `json:"from_run_id,omitempty"`
	RestartNode    string             `json:"restart_node_id,omitempty"`
	Inputs         map[string]string  `json:"inputs,omitempty"`
	InputsSHA256   string             `json:"inputs_sha256,omitempty"`
	ID             string             `json:"id"`
	WorkflowID     string             `json:"workflow_id"`
	RevisionID     string             `json:"revision_id"`
	SHA256         string             `json:"sha256"`
	ProjectVersion int                `json:"project_version"`
	Actor          string             `json:"created_by"`
	Status         string             `json:"status"`
	Version        int                `json:"version"`
	CreatedAt      string             `json:"created_at"`
	Nodes          map[string]NodeRun `json:"nodes"`
	Graph          Graph              `json:"graph"`
}

func Digest(v any) string {
	b, _ := json.Marshal(v)
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}
func ModelKind(kind string) bool {
	return kind == "image" || kind == "speech" || kind == "video" || kind == "llm"
}
func Terminal(status string) bool {
	return status == "succeeded" || status == "failed" || status == "cancelled" || status == "unknown"
}
func kindType(n Node) string {
	switch n.Kind {
	case "text", "template", "variable", "llm":
		return "text"
	case "asset":
		return n.AssetKind
	case "image", "video":
		return n.Kind
	case "speech":
		return "audio"
	}
	return ""
}

// Validate returns stable execution order. Drafts may omit inputs and models,
// but malformed identities, cycles, duplicate ports and incompatible types are
// never accepted. A run additionally requires every step to reach an output.
func Validate(g Graph, run bool) ([]string, error) {
	if g.Viewport != nil && (math.IsNaN(g.Viewport.X) || math.IsNaN(g.Viewport.Y) || math.IsInf(g.Viewport.X, 0) || math.IsInf(g.Viewport.Y, 0) || math.IsNaN(g.Viewport.Zoom) || math.IsInf(g.Viewport.Zoom, 0) || g.Viewport.Zoom < .25 || g.Viewport.Zoom > 2) {
		return nil, ErrInput
	}
	if len(g.Nodes) == 0 {
		return nil, ErrInput
	}
	validationInputs := map[string]string{}
	for _, v := range g.Variables {
		validationInputs[v.Name] = "x"
	}
	if _, err := ResolveInputs(g, validationInputs); err != nil {
		return nil, err
	}
	nodes := map[string]Node{}
	degree := map[string]int{}
	children := map[string][]string{}
	incoming := map[string]map[string]string{}
	for _, n := range g.Nodes {
		if !ID.MatchString(n.ID) || len(n.Label) > 320 || math.IsNaN(n.X) || math.IsNaN(n.Y) || math.IsInf(n.X, 0) || math.IsInf(n.Y, 0) || len(n.Text) > 1<<20 {
			return nil, ErrInput
		}
		if _, ok := nodes[n.ID]; ok {
			return nil, ErrInput
		}
		switch n.Kind {
		case "text", "template", "variable", "llm", "asset", "image", "speech", "video", "review", "output":
		default:
			return nil, ErrInput
		}
		if n.Kind == "asset" && (n.AssetKind != "image" && n.AssetKind != "audio" && n.AssetKind != "video") {
			return nil, ErrInput
		}
		if run && ((n.Kind == "text" || n.Kind == "template" || n.Kind == "llm") && strings.TrimSpace(n.Text) == "" || n.Kind == "asset" && !ID.MatchString(n.AssetID) || ModelKind(n.Kind) && (!ID.MatchString(n.ModelID) || n.ModelVersion < 1)) {
			return nil, ErrInput
		}
		if n.Kind == "variable" && (n.Variable != "" || run) {
			if _, ok := validationInputs[n.Variable]; !ok {
				return nil, ErrInput
			}
		}
		if n.Kind == "template" && strings.TrimSpace(n.Text) != "" {
			values, e := TemplateValues(n.Text, validationInputs, "x")
			if e != nil {
				return nil, ErrInput
			}
			if _, err := prompts.ExpandLiteral(n.Text, values, 2000); err != nil {
				return nil, ErrInput
			}
		}
		if n.Ratio != "" && n.Ratio != "9:16" && n.Ratio != "16:9" && n.Ratio != "1:1" && n.Ratio != "4:5" {
			return nil, ErrInput
		}
		nodes[n.ID] = n
		degree[n.ID] = 0
		incoming[n.ID] = map[string]string{}
	}
	for _, e := range g.Edges {
		_, a := nodes[e.Source]
		target, b := nodes[e.Target]
		if !a || !b || e.Source == e.Target || incoming[e.Target][e.Input] != "" {
			return nil, ErrInput
		}
		valid := e.Input == "prompt" && (ModelKind(target.Kind) || target.Kind == "template") || e.Input == "reference" && (target.Kind == "image" || target.Kind == "video") || e.Input == "media" && (target.Kind == "review" || target.Kind == "output")
		if !valid {
			return nil, ErrInput
		}
		incoming[e.Target][e.Input] = e.Source
		degree[e.Target]++
		children[e.Source] = append(children[e.Source], e.Target)
	}
	order := []string{}
	ready := []string{}
	for id, d := range degree {
		if d == 0 {
			ready = append(ready, id)
		}
	}
	types := map[string]string{}
	for len(ready) > 0 {
		sort.Strings(ready)
		id := ready[0]
		ready = ready[1:]
		n := nodes[id]
		output := kindType(n)
		for input, parent := range incoming[id] {
			v := types[parent]
			if input == "prompt" && v != "text" || input == "reference" && v != "image" || input == "media" && (v != "image" && v != "audio" && v != "video" && v != "text") {
				return nil, ErrInput
			}
			if input == "media" {
				output = v
			}
		}
		if run {
			names, _ := prompts.LiteralNames(n.Text)
			needsInput := false
			for _, name := range names {
				needsInput = needsInput || name == "input"
			}
			if (ModelKind(n.Kind) || n.Kind == "template" && needsInput) && incoming[id]["prompt"] == "" || (n.Kind == "review" || n.Kind == "output") && incoming[id]["media"] == "" {
				return nil, ErrInput
			}
		}
		types[id] = output
		order = append(order, id)
		for _, child := range children[id] {
			degree[child]--
			if degree[child] == 0 {
				ready = append(ready, child)
			}
		}
	}
	if len(order) != len(nodes) {
		return nil, ErrInput
	}
	if run {
		reaches := map[string]bool{}
		outputs := 0
		for i := len(order) - 1; i >= 0; i-- {
			id := order[i]
			if nodes[id].Kind == "output" {
				reaches[id] = true
				outputs++
			}
			for _, child := range children[id] {
				reaches[id] = reaches[id] || reaches[child]
			}
		}
		if outputs == 0 {
			return nil, ErrInput
		}
		for _, id := range order {
			if !reaches[id] {
				return nil, ErrInput
			}
		}
	}
	return order, nil
}
