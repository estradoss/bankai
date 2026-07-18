package engine

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/estradoss/bankai/internal/agent"
	"github.com/estradoss/bankai/internal/goal"
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

// Submit adds a user message and runs the tool loop until the model stops needing tools.
func (e *Engine) Submit(ctx context.Context, userInput string) error {
	if g := e.Goals.Get(); g != nil && g.IsActive() {
		e.Messages = append(e.Messages, agent.UserText(goal.ContinuationPrompt(g)))
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
		if e.Transcript != nil {
			_ = e.Transcript.WriteAssistant(e.Client.Model, res.Content, res.StopReason, &res.Usage)
		}

		if g := e.Goals.Get(); g != nil && g.IsActive() {
			_ = e.Goals.AddUsage(res.Usage.Total(), time.Since(start))
		}

		if res.StopReason != "tool_use" {
			if g := e.Goals.Get(); g != nil && g.Status == goal.StatusBudgetLimited {
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
			out := e.Tools.Execute(ctx, blk.Name, blk.Input)
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
