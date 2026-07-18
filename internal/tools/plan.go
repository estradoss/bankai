package tools

import (
	"context"
	"encoding/json"
	"fmt"
)

// ExitPlanModeTool lets the model signal it has finished planning and present
// the plan for the user to approve before any edits are made. The plan text is
// echoed back; the REPL clears plan mode when it sees a successful call.
type ExitPlanModeTool struct{}

func (ExitPlanModeTool) Name() string { return "ExitPlanMode" }

func (ExitPlanModeTool) Description() string {
	return "Call this when you are in plan mode and have finished researching and drafting a plan for the work ahead. Pass the plan as markdown. This presents the plan to the user for approval; do NOT make any file edits before it is approved. Only use this for tasks that require writing code — not for read-only research questions."
}

func (ExitPlanModeTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"plan": {"type": "string", "description": "The implementation plan, in markdown"}
		},
		"required": ["plan"]
	}`)
}

func (ExitPlanModeTool) Call(ctx context.Context, input json.RawMessage) (Result, error) {
	var in struct {
		Plan string `json:"plan"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return Result{IsError: true, Output: fmt.Sprintf("bad input: %v", err)}, nil
	}
	if in.Plan == "" {
		return Result{IsError: true, Output: "plan is required"}, nil
	}
	return Result{Output: "Plan presented to user for approval:\n\n" + in.Plan}, nil
}
