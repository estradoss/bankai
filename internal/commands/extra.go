package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/estradoss/bankai/internal/memory"
	"github.com/estradoss/bankai/internal/plugins"
	"github.com/estradoss/bankai/internal/theme"
	"github.com/estradoss/bankai/internal/voice"
)

// Additional small informational slash commands (part of the ongoing port of
// vibelearn's command surface).

// PWD prints the current working directory.
type PWD struct{}

func (PWD) Name() string        { return "pwd" }
func (PWD) Description() string { return "Print the current working directory" }
func (PWD) Run(ctx Context, args string) (Result, error) {
	wd, err := os.Getwd()
	if err != nil {
		return Result{}, err
	}
	return Result{Text: wd}, nil
}

// Tools lists the tools currently available to the model.
type Tools struct{}

func (Tools) Name() string        { return "tools" }
func (Tools) Description() string { return "List tools available to the model" }
func (Tools) Run(ctx Context, args string) (Result, error) {
	all := ctx.Engine.Tools.All()
	names := make([]string, 0, len(all))
	for _, t := range all {
		names = append(names, t.Name())
	}
	sort.Strings(names)
	return Result{Text: fmt.Sprintf("%d tools:\n  %s", len(names), strings.Join(names, "\n  "))}, nil
}

// Features lists resolved feature flags. The list is captured at startup.
type Features struct{ Flags []string }

func (Features) Name() string        { return "features" }
func (Features) Description() string { return "List resolved feature flags" }
func (f Features) Run(ctx Context, args string) (Result, error) {
	if len(f.Flags) == 0 {
		return Result{Text: "no feature flags"}, nil
	}
	return Result{Text: "feature flags:\n  " + strings.Join(f.Flags, "\n  ")}, nil
}

// Plugins lists loaded plugins (name, version, description). The list is
// captured at startup and passed in so the command stays a pure reader.
type Plugins struct{ Lines []string }

func (Plugins) Name() string        { return "plugins" }
func (Plugins) Description() string { return "List loaded plugins" }
func (p Plugins) Run(ctx Context, args string) (Result, error) {
	if len(p.Lines) == 0 {
		return Result{Text: "no plugins loaded (install under ~/.claude/plugins/<name>/plugin.json)"}, nil
	}
	return Result{Text: fmt.Sprintf("%d plugin(s):\n  %s", len(p.Lines), strings.Join(p.Lines, "\n  "))}, nil
}

// System prints the active system prompt (useful for debugging goal/memory
// injection). Truncated to keep the terminal readable.
type System struct{}

func (System) Name() string        { return "system" }
func (System) Description() string { return "Show the active system prompt" }
func (System) Run(ctx Context, args string) (Result, error) {
	s := ctx.Engine.System
	const max = 4000
	if len(s) > max {
		s = s[:max] + fmt.Sprintf("\n… (%d more chars)", len(ctx.Engine.System)-max)
	}
	return Result{Text: s}, nil
}

// gitCmd runs a git subcommand in the cwd and returns combined output.
func gitCmd(ctx Context, args ...string) (Result, error) {
	c := exec.CommandContext(ctx.Ctx, "git", args...)
	out, err := c.CombinedOutput()
	text := strings.TrimRight(string(out), "\n")
	if err != nil && text == "" {
		text = err.Error()
	}
	if text == "" {
		text = "(no output)"
	}
	return Result{Text: text}, nil
}

// Diff shows the working-tree diff (ported from vibelearn's /diff).
type Diff struct{}

func (Diff) Name() string { return "diff" }
func (Diff) Description() string {
	return "Show the git diff of the working tree (pass args like HEAD~1)"
}
func (Diff) Run(ctx Context, args string) (Result, error) {
	gitArgs := []string{"--no-pager", "diff"}
	if strings.TrimSpace(args) != "" {
		gitArgs = append(gitArgs, strings.Fields(args)...)
	}
	return gitCmd(ctx, gitArgs...)
}

// GitStatus shows a short git status (ported from vibelearn's /status).
type GitStatus struct{}

