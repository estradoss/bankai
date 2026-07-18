package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"time"
)

type BashTool struct{}

func (BashTool) Name() string { return "Bash" }

func (BashTool) Description() string {
	return "Execute a shell command via /bin/sh. Returns combined stdout+stderr. Use for git, ls, grep, find, curl, build/test commands. Do NOT use for reading files (use Read) or editing files (use Edit)."
}

func (BashTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"command": {"type": "string", "description": "Shell command to run"},
			"timeout_ms": {"type": "integer", "description": "Timeout in ms (default 120000, max 600000)"}
		},
		"required": ["command"]
	}`)
}

func (BashTool) Call(ctx context.Context, input json.RawMessage) (Result, error) {
	var in struct {
		Command   string `json:"command"`
		TimeoutMS int    `json:"timeout_ms"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return Result{IsError: true, Output: fmt.Sprintf("bad input: %v", err)}, nil
	}
	if in.Command == "" {
		return Result{IsError: true, Output: "command is required"}, nil
	}
	timeout := time.Duration(in.TimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	if timeout > 10*time.Minute {
		timeout = 10 * time.Minute
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, "/bin/sh", "-c", in.Command)
	out, err := cmd.CombinedOutput()
	res := Result{Output: string(out)}
	if cctx.Err() == context.DeadlineExceeded {
		res.IsError = true
		res.Output += "\n[timed out]"
		return res, nil
	}
	if err != nil {
		res.IsError = true
		res.Output = fmt.Sprintf("exit error: %v\n%s", err, string(out))
	}
	return res, nil
}
