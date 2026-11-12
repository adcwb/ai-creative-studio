package application

import (
	"encoding/json"
	"github.com/adcwb/ai-creative-studio/backend/internal/platform/diagnostics"
)

// Execution bindings belong in durable storage, not API responses. This copy-only
// boundary never mutates the persisted actor, budget confirmation or task.
func publicExecutionResponse(value any) any {
	switch v := value.(type) {
	case Task:
		v.RuntimeSelection = nil
		return publicDiagnosticValue(v, v.TraceParent, v.ErrorCode)
	case ScriptGeneration:
		v.RuntimeSelection = nil
		return publicDiagnosticValue(v, v.TraceParent, v.ErrorCode)
	case StoryboardGeneration:
		v.RuntimeSelection = nil
		return publicDiagnosticValue(v, v.TraceParent, v.ErrorCode)
	case CreativeJob:
		v.RuntimeSelection = nil
		return publicDiagnosticValue(v, v.TraceParent, v.ErrorCode)
	case AlignmentTask:
		v.RuntimeSelection = nil
		return publicDiagnosticValue(v, v.TraceParent, v.ErrorCode)
	case Project:
		if v.LatestGeneration != nil {
			g := *v.LatestGeneration
			g.RuntimeSelection = nil
			v.LatestGeneration = &g
		}
		if v.LatestStoryboardGeneration != nil {
			g := *v.LatestStoryboardGeneration
			g.RuntimeSelection = nil
			v.LatestStoryboardGeneration = &g
		}
		return v
	case []Task:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = publicExecutionResponse(item)
		}
		return out
	case []ScriptGeneration:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = publicExecutionResponse(item)
		}
		return out
	case []StoryboardGeneration:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = publicExecutionResponse(item)
		}
		return out
	case []CreativeJob:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = publicExecutionResponse(item)
		}
		return out
	case []AlignmentTask:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = publicExecutionResponse(item)
		}
		return out
	case []Project:
		out := make([]Project, len(v))
		for i, item := range v {
			out[i] = publicExecutionResponse(item).(Project)
		}
		return out
	case MediaState:
		exports := make([]any, len(v.Exports))
		for i, e := range v.Exports {
			code := ""
			if e.Status == "failed" {
				code = "execution_interrupted"
			}
			exports[i] = publicDiagnosticValue(e, e.TraceParent, code)
		}
		return diagnosticJSON{value: v, extra: map[string]any{"exports": exports}}
	case MediaExport:
		code := ""
		if v.Status == "failed" {
			code = "execution_interrupted"
		}
		return publicDiagnosticValue(v, v.TraceParent, code)
	default:
		return value
	}
}

// Marshal on a copy at the API boundary: domain records and their hashes do not
// acquire new persistence fields, and legacy JSON readers remain compatible.
type diagnosticJSON struct {
	value any
	extra map[string]any
}

func (d diagnosticJSON) MarshalJSON() ([]byte, error) {
	raw, e := json.Marshal(d.value)
	if e != nil {
		return nil, e
	}
	var fields map[string]json.RawMessage
	if e = json.Unmarshal(raw, &fields); e != nil {
		return nil, e
	}
	for k, v := range d.extra {
		b, e := json.Marshal(v)
		if e != nil {
			return nil, e
		}
		fields[k] = b
	}
	return json.Marshal(fields)
}
func publicDiagnosticValue(v any, parent, code string) any {
	extra := map[string]any{}
	if id := diagnostics.FromParent(parent); id != "" {
		extra["correlation_id"] = id
	}
	if detail := taskDiagnostic(parent, code); detail != nil {
		extra["diagnostics"] = detail
	}
	if len(extra) == 0 {
		return v
	}
	return diagnosticJSON{value: v, extra: extra}
}
