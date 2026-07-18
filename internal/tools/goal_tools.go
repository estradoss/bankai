package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/estradoss/bankai/internal/goal"
)

// GoalStore is the minimal interface goal tools depend on (avoids import cycle later).
type GoalStore interface {
	Get() *goal.Goal
	Set(*goal.Goal) error
	Update(func(*goal.Goal)) error
}

// CreateGoalTool lets the model start a fresh goal.
type CreateGoalTool struct{ Store GoalStore }

func (t *CreateGoalTool) Name() string { return "create_goal" }
func (t *CreateGoalTool) Description() string {
	return "Create a new session goal. Only call when the user has clearly asked to work toward a persistent multi-turn objective, or when the current turn's request will not fit in one turn and requires continuation."
}
func (t *CreateGoalTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"objective":    {"type": "string", "description": "The user-facing objective, verbatim from the user where possible"},
			"token_budget": {"type": "integer", "description": "Optional cap on tokens spent pursuing this goal"}
		},
		"required": ["objective"]
	}`)
}
func (t *CreateGoalTool) Call(ctx context.Context, input json.RawMessage) (Result, error) {
	var in struct {
		Objective   string `json:"objective"`
		TokenBudget int    `json:"token_budget"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return Result{IsError: true, Output: err.Error()}, nil
	}
	if in.Objective == "" {
		return Result{IsError: true, Output: "objective required"}, nil
	}
	now := time.Now()
	g := &goal.Goal{
		Objective:   in.Objective,
		Status:      goal.StatusActive,
		TokenBudget: in.TokenBudget,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := t.Store.Set(g); err != nil {
		return Result{IsError: true, Output: err.Error()}, nil
	}
	return Result{Output: fmt.Sprintf("goal created: %s", in.Objective)}, nil
}

// UpdateGoalTool lets the model transition status (complete/blocked).
type UpdateGoalTool struct{ Store GoalStore }

func (t *UpdateGoalTool) Name() string { return "update_goal" }
func (t *UpdateGoalTool) Description() string {
	return "Update the active goal status. Use \"complete\" only after strict completion audit. Use \"blocked\" only after the same blocker recurs for 3+ consecutive goal turns."
}
func (t *UpdateGoalTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"status": {"type": "string", "enum": ["complete", "blocked"]}
		},
		"required": ["status"]
	}`)
}
func (t *UpdateGoalTool) Call(ctx context.Context, input json.RawMessage) (Result, error) {
	var in struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return Result{IsError: true, Output: err.Error()}, nil
	}
	if t.Store.Get() == nil {
		return Result{IsError: true, Output: "no active goal"}, nil
	}
	var s goal.Status
	switch in.Status {
	case "complete":
		s = goal.StatusComplete
	case "blocked":
		s = goal.StatusBlocked
	default:
		return Result{IsError: true, Output: "status must be complete or blocked"}, nil
	}
	if err := t.Store.Update(func(g *goal.Goal) { g.Status = s }); err != nil {
		return Result{IsError: true, Output: err.Error()}, nil
	}
	return Result{Output: fmt.Sprintf("goal status = %s", s)}, nil
}

// GetGoalTool exposes current goal state to the model.
type GetGoalTool struct{ Store GoalStore }

func (t *GetGoalTool) Name() string { return "get_goal" }
func (t *GetGoalTool) Description() string {
	return "Return the current session goal (objective, status, budget, usage) or a note that no goal is set."
}
func (t *GetGoalTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type": "object", "properties": {}}`)
}
func (t *GetGoalTool) Call(ctx context.Context, input json.RawMessage) (Result, error) {
	g := t.Store.Get()
	if g == nil {
		return Result{Output: "no active goal"}, nil
	}
	b, _ := json.MarshalIndent(g, "", "  ")
	return Result{Output: string(b)}, nil
}
