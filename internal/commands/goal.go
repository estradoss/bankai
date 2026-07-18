package commands

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/estradoss/bankai/internal/goal"
)

// GoalCmd implements /goal [subcommand|objective].
//
// Behavior (mirrors codex + pi-goal):
//
//	/goal                 -> status
//	/goal clear           -> remove goal
//	/goal pause           -> pause active goal
//	/goal resume          -> resume paused goal (also fires continuation next turn)
//	/goal <objective>     -> set (or replace) objective. If replacing, next turn
//	                         gets an objective_updated hidden prompt.
//	/goal --budget=N ...  -> attach token budget when setting
type GoalCmd struct{}

func (GoalCmd) Name() string { return "goal" }
func (GoalCmd) Description() string {
	return "Set or manage a session goal that persists across turns"
}

func (GoalCmd) Run(ctx Context, args string) (Result, error) {
	args = strings.TrimSpace(args)
	if args == "" {
		return goalStatus(ctx), nil
	}

	// Subcommands first.
	switch strings.ToLower(strings.SplitN(args, " ", 2)[0]) {
	case "clear":
		if err := ctx.Goals.Clear(); err != nil {
			return Result{Text: "error: " + err.Error()}, nil
		}
		return Result{Text: "goal cleared"}, nil
	case "pause":
		g := ctx.Goals.Get()
		if g == nil {
			return Result{Text: "no goal set"}, nil
		}
		if err := ctx.Goals.Update(func(g *goal.Goal) { g.Status = goal.StatusPaused }); err != nil {
			return Result{Text: "error: " + err.Error()}, nil
		}
		return Result{Text: "goal paused (/goal resume to reactivate)"}, nil
	case "resume":
		g := ctx.Goals.Get()
		if g == nil {
			return Result{Text: "no goal set"}, nil
		}
		if err := ctx.Goals.Update(func(g *goal.Goal) { g.Status = goal.StatusActive }); err != nil {
			return Result{Text: "error: " + err.Error()}, nil
		}
		return Result{Text: "goal resumed"}, nil
	case "status":
		return goalStatus(ctx), nil
	}

	// Otherwise: set/replace objective.
	objective, budget := parseGoalArgs(args)
	if objective == "" {
		return Result{Text: "usage: /goal <objective> [--budget=N]"}, nil
	}

	prev := ctx.Goals.Get()
	now := time.Now()
	g := &goal.Goal{
		Objective:   objective,
		Status:      goal.StatusActive,
		TokenBudget: budget,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := ctx.Goals.Set(g); err != nil {
		return Result{Text: "error: " + err.Error()}, nil
	}

	msg := fmt.Sprintf("goal set: %s", objective)
	if budget > 0 {
		msg += fmt.Sprintf(" (budget %d tok)", budget)
	}

	// If replacing an existing goal, cue objective_updated hidden prompt for next turn.
	if prev != nil {
		ctx.Engine.SetObjectiveUpdated(g)
		msg += "\n(previous objective replaced — next turn will pursue the new one)"
	}

	return Result{Text: msg}, nil
}

func parseGoalArgs(s string) (objective string, budget int) {
	var parts []string
	for _, tok := range strings.Fields(s) {
		if strings.HasPrefix(tok, "--budget=") {
			if n, err := strconv.Atoi(strings.TrimPrefix(tok, "--budget=")); err == nil {
				budget = n
			}
			continue
		}
		parts = append(parts, tok)
	}
	return strings.Join(parts, " "), budget
}

func goalStatus(ctx Context) Result {
	g := ctx.Goals.Get()
	if g == nil {
		return Result{Text: "no goal set — use /goal <objective> to set one"}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "goal:      %s\n", g.Objective)
	fmt.Fprintf(&b, "status:    %s\n", g.Status)
	fmt.Fprintf(&b, "tokens:    %d", g.TokensUsed)
	if g.TokenBudget > 0 {
		fmt.Fprintf(&b, " / %d (remaining %d)", g.TokenBudget, g.RemainingTokens())
	}
	b.WriteString("\n")
	fmt.Fprintf(&b, "time:      %ds\n", g.TimeUsedSeconds)
	fmt.Fprintf(&b, "created:   %s\n", g.CreatedAt.Format(time.RFC3339))
	return Result{Text: b.String()}
}
