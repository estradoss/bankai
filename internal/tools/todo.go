package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

// TodoStore holds the current todo list for a session. Shared between the
// TodoWrite tool and the REPL (which renders it).
type TodoStore struct {
	mu    sync.RWMutex
	items []TodoItem
}

type TodoItem struct {
	Content    string `json:"content"`
	Status     string `json:"status"` // pending | in_progress | completed
	ActiveForm string `json:"activeForm,omitempty"`
}

func NewTodoStore() *TodoStore { return &TodoStore{} }

func (s *TodoStore) Set(items []TodoItem) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.items = items
}

func (s *TodoStore) Items() []TodoItem {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]TodoItem, len(s.items))
	copy(out, s.items)
	return out
}

// Render returns a human-readable checklist.
func (s *TodoStore) Render() string {
	items := s.Items()
	if len(items) == 0 {
		return "(no todos)"
	}
	var b strings.Builder
	for _, it := range items {
		mark := "[ ]"
		switch it.Status {
		case "completed":
			mark = "[x]"
		case "in_progress":
			mark = "[~]"
		}
		fmt.Fprintf(&b, "%s %s\n", mark, it.Content)
	}
	return strings.TrimRight(b.String(), "\n")
}

type TodoWriteTool struct{ Store *TodoStore }

func (TodoWriteTool) Name() string { return "TodoWrite" }

func (TodoWriteTool) Description() string {
	return "Create and manage a structured task list for the current work. Replaces the whole list each call. Each todo has: content (imperative), status (pending|in_progress|completed), activeForm (present-continuous label). Keep exactly one task in_progress at a time. Use for multi-step tasks to track progress; skip for trivial single-step work."
}

func (TodoWriteTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"todos": {
				"type": "array",
				"items": {
					"type": "object",
					"properties": {
						"content": {"type": "string"},
						"status": {"type": "string", "enum": ["pending", "in_progress", "completed"]},
						"activeForm": {"type": "string"}
					},
					"required": ["content", "status"]
				}
			}
		},
		"required": ["todos"]
	}`)
}

func (t TodoWriteTool) Call(ctx context.Context, input json.RawMessage) (Result, error) {
	var in struct {
		Todos []TodoItem `json:"todos"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return Result{IsError: true, Output: fmt.Sprintf("bad input: %v", err)}, nil
	}
	t.Store.Set(in.Todos)
	return Result{Output: "Todos updated.\n" + t.Store.Render()}, nil
}
