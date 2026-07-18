package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/estradoss/bankai/internal/permission"
)

// EnterPlanModeTool is the Go port of vibelearn's EnterPlanModeTool. It flips
// the session into plan mode — read-only research, no edits — the counterpart
// to ExitPlanMode. In TS it mutates appState.toolPermissionContext; here it sets
// the permission gate to ModePlan so edits/writes are hard-denied until the
// model calls ExitPlanMode and the user approves.
type EnterPlanModeTool struct{ Perms *permission.Gate }

func (EnterPlanModeTool) Name() string { return "EnterPlanMode" }

func (EnterPlanModeTool) Description() string {
	return "Requests to enter plan mode for complex tasks requiring exploration and design before writing code. In plan mode you research with read-only tools and do NOT edit files; when ready, call ExitPlanMode to present the plan for approval."
}

func (EnterPlanModeTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{}}`)
}

func (t EnterPlanModeTool) Call(ctx context.Context, input json.RawMessage) (Result, error) {
	if t.Perms != nil {
		t.Perms.SetMode(permission.ModePlan)
	}
	return Result{Output: "Entered plan mode. Focus on exploring the codebase and designing an implementation approach.\n\nIn plan mode you should:\n1. Explore the codebase to understand existing patterns\n2. Identify similar features and architectural approaches\n3. Consider multiple approaches and their trade-offs\n4. Design a concrete implementation strategy\n5. When ready, call ExitPlanMode to present your plan for approval\n\nDo NOT write or edit any files yet — this is a read-only exploration and planning phase."}, nil
}

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