func (GitStatus) Name() string        { return "status" }
func (GitStatus) Description() string { return "Show a short git status of the working tree" }
func (GitStatus) Run(ctx Context, args string) (Result, error) {
	return gitCmd(ctx, "-c", "color.status=always", "status", "--short", "--branch")
}

// Export writes the current conversation to a Markdown file (ported from
// vibelearn's /export). Optional arg is the destination path; default is
// bankai-export-<sessionid>.md in the cwd.
type Export struct{}

func (Export) Name() string { return "export" }
func (Export) Description() string {
	return "Export the conversation to a Markdown file (optional: /export <path>)"
}
func (Export) Run(ctx Context, args string) (Result, error) {
	path := strings.TrimSpace(args)
	if path == "" {
		id := "session"
		if ctx.Engine.Transcript != nil && ctx.Engine.Transcript.SessionID != "" {
			id = ctx.Engine.Transcript.SessionID
		}
		path = fmt.Sprintf("bankai-export-%s.md", id)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# bankai conversation export\n\nmodel: %s\n\n", ctx.Engine.Client.Model)
	for _, m := range ctx.Engine.Messages {
		fmt.Fprintf(&b, "## %s\n\n", m.Role)
		for _, blk := range m.Content {
			switch blk.Type {
			case "text", "thinking":
				if blk.Text != "" {
					b.WriteString(blk.Text)
					b.WriteString("\n\n")
				}
			case "tool_use":
				fmt.Fprintf(&b, "> **tool_use** `%s` %s\n\n", blk.Name, string(blk.Input))
			case "tool_result":
				c := blk.Content
				if len(c) > 2000 {
					c = c[:2000] + "\n… (truncated)"
				}
				fmt.Fprintf(&b, "> **tool_result**\n```\n%s\n```\n\n", c)
			}
		}
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return Result{}, err
	}
	return Result{Text: fmt.Sprintf("exported %d messages to %s", len(ctx.Engine.Messages), path)}, nil
}

// ReleaseNotes shows recent commit history as release notes (ported loosely
// from vibelearn's /release-notes, which shows the CLI changelog).
type ReleaseNotes struct{}

func (ReleaseNotes) Name() string        { return "release-notes" }
func (ReleaseNotes) Description() string { return "Show recent commits as release notes" }
func (ReleaseNotes) Run(ctx Context, args string) (Result, error) {
	n := "15"
	if strings.TrimSpace(args) != "" {
		n = strings.Fields(args)[0]
	}
	return gitCmd(ctx, "--no-pager", "log", "--oneline", "--decorate", "-n", n)
}

// Copy copies the most recent assistant text to the system clipboard, or prints
// it if no clipboard backend is available (ported from vibelearn's /copy).
type Copy struct{}

func (Copy) Name() string        { return "copy" }
func (Copy) Description() string { return "Copy the last assistant message to the clipboard" }
func (Copy) Run(ctx Context, args string) (Result, error) {
	var last string
	for i := len(ctx.Engine.Messages) - 1; i >= 0; i-- {
		m := ctx.Engine.Messages[i]
		if m.Role != "assistant" {
			continue
		}
		var b strings.Builder
		for _, blk := range m.Content {
			if blk.Type == "text" && blk.Text != "" {
				b.WriteString(blk.Text)
			}
		}
		if b.Len() > 0 {
			last = b.String()
			break
		}
	}
	if last == "" {
		return Result{Text: "no assistant text to copy"}, nil
	}
	for _, cand := range [][]string{{"pbcopy"}, {"wl-copy"}, {"xclip", "-selection", "clipboard"}, {"xsel", "-b"}} {
		if _, err := exec.LookPath(cand[0]); err != nil {
			continue
		}
		c := exec.CommandContext(ctx.Ctx, cand[0], cand[1:]...)
		c.Stdin = strings.NewReader(last)
		if err := c.Run(); err == nil {
			return Result{Text: fmt.Sprintf("copied %d chars via %s", len(last), cand[0])}, nil
		}
	}
	return Result{Text: "no clipboard tool found; last assistant message:\n\n" + last}, nil
}

// Theme lists available TUI themes and sets the active one (persisted to
// ~/.claude/settings.json under "theme"; applies on next start). Ported from
// vibelearn's /theme.
type Theme struct{}

func (Theme) Name() string { return "theme" }
func (Theme) Description() string {
	return "List color themes, or set one: /theme <name>. Persists to settings; applies on next start."
}
func (Theme) Run(ctx Context, args string) (Result, error) {
	name := strings.TrimSpace(args)
	if name == "" {
		var b strings.Builder
		b.WriteString("available themes:\n  ")
		b.WriteString(strings.Join(theme.Names(), "\n  "))
		b.WriteString("\n\nset with: /theme <name>")
		return Result{Text: b.String()}, nil
	}
	if _, ok := theme.Get(name); !ok {
		return Result{Text: fmt.Sprintf("unknown theme %q. Available: %s", name, strings.Join(theme.Names(), ", "))}, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return Result{}, err
	}
	path := filepath.Join(home, ".claude", "settings.json")
	settings := map[string]any{}
	if raw, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(raw, &settings)
	}
	settings["theme"] = strings.ToLower(name)
	out, _ := json.MarshalIndent(settings, "", "  ")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return Result{}, err
	}
	if err := os.WriteFile(path, append(out, '\n'), 0o644); err != nil {
		return Result{}, err
	}
	return Result{Text: fmt.Sprintf("theme set to %q (applies on next start)", strings.ToLower(name))}, nil
}

