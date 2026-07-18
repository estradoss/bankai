package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// StructuredOutputTool is the Go port of vibelearn's SyntheticOutputTool
// (SYNTHETIC_OUTPUT_TOOL_NAME = "StructuredOutput"). In non-interactive / SDK
// use the model is required to return its final answer by calling this tool
// exactly once, validated against a caller-supplied JSON schema. bankai uses it
// the same way: a runner sets Schema to the desired shape, the model calls
// StructuredOutput, and Result carries the (validated) JSON back.
//
// The TS build validates with ajv. Go has no ajv; we do a pragmatic check of
// the two things schemas actually constrain in practice — declared "required"
// keys must be present, and top-level "type":"object" must get an object. This
// mirrors the SyntheticOutput contract without pulling in a schema library.
type StructuredOutputTool struct {
	// Schema is the JSON Schema the output must satisfy. When nil, any object
	// is accepted (matching the un-parameterized SyntheticOutputTool).
	Schema json.RawMessage
}

func (StructuredOutputTool) Name() string { return "StructuredOutput" }

func (StructuredOutputTool) Description() string {
	return "Return your final response in the requested structured format. You MUST call this tool exactly once at the end of your response to provide the structured output."
}

func (t StructuredOutputTool) InputSchema() json.RawMessage {
	if len(t.Schema) > 0 {
		return t.Schema
	}
	return json.RawMessage(`{"type":"object"}`)
}

func (t StructuredOutputTool) Call(ctx context.Context, input json.RawMessage) (Result, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(input, &obj); err != nil {
		return Result{IsError: true, Output: fmt.Sprintf("Output does not match required schema: expected a JSON object (%v)", err)}, nil
	}
	if missing := t.missingRequired(obj); len(missing) > 0 {
		return Result{IsError: true, Output: "Output does not match required schema: missing required field(s): " + strings.Join(missing, ", ")}, nil
	}
	// Echo the validated structured output straight back — callers read it off
	// the Result. Compact so the transcript stays clean.
	return Result{Output: string(input)}, nil
}

func (t StructuredOutputTool) missingRequired(obj map[string]json.RawMessage) []string {
	if len(t.Schema) == 0 {
		return nil
	}
	var s struct {
		Required []string `json:"required"`
	}
	if err := json.Unmarshal(t.Schema, &s); err != nil {
		return nil
	}
	var missing []string
	for _, k := range s.Required {
		if _, ok := obj[k]; !ok {
			missing = append(missing, k)
		}
	}
	return missing
}
