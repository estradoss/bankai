package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// SendUserMessageTool is the Go port of vibelearn's BriefTool
// (BRIEF_TOOL_NAME = "SendUserMessage", legacy alias "Brief"). In the hosted TS
// build, plain assistant text lands in a collapsed detail view most users never
// open, so the real reply must go through this tool. bankai's REPL prints
// assistant text directly, so here the tool is a thin, honest surface: it emits
// the message to the user (via the injected Emit sink, else stdout) and labels
// intent via `status`. Attachments are noted by path; the caller/editor renders
// them.
type SendUserMessageTool struct {
	// Emit, when set, delivers the message to the user-facing surface (TUI/REPL
	// or remote SSE). When nil the tool writes to stdout.
	Emit func(status, message string)
}

func (SendUserMessageTool) Name() string { return "SendUserMessage" }

func (SendUserMessageTool) Description() string {
	return "Send a message the user will read. `message` supports markdown. `attachments` takes file paths (absolute or cwd-relative) for images, diffs, logs. `status` labels intent: 'normal' when replying to what they just asked; 'proactive' when you're initiating (a background task finished, a blocker surfaced, you need input on something unasked). Set it honestly."
}

func (SendUserMessageTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"message": {"type": "string", "description": "The message to the user, in markdown"},
			"attachments": {"type": "array", "items": {"type": "string"}, "description": "File paths to attach (images, diffs, logs)"},
			"status": {"type": "string", "enum": ["normal", "proactive"], "description": "Intent label for downstream routing"}
		},
		"required": ["message"]
	}`)
}

func (t SendUserMessageTool) Call(ctx context.Context, input json.RawMessage) (Result, error) {
	var in struct {
		Message     string   `json:"message"`
		Attachments []string `json:"attachments"`
		Status      string   `json:"status"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return Result{IsError: true, Output: fmt.Sprintf("bad input: %v", err)}, nil
	}
	if in.Message == "" {
		return Result{IsError: true, Output: "message is required"}, nil
	}
	if in.Status == "" {
		in.Status = "normal"
	}
	if t.Emit != nil {
		t.Emit(in.Status, in.Message)
	} else {
		fmt.Fprintln(os.Stdout, in.Message)
	}
	// Note missing attachments so the model can correct paths.
	var notes []string
	for _, a := range in.Attachments {
		if _, err := os.Stat(a); err != nil {
			notes = append(notes, "missing attachment: "+a)
		}
	}
	out := "Message sent."
	if len(notes) > 0 {
		out += " (" + strings.Join(notes, "; ") + ")"
	}
	return Result{Output: out}, nil
}
