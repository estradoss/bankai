package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/estradoss/bankai/internal/cron"
)

// Cron{Create,List,Delete} tools are the Go port of vibelearn's ScheduleCronTool
// family. They schedule a prompt to be enqueued on a 5-field cron schedule,
// recurring or one-shot, optionally durable (persisted to
// .claude/scheduled_tasks.json so it survives restarts). Backed by internal/cron.

type CronCreateTool struct{ Store *cron.Store }

func (CronCreateTool) Name() string { return "CronCreate" }
func (CronCreateTool) Description() string {
	return "Schedule a prompt to run on a standard 5-field cron schedule (M H DoM Mon DoW, local time). `recurring` true (default) fires on every match until deleted or auto-expired after 30 days; false fires once then auto-deletes. `durable` true persists to .claude/scheduled_tasks.json and survives restarts; false (default) is session-only."
}
func (CronCreateTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"cron": {"type": "string", "description": "5-field cron expression: M H DoM Mon DoW (e.g. \"*/5 * * * *\")"},
			"prompt": {"type": "string", "description": "Prompt to enqueue at each fire time"},
			"recurring": {"type": "boolean", "description": "Fire on every match (default true) or once (false)"},
			"durable": {"type": "boolean", "description": "Persist across restarts (default false)"}
		},
		"required": ["cron", "prompt"]
	}`)
}
func (t CronCreateTool) Call(ctx context.Context, input json.RawMessage) (Result, error) {
	if t.Store == nil {
		return Result{IsError: true, Output: "cron scheduler not configured"}, nil
	}
	var in struct {
		Cron      string `json:"cron"`
		Prompt    string `json:"prompt"`
		Recurring *bool  `json:"recurring"`
		Durable   bool   `json:"durable"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return Result{IsError: true, Output: fmt.Sprintf("bad input: %v", err)}, nil
	}
	if in.Cron == "" || in.Prompt == "" {
		return Result{IsError: true, Output: "cron and prompt are required"}, nil
	}
	recurring := true
	if in.Recurring != nil {
		recurring = *in.Recurring
	}
	task, err := t.Store.Add(in.Cron, in.Prompt, recurring, in.Durable)
	if err != nil {
		return Result{IsError: true, Output: err.Error()}, nil
	}
	t.Store.Start()
	where := "Session-only (dies when bankai exits)"
	if task.Durable {
		where = "Persisted to .claude/scheduled_tasks.json"
	}
	kind := "recurring job"
	tail := "Auto-expires after 30 days. Use CronDelete to cancel sooner."
	if !recurring {
		kind = "one-shot task"
		tail = "It will fire once then auto-delete."
	}
	return Result{Output: fmt.Sprintf("Scheduled %s %s (%s), next run %s. %s. %s", kind, task.ID, task.Cron, task.NextRun.Format("2006-01-02 15:04"), where, tail)}, nil
}

type CronListTool struct{ Store *cron.Store }

func (CronListTool) Name() string { return "CronList" }
func (CronListTool) Description() string {
	return "List all scheduled cron tasks with their schedules and next run times."
}
func (CronListTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{}}`)
}
func (t CronListTool) Call(ctx context.Context, input json.RawMessage) (Result, error) {
	if t.Store == nil {
		return Result{IsError: true, Output: "cron scheduler not configured"}, nil
	}
	tasks := t.Store.List()
	if len(tasks) == 0 {
		return Result{Output: "no scheduled tasks"}, nil
	}
	var b strings.Builder
	for i, task := range tasks {
		if i > 0 {
			b.WriteByte('\n')
		}
		kind := "recurring"
		if !task.Recurring {
			kind = "one-shot"
		}
		dur := "session"
		if task.Durable {
			dur = "durable"
		}
		fmt.Fprintf(&b, "%s [%s,%s] %q next=%s :: %s", task.ID, kind, dur, task.Cron, task.NextRun.Format("2006-01-02 15:04"), task.Prompt)
	}
	return Result{Output: b.String()}, nil
}

type CronDeleteTool struct{ Store *cron.Store }

func (CronDeleteTool) Name() string { return "CronDelete" }
func (CronDeleteTool) Description() string {
	return "Cancel a scheduled cron task by its id (from CronCreate/CronList)."
}
func (CronDeleteTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {"id": {"type": "string", "description": "Task id to cancel"}},
		"required": ["id"]
	}`)
}
func (t CronDeleteTool) Call(ctx context.Context, input json.RawMessage) (Result, error) {
	if t.Store == nil {
		return Result{IsError: true, Output: "cron scheduler not configured"}, nil
	}
	var in struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return Result{IsError: true, Output: fmt.Sprintf("bad input: %v", err)}, nil
	}
	if in.ID == "" {
		return Result{IsError: true, Output: "id is required"}, nil
	}
	if !t.Store.Delete(in.ID) {
		return Result{IsError: true, Output: "unknown task id: " + in.ID}, nil
	}
	return Result{Output: "Cancelled " + in.ID}, nil
}
