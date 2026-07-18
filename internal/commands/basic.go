package commands

import (
	"fmt"
	"strings"
)

type Help struct{ Registry *Registry }

func (h Help) Name() string        { return "help" }
func (h Help) Description() string { return "List slash commands" }
func (h Help) Run(ctx Context, args string) (Result, error) {
	var b strings.Builder
	b.WriteString("Slash commands:\n")
	for _, c := range h.Registry.List() {
		fmt.Fprintf(&b, "  /%-10s  %s\n", c.Name(), c.Description())
	}
	return Result{Text: b.String()}, nil
}

type Clear struct{}

func (Clear) Name() string        { return "clear" }
func (Clear) Description() string { return "Clear conversation history" }
func (Clear) Run(ctx Context, args string) (Result, error) {
	ctx.Engine.Messages = ctx.Engine.Messages[:0]
	return Result{Text: "conversation cleared"}, nil
}

type Exit struct{}

func (Exit) Name() string        { return "exit" }
func (Exit) Description() string { return "Quit bankai" }
func (Exit) Run(ctx Context, args string) (Result, error) {
	return Result{Text: "bye", Exit: true}, nil
}

type Model struct{}

func (Model) Name() string        { return "model" }
func (Model) Description() string { return "Show or set model (usage: /model [name])" }
func (Model) Run(ctx Context, args string) (Result, error) {
	if args == "" {
		return Result{Text: "model: " + ctx.Engine.Client.Model}, nil
	}
	ctx.Engine.Client.Model = args
	return Result{Text: "model set to " + args}, nil
}

type Dump struct{}

func (Dump) Name() string        { return "dump" }
func (Dump) Description() string { return "Dump raw message history (debug)" }
func (Dump) Run(ctx Context, args string) (Result, error) {
	return Result{Text: ctx.Engine.DumpMessages()}, nil
}

