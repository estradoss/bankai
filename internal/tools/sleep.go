package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// SleepTool pauses execution for a bounded duration. Ported from vibelearn's
// SleepTool — useful for polling loops and rate-limit backoff.
type SleepTool struct{}

func (SleepTool) Name() string { return "Sleep" }

func (SleepTool) Description() string {
	return "Pause for a number of seconds before continuing. Max 300s. Use to wait between polls of long-running work."
}

func (SleepTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"seconds": {"type": "number", "description": "Seconds to sleep (0-300)"}
		},
		"required": ["seconds"]
	}`)
}

func (SleepTool) Call(ctx context.Context, input json.RawMessage) (Result, error) {
	var in struct {
		Seconds float64 `json:"seconds"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return Result{IsError: true, Output: fmt.Sprintf("bad input: %v", err)}, nil
	}
	if in.Seconds < 0 {
		in.Seconds = 0
	}
	if in.Seconds > 300 {
		in.Seconds = 300
	}
	d := time.Duration(in.Seconds * float64(time.Second))
	select {
	case <-time.After(d):
		return Result{Output: fmt.Sprintf("slept %.3gs", in.Seconds)}, nil
	case <-ctx.Done():
		return Result{IsError: true, Output: "sleep interrupted: " + ctx.Err().Error()}, nil
	}
}
