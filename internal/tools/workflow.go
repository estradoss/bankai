package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

// WorkflowTool is a pragmatic Go port of vibelearn's Workflow orchestration.
// The TS original runs a JavaScript script that fans agents out with
// pipeline()/parallel() primitives — a large runtime bankai does not embed.
// This port keeps the core capability: run an ordered list of steps, each a
// sub-agent prompt, either sequentially (each step sees the previous step's
// output) or in parallel (all run concurrently, independent). It returns the
// per-step outputs. Backed by the same SubagentRunner as the Task tools.
type WorkflowTool struct{ Run SubagentFunc }

func (WorkflowTool) Name() string { return "Workflow" }

func (WorkflowTool) Description() string {
	return "Orchestrate a multi-step workflow across sub-agents. Provide `steps` (each a standalone prompt). mode=sequential (default) runs steps in order and feeds each step the prior step's output; mode=parallel runs all steps concurrently and independently. Returns every step's output. Use for decompose-and-cover or multi-phase work."
}

func (WorkflowTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"steps": {
				"type": "array",
				"items": {
					"type": "object",
					"properties": {
						"label": {"type": "string", "description": "Short label for the step"},
						"prompt": {"type": "string", "description": "Standalone task for the sub-agent"}
					},
					"required": ["prompt"]
				},
				"description": "Ordered workflow steps"
			},
			"mode": {"type": "string", "enum": ["sequential", "parallel"], "description": "sequential (default) threads output between steps; parallel runs all at once"}
		},
		"required": ["steps"]
	}`)
}

func (t WorkflowTool) Call(ctx context.Context, input json.RawMessage) (Result, error) {
	if t.Run == nil {
		return Result{IsError: true, Output: "workflow runner not configured"}, nil
	}
	var in struct {
		Steps []struct {
			Label  string `json:"label"`
			Prompt string `json:"prompt"`
		} `json:"steps"`
		Mode string `json:"mode"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return Result{IsError: true, Output: fmt.Sprintf("bad input: %v", err)}, nil
	}
	if len(in.Steps) == 0 {
		return Result{IsError: true, Output: "steps is required and must be non-empty"}, nil
	}
	label := func(i int) string {
		if in.Steps[i].Label != "" {
			return in.Steps[i].Label
		}
		return fmt.Sprintf("step %d", i+1)
	}

	outputs := make([]string, len(in.Steps))
	if in.Mode == "parallel" {
		var wg sync.WaitGroup
		for i := range in.Steps {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				out, err := t.Run(ctx, in.Steps[i].Prompt)
				if err != nil {
					outputs[i] = "ERROR: " + err.Error()
					return
				}
				outputs[i] = out
			}(i)
		}
		wg.Wait()
	} else {
		var prev string
		for i := range in.Steps {
			prompt := in.Steps[i].Prompt
			if prev != "" {
				prompt = "Previous step output:\n" + prev + "\n\n---\n\n" + prompt
			}
			out, err := t.Run(ctx, prompt)
			if err != nil {
				outputs[i] = "ERROR: " + err.Error()
				break // sequential: abort the chain on failure
			}
			outputs[i] = out
			prev = out
		}
	}

	var b strings.Builder
	for i := range in.Steps {
		if outputs[i] == "" {
			continue
		}
		fmt.Fprintf(&b, "## %s\n%s\n\n", label(i), outputs[i])
	}
	return Result{Output: strings.TrimSpace(b.String())}, nil
}
