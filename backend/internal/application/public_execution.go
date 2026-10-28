package application

// Execution bindings belong in durable storage, not API responses. This copy-only
// boundary never mutates the persisted actor, budget confirmation or task.
func publicExecutionResponse(value any) any {
	switch v := value.(type) {
	case Task:
		v.RuntimeSelection = nil
		return v
	case ScriptGeneration:
		v.RuntimeSelection = nil
		return v
	case StoryboardGeneration:
		v.RuntimeSelection = nil
		return v
	case CreativeJob:
		v.RuntimeSelection = nil
		return v
	case AlignmentTask:
		v.RuntimeSelection = nil
		return v
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
		out := make([]Task, len(v))
		for i, item := range v {
			out[i] = publicExecutionResponse(item).(Task)
		}
		return out
	case []ScriptGeneration:
		out := make([]ScriptGeneration, len(v))
		for i, item := range v {
			out[i] = publicExecutionResponse(item).(ScriptGeneration)
		}
		return out
	case []StoryboardGeneration:
		out := make([]StoryboardGeneration, len(v))
		for i, item := range v {
			out[i] = publicExecutionResponse(item).(StoryboardGeneration)
		}
		return out
	case []CreativeJob:
		out := make([]CreativeJob, len(v))
		for i, item := range v {
			out[i] = publicExecutionResponse(item).(CreativeJob)
		}
		return out
	case []AlignmentTask:
		out := make([]AlignmentTask, len(v))
		for i, item := range v {
			out[i] = publicExecutionResponse(item).(AlignmentTask)
		}
		return out
	case []Project:
		out := make([]Project, len(v))
		for i, item := range v {
			out[i] = publicExecutionResponse(item).(Project)
		}
		return out
	default:
		return value
	}
}
