package tui

import (
	"context"

	"github.com/estradoss/bankai/internal/commands"
	"github.com/estradoss/bankai/internal/engine"
	"github.com/estradoss/bankai/internal/goal"
	"github.com/estradoss/bankai/internal/tools"
)

// Frontend is an interactive presentation layer over the engine. Both the line
// REPL and the Bubbletea TUI implement it, so they are interchangeable: each
// owns its own Run loop and wires the engine's callbacks (OnText, OnToolStart,
// permission asker, question prompter) internally. Pick one via Select.
type Frontend interface {
	Run(ctx context.Context) error
}

// Deps is everything a frontend needs to construct itself. Fields specific to
// one frontend (Banner, Vim) are ignored by the other.
type Deps struct {
	Engine *engine.Engine
	Cmds   *commands.Registry
	Goals  *goal.Store
	// Ask bridges the AskUserQuestion tool onto the REPL's stdin (line REPL only;
	// the Bubbletea TUI prompts through its own modal).
	Ask *tools.AskBridge
	// Banner + Vim configure the Bubbletea TUI (ignored by the line REPL).
	Banner BannerInfo
	Vim    bool
}

// Select builds the requested frontend. tui=true → the rich Bubbletea TUI,
// tui=false → the line REPL fallback. The two are fully interchangeable behind
// the Frontend interface.
func Select(tui bool, d Deps) Frontend {
	if tui {
		bub := NewBubbleWithBanner(context.Background(), d.Engine, d.Cmds, d.Goals, d.Banner)
		if d.Vim {
			bub.SetVim(true)
		}
		return bub
	}
	r := New(d.Engine, d.Cmds, d.Goals)
	r.Ask = d.Ask
	return r
}
