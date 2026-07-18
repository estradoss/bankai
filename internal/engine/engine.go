package engine

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/estradoss/bankai/internal/agent"
	"github.com/estradoss/bankai/internal/goal"
	"github.com/estradoss/bankai/internal/permission"
	"github.com/estradoss/bankai/internal/provider"
	"github.com/estradoss/bankai/internal/tools"
	"github.com/estradoss/bankai/internal/transcript"
)

// Engine holds conversation state and runs the tool-calling loop.
type Engine struct {
	Client     *provider.Client
	Tools      *tools.Registry
	Goals      *goal.Store
	Messages   []agent.Message
	System     string
	OnText     func(string)
	Transcript *transcript.Writer // optional; nil = don't record
	Perms      *permission.Gate   // optional; nil = allow all (no gating)
	// TotalUsage accumulates token usage across every model turn this session.
	TotalUsage agent.Usage
	// Turns counts model turns (round-trips) this session.
	Turns int
}

// ClaudeCodePrefix is required as the system prompt when authenticating via a
// Claude Code OAuth token — Anthropic gates OAuth Messages API calls on this
// exact identifier.
const ClaudeCodePrefix = "You are Claude Code, Anthropic's official CLI for Claude."

func New(cli *provider.Client, reg *tools.Registry, goals *goal.Store) *Engine {
	return &Engine{
		Client: cli,
		Tools:  reg,
		Goals:  goals,
		System: ClaudeCodePrefix,
	}
}

// AutoCompactChars is the approximate context size (in characters) at which
// Submit compacts the conversation before the next turn. ~4 chars/token, so
// 600k chars ≈ 150k tokens. Zero disables auto-compaction.
var AutoCompactChars = 600_000

// contextChars estimates the size of the current conversation.
func (e *Engine) contextChars() int {
	n := 0
	for _, m := range e.Messages {
		for _, c := range m.Content {
			n += len(c.Text) + len(c.Content) + len(c.Input)
		}
	}
	return n
}

// Submit adds a user message and runs the tool loop until the model stops needing tools.
func (e *Engine) Submit(ctx context.Context, userInput string) error {
	if AutoCompactChars > 0 && e.contextChars() > AutoCompactChars {
		if _, err := e.Compact(ctx); err != nil {
			// Non-fatal: continue with the full history if compaction fails.
			if e.OnText != nil {
				e.OnText("\n[auto-compact failed: " + err.Error() + "]\n")
			}
		} else if e.OnText != nil {
			e.OnText("\n[context auto-compacted]\n")
		}
	}
	if e.Goals != nil {
		if g := e.Goals.Get(); g != nil && g.IsActive() {
			e.Messages = append(e.Messages, agent.UserText(goal.ContinuationPrompt(g)))
		}
	}
	e.Messages = append(e.Messages, agent.UserText(userInput))
	if e.Transcript != nil {
		_ = e.Transcript.WriteUser(userInput)
	}
	return e.runLoop(ctx)
}

func (e *Engine) runLoop(ctx context.Context) error {
	specs := e.Tools.Specs()
	for {
		start := time.Now()
		req := provider.StreamRequest{
			Model:    e.Client.Model,
			System:   e.System,
			Messages: e.Messages,
			Tools:    specs,
		}
		res, err := e.Client.Stream(ctx, req, e.OnText)
		if err != nil {
			return err
		}
		e.Messages = append(e.Messages, agent.Message{Role: "assistant", Content: res.Content})
		e.addUsage(res.Usage)
		if e.Transcript != nil {
			_ = e.Transcript.WriteAssistant(e.Client.Model, res.Content, res.StopReason, &res.Usage)
		}

		if e.Goals != nil {
			if g := e.Goals.Get(); g != nil && g.IsActive() {
				_ = e.Goals.AddUsage(res.Usage.Total(), time.Since(start))
			}
		}

		if res.StopReason != "tool_use" {
			if g := e.goalOrNil(); g != nil && g.Status == goal.StatusBudgetLimited {
				e.Messages = append(e.Messages, agent.UserText(goal.BudgetLimitPrompt(g)))
				if e.Transcript != nil {
					_ = e.Transcript.WriteUser(goal.BudgetLimitPrompt(g))
				}
				req2 := provider.StreamRequest{
					Model:    e.Client.Model,
					System:   e.System,
					Messages: e.Messages,
					Tools:    specs,
				}
				res2, err := e.Client.Stream(ctx, req2, e.OnText)
				if err != nil {
					return err
				}
				e.Messages = append(e.Messages, agent.Message{Role: "assistant", Content: res2.Content})
				e.addUsage(res2.Usage)
				if e.Transcript != nil {
					_ = e.Transcript.WriteAssistant(e.Client.Model, res2.Content, res2.StopReason, &res2.Usage)
				}
			}
			return nil
		}

		var results []agent.ContentBlock
		for _, blk := range res.Content {
			if blk.Type != "tool_use" {
				continue
			}
			var out tools.Result
			if e.Perms != nil {
				if ok, reason := e.Perms.Check(blk.Name, blk.Input); !ok {
					out = tools.Result{Output: reason, IsError: true}
				} else {
					out = e.Tools.Execute(ctx, blk.Name, blk.Input)
				}
			} else {
				out = e.Tools.Execute(ctx, blk.Name, blk.Input)
			}
			results = append(results, agent.ContentBlock{
				Type:      "tool_result",
				ToolUseID: blk.ID,
				Content:   truncate(out.Output, 200_000),
				IsError:   out.IsError,
			})
		}
		if len(results) == 0 {
			return nil
		}
		e.Messages = append(e.Messages, agent.Message{Role: "user", Content: results})
		if e.Transcript != nil {
			_ = e.Transcript.WriteToolResults(results)
		}
	}
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + fmt.Sprintf("\n[truncated %d bytes]", len(s)-max)
}