// Vim toggles vim modal editing in the TUI (persisted to settings "editorMode";
// applies on next start). Ported from vibelearn's /vim.
type Vim struct{}

func (Vim) Name() string { return "vim" }
func (Vim) Description() string {
	return "Enable/disable vim editing mode in the TUI (applies next start): /vim on|off"
}
func (Vim) Run(ctx Context, args string) (Result, error) {
	mode := strings.TrimSpace(strings.ToLower(args))
	editorMode := "vim"
	switch mode {
	case "", "on", "vim":
		editorMode = "vim"
	case "off", "normal", "emacs":
		editorMode = "normal"
	default:
		return Result{Text: "usage: /vim on|off"}, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return Result{}, err
	}
	path := filepath.Join(home, ".claude", "settings.json")
	settings := map[string]any{}
	if raw, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(raw, &settings)
	}
	settings["editorMode"] = editorMode
	out, _ := json.MarshalIndent(settings, "", "  ")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return Result{}, err
	}
	if err := os.WriteFile(path, append(out, '\n'), 0o644); err != nil {
		return Result{}, err
	}
	return Result{Text: fmt.Sprintf("editorMode = %q (applies on next start)", editorMode)}, nil
}

// Plugin manages plugin installation from git (ported from vibelearn's /plugin):
// /plugin install <git-url>, /plugin update <name>, /plugin remove <name>,
// /plugin list. Changes apply on next start (plugins load at startup).
type Plugin struct{}

func (Plugin) Name() string { return "plugin" }
func (Plugin) Description() string {
	return "Manage plugins: /plugin install <git-url> | update <name> | remove <name> | list"
}
func (Plugin) Run(ctx Context, args string) (Result, error) {
	fields := strings.Fields(args)
	home, err := os.UserHomeDir()
	if err != nil {
		return Result{}, err
	}
	if len(fields) == 0 {
		return Result{Text: "usage: /plugin install <git-url> | update <name> | remove <name> | list"}, nil
	}
	switch fields[0] {
	case "install":
		if len(fields) < 2 {
			return Result{Text: "usage: /plugin install <git-url>"}, nil
		}
		name, err := plugins.Install(home, fields[1])
		if err != nil {
			return Result{Text: "install failed: " + err.Error()}, nil
		}
		return Result{Text: fmt.Sprintf("installed plugin %q (restart to load it)", name)}, nil
	case "update":
		if len(fields) < 2 {
			return Result{Text: "usage: /plugin update <name>"}, nil
		}
		out, err := plugins.Update(home, fields[1])
		if err != nil {
			return Result{Text: "update failed: " + err.Error()}, nil
		}
		return Result{Text: fmt.Sprintf("updated %q: %s", fields[1], out)}, nil
	case "remove", "uninstall":
		if len(fields) < 2 {
			return Result{Text: "usage: /plugin remove <name>"}, nil
		}
		if err := plugins.Remove(home, fields[1]); err != nil {
			return Result{Text: "remove failed: " + err.Error()}, nil
		}
		return Result{Text: fmt.Sprintf("removed plugin %q", fields[1])}, nil
	case "list":
		ps := plugins.Load(home, nil)
		if len(ps) == 0 {
			return Result{Text: "no plugins installed"}, nil
		}
		var b strings.Builder
		for _, p := range ps {
			fmt.Fprintf(&b, "%s %s — %s\n", p.Name, p.Version, p.Description)
		}
		return Result{Text: strings.TrimRight(b.String(), "\n")}, nil
	default:
		return Result{Text: "unknown subcommand; use install|update|remove|list"}, nil
	}
}

