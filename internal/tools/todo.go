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

// Update mutates a single item matched by content, without replacing the whole
// list. Returns false if no item matches. Empty new-values leave a field as-is;
// status "deleted" removes the item.
func (s *TodoStore) Update(content, subject, status, activeForm string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.items {
		if s.items[i].Content != content {
			continue
		}
		if status == "deleted" {
			s.items = append(s.items[:i], s.items[i+1:]...)
			return true
		}
		if subject != "" {
			s.items[i].Content = subject
		}
		if status != "" {
			s.items[i].Status = status
		}
		if activeForm != "" {
			s.items[i].ActiveForm = activeForm
		}
		return true
	}
	return false
}

// TaskUpdateTool is the Go port of vibelearn's TaskUpdateTool. Where TodoWrite
// replaces the whole list, TaskUpdate mutates one task in place — flip status,
// rename, or delete — matched by its current content. (The TS original keys on
// a taskId in the todo-v2 file store; bankai's todo list is content-keyed, so
// we match on content.)
type TaskUpdateTool struct{ Store *TodoStore }

func (TaskUpdateTool) Name() string { return "TaskUpdate" }

func (TaskUpdateTool) Description() string {
	return "Update a single task in the todo list in place (without rewriting the whole list): change its status, rename it, or delete it. Identify the task by its current `content`. Keep exactly one task in_progress at a time."
}

func (TaskUpdateTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"content": {"type": "string", "description": "Current content of the task to update"},
			"subject": {"type": "string", "description": "New content/subject for the task"},
			"status": {"type": "string", "enum": ["pending", "in_progress", "completed", "deleted"], "description": "New status; 'deleted' removes the task"},
			"activeForm": {"type": "string", "description": "Present-continuous label shown while in_progress"}
		},
		"required": ["content"]
	}`)
}

func (t TaskUpdateTool) Call(ctx context.Context, input json.RawMessage) (Result, error) {
	var in struct {
		Content    string `json:"content"`
		Subject    string `json:"subject"`
		Status     string `json:"status"`
		ActiveForm string `json:"activeForm"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return Result{IsError: true, Output: fmt.Sprintf("bad input: %v", err)}, nil
	}
	if in.Content == "" {
		return Result{IsError: true, Output: "content is required"}, nil
	}
	if !t.Store.Update(in.Content, in.Subject, in.Status, in.ActiveForm) {
		return Result{IsError: true, Output: "no task found with content: " + in.Content}, nil
	}
	return Result{Output: "Task updated.\n" + t.Store.Render()}, nil
}
