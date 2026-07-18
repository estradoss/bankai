package tools

import (
	"context"
	"encoding/json"
)

// Faithful ports of two vibelearn tools that are intentionally disabled in the
// open build. In the TS original both set isEnabled()=>false and their call()
// returns a fixed "unavailable" message (see TungstenTool.ts /
// VerifyPlanExecutionTool.ts). We keep them for parity and honest surfacing:
// if the model reaches for one, it gets the same message the TS build gives.

// TungstenTool — Anthropic-internal live monitor; unavailable in this build.
type TungstenTool struct{}

func (TungstenTool) Name() string { return "Tungsten" }
func (TungstenTool) Description() string {
	return "Tungsten is only available in Anthropic internal builds."
}
func (TungstenTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{}}`)
}
func (TungstenTool) Call(ctx context.Context, input json.RawMessage) (Result, error) {
	return Result{Output: "Tungsten is only available in Anthropic internal builds."}, nil
}

// VerifyPlanExecutionTool — plan-execution verifier; unavailable in this build.
type VerifyPlanExecutionTool struct{}

func (VerifyPlanExecutionTool) Name() string { return "VerifyPlanExecution" }
func (VerifyPlanExecutionTool) Description() string {
	return "Plan execution verification is unavailable in this reconstructed build."
}
func (VerifyPlanExecutionTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{}}`)
}
func (VerifyPlanExecutionTool) Call(ctx context.Context, input json.RawMessage) (Result, error) {
	return Result{Output: "Plan execution verification is unavailable in this reconstructed build."}, nil
}