// Dream reports clusters of near-duplicate memories for consolidation (ported
// from vibelearn's dream service — analysis only, proposes merges).
type Dream struct{ Store *memory.Store }

func (Dream) Name() string { return "dream" }
func (Dream) Description() string {
	return "Find near-duplicate memories to consolidate (optional threshold 0-1)"
}
func (d Dream) Run(ctx Context, args string) (Result, error) {
	if d.Store == nil {
		return Result{Text: "memory is not configured"}, nil
	}
	threshold := 0.6
	if s := strings.TrimSpace(args); s != "" {
		if v, err := strconv.ParseFloat(s, 64); err == nil && v > 0 && v <= 1 {
			threshold = v
		}
	}
	clusters := d.Store.Consolidate(threshold)
	if len(clusters) == 0 {
		return Result{Text: fmt.Sprintf("no near-duplicate memories (threshold %.2f)", threshold)}, nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "duplicate clusters (threshold %.2f) — review and merge:\n", threshold)
	for _, c := range clusters {
		fmt.Fprintf(&b, "  ~%.0f%%: %s\n", c.Similarity*100, strings.Join(c.Names, ", "))
	}
	return Result{Text: strings.TrimRight(b.String(), "\n")}, nil
}

// MemoryExtract proposes durable memories from the conversation (auto-extract).
// It does not save them — it prints proposals for the user to approve.
type MemoryExtract struct{ Store *memory.Store }

func (MemoryExtract) Name() string { return "memory-extract" }
func (MemoryExtract) Description() string {
	return "Propose durable memories extracted from this conversation (review, then save with create_memory)"
}
func (m MemoryExtract) Run(ctx Context, args string) (Result, error) {
	cands, err := ctx.Engine.ExtractMemories(ctx.Ctx)
	if err != nil {
		return Result{Text: "extract failed: " + err.Error()}, nil
	}
	if len(cands) == 0 {
		return Result{Text: "nothing worth saving from this conversation"}, nil
	}
	var b strings.Builder
	b.WriteString("proposed memories (not saved — approve to persist):\n")
	for _, c := range cands {
		fmt.Fprintf(&b, "  [%s] %s — %s\n", c.Type, c.Name, c.Description)
	}
	return Result{Text: strings.TrimRight(b.String(), "\n")}, nil
}

// MemorySync pushes/pulls memories to a team memory server (ported from
// vibelearn's team memory sync). Usage: /memory-sync push|pull <base-url> [token]
type MemorySync struct{ Store *memory.Store }

func (MemorySync) Name() string { return "memory-sync" }
func (MemorySync) Description() string {
	return "Sync memories with a team server: /memory-sync push|pull <base-url> [token]"
}
func (m MemorySync) Run(ctx Context, args string) (Result, error) {
	if m.Store == nil {
		return Result{Text: "memory is not configured"}, nil
	}
	f := strings.Fields(args)
	if len(f) < 2 {
		return Result{Text: "usage: /memory-sync push|pull <base-url> [token]"}, nil
	}
	token := ""
	if len(f) >= 3 {
		token = f[2]
	}
	client := &memory.SyncClient{BaseURL: strings.TrimRight(f[1], "/"), Token: token}
	switch f[0] {
	case "push":
		n, err := client.Push(ctx.Ctx, m.Store)
		if err != nil {
			return Result{Text: "push failed: " + err.Error()}, nil
		}
		return Result{Text: fmt.Sprintf("pushed %d memories to %s", n, f[1])}, nil
	case "pull":
		n, err := client.Pull(ctx.Ctx, m.Store)
		if err != nil {
			return Result{Text: "pull failed: " + err.Error()}, nil
		}
		return Result{Text: fmt.Sprintf("pulled/merged %d memories from %s", n, f[1])}, nil
	default:
		return Result{Text: "usage: /memory-sync push|pull <base-url> [token]"}, nil
	}
}

