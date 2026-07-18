package commands

import (
	"fmt"
	"strings"

	"github.com/estradoss/bankai/internal/permission"
	"github.com/estradoss/bankai/internal/tools"
)

// Permissions shows or switches the permission mode at runtime.
type Permissions struct{}

func (Permissions) Name() string { return "permissions" }
func (Permissions) Description() string {
	return "Show or set permission mode (usage: /permissions [default|acceptEdits|bypassPermissions|dontAsk|plan])"
}
func (Permissions) Run(ctx Context, args string) (Result, error) {
	g := ctx.Engine.Perms
	if g == nil {
		return Result{Text: "permission gating is not enabled for this session"}, nil
	}
	args = strings.TrimSpace(args)
	if args == "" {
		return Result{Text: fmt.Sprintf("permission mode: %s\nmodes: default | acceptEdits | bypassPermissions | dontAsk | plan", g.Mode())}, nil
	}
	m := permission.Mode(args)
	if !m.Valid() {
		return Result{Text: fmt.Sprintf("unknown mode %q (default|acceptEdits|bypassPermissions|dontAsk|plan)", args)}, nil
	}
	g.SetMode(m)
	return Result{Text: "permission mode → " + args}, nil
}

// Compact summarizes the conversation to reclaim context.
type Compact struct{}

func (Compact) Name() string        { return "compact" }
func (Compact) Description() string { return "Summarize the conversation to free up context" }
func (Compact) Run(ctx Context, args string) (Result, error) {
	sum, err := ctx.Engine.Compact(ctx.Ctx)
	if err != nil {
		return Result{}, err
	}
	return Result{Text: "Conversation compacted.\n\n" + sum}, nil
}

// Cost reports token usage this session.
type Cost struct{}

func (Cost) Name() string        { return "cost" }
func (Cost) Description() string { return "Show token usage for this session" }
func (Cost) Run(ctx Context, args string) (Result, error) {
	u := ctx.Engine.TotalUsage
	var b strings.Builder
	fmt.Fprintf(&b, "Session usage (%d model turns):\n", ctx.Engine.Turns)
	fmt.Fprintf(&b, "  input tokens:        %d\n", u.InputTokens)
	fmt.Fprintf(&b, "  output tokens:       %d\n", u.OutputTokens)
	if u.CacheReadInputTokens > 0 || u.CacheCreationInputTokens > 0 {
		fmt.Fprintf(&b, "  cache read tokens:   %d\n", u.CacheReadInputTokens)
		fmt.Fprintf(&b, "  cache create tokens: %d\n", u.CacheCreationInputTokens)
	}
	fmt.Fprintf(&b, "  total:               %d", u.Total())
	return Result{Text: b.String()}, nil
}

// Context shows a rough view of current context size.
type ContextCmd struct{}

func (ContextCmd) Name() string        { return "context" }
func (ContextCmd) Description() string { return "Show conversation size (messages / approx tokens)" }
func (ContextCmd) Run(ctx Context, args string) (Result, error) {
	msgs := ctx.Engine.Messages
	chars := 0
	for _, m := range msgs {
		for _, c := range m.Content {
			chars += len(c.Text) + len(c.Content) + len(c.Input)
		}
	}
	// ~4 chars per token heuristic.
	return Result{Text: fmt.Sprintf("%d messages, ~%d chars (~%d tokens est.). /compact to shrink.",
		len(msgs), chars, chars/4)}, nil
}

// Todos prints the current todo list.
type Todos struct{ Store *tools.TodoStore }

func (Todos) Name() string        { return "todos" }
func (Todos) Description() string { return "Show the current todo list" }
func (t Todos) Run(ctx Context, args string) (Result, error) {
	return Result{Text: t.Store.Render()}, nil
}

// Init generates a CLAUDE.md by asking the model to analyze the repo.
type Init struct{}

func (Init) Name() string        { return "init" }
func (Init) Description() string { return "Analyze the repo and write a CLAUDE.md" }
func (Init) Run(ctx Context, args string) (Result, error) {
	return Result{Submit: `Analyze this codebase and create a CLAUDE.md file at the repo root. Include: a one-paragraph overview, the build/test/run commands, the high-level architecture (key directories and how they fit together), and any important conventions. Use the Bash, Glob, Grep, and Read tools to inspect the project first, then Write the file. If a CLAUDE.md already exists, improve it rather than overwriting blindly.`}, nil
}

// Commit stages nothing but asks the model to craft and make a git commit.
type Commit struct{}

func (Commit) Name() string        { return "commit" }
func (Commit) Description() string { return "Review changes and create a git commit" }
func (Commit) Run(ctx Context, args string) (Result, error) {
	extra := ""
	if strings.TrimSpace(args) != "" {
		extra = "\n\nAdditional instructions: " + args
	}
	return Result{Submit: `Create a git commit for the current changes. First run "git status" and "git diff" (and "git log --oneline -5" for message style) using Bash. Stage the relevant files, then commit with a concise Conventional Commits message summarizing the why. Do not push.` + extra}, nil
}

