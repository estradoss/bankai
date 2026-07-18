package engine

import (
	"context"
	"encoding/json"
	"os/exec"
	"regexp"
	"strings"
)

// Hook is a command run on a tool-call lifecycle event. Ported from vibelearn's
// hook system (and Claude Code's settings.json hooks). The command receives a
// JSON payload on stdin describing the tool call.
type Hook struct {
	Event    string // e.g. "PostToolUse"
	Matcher  string // regexp matched against the tool name ("" or "*" = any)
	Command  string // shell command to run
	re       *regexp.Regexp
	compiled bool
}

func (h *Hook) matches(toolName string) bool {
	if h.Matcher == "" || h.Matcher == "*" {
		return true
	}
	if !h.compiled {
		h.re, _ = regexp.Compile(h.Matcher)
		h.compiled = true
	}
	if h.re == nil {
		return strings.Contains(toolName, h.Matcher)
	}
	return h.re.MatchString(toolName)
}

// runHooks fires every hook registered for the event whose matcher matches the
// tool name. Hooks run best-effort via `sh -c`; failures are ignored so a bad
// hook never breaks the tool loop. The payload mirrors Claude Code's hook JSON.
func (e *Engine) runHooks(ctx context.Context, event, toolName string, input json.RawMessage, output string) {
	if len(e.Hooks) == 0 {
		return
	}
	payload, _ := json.Marshal(map[string]any{
		"hook_event_name": event,
		"tool_name":       toolName,
		"tool_input":      json.RawMessage(input),
		"tool_output":     output,
	})
	for i := range e.Hooks {
		h := &e.Hooks[i]
		if h.Event != event || h.Command == "" || !h.matches(toolName) {
			continue
		}
		cmd := exec.CommandContext(ctx, "sh", "-c", h.Command)
		cmd.Stdin = strings.NewReader(string(payload))
		_ = cmd.Run()
	}
}
