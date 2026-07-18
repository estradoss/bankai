package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
)

// Tool is the interface every agent-callable tool implements.
type Tool interface {
	Name() string
	Description() string
	InputSchema() json.RawMessage
	Call(ctx context.Context, input json.RawMessage) (Result, error)
}

// Result is what a Tool returns after execution.
type Result struct {
	Output  string
	IsError bool
}

// Registry stores tools by name.
type Registry struct {
	mu    sync.RWMutex
	tools map[string]Tool
}

func NewRegistry() *Registry {
	return &Registry{tools: map[string]Tool{}}
}

func (r *Registry) Register(t Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools[t.Name()] = t
}

func (r *Registry) Get(name string) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

func (r *Registry) All() []Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Tool, 0, len(r.tools))
	for _, t := range r.tools {
		out = append(out, t)
	}
	return out
}

// Spec is the JSON shape sent to Anthropic in the `tools` field.
type Spec struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

func (r *Registry) Specs() []Spec {
	all := r.All()
	specs := make([]Spec, 0, len(all))
	for _, t := range all {
		specs = append(specs, Spec{
			Name:        t.Name(),
			Description: t.Description(),
			InputSchema: t.InputSchema(),
		})
	}
	return specs
}

// Execute runs a tool by name with the given raw input.
func (r *Registry) Execute(ctx context.Context, name string, input json.RawMessage) Result {
	t, ok := r.Get(name)
	if !ok {
		return Result{Output: fmt.Sprintf("unknown tool: %s", name), IsError: true}
	}
	res, err := t.Call(ctx, input)
	if err != nil {
		return Result{Output: err.Error(), IsError: true}
	}
	return res
}
