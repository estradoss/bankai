package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/estradoss/bankai/internal/commands"
	"github.com/estradoss/bankai/internal/config"
	"github.com/estradoss/bankai/internal/engine"
	"github.com/estradoss/bankai/internal/goal"
	"github.com/estradoss/bankai/internal/provider"
	"github.com/estradoss/bankai/internal/session"
	"github.com/estradoss/bankai/internal/tools"
	"github.com/estradoss/bankai/internal/transcript"
	"github.com/estradoss/bankai/internal/tui"
)

const version = "0.1.0"

type opts struct {
	prompt    string
	printMode bool
	cont      bool
	resume    string
	sessionID string
	model     string
	help      bool
	ver       bool
}

func parseArgs(args []string) (opts, error) {
	fs := flag.NewFlagSet("bankai", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var o opts
	fs.StringVar(&o.prompt, "p", "", "one-shot prompt; prints reply and exits (compat with claude -p)")
	fs.StringVar(&o.prompt, "print", "", "same as -p")
	fs.BoolVar(&o.cont, "c", false, "continue the most recent session in cwd")
	fs.BoolVar(&o.cont, "continue", false, "same as -c")
	fs.StringVar(&o.resume, "resume", "", "resume a specific session by uuid")
	fs.StringVar(&o.sessionID, "session-id", "", "start a new session with this uuid (interop with claude --session-id)")
	fs.StringVar(&o.model, "model", "", "override BANKAI_MODEL for this run")
	fs.BoolVar(&o.help, "h", false, "help")
	fs.BoolVar(&o.help, "help", false, "help")
	fs.BoolVar(&o.ver, "v", false, "version")
	fs.BoolVar(&o.ver, "version", false, "version")
	if err := fs.Parse(args); err != nil {
		return o, err
	}
	if o.prompt != "" {
		o.printMode = true
	} else if fs.NArg() > 0 {
		// support `bankai "prompt words"` positional as oneshot too
		o.prompt = strings.Join(fs.Args(), " ")
		o.printMode = true
	}
	return o, nil
}

func main() {
	o, err := parseArgs(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, "bankai:", err)
		os.Exit(2)
	}
	if o.ver {
		fmt.Println("bankai", version)
		return
	}
	if o.help {
		printHelp()
		return
	}
	if err := run(o); err != nil {
		fmt.Fprintln(os.Stderr, "bankai:", err)
		os.Exit(1)
	}
}

func run(o opts) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if o.model != "" {
		cfg.Model = o.model
	}
	sess, err := session.New(cfg.DataDir)
	if err != nil {
		return err
	}
	goals := goal.NewStore(sess.Dir)
	if err := goals.Load(); err != nil {
		return err
	}

	toolReg := tools.NewRegistry()
	toolReg.Register(tools.BashTool{})
	toolReg.Register(tools.ReadTool{})
	toolReg.Register(tools.EditTool{})
	toolReg.Register(tools.WriteTool{})
	toolReg.Register(&tools.CreateGoalTool{Store: goals})
	toolReg.Register(&tools.UpdateGoalTool{Store: goals})
	toolReg.Register(&tools.GetGoalTool{Store: goals})

	client := provider.NewClient(cfg.Auth, cfg.Model)
	eng := engine.New(client, toolReg, goals)

	cwd, _ := os.Getwd()
	if resolved, err := filepath.EvalSymlinks(cwd); err == nil {
		cwd = resolved
	}

	// Transcript setup + optional resume/continue.
	var priorSession string
	var priorPath string
	if o.cont {
		p, err := transcript.LatestSession(cwd)
		if err != nil {
			return err
		}
		if p == "" {
			return fmt.Errorf("no prior session found for %s", cwd)
		}
		priorPath = p
		priorSession = strings.TrimSuffix(filepath.Base(p), ".jsonl")
	} else if o.resume != "" {
		p, err := transcript.FindSession(cwd, o.resume)
		if err != nil {
			return err
		}
		priorPath = p
		priorSession = o.resume
	}

	writerID := priorSession
	if writerID == "" {
		writerID = o.sessionID // may still be empty → writer generates new uuid
	}
	tw, err := transcript.New(cwd, writerID)
	if err != nil {
		return err
	}
	eng.Transcript = tw

	if priorPath != "" {
		res, err := transcript.Load(priorPath)
		if err != nil {
			return fmt.Errorf("load transcript: %w", err)
		}
		eng.Messages = res.Messages
		tw.SetParent(res.LastUUID)
	}

	if o.printMode {
		return oneShot(context.Background(), eng, o.prompt)
	}

	cmdReg := commands.NewRegistry()
	cmdReg.Register(commands.Clear{})
	cmdReg.Register(commands.Exit{})
	cmdReg.Register(commands.Model{})
	cmdReg.Register(commands.Dump{})
	cmdReg.Register(commands.GoalCmd{})
	cmdReg.Register(commands.Help{Registry: cmdReg})

	ctx, cancel := signalCtx()
	defer cancel()

	fmt.Fprintf(os.Stderr, "bankai %s — auth=%s session=%s (%s)\n",
		version, cfg.Source, tw.SessionID, tw.Path)

	repl := tui.New(eng, cmdReg, goals)
	return repl.Run(ctx)
}

func oneShot(ctx context.Context, eng *engine.Engine, prompt string) error {
	eng.OnText = func(chunk string) { fmt.Print(chunk) }
	if err := eng.Submit(ctx, prompt); err != nil {
		return err
	}
	fmt.Println()
	return nil
}

func signalCtx() (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-ch
		cancel()
	}()
	return ctx, cancel
}

func printHelp() {
	fmt.Println(`bankai — terminal coding agent (Go)

Usage:
  bankai                        interactive REPL, new session
  bankai -c                     continue most recent session in this cwd
  bankai --resume <uuid>        resume specific session
  bankai --session-id <uuid>    new session with a chosen uuid
  bankai -p "<prompt>"          one-shot; print reply and exit
  bankai "<prompt words>"       same as -p
  bankai --model <name>         override model for this run

Interop:
  Sessions live at ~/.claude/projects/<sanitized-cwd>/<uuid>.jsonl —
  the exact same file Claude Code uses. bankai and claude can hand a
  session back and forth: run one, exit, run the other with -c/--resume.

Env:
  CLAUDE_CODE_OAUTH_TOKEN   override OAuth access token
  ANTHROPIC_API_KEY         used when no OAuth creds found
  BANKAI_MODEL              default model (default: ` + config.DefaultModel + `)

Slash commands (REPL):
  /help /goal /model /clear /dump /exit`)
}