// Review asks the model to review the working diff.
type Review struct{}

func (Review) Name() string        { return "review" }
func (Review) Description() string { return "Review the current working diff" }
func (Review) Run(ctx Context, args string) (Result, error) {
	return Result{Submit: `Review the current working changes for correctness bugs, security issues, and obvious simplifications. Run "git diff" (and "git diff --staged") via Bash to see the changes. Report findings grouped by severity, each as: file:line — problem — suggested fix. Be concise; skip praise and style nits unless they change meaning.`}, nil
}

// Plan enters plan mode: the model researches and drafts a plan without making
// edits, then calls ExitPlanMode to present it.
type Plan struct{}

func (Plan) Name() string        { return "plan" }
func (Plan) Description() string { return "Plan a task read-only before editing (usage: /plan <task>)" }
func (Plan) Run(ctx Context, args string) (Result, error) {
	task := strings.TrimSpace(args)
	if task == "" {
		return Result{Text: "usage: /plan <what to build>"}, nil
	}
	// Engage plan mode on the gate so edits/writes are hard-denied, not just
	// discouraged by the prompt. Cleared with /permissions default after approval.
	if ctx.Engine.Perms != nil {
		ctx.Engine.Perms.SetMode(permission.ModePlan)
	}
	return Result{Submit: "You are in PLAN MODE. Research the codebase using read-only tools (Read, Glob, Grep, Bash for inspection only — no edits, no writes). Do NOT modify any files. When you have a concrete implementation plan, call the ExitPlanMode tool with the plan as markdown for my approval.\n\nTask: " + task}, nil
}

// MCP lists MCP tools currently bridged into the tool registry.
type MCP struct{}

func (MCP) Name() string        { return "mcp" }
func (MCP) Description() string { return "List connected MCP servers and their tools" }
func (MCP) Run(ctx Context, args string) (Result, error) {
	var names []string
	for _, t := range ctx.Engine.Tools.All() {
		if strings.HasPrefix(t.Name(), "mcp__") {
			names = append(names, t.Name())
		}
	}
	if len(names) == 0 {
		return Result{Text: "no MCP tools connected (configure mcpServers in .claude/settings.json)"}, nil
	}
	return Result{Text: fmt.Sprintf("MCP tools (%d):\n  %s", len(names), strings.Join(names, "\n  "))}, nil
}

// Limits shows the most recent Anthropic rate-limit / billing headers.
type Limits struct{}

func (Limits) Name() string        { return "limits" }
func (Limits) Description() string { return "Show latest API rate-limit / billing headers" }
func (Limits) Run(ctx Context, args string) (Result, error) {
	s := ctx.Engine.Client.Limits.Snapshot()
	if !s.Seen {
		return Result{Text: "no rate-limit headers seen yet (make a request first)"}, nil
	}
	var b strings.Builder
	b.WriteString("rate limits (last response):\n")
	row := func(label, rem, lim, reset string) {
		if lim == "" && rem == "" {
			return
		}
		fmt.Fprintf(&b, "  %-9s %s/%s remaining", label, dash(rem), dash(lim))
		if reset != "" {
			fmt.Fprintf(&b, "  resets %s", reset)
		}
		b.WriteByte('\n')
	}
	row("requests", s.RequestsRemaining, s.RequestsLimit, s.RequestsReset)
	row("tokens", s.TokensRemaining, s.TokensLimit, s.TokensReset)
	row("unified", s.UnifiedRemaining, s.UnifiedLimit, s.UnifiedReset)
	if s.RetryAfter != "" {
		fmt.Fprintf(&b, "  retry-after: %ss\n", s.RetryAfter)
	}
	return Result{Text: strings.TrimRight(b.String(), "\n")}, nil
}

func dash(s string) string {
	if s == "" {
		return "?"
	}
	return s
}

// Doctor prints a short environment/health summary.
type Doctor struct{ Source string }

func (Doctor) Name() string        { return "doctor" }
func (Doctor) Description() string { return "Show environment and auth health" }
func (d Doctor) Run(ctx Context, args string) (Result, error) {
	var b strings.Builder
	b.WriteString("bankai doctor\n")
	fmt.Fprintf(&b, "  auth source: %s\n", d.Source)
	fmt.Fprintf(&b, "  model:       %s\n", ctx.Engine.Client.Model)
	backend := "anthropic"
	if ctx.Engine.Client.OpenAI != nil {
		backend = "codex (openai responses)"
	}
	fmt.Fprintf(&b, "  backend:     %s\n", backend)
	fmt.Fprintf(&b, "  base url:    %s\n", ctx.Engine.Client.BaseURL)
	fmt.Fprintf(&b, "  tools:       %d registered\n", len(ctx.Engine.Tools.All()))
	fmt.Fprintf(&b, "  messages:    %d in context", len(ctx.Engine.Messages))
	return Result{Text: b.String()}, nil
}
