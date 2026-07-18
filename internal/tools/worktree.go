package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// WorktreeState is shared between EnterWorktreeTool and ExitWorktreeTool. It
// tracks whether the session is currently inside a worktree it created and how
// to get back out. Ported from vibelearn's worktree session concept.
type WorktreeState struct {
	mu           sync.Mutex
	Active       bool
	OriginalCwd  string // cwd before entering the worktree
	WorktreePath string
	Branch       string
	BaseCommit   string // HEAD commit the worktree was branched from
}

func git(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// EnterWorktreeTool creates an isolated git worktree under .claude/worktrees/
// with a fresh branch off HEAD and switches the process into it.
type EnterWorktreeTool struct{ State *WorktreeState }

func (EnterWorktreeTool) Name() string { return "EnterWorktree" }

func (EnterWorktreeTool) Description() string {
	return "Use ONLY when the user explicitly asks to work in a worktree. Creates an isolated git worktree under " +
		".claude/worktrees/ with a new branch off HEAD and switches the session into it. Requires a git repo. " +
		"Use ExitWorktree to leave. Do not use for ordinary branch switching — use git commands for that."
}

func (EnterWorktreeTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"branch": {"type": "string", "description": "Optional name for the new branch (auto-generated if omitted)"}
		}
	}`)
}

func (t EnterWorktreeTool) Call(ctx context.Context, input json.RawMessage) (Result, error) {
	if t.State == nil {
		return Result{IsError: true, Output: "worktree state unavailable"}, nil
	}
	t.State.mu.Lock()
	defer t.State.mu.Unlock()
	if t.State.Active {
		return Result{IsError: true, Output: "already inside a worktree at " + t.State.WorktreePath + "; use ExitWorktree first"}, nil
	}
	var in struct {
		Branch string `json:"branch"`
	}
	_ = json.Unmarshal(input, &in)

	cwd, err := os.Getwd()
	if err != nil {
		return Result{IsError: true, Output: err.Error()}, nil
	}
	root, err := git(cwd, "rev-parse", "--show-toplevel")
	if err != nil {
		return Result{IsError: true, Output: "not a git repository: " + root}, nil
	}
	base, _ := git(root, "rev-parse", "HEAD")
	branch := in.Branch
	if branch == "" {
		branch = "worktree-" + strconv.FormatInt(time.Now().Unix(), 36)
	}
	wtPath := filepath.Join(root, ".claude", "worktrees", branch)
	if out, err := git(root, "worktree", "add", "-b", branch, wtPath, "HEAD"); err != nil {
		return Result{IsError: true, Output: "git worktree add failed: " + out}, nil
	}
	if err := os.Chdir(wtPath); err != nil {
		// best-effort rollback
		_, _ = git(root, "worktree", "remove", "--force", wtPath)
		return Result{IsError: true, Output: "chdir failed: " + err.Error()}, nil
	}
	t.State.Active = true
	t.State.OriginalCwd = cwd
	t.State.WorktreePath = wtPath
	t.State.Branch = branch
	t.State.BaseCommit = base
	return Result{Output: fmt.Sprintf("Created worktree at %s on branch %s. Session now working in the worktree. Use ExitWorktree to leave.", wtPath, branch)}, nil
}

// ExitWorktreeTool leaves the current worktree, switching back to the original
// cwd and optionally removing the worktree and its branch.
type ExitWorktreeTool struct{ State *WorktreeState }

func (ExitWorktreeTool) Name() string { return "ExitWorktree" }

func (ExitWorktreeTool) Description() string {
	return "Leave the worktree created by EnterWorktree and return to the original directory. " +
		"action=\"keep\" leaves the worktree and branch on disk; action=\"remove\" deletes both. " +
		"With action=remove, refuses if there are uncommitted or unpushed changes unless discard_changes=true."
}

func (ExitWorktreeTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"action": {"type": "string", "enum": ["keep", "remove"], "description": "keep or remove the worktree"},
			"discard_changes": {"type": "boolean", "description": "Force removal despite uncommitted/unpushed changes"}
		},
		"required": ["action"]
	}`)
}

func (t ExitWorktreeTool) Call(ctx context.Context, input json.RawMessage) (Result, error) {
	if t.State == nil {
		return Result{IsError: true, Output: "worktree state unavailable"}, nil
	}
	t.State.mu.Lock()
	defer t.State.mu.Unlock()
	if !t.State.Active {
		return Result{IsError: true, Output: "not currently inside a worktree created by EnterWorktree"}, nil
	}
	var in struct {
		Action         string `json:"action"`
		DiscardChanges bool   `json:"discard_changes"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return Result{IsError: true, Output: fmt.Sprintf("bad input: %v", err)}, nil
	}
	if in.Action != "keep" && in.Action != "remove" {
		return Result{IsError: true, Output: "action must be keep or remove"}, nil
	}

	wtPath := t.State.WorktreePath
	orig := t.State.OriginalCwd
	branch := t.State.Branch

	if in.Action == "remove" && !in.DiscardChanges {
		if changes := worktreeChanges(wtPath, t.State.BaseCommit); changes != "" {
			return Result{IsError: true, Output: "worktree has changes that would be lost:\n" + changes +
				"\nConfirm with the user, then re-invoke with discard_changes=true to remove anyway."}, nil
		}
	}

	if err := os.Chdir(orig); err != nil {
		return Result{IsError: true, Output: "chdir back failed: " + err.Error()}, nil
	}

	msg := fmt.Sprintf("Exited worktree; back in %s.", orig)
	if in.Action == "remove" {
		if out, err := git(orig, "worktree", "remove", "--force", wtPath); err != nil {
			return Result{IsError: true, Output: "returned to " + orig + " but git worktree remove failed: " + out}, nil
		}
		_, _ = git(orig, "branch", "-D", branch)
		msg = fmt.Sprintf("Exited and removed worktree %s (branch %s); back in %s.", wtPath, branch, orig)
	}
	t.State.Active = false
	t.State.OriginalCwd = ""
	t.State.WorktreePath = ""
	t.State.Branch = ""
	t.State.BaseCommit = ""
	return Result{Output: msg}, nil
}

// worktreeChanges returns a non-empty description if the worktree has
// uncommitted changes or commits not present on its upstream/HEAD baseline.
func worktreeChanges(wtPath, base string) string {
	var parts []string
	if st, err := git(wtPath, "status", "--porcelain"); err == nil && st != "" {
		parts = append(parts, "uncommitted changes:\n"+st)
	}
	// Commits made in the worktree beyond the baseline it branched from.
	if base != "" {
		if ahead, err := git(wtPath, "rev-list", "--count", "HEAD", "^"+base); err == nil {
			if n, _ := strconv.Atoi(ahead); n > 0 {
				parts = append(parts, fmt.Sprintf("%d commit(s) not on the base branch", n))
			}
		}
	}
	return strings.Join(parts, "\n")
}
