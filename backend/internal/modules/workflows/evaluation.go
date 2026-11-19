package workflows

import "github.com/adcwb/ai-creative-studio/backend/internal/modules/prompts"

// EvaluateLocalNode applies steps that can run using only the pinned run values.
func EvaluateLocalNode(definition Node, node NodeRun, variables map[string]string, inputs map[string]Result) (NodeRun, error) {
	switch definition.Kind {
	case "text":
		node.Result.Text = definition.Text
	case "variable":
		node.Result.Text = variables[definition.Variable]
	case "template":
		values, err := TemplateValues(definition.Text, variables, inputs["prompt"].Text)
		if err != nil {
			return node, err
		}
		node.Result.Text, err = prompts.ExpandLiteral(definition.Text, values, 2000)
		if err != nil {
			node.Status = "failed"
			node.ErrorCode = "invalid_workflow"
			return node, nil
		}
	case "review":
		node.Status = "waiting_review"
		node.Result = inputs["media"]
		return node, nil
	case "output":
		node.Result = inputs["media"]
	default:
		return node, ErrInput
	}
	node.Status = "succeeded"
	return node, nil
}
