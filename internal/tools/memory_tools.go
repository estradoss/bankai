package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/estradoss/bankai/internal/memory"
)

// The memory tools let the model persist and recall facts across sessions via
// the file-based memory store. Port of vibelearn's memory tooling.

// CreateMemoryTool writes or updates a memory.
type CreateMemoryTool struct{ Store *memory.Store }

func (CreateMemoryTool) Name() string { return "create_memory" }
func (CreateMemoryTool) Description() string {
	return "Save a durable memory (persists across sessions). Use for context NOT derivable from the project itself: the user's role/preferences (type=user), guidance on how to work (type=feedback), ongoing project goals/constraints (type=project), or external references/URLs (type=reference). Do NOT save code structure, git history, or things a grep would reveal. Saving with an existing name overwrites it."
}
func (CreateMemoryTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"name": {"type": "string", "description": "Short kebab-case slug identifying the memory"},
			"description": {"type": "string", "description": "One-line summary used to judge relevance on recall"},
			"type": {"type": "string", "enum": ["user","feedback","project","reference"], "description": "Memory type"},
			"body": {"type": "string", "description": "The fact to remember"}
		},
		"required": ["name", "body"]
	}`)
}
func (t CreateMemoryTool) Call(ctx context.Context, input json.RawMessage) (Result, error) {
	if t.Store == nil {
		return Result{IsError: true, Output: "memory store not configured"}, nil
	}
	var in struct {
		Name, Description, Type, Body string
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return Result{IsError: true, Output: fmt.Sprintf("bad input: %v", err)}, nil
	}
	m := memory.Memory{
		Name:        in.Name,
		Description: in.Description,
		Type:        memory.ParseType(in.Type),
		Body:        in.Body,
	}
	if err := t.Store.Save(m); err != nil {
		return Result{IsError: true, Output: err.Error()}, nil
	}
	return Result{Output: "saved memory: " + in.Name}, nil
}

// SearchMemoryTool recalls memories relevant to a query.
type SearchMemoryTool struct{ Store *memory.Store }

func (SearchMemoryTool) Name() string { return "search_memory" }
func (SearchMemoryTool) Description() string {
	return "Recall saved memories relevant to a query. Returns the most relevant memories with their bodies. Use before starting work to surface prior context about the user, project conventions, or feedback."
}
func (SearchMemoryTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"query": {"type": "string", "description": "What to recall"},
			"limit": {"type": "integer", "description": "Max results (default 5)"}
		},
		"required": ["query"]
	}`)
}
func (t SearchMemoryTool) Call(ctx context.Context, input json.RawMessage) (Result, error) {
	if t.Store == nil {
		return Result{IsError: true, Output: "memory store not configured"}, nil
	}
	var in struct {
		Query string `json:"query"`
		Limit int    `json:"limit"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return Result{IsError: true, Output: fmt.Sprintf("bad input: %v", err)}, nil
	}
	if in.Limit <= 0 {
		in.Limit = 5
	}
	mems := t.Store.FindRelevant(in.Query, in.Limit)
	if len(mems) == 0 {
		return Result{Output: "no relevant memories"}, nil
	}
	var b strings.Builder
	for i, m := range mems {
		if i > 0 {
			b.WriteString("\n\n")
		}
		fmt.Fprintf(&b, "[%s] %s: %s\n%s", m.Type, m.Name, m.Description, m.Body)
	}
	return Result{Output: b.String()}, nil
}

// DeleteMemoryTool removes a memory that has become wrong or obsolete.
type DeleteMemoryTool struct{ Store *memory.Store }

func (DeleteMemoryTool) Name() string { return "delete_memory" }
func (DeleteMemoryTool) Description() string {
	return "Delete a memory by name when it has become incorrect or obsolete."
}
func (DeleteMemoryTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {"name": {"type": "string", "description": "Name of the memory to delete"}},
		"required": ["name"]
	}`)
}
func (t DeleteMemoryTool) Call(ctx context.Context, input json.RawMessage) (Result, error) {
	if t.Store == nil {
		return Result{IsError: true, Output: "memory store not configured"}, nil
	}
	var in struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return Result{IsError: true, Output: fmt.Sprintf("bad input: %v", err)}, nil
	}
	if err := t.Store.Delete(in.Name); err != nil {
		return Result{IsError: true, Output: err.Error()}, nil
	}
	return Result{Output: "deleted memory: " + in.Name}, nil
}
