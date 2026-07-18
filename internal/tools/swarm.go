package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Multi-agent / swarm tools, Go ports of vibelearn's SendMessageTool,
// TeamCreateTool/TeamDeleteTool and RemoteTriggerTool.
//
// vibelearn targets the hosted teammate/coordinator infra. bankai runs inside
// the clawx workspace, so the inter-agent surface maps onto the `master` CLI
// (peer messaging + trigger rules) and onto local team files under
// ~/.claude/teams — the same on-disk shape the TS TeamCreate documents.

// --- SendMessage -----------------------------------------------------------

// SendMessageTool sends a message to a peer agent and returns its reply. Ported
// from SendMessageTool; delivery goes through clawx `master chat <id> <msg>`.
type SendMessageTool struct{}

func (SendMessageTool) Name() string { return "SendMessage" }
func (SendMessageTool) Description() string {
	return "Send a message to a peer agent by id and get its reply (synchronous). Use to delegate to or coordinate with another agent in this workspace. List ids with the peer roster / `master agent-ls`."
}
func (SendMessageTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"agent_id": {"type": "string", "description": "Target agent id"},
			"message": {"type": "string", "description": "Message to send"}
		},
		"required": ["agent_id", "message"]
	}`)
}
func (SendMessageTool) Call(ctx context.Context, input json.RawMessage) (Result, error) {
	var in struct {
		AgentID string `json:"agent_id"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return Result{IsError: true, Output: fmt.Sprintf("bad input: %v", err)}, nil
	}
	if in.AgentID == "" || in.Message == "" {
		return Result{IsError: true, Output: "agent_id and message are required"}, nil
	}
	out, err := runMaster(ctx, "chat", in.AgentID, in.Message)
	if err != nil {
		return Result{IsError: true, Output: err.Error()}, nil
	}
	return Result{Output: out}, nil
}

// --- RemoteTrigger ---------------------------------------------------------

// RemoteTriggerTool manages scheduled/remote agent triggers. Ported from
// RemoteTriggerTool; where the TS build calls the claude.ai CCR triggers API,
// bankai drives clawx trigger rules via `master`.
type RemoteTriggerTool struct{}

func (RemoteTriggerTool) Name() string { return "RemoteTrigger" }
func (RemoteTriggerTool) Description() string {
	return "Manage remote agent triggers. action=list lists trigger rules; action=create wires a source→destination trigger (needs src_agent, dst_agent, optional prompt); action=run fires a run by id (needs run_id)."
}
func (RemoteTriggerTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"action": {"type": "string", "enum": ["list", "create", "run"]},
			"src_agent": {"type": "string", "description": "Source agent id (create)"},
			"dst_agent": {"type": "string", "description": "Destination agent id (create)"},
			"prompt": {"type": "string", "description": "Optional prompt (create)"},
			"run_id": {"type": "string", "description": "Run id (run)"}
		},
		"required": ["action"]
	}`)
}
func (RemoteTriggerTool) Call(ctx context.Context, input json.RawMessage) (Result, error) {
	var in struct {
		Action   string `json:"action"`
		SrcAgent string `json:"src_agent"`
		DstAgent string `json:"dst_agent"`
		Prompt   string `json:"prompt"`
		RunID    string `json:"run_id"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return Result{IsError: true, Output: fmt.Sprintf("bad input: %v", err)}, nil
	}
	var args []string
	switch in.Action {
	case "list":
		args = []string{"triggers"}
	case "create":
		if in.SrcAgent == "" || in.DstAgent == "" {
			return Result{IsError: true, Output: "src_agent and dst_agent are required for create"}, nil
		}
		args = []string{"trigger-create", in.SrcAgent, in.DstAgent}
		if in.Prompt != "" {
			args = append(args, in.Prompt)
		}
	case "run":
		if in.RunID == "" {
			return Result{IsError: true, Output: "run_id is required for run"}, nil
		}
		args = []string{"run", in.RunID}
	default:
		return Result{IsError: true, Output: "action must be list, create, or run"}, nil
	}
	out, err := runMaster(ctx, args...)
	if err != nil {
		return Result{IsError: true, Output: err.Error()}, nil
	}
	return Result{Output: out}, nil
}

// --- Team{Create,Delete} ---------------------------------------------------

// teamConfig mirrors the on-disk shape TeamCreateTool documents.
type teamConfig struct {
	TeamName    string    `json:"team_name"`
	Description string    `json:"description"`
	Created     time.Time `json:"created"`
}

func teamsDir(home string) string { return filepath.Join(home, ".claude", "teams") }