func (e *Engine) addUsage(u agent.Usage) {
	e.Turns++
	e.TotalUsage.InputTokens += u.InputTokens
	e.TotalUsage.OutputTokens += u.OutputTokens
	e.TotalUsage.CacheCreationInputTokens += u.CacheCreationInputTokens
	e.TotalUsage.CacheReadInputTokens += u.CacheReadInputTokens
}

func (e *Engine) goalOrNil() *goal.Goal {
	if e.Goals == nil {
		return nil
	}
	return e.Goals.Get()
}

// LastAssistantText returns the concatenated text blocks of the most recent
// assistant message (used to capture a sub-agent's final report).
func (e *Engine) LastAssistantText() string {
	for i := len(e.Messages) - 1; i >= 0; i-- {
		m := e.Messages[i]
		if m.Role != "assistant" {
			continue
		}
		var b strings.Builder
		for _, c := range m.Content {
			if c.Type == "text" {
				b.WriteString(c.Text)
			}
		}
		return strings.TrimSpace(b.String())
	}
	return ""
}

// SubagentRunner builds a runner that spawns an isolated sub-engine sharing the
// parent's provider client and tool set, runs a task to completion, and returns
// the sub-agent's final text. The sub-engine has no goal state and does not
// record to the transcript.
func SubagentRunner(client *provider.Client, reg *tools.Registry, system string) tools.SubagentFunc {
	return func(ctx context.Context, prompt string) (string, error) {
		sub := &Engine{Client: client, Tools: reg, System: system}
		if err := sub.Submit(ctx, prompt); err != nil {
			return "", err
		}
		return sub.LastAssistantText(), nil
	}
}

const compactPrompt = `Summarize our conversation so far into a compact hand-off note that preserves everything needed to continue the work. Include: the user's goal and constraints, key decisions, files and functions touched, current state, and the next steps. Be thorough but concise. Output only the summary.`

// Compact replaces the conversation history with a single model-generated
// summary, freeing context while preserving continuity. Returns the summary.
func (e *Engine) Compact(ctx context.Context) (string, error) {
	if len(e.Messages) == 0 {
		return "", fmt.Errorf("nothing to compact")
	}
	msgs := append([]agent.Message{}, e.Messages...)
	msgs = append(msgs, agent.UserText(compactPrompt))
	res, err := e.Client.Stream(ctx, provider.StreamRequest{
		Model:    e.Client.Model,
		System:   e.System,
		Messages: msgs,
	}, nil)
	if err != nil {
		return "", err
	}
	e.addUsage(res.Usage)
	var summary strings.Builder
	for _, c := range res.Content {
		if c.Type == "text" {
			summary.WriteString(c.Text)
		}
	}
	sum := strings.TrimSpace(summary.String())
	if sum == "" {
		return "", fmt.Errorf("compaction produced no summary")
	}
	e.Messages = []agent.Message{
		agent.UserText("[Earlier conversation summarized to save context]\n\n" + sum),
	}
	return sum, nil
}

// SetObjectiveUpdated queues the objective_updated hidden prompt for the next turn.
func (e *Engine) SetObjectiveUpdated(g *goal.Goal) {
	e.Messages = append(e.Messages, agent.UserText(goal.ObjectiveUpdatedPrompt(g)))
}

func (e *Engine) DumpMessages() string {
	var b strings.Builder
	for i, m := range e.Messages {
		fmt.Fprintf(&b, "--- [%d] %s ---\n", i, m.Role)
		for _, c := range m.Content {
			switch c.Type {
			case "text", "thinking":
				b.WriteString(c.Text)
				b.WriteString("\n")
			case "tool_use":
				fmt.Fprintf(&b, "[tool_use %s(%s)]\n", c.Name, string(c.Input))
			case "tool_result":
				fmt.Fprintf(&b, "[tool_result %s err=%v]\n%s\n", c.ToolUseID, c.IsError, c.Content)
			}
		}
	}
	return b.String()
}