// Dictate records microphone audio and transcribes it into a prompt (voice
// push-to-talk). Usage: /dictate [seconds] — the transcript is submitted to the
// model as a normal turn. Ported from vibelearn's voice dictation.
type Dictate struct{ Session *voice.Session }

func (Dictate) Name() string { return "dictate" }
func (Dictate) Description() string {
	return "Record mic audio and send the transcription as a prompt: /dictate [seconds]"
}
func (d Dictate) Run(ctx Context, args string) (Result, error) {
	if d.Session == nil {
		return Result{Text: "voice is not enabled (set the VOICE_MODE feature)"}, nil
	}
	seconds := 5
	if s := strings.TrimSpace(args); s != "" {
		if v, err := strconv.Atoi(s); err == nil && v > 0 {
			seconds = v
		}
	}
	text, err := d.Session.Dictate(ctx.Ctx, nil, seconds)
	if err != nil {
		return Result{Text: "dictation failed: " + err.Error()}, nil
	}
	if strings.TrimSpace(text) == "" {
		return Result{Text: "(no speech detected)"}, nil
	}
	// Submit the transcription as a model turn.
	return Result{Text: "heard: " + text, Submit: text}, nil
}

// Version prints the bankai version. Ported from vibelearn's /version.
type Version struct{ V string }

func (Version) Name() string        { return "version" }
func (Version) Description() string { return "Show the bankai version" }
func (v Version) Run(ctx Context, args string) (Result, error) {
	return Result{Text: "bankai " + v.V}, nil
}

// Env prints environment/runtime info. Ported from vibelearn's /env.
type Env struct{}

func (Env) Name() string        { return "env" }
func (Env) Description() string { return "Show environment: model, cwd, OS" }
func (Env) Run(ctx Context, args string) (Result, error) {
	wd, _ := os.Getwd()
	return Result{Text: fmt.Sprintf("model: %s\ncwd:   %s\nos:    %s/%s\ngo:    %s",
		ctx.Engine.Client.Model, wd, runtime.GOOS, runtime.GOARCH, runtime.Version())}, nil
}

// Stats shows session turn/token counts. Ported from vibelearn's /stats.
type Stats struct{}

func (Stats) Name() string        { return "stats" }
func (Stats) Description() string { return "Show session turn and token counts" }
func (Stats) Run(ctx Context, args string) (Result, error) {
	u := ctx.Engine.TotalUsage
	return Result{Text: fmt.Sprintf("turns: %d\ninput tokens:  %d\noutput tokens: %d\ncache read: %d  cache write: %d\nmessages in context: %d",
		ctx.Engine.Turns, u.InputTokens, u.OutputTokens, u.CacheReadInputTokens, u.CacheCreationInputTokens, len(ctx.Engine.Messages))}, nil
}

// Usage is an alias for stats-style token accounting. Ported from /usage.
type Usage struct{}

func (Usage) Name() string        { return "usage" }
func (Usage) Description() string { return "Show token usage this session" }
func (Usage) Run(ctx Context, args string) (Result, error) {
	u := ctx.Engine.TotalUsage
	return Result{Text: fmt.Sprintf("%d tokens total (%d in / %d out) over %d turns",
		u.Total(), u.InputTokens, u.OutputTokens, ctx.Engine.Turns)}, nil
}

// Rewind drops the most recent user↔assistant exchange from the context.
// Ported from vibelearn's /rewind.
type Rewind struct{}