// TeamCreateTool creates a team: a config at ~/.claude/teams/<name>/config.json
// plus a matching task-list dir at ~/.claude/tasks/<name>/ (Team = TaskList).
type TeamCreateTool struct{ HomeDir string }

func (TeamCreateTool) Name() string { return "TeamCreate" }
func (TeamCreateTool) Description() string {
	return "Create a team to coordinate multiple agents on a project. Creates a team config at ~/.claude/teams/<name>/config.json and a matching task list at ~/.claude/tasks/<name>/ (Team = TaskList)."
}
func (TeamCreateTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"team_name": {"type": "string", "description": "Team name (also the task-list name)"},
			"description": {"type": "string", "description": "What the team is working on"}
		},
		"required": ["team_name"]
	}`)
}
func (t TeamCreateTool) Call(ctx context.Context, input json.RawMessage) (Result, error) {
	var in struct {
		TeamName    string `json:"team_name"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return Result{IsError: true, Output: fmt.Sprintf("bad input: %v", err)}, nil
	}
	name := sanitizeTeamName(in.TeamName)
	if name == "" {
		return Result{IsError: true, Output: "team_name is required"}, nil
	}
	dir := filepath.Join(teamsDir(t.HomeDir), name)
	if _, err := os.Stat(filepath.Join(dir, "config.json")); err == nil {
		return Result{IsError: true, Output: "team already exists: " + name}, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Result{IsError: true, Output: err.Error()}, nil
	}
	if err := os.MkdirAll(filepath.Join(t.HomeDir, ".claude", "tasks", name), 0o755); err != nil {
		return Result{IsError: true, Output: err.Error()}, nil
	}
	cfg := teamConfig{TeamName: name, Description: in.Description, Created: time.Now()}
	data, _ := json.MarshalIndent(cfg, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, "config.json"), data, 0o644); err != nil {
		return Result{IsError: true, Output: err.Error()}, nil
	}
	return Result{Output: fmt.Sprintf("Created team %q (config %s, task list ~/.claude/tasks/%s/). Use the Task tools to add work, SendMessage/RemoteTrigger to coordinate agents.", name, filepath.Join(dir, "config.json"), name)}, nil
}

// TeamDeleteTool removes a team's config and task-list directories.
type TeamDeleteTool struct{ HomeDir string }

func (TeamDeleteTool) Name() string { return "TeamDelete" }
func (TeamDeleteTool) Description() string {
	return "Delete a team created by TeamCreate: removes ~/.claude/teams/<name>/ and its ~/.claude/tasks/<name>/ task list."
}
func (TeamDeleteTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {"team_name": {"type": "string", "description": "Team to delete"}},
		"required": ["team_name"]
	}`)
}
func (t TeamDeleteTool) Call(ctx context.Context, input json.RawMessage) (Result, error) {
	var in struct {
		TeamName string `json:"team_name"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return Result{IsError: true, Output: fmt.Sprintf("bad input: %v", err)}, nil
	}
	name := sanitizeTeamName(in.TeamName)
	if name == "" {
		return Result{IsError: true, Output: "team_name is required"}, nil
	}
	dir := filepath.Join(teamsDir(t.HomeDir), name)
	if _, err := os.Stat(dir); err != nil {
		return Result{IsError: true, Output: "unknown team: " + name}, nil
	}
	os.RemoveAll(dir)
	os.RemoveAll(filepath.Join(t.HomeDir, ".claude", "tasks", name))
	return Result{Output: "Deleted team " + name}, nil
}

// --- helpers ---------------------------------------------------------------

// runMaster invokes the clawx `master` CLI. Absent (not in a clawx workspace),
// it reports so rather than failing opaquely.
func runMaster(ctx context.Context, args ...string) (string, error) {
	if _, err := exec.LookPath("master"); err != nil {
		return "", fmt.Errorf("the `master` CLI is not available (not running inside a clawx workspace)")
	}
	cctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	out, err := exec.CommandContext(cctx, "master", args...).CombinedOutput()
	s := strings.TrimSpace(string(out))
	if err != nil {
		if s != "" {
			return "", fmt.Errorf("master %s: %v: %s", strings.Join(args, " "), err, s)
		}
		return "", fmt.Errorf("master %s: %v", strings.Join(args, " "), err)
	}
	if s == "" {
		s = "(no output)"
	}
	return s, nil
}

func sanitizeTeamName(name string) string {
	name = strings.TrimSpace(name)
	name = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		case r == ' ':
			return '-'
		default:
			return -1
		}
	}, name)
	return name
}
