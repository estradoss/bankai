package tools

import (
	"context"
	"encoding/json"
	"fmt"
)

// SubagentFunc runs an autonomous sub-agent with the given task prompt and
// returns its final text output. Injected by main to avoid an import cycle
// between tools and engine.
type SubagentFunc func(ctx context.Context, prompt string) (string, error)

type AgentTool struct {
	Run SubagentFunc
}

func (AgentTool) Name() string { return "Task" }

func (AgentTool) Description() string {
	return "Launch an autonomous sub-agent to handle a multi-step task on its own. The sub-agent has the same file/search/exec tools and runs its own tool-calling loop, then returns a final report. Use for open-ended searches or self-contained subtasks so the main context stays focused. The sub-agent cannot ask you questions — give it a complete, standalone prompt. It runs synchronously and returns only its final message."
}

func (AgentTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"description": {"type": "string", "description": "Short 3-5 word task description"},
			"prompt": {"type": "string", "description": "Complete standalone task for the sub-agent"}
		},
		"required": ["prompt"]
	}`)
}

func (a AgentTool) Call(ctx context.Context, input json.RawMessage) (Result, error) {
	var in struct {
		Description string `json:"description"`
		Prompt      string `json:"prompt"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return Result{IsError: true, Output: fmt.Sprintf("bad input: %v", err)}, nil
	}
	if in.Prompt == "" {
		return Result{IsError: true, Output: "prompt is required"}, nil
	}
	if a.Run == nil {
		return Result{IsError: true, Output: "subagent runner not configured"}, nil
	}
	out, err := a.Run(ctx, in.Prompt)
	if err != nil {
		return Result{IsError: true, Output: "subagent error: " + err.Error()}, nil
	}
	if out == "" {
		out = "(sub-agent produced no output)"
	}
	return Result{Output: out}, nil
}
