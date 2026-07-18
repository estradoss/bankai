package tui

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/estradoss/bankai/internal/commands"
	"github.com/estradoss/bankai/internal/engine"
	"github.com/estradoss/bankai/internal/goal"
	"github.com/estradoss/bankai/internal/permission"
	"github.com/estradoss/bankai/internal/tools"
)

const (
	ansiBold  = "\x1b[1m"
	ansiDim   = "\x1b[2m"
	ansiCyan  = "\x1b[36m"
	ansiGreen = "\x1b[32m"
	ansiRed   = "\x1b[31m"
	ansiReset = "\x1b[0m"
)

type REPL struct {
	Engine *engine.Engine
	Cmds   *commands.Registry
	Goals  *goal.Store
	In     io.Reader
	Out    io.Writer
	// Ask, if set, receives the interactive prompter so the AskUserQuestion
	// tool can prompt on the same stdin the REPL uses.
	Ask *tools.AskBridge
}

func New(e *engine.Engine, r *commands.Registry, g *goal.Store) *REPL {
	return &REPL{Engine: e, Cmds: r, Goals: g, In: os.Stdin, Out: os.Stdout}
}

func (r *REPL) Run(ctx context.Context) error {
	r.Engine.OnText = func(chunk string) {
		fmt.Fprint(r.Out, chunk)
	}
	fmt.Fprintf(r.Out, "%sbankai%s — model=%s. type /help for commands, /exit to quit.\n",
		ansiBold, ansiReset, r.Engine.Client.Model)

	reader := bufio.NewReader(r.In)
	// A shared reader lets the permission asker read a y/n line from the same
	// stdin the REPL uses for prompts, without a second competing buffer.
	if r.Engine.Perms != nil {
		r.Engine.Perms.Asker = r.makeAsker(reader)
	}
	if r.Ask != nil {
		r.Ask.Prompter = r.makeQuestionPrompter(reader)
	}

	for {
		r.printPrompt()
		raw, err := reader.ReadString('\n')
		if err != nil {
			if raw == "" {
				fmt.Fprintln(r.Out)
				return nil
			}
		}
		line := strings.TrimSpace(raw)
		if line == "" {
			if err != nil {
				fmt.Fprintln(r.Out)
				return nil
			}
			continue
		}

		if name, args, ok := commands.Parse(line); ok {
			cmd, found := r.Cmds.Get(name)
			if !found {
				fmt.Fprintf(r.Out, "%sunknown command: /%s%s\n", ansiRed, name, ansiReset)
				continue
			}
			res, err := cmd.Run(commands.Context{Ctx: ctx, Engine: r.Engine, Goals: r.Goals}, args)
			if err != nil {
				fmt.Fprintf(r.Out, "%serror: %v%s\n", ansiRed, err, ansiReset)
				continue
			}
			if res.Text != "" {
				fmt.Fprintln(r.Out, res.Text)
			}
			if res.Exit {
				return nil
			}
			if res.Submit != "" {
				line = res.Submit
			} else {
				continue
			}
		}

		fmt.Fprintf(r.Out, "%s", ansiDim)
		if err := r.Engine.Submit(ctx, line); err != nil {
			// ctrl+c cancels the shared context; don't spin forever on a dead
			// ctx — surface it once and exit cleanly.
			if ctx.Err() != nil {
				fmt.Fprintf(r.Out, "%s\n%sinterrupted%s\n", ansiReset, ansiDim, ansiReset)
				return nil
			}
			fmt.Fprintf(r.Out, "%s\n%serror: %v%s\n", ansiReset, ansiRed, err, ansiReset)
			continue
		}
		fmt.Fprintf(r.Out, "%s\n", ansiReset)
	}
}

