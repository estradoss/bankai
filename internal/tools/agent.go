package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// SubagentFunc runs an autonomous sub-agent with the given task prompt and
// returns its final text output. Injected by main to avoid an import cycle
// between tools and engine.
type SubagentFunc func(ctx context.Context, prompt string) (string, error)

// SubagentTypedFunc runs a sub-agent with an extra system-prompt fragment
// (an agent type's persona), appended to the base system prompt.
type SubagentTypedFunc func(ctx context.Context, systemExtra, prompt string) (string, error)

// AgentDef is a named sub-agent type (e.g. contributed by a plugin).
type AgentDef struct {
	Name        string
	Description string
	Prompt      string // extra system prompt defining the agent's persona
}

type AgentTool struct {
	Run      SubagentFunc
	RunTyped SubagentTypedFunc
	Agents   map[string]AgentDef // available agent types by name
}

func (AgentTool) Name() string { return "Task" }

func (a AgentTool) Description() string {
	base := "Launch an autonomous sub-agent to handle a multi-step task on its own. The sub-agent has the same file/search/exec tools and runs its own tool-calling loop, then returns a final report. Use for open-ended searches or self-contained subtasks so the main context stays focused. The sub-agent cannot ask you questions — give it a complete, standalone prompt. It runs synchronously and returns only its final message."
	if len(a.Agents) == 0 {
		return base
	}
	var b strings.Builder
	b.WriteString(base)
	b.WriteString("\n\nAvailable agent types (pass as subagent_type):")
	for _, name := range sortedAgentNames(a.Agents) {
		fmt.Fprintf(&b, "\n- %s: %s", name, a.Agents[name].Description)
	}
	return b.String()
}

func (a AgentTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"description": {"type": "string", "description": "Short 3-5 word task description"},
			"prompt": {"type": "string", "description": "Complete standalone task for the sub-agent"},
			"subagent_type": {"type": "string", "description": "Optional named agent type to use"}
		},
		"required": ["prompt"]
	}`)
}

func (a AgentTool) Call(ctx context.Context, input json.RawMessage) (Result, error) {
	var in struct {
		Description  string `json:"description"`
		Prompt       string `json:"prompt"`
		SubagentType string `json:"subagent_type"`
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
	var out string
	var err error
	if in.SubagentType != "" {
		def, ok := a.Agents[in.SubagentType]
		if !ok {
			return Result{IsError: true, Output: fmt.Sprintf("unknown subagent_type %q; available: %s",
				in.SubagentType, strings.Join(sortedAgentNames(a.Agents), ", "))}, nil
		}
		if a.RunTyped == nil {
			return Result{IsError: true, Output: "typed subagent runner not configured"}, nil
		}
		out, err = a.RunTyped(ctx, def.Prompt, in.Prompt)
	} else {
		out, err = a.Run(ctx, in.Prompt)
	}
	if err != nil {
		return Result{IsError: true, Output: "subagent error: " + err.Error()}, nil
	}
	if out == "" {
		out = "(sub-agent produced no output)"
	}
	return Result{Output: out}, nil
}

func sortedAgentNames(m map[string]AgentDef) []string {
	names := make([]string, 0, len(m))
	for n := range m {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