func (Rewind) Name() string        { return "rewind" }
func (Rewind) Description() string { return "Drop the last exchange from the conversation context" }
func (Rewind) Run(ctx Context, args string) (Result, error) {
	msgs := ctx.Engine.Messages
	// Remove trailing messages back through the last user message.
	i := len(msgs) - 1
	for i >= 0 && msgs[i].Role != "user" {
		i--
	}
	if i < 0 {
		return Result{Text: "nothing to rewind"}, nil
	}
	dropped := len(msgs) - i
	ctx.Engine.Messages = msgs[:i]
	return Result{Text: fmt.Sprintf("rewound %d message(s); %d remain", dropped, len(ctx.Engine.Messages))}, nil
}

// Hooks lists the active tool-call hooks. Ported from vibelearn's /hooks.
type Hooks struct{}

func (Hooks) Name() string        { return "hooks" }
func (Hooks) Description() string { return "List active tool-call hooks" }
func (Hooks) Run(ctx Context, args string) (Result, error) {
	hs := ctx.Engine.Hooks
	if len(hs) == 0 {
		return Result{Text: "no hooks configured"}, nil
	}
	var b strings.Builder
	for _, h := range hs {
		m := h.Matcher
		if m == "" {
			m = "*"
		}
		fmt.Fprintf(&b, "%s [%s] → %s\n", h.Event, m, h.Command)
	}
	return Result{Text: strings.TrimRight(b.String(), "\n")}, nil
}

// Summary asks the model for a non-destructive summary of the conversation.
// Ported from vibelearn's /summary (unlike /compact it doesn't replace history).
type Summary struct{}

func (Summary) Name() string        { return "summary" }
func (Summary) Description() string { return "Summarize the conversation so far (without compacting)" }
func (Summary) Run(ctx Context, args string) (Result, error) {
	return Result{Submit: "Summarize our conversation so far: the goal, key decisions, what changed, and the current state. Keep it concise."}, nil
}

// SecurityReview runs a security-focused review of the working changes. Ported
// from vibelearn's /security-review.
type SecurityReview struct{}

func (SecurityReview) Name() string { return "security-review" }
func (SecurityReview) Description() string {
	return "Review the working-tree changes for security issues"
}
func (SecurityReview) Run(ctx Context, args string) (Result, error) {
	return Result{Submit: "Run `git diff` and review the changes strictly for security vulnerabilities: injection, auth/authorization gaps, secret exposure, unsafe deserialization, path traversal, SSRF, and missing input validation. Report concrete findings with file:line and a fix for each, or state that no security issues were found."}, nil
}

// Effort persists the reasoning-effort setting. Ported from vibelearn's /effort.
type Effort struct{}

func (Effort) Name() string { return "effort" }
func (Effort) Description() string {
	return "Set reasoning effort (low|medium|high), persisted: /effort <level>"
}
func (Effort) Run(ctx Context, args string) (Result, error) {
	lvl := strings.TrimSpace(strings.ToLower(args))
	switch lvl {
	case "":
		return Result{Text: "usage: /effort low|medium|high"}, nil
	case "low", "medium", "high":
	default:
		return Result{Text: "effort must be low, medium, or high"}, nil
	}
	if err := persistSetting("effort", lvl); err != nil {
		return Result{}, err
	}
	return Result{Text: fmt.Sprintf("effort = %q (applies on next start)", lvl)}, nil
}

// OutputStyle persists the output-style setting. Ported from /output-style.
type OutputStyle struct{}

func (OutputStyle) Name() string { return "output-style" }
func (OutputStyle) Description() string {
	return "Set output style (e.g. default|concise|explanatory), persisted"
}
func (OutputStyle) Run(ctx Context, args string) (Result, error) {
	style := strings.TrimSpace(strings.ToLower(args))
	if style == "" {
		return Result{Text: "usage: /output-style <name>"}, nil
	}
	if err := persistSetting("outputStyle", style); err != nil {
		return Result{}, err
	}
	return Result{Text: fmt.Sprintf("outputStyle = %q (applies on next start)", style)}, nil
}

// persistSetting writes a top-level string setting to ~/.claude/settings.json.
func persistSetting(key, value string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	path := filepath.Join(home, ".claude", "settings.json")
	settings := map[string]any{}
	if raw, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(raw, &settings)
	}
	settings[key] = value
	out, _ := json.MarshalIndent(settings, "", "  ")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, append(out, '\n'), 0o644)
}
