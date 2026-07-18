package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/estradoss/bankai/internal/bridge"
)

// IDESelectionTool reads the IDE's current editor selection (ported from
// vibelearn's getCurrentSelection bridge action).
type IDESelectionTool struct{ Bridge *bridge.Bridge }

func (IDESelectionTool) Name() string { return "ide_selection" }
func (IDESelectionTool) Description() string {
	return "Get the user's current selection in their IDE (file, selected text, line range). Empty if nothing is selected or no IDE is connected."
}
func (IDESelectionTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{}}`)
}
func (t IDESelectionTool) Call(ctx context.Context, input json.RawMessage) (Result, error) {
	if t.Bridge == nil {
		return Result{IsError: true, Output: "no IDE bridge configured"}, nil
	}
	s := t.Bridge.Selection()
	if s.File == "" {
		return Result{Output: "no active IDE selection"}, nil
	}
	return Result{Output: fmt.Sprintf("%s:%d-%d\n%s", s.File, s.StartLine, s.EndLine, s.Text)}, nil
}

// IDEOpenTool asks the connected IDE to open a file (ported from the openFile
// bridge action). The IDE picks the command up by polling.
type IDEOpenTool struct{ Bridge *bridge.Bridge }

func (IDEOpenTool) Name() string { return "ide_open" }
func (IDEOpenTool) Description() string {
	return "Ask the connected IDE to open a file in the editor. No-op if no IDE is connected (the command is queued)."
}
func (IDEOpenTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type":"object",
		"properties":{"file":{"type":"string","description":"Absolute path to open"}},
		"required":["file"]
	}`)
}
func (t IDEOpenTool) Call(ctx context.Context, input json.RawMessage) (Result, error) {
	if t.Bridge == nil {
		return Result{IsError: true, Output: "no IDE bridge configured"}, nil
	}
	var in struct {
		File string `json:"file"`
	}
	if err := json.Unmarshal(input, &in); err != nil || in.File == "" {
		return Result{IsError: true, Output: "file is required"}, nil
	}
	t.Bridge.Enqueue(bridge.Command{Kind: "openFile", File: in.File})
	return Result{Output: "requested IDE open " + in.File}, nil
}

// IDEDiffTool asks the connected IDE to show a diff view (ported from the
// showDiff / openDiff bridge action).
type IDEDiffTool struct{ Bridge *bridge.Bridge }

func (IDEDiffTool) Name() string { return "ide_diff" }
func (IDEDiffTool) Description() string {
	return "Ask the connected IDE to show a diff for a file (old vs new text). Queued for the IDE to display."
}
func (IDEDiffTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type":"object",
		"properties":{
			"file":{"type":"string"},
			"old":{"type":"string"},
			"new":{"type":"string"}
		},
		"required":["file","new"]
	}`)
}
func (t IDEDiffTool) Call(ctx context.Context, input json.RawMessage) (Result, error) {
	if t.Bridge == nil {
		return Result{IsError: true, Output: "no IDE bridge configured"}, nil
	}
	var in struct {
		File string `json:"file"`
		Old  string `json:"old"`
		New  string `json:"new"`
	}
	if err := json.Unmarshal(input, &in); err != nil || in.File == "" {
		return Result{IsError: true, Output: "file is required"}, nil
	}
	t.Bridge.Enqueue(bridge.Command{Kind: "showDiff", File: in.File, Old: in.Old, New: in.New})
	return Result{Output: "requested IDE diff for " + in.File}, nil
}
