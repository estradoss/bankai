package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/estradoss/bankai/internal/task"
)

// The async Task* tools expose bankai's background task registry to the model:
// launch a sub-agent without blocking (TaskCreate), then poll (TaskGet/TaskList/
// TaskOutput) or cancel (TaskStop). This is the async counterpart to the
// synchronous Task tool in agent.go.

func fmtSnapshot(s task.Snapshot) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s [%s] %s", s.ID, s.Status, s.Description)
	if !s.Started.IsZero() {
		dur := time.Since(s.Started)
		if !s.Ended.IsZero() {
			dur = s.Ended.Sub(s.Started)
		}
		fmt.Fprintf(&b, " (%s)", dur.Round(time.Millisecond))
	}
	if s.Error != "" {
		fmt.Fprintf(&b, "\n  error: %s", s.Error)
	}
	return b.String()
}

// TaskCreateTool launches a background sub-agent task.
type TaskCreateTool struct{ Reg *task.Registry }

func (TaskCreateTool) Name() string { return "TaskCreate" }
func (TaskCreateTool) Description() string {
	return "Launch a sub-agent task in the BACKGROUND and return immediately with a task id. The task runs asynchronously; poll it with TaskGet/TaskOutput or cancel with TaskStop. Use when you want to start long-running work and keep going. Give a complete, standalone prompt — the sub-agent cannot ask questions."
}
func (TaskCreateTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"description": {"type": "string", "description": "Short 3-5 word task description"},
			"prompt": {"type": "string", "description": "Complete standalone task for the sub-agent"}
		},
		"required": ["prompt"]
	}`)
}
func (t TaskCreateTool) Call(ctx context.Context, input json.RawMessage) (Result, error) {
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
	if t.Reg == nil {
		return Result{IsError: true, Output: "task registry not configured"}, nil
	}
	if in.Description == "" {
		in.Description = "background task"
	}
	s, err := t.Reg.Create(in.Description, in.Prompt)
	if err != nil {
		return Result{IsError: true, Output: err.Error()}, nil
	}
	return Result{Output: fmt.Sprintf("Started %s. Poll with TaskGet/TaskOutput id=%s.", s.ID, s.ID)}, nil
}

// TaskGetTool reports a task's status.
type TaskGetTool struct{ Reg *task.Registry }

func (TaskGetTool) Name() string { return "TaskGet" }
func (TaskGetTool) Description() string {
	return "Get the current status of a background task by id (running/completed/failed/stopped). Does not include full output; use TaskOutput for that."
}
func (TaskGetTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {"id": {"type": "string", "description": "Task id from TaskCreate"}},
		"required": ["id"]
	}`)
}
func (t TaskGetTool) Call(ctx context.Context, input json.RawMessage) (Result, error) {
	id, errRes := taskID(input, t.Reg)
	if errRes != nil {
		return *errRes, nil
	}
	s, _ := t.Reg.Get(id)
	return Result{Output: fmtSnapshot(s)}, nil
}

// TaskListTool lists all tasks.
type TaskListTool struct{ Reg *task.Registry }

func (TaskListTool) Name() string { return "TaskList" }
func (TaskListTool) Description() string {
	return "List all background tasks this session with their statuses."
}
func (TaskListTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type": "object", "properties": {}}`)
}
func (t TaskListTool) Call(ctx context.Context, input json.RawMessage) (Result, error) {
	if t.Reg == nil {
		return Result{IsError: true, Output: "task registry not configured"}, nil
	}
	all := t.Reg.List()
	if len(all) == 0 {
		return Result{Output: "no background tasks"}, nil
	}
	var b strings.Builder
	for i, s := range all {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(fmtSnapshot(s))
	}
	return Result{Output: b.String()}, nil
}

// TaskOutputTool returns a task's final output.
type TaskOutputTool struct{ Reg *task.Registry }

func (TaskOutputTool) Name() string { return "TaskOutput" }
func (TaskOutputTool) Description() string {
	return "Get the output of a background task by id. If the task is still running, reports that instead of blocking."
}
func (TaskOutputTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {"id": {"type": "string", "description": "Task id from TaskCreate"}},
		"required": ["id"]
	}`)
}
func (t TaskOutputTool) Call(ctx context.Context, input json.RawMessage) (Result, error) {
	id, errRes := taskID(input, t.Reg)
	if errRes != nil {
		return *errRes, nil
	}
	s, _ := t.Reg.Get(id)
	switch s.Status {
	case task.StatusRunning:
		return Result{Output: fmt.Sprintf("%s is still running; no output yet.", id)}, nil
	case task.StatusFailed:
		return Result{Output: fmt.Sprintf("%s failed: %s", id, s.Error)}, nil
	case task.StatusStopped:
		return Result{Output: fmt.Sprintf("%s was stopped before finishing.", id)}, nil
	default:
		out := s.Output
		if out == "" {
			out = "(no output)"
		}
		return Result{Output: out}, nil
	}
}

// TaskStopTool cancels a running task.
type TaskStopTool struct{ Reg *task.Registry }

func (TaskStopTool) Name() string { return "TaskStop" }
func (TaskStopTool) Description() string {
	return "Cancel a running background task by id. Finished tasks are unaffected."
}
func (TaskStopTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {"id": {"type": "string", "description": "Task id from TaskCreate"}},
		"required": ["id"]
	}`)
}
func (t TaskStopTool) Call(ctx context.Context, input json.RawMessage) (Result, error) {
	id, errRes := taskID(input, t.Reg)
	if errRes != nil {
		return *errRes, nil
	}
	t.Reg.Stop(id)
	return Result{Output: "Stopped " + id}, nil
}

// taskID parses {"id": ...}, validating the registry and existence. On error it
// returns a non-nil *Result to hand straight back to the model.
func taskID(input json.RawMessage, reg *task.Registry) (string, *Result) {
	if reg == nil {
		return "", &Result{IsError: true, Output: "task registry not configured"}
	}
	var in struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return "", &Result{IsError: true, Output: fmt.Sprintf("bad input: %v", err)}
	}
	if in.ID == "" {
		return "", &Result{IsError: true, Output: "id is required"}
	}
	if _, ok := reg.Get(in.ID); !ok {
		return "", &Result{IsError: true, Output: "unknown task id: " + in.ID}
	}
	return in.ID, nil
}
