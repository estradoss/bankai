package commands

import (
	"fmt"
	"os"
	"sort"
	"strings"
)

// Additional small informational slash commands (part of the ongoing port of
// vibelearn's command surface).

// PWD prints the current working directory.
type PWD struct{}

func (PWD) Name() string        { return "pwd" }
func (PWD) Description() string { return "Print the current working directory" }
func (PWD) Run(ctx Context, args string) (Result, error) {
	wd, err := os.Getwd()
	if err != nil {
		return Result{}, err
	}
	return Result{Text: wd}, nil
}

// Tools lists the tools currently available to the model.
type Tools struct{}

func (Tools) Name() string        { return "tools" }
func (Tools) Description() string { return "List tools available to the model" }
func (Tools) Run(ctx Context, args string) (Result, error) {
	all := ctx.Engine.Tools.All()
	names := make([]string, 0, len(all))
	for _, t := range all {
		names = append(names, t.Name())
	}
	sort.Strings(names)
	return Result{Text: fmt.Sprintf("%d tools:\n  %s", len(names), strings.Join(names, "\n  "))}, nil
}

// Plugins lists loaded plugins (name, version, description). The list is
// captured at startup and passed in so the command stays a pure reader.
type Plugins struct{ Lines []string }

func (Plugins) Name() string        { return "plugins" }
func (Plugins) Description() string { return "List loaded plugins" }
func (p Plugins) Run(ctx Context, args string) (Result, error) {
	if len(p.Lines) == 0 {
		return Result{Text: "no plugins loaded (install under ~/.claude/plugins/<name>/plugin.json)"}, nil
	}
	return Result{Text: fmt.Sprintf("%d plugin(s):\n  %s", len(p.Lines), strings.Join(p.Lines, "\n  "))}, nil
}

// System prints the active system prompt (useful for debugging goal/memory
// injection). Truncated to keep the terminal readable.
type System struct{}

func (System) Name() string        { return "system" }
func (System) Description() string { return "Show the active system prompt" }
func (System) Run(ctx Context, args string) (Result, error) {
	s := ctx.Engine.System
	const max = 4000
	if len(s) > max {
		s = s[:max] + fmt.Sprintf("\n… (%d more chars)", len(ctx.Engine.System)-max)
	}
	return Result{Text: s}, nil
}
