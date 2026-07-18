package commands

import (
	"context"
	"sort"
	"strings"

	"github.com/estradoss/bankai/internal/engine"
	"github.com/estradoss/bankai/internal/goal"
)

// Context is threaded to each command's Run.
type Context struct {
	Ctx    context.Context
	Engine *engine.Engine
	Goals  *goal.Store
}

// Result is what a command returns to the REPL.
type Result struct {
	// Text is printed to the user.
	Text string
	// Exit ends the REPL loop.
	Exit bool
	// Submit — if non-empty, the REPL treats this as user input to send to the model.
	Submit string
}

// Command is a slash-command implementation.
type Command interface {
	Name() string
	Description() string
	Run(ctx Context, args string) (Result, error)
}

// Registry stores commands by name.
type Registry struct {
	cmds map[string]Command
}

func NewRegistry() *Registry { return &Registry{cmds: map[string]Command{}} }

func (r *Registry) Register(c Command) { r.cmds[c.Name()] = c }

func (r *Registry) Get(name string) (Command, bool) {
	c, ok := r.cmds[name]
	return c, ok
}

func (r *Registry) List() []Command {
	out := make([]Command, 0, len(r.cmds))
	for _, c := range r.cmds {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

// Parse extracts the command name and args from a "/foo bar baz" line.
// Returns ("", "", false) if the line is not a slash command.
func Parse(line string) (name, args string, ok bool) {
	if !strings.HasPrefix(line, "/") {
		return "", "", false
	}
	trimmed := strings.TrimPrefix(line, "/")
	sp := strings.IndexAny(trimmed, " \t")
	if sp < 0 {
		return trimmed, "", true
	}
	return trimmed[:sp], strings.TrimSpace(trimmed[sp+1:]), true
}
