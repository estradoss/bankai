// Package permission gates every agent tool call behind an ask/allow/deny
// decision. It is the Go port of vibelearn's permission subsystem
// (src/types/permissions.ts, src/services/permissions*): a set of permission
// modes, a rule list (explicit allow/deny by tool + optional content match),
// and an interactive asker for the interactive REPL.
package permission

import (
	"encoding/json"
	"strings"
	"sync"
)

// Mode is a permission mode. It sets the default behavior for tool categories
// before any explicit rule is consulted.
type Mode string

const (
	// ModeDefault asks before mutating/exec tools, auto-allows read-only ones.
	ModeDefault Mode = "default"
	// ModeAcceptEdits auto-allows file edits/writes but still asks for exec.
	ModeAcceptEdits Mode = "acceptEdits"
	// ModeBypass allows every tool without asking.
	ModeBypass Mode = "bypassPermissions"
	// ModeDontAsk is an alias of bypass: never prompt.
	ModeDontAsk Mode = "dontAsk"
	// ModePlan forbids edits/writes and task spawning; exec is still asked so
	// the model can inspect the repo read-only while planning.
	ModePlan Mode = "plan"
)

// Valid reports whether m is a recognized mode.
func (m Mode) Valid() bool {
	switch m {
	case ModeDefault, ModeAcceptEdits, ModeBypass, ModeDontAsk, ModePlan:
		return true
	}
	return false
}

// Behavior is the outcome of evaluating a tool call.
type Behavior string

const (
	Allow Behavior = "allow"
	Deny  Behavior = "deny"
	Ask   Behavior = "ask"
)

// category classifies a tool for mode-based defaults.
type category int

const (
	catReadOnly category = iota
	catEdit
	catExec
	catTask
)

// toolCategory maps a tool name to its category. Unknown tools default to
// catExec (treated as the most sensitive) so new tools fail closed.
func toolCategory(name string) category {
	switch name {
	case "Read", "Glob", "Grep", "WebFetch", "WebSearch",
		"get_goal", "create_goal", "update_goal", "TodoWrite", "ExitPlanMode":
		return catReadOnly
	case "Edit", "Write", "NotebookEdit":
		return catEdit
	case "Task":
		return catTask
	default:
		return catExec
	}
}

// Rule is an explicit allow/deny entry. Match is optional: when set, the rule
// only applies if the tool's serialized input contains Match (case-sensitive
// substring). ToolName "*" matches any tool.
type Rule struct {
	ToolName string
	Match    string
	Behavior Behavior // Allow or Deny
}

func (r Rule) matches(name string, input json.RawMessage) bool {
	if r.ToolName != "*" && r.ToolName != name {
		return false
	}
	if r.Match == "" {
		return true
	}
	return strings.Contains(string(input), r.Match)
}

// Request describes a tool call awaiting a decision, handed to an Asker.
type Request struct {
	Tool  string
	Input json.RawMessage
}

// Decision is what an Asker returns.
type Decision int

const (
	// DecideDeny rejects this one call.
	DecideDeny Decision = iota
	// DecideAllowOnce permits this one call.
	DecideAllowOnce
	// DecideAllowAlways permits this call and adds a session allow-rule for the
	// tool so future calls to it are not asked again.
	DecideAllowAlways
)

// Asker prompts the user for a decision on a Request. A nil Asker on a Gate
// makes every Ask resolve to Deny (non-interactive fail-closed).
type Asker func(Request) Decision

// Gate evaluates tool calls against the current mode and rule set.
type Gate struct {
	mu      sync.Mutex
	mode    Mode
	allow   []Rule
	deny    []Rule
	session []Rule // rules added at runtime via DecideAllowAlways
	Asker   Asker
}

// New returns a Gate in the given mode (defaulting to ModeDefault if invalid),
// seeded with the provided allow/deny rules.
func New(mode Mode, allow, deny []Rule) *Gate {
	if !mode.Valid() {
		mode = ModeDefault
	}
	return &Gate{mode: mode, allow: allow, deny: deny}
}

// Mode returns the current mode.
func (g *Gate) Mode() Mode {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.mode
}

// SetMode switches the mode.
func (g *Gate) SetMode(m Mode) {
	if !m.Valid() {
		return
	}
	g.mu.Lock()
	g.mode = m
	g.mu.Unlock()
}

// AddSessionRule appends a runtime rule (highest precedence within its class).
func (g *Gate) AddSessionRule(r Rule) {
	g.mu.Lock()
	g.session = append(g.session, r)
	g.mu.Unlock()
}

// Evaluate resolves a tool call to a Behavior WITHOUT prompting. Precedence:
// explicit deny rules > explicit allow rules (incl. session) > mode default.
func (g *Gate) Evaluate(name string, input json.RawMessage) Behavior {
	g.mu.Lock()
	mode := g.mode
	deny := g.deny
	allow := append(append([]Rule{}, g.allow...), g.session...)
	g.mu.Unlock()

	for _, r := range deny {
		if r.Behavior == Deny && r.matches(name, input) {
			return Deny
		}
	}
	for _, r := range allow {
		if r.Behavior == Allow && r.matches(name, input) {
			return Allow
		}
	}
	return modeDefault(mode, toolCategory(name))
}

// modeDefault is the behavior for a category in a mode, absent any rule.
func modeDefault(mode Mode, cat category) Behavior {
	switch mode {
	case ModeBypass, ModeDontAsk:
		return Allow
	case ModePlan:
		switch cat {
		case catReadOnly:
			return Allow
		case catExec:
			return Ask // inspection allowed after confirmation
		default: // edit, task
			return Deny
		}
	case ModeAcceptEdits:
		switch cat {
		case catReadOnly, catEdit:
			return Allow
		default:
			return Ask
		}
	default: // ModeDefault
		if cat == catReadOnly {
			return Allow
		}
		return Ask
	}
}

// Check resolves a tool call, prompting via the Asker when the behavior is Ask.
// It returns whether the call may proceed and, when blocked, a short reason
// suitable for feeding back to the model as a tool_result error.
func (g *Gate) Check(name string, input json.RawMessage) (ok bool, reason string) {
	switch g.Evaluate(name, input) {
	case Allow:
		return true, ""
	case Deny:
		return false, "Permission denied: the " + name + " tool is blocked in " +
			string(g.Mode()) + " mode. Do not retry; choose a different approach."
	default: // Ask
		if g.Asker == nil {
			return false, "Permission denied: " + name + " requires approval but no " +
				"interactive prompt is available (non-interactive session)."
		}
		switch g.Asker(Request{Tool: name, Input: input}) {
		case DecideAllowOnce:
			return true, ""
		case DecideAllowAlways:
			g.AddSessionRule(Rule{ToolName: name, Behavior: Allow})
			return true, ""
		default:
			return false, "Permission denied by user for the " + name +
				" tool. Do not retry this call."
		}
	}
}