// makeAsker returns a permission.Asker that prompts on the terminal, reading
// its answer from the shared REPL reader. y=allow once, a=allow always, n/other=deny.
func (r *REPL) makeAsker(reader *bufio.Reader) permission.Asker {
	return func(req permission.Request) permission.Decision {
		in := string(req.Input)
		if len(in) > 200 {
			in = in[:197] + "..."
		}
		fmt.Fprintf(r.Out, "\n%s permission%s %s%s%s wants to run:\n  %s%s%s\n%sallow? [y]es once / [a]lways / [N]o:%s ",
			ansiBold, ansiReset, ansiCyan, req.Tool, ansiReset, ansiDim, in, ansiReset, ansiGreen, ansiReset)
		ans, _ := reader.ReadString('\n')
		switch strings.ToLower(strings.TrimSpace(ans)) {
		case "y", "yes":
			return permission.DecideAllowOnce
		case "a", "always":
			return permission.DecideAllowAlways
		default:
			return permission.DecideDeny
		}
	}
}

// makeQuestionPrompter returns a prompter that renders each AskUserQuestion
// question as a numbered menu on the terminal and reads the selection from the
// shared REPL reader. Users type option numbers (space/comma separated when
// multiSelect), or free text for the automatic "Other" choice.
func (r *REPL) makeQuestionPrompter(reader *bufio.Reader) tools.AskPrompter {
	return func(ctx context.Context, questions []tools.AskQuestion) ([]tools.AskAnswer, error) {
		answers := make([]tools.AskAnswer, 0, len(questions))
		for _, q := range questions {
			fmt.Fprintf(r.Out, "\n%s%s%s %s\n", ansiCyan, q.Header, ansiReset, q.Question)
			for i, o := range q.Options {
				fmt.Fprintf(r.Out, "  %s%d%s) %s%s%s — %s\n", ansiBold, i+1, ansiReset, ansiGreen, o.Label, ansiReset, o.Description)
			}
			hint := "choose a number"
			if q.MultiSelect {
				hint = "choose numbers (space/comma separated)"
			}
			fmt.Fprintf(r.Out, "%s%s, or type your own answer:%s ", ansiDim, hint, ansiReset)
			line, _ := reader.ReadString('\n')
			line = strings.TrimSpace(line)

			var selected []string
			fields := strings.FieldsFunc(line, func(c rune) bool { return c == ' ' || c == ',' })
			allNums := len(fields) > 0
			for _, f := range fields {
				n := 0
				if _, err := fmt.Sscanf(f, "%d", &n); err != nil || n < 1 || n > len(q.Options) {
					allNums = false
					break
				}
			}
			if allNums {
				for _, f := range fields {
					n := 0
					fmt.Sscanf(f, "%d", &n)
					selected = append(selected, q.Options[n-1].Label)
					if !q.MultiSelect {
						break
					}
				}
			} else if line != "" {
				selected = []string{line} // free-text "Other"
			}
			answers = append(answers, tools.AskAnswer{Header: q.Header, Question: q.Question, Selected: selected})
		}
		return answers, nil
	}
}

func (r *REPL) printPrompt() {
	if g := r.Goals.Get(); g != nil {
		label := goalLabel(g)
		fmt.Fprintf(r.Out, "\n%s%s%s\n%s>%s ", ansiDim, label, ansiReset, ansiCyan, ansiReset)
		return
	}
	fmt.Fprintf(r.Out, "\n%s>%s ", ansiCyan, ansiReset)
}

func goalLabel(g *goal.Goal) string {
	obj := g.Objective
	if len(obj) > 60 {
		obj = obj[:57] + "..."
	}
	switch g.Status {
	case goal.StatusActive:
		return fmt.Sprintf("Pursuing goal: %s", obj)
	case goal.StatusPaused:
		return fmt.Sprintf("Goal paused: %s (/goal resume)", obj)
	case goal.StatusBlocked:
		return fmt.Sprintf("Goal blocked: %s", obj)
	case goal.StatusBudgetLimited:
		return fmt.Sprintf("Goal budget hit: %s", obj)
	case goal.StatusComplete:
		return fmt.Sprintf("Goal achieved: %s", obj)
	}
	return fmt.Sprintf("Goal (%s): %s", g.Status, obj)
}
